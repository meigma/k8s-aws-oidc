// Command oidc-proxy serves a minimal OIDC discovery + JWKS endpoint
// publicly via Tailscale Funnel, fetching the JWKS from the in-cluster
// Kubernetes apiserver.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/k8s-aws-oidc/internal/config"
	"github.com/meigma/k8s-aws-oidc/internal/leader"
	"github.com/meigma/k8s-aws-oidc/internal/logx"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
	"github.com/meigma/k8s-aws-oidc/internal/netx"
	"github.com/meigma/k8s-aws-oidc/internal/oidc"
	"github.com/meigma/k8s-aws-oidc/internal/tsrunner"
)

func main() {
	logger := bootstrapLogger()
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	activeLogger := logger
	var runErr error
	defer func() {
		attrs := []slog.Attr{slog.String("result", "success")}
		level := slog.LevelInfo
		if runErr != nil {
			level = slog.LevelError
			attrs = append(attrs,
				slog.String("result", "error"),
				slog.String("error_kind", processErrorKind(runErr)),
				slog.String("error", processErrorSummary(runErr)),
			)
		}
		logx.Log(context.Background(), activeLogger, level, "process", "process_stop", "process stopping", attrs...)
	}()
	finish := func(err error) error {
		runErr = err
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return finish(fmt.Errorf("config: %w", err))
	}

	logger, err = logx.NewLogger(os.Stderr, cfg.LogFormat, &slog.HandlerOptions{Level: cfg.LogLevel})
	if err != nil {
		return finish(fmt.Errorf("logger: %w", err))
	}
	activeLogger = logger
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := &runtimeState{}
	state.SetLeaderElectionEnabled(cfg.LeaderElectionEnabled)
	go func() {
		<-ctx.Done()
		state.SetShuttingDown(true)
	}()

	issuerURL, err := url.Parse(cfg.IssuerURL)
	if err != nil {
		return finish(fmt.Errorf("parse issuer URL: %w", err))
	}

	logx.Info(ctx, logger, "process", "process_start", "process starting",
		slog.String("hostname", cfg.TSHostname),
		slog.String("funnel_addr", cfg.FunnelAddr),
		slog.String("issuer_host", issuerURL.Hostname()),
		slog.String("log_format", string(cfg.LogFormat)),
		slog.String("log_level", cfg.LogLevel.String()),
		slog.Bool("source_ip_allowlist_enabled", cfg.SourceIPAllowlistEnabled),
		slog.Int("source_ip_allowlist_cidr_count", len(cfg.SourceIPAllowlistCIDRs)),
	)

	metricsRecorder := metrics.New(cfg.JWKSCacheTTL + cfg.JWKSCacheMaxAgeHeader)

	cache, err := startCache(ctx, cfg, logger, metricsRecorder)
	if err != nil {
		return finish(err)
	}

	if !cfg.LeaderElectionEnabled {
		state.SetLeader(true)
		metricsRecorder.SetLeader(true)
	}

	handler, err := oidc.NewHandler(cfg.IssuerURL, cfg.DiscoveryMaxAgeHeader, cache, state.PublicReady, logger)
	if err != nil {
		return finish(fmt.Errorf("handler: %w", err))
	}
	handler.MetricsHandler = metricsRecorder.Handler()
	handler.Live = state.Live
	handler.Ready = func() bool { return state.Ready(cache.Ready()) }
	handler.LeaderReady = func() bool { return state.LeaderReady(cache.Ready()) }

	runnerCfg, err := buildRunnerConfig(cfg, handler, func(v bool) {
		state.SetPublicReady(v)
		metricsRecorder.SetPublicReady(v)
	}, logger, metricsRecorder)
	if err != nil {
		return finish(err)
	}

	factory := tsrunner.NewRealServerFactory(tsrunner.RealServerConfig{
		Hostname:    cfg.TSHostname,
		StateSecret: cfg.TSStateSecret,
		Logger:      logger,
	})
	minter := &tsrunner.OAuthMinter{
		ClientID:     cfg.TSAPIClientID,
		ClientSecret: cfg.TSAPIClientSecret,
		Tags:         []string{cfg.TSTag},
		Logger:       logger,
		Metrics:      metricsRecorder,
	}

	publicRunner := func(runCtx context.Context) error {
		return tsrunner.Run(runCtx, runnerCfg, factory, minter)
	}
	processRunner := publicRunner
	if cfg.LeaderElectionEnabled {
		processRunner = func(runCtx context.Context) error {
			return runWithLeaderElection(runCtx, cfg, state, logger, metricsRecorder, leader.Run, publicRunner)
		}
	}

	return finish(runProcessWithHealthServer(ctx, stop, cfg, handler, runnerCfg.HTTPTimeouts, logger, metricsRecorder, processRunner))
}

// startCache builds the HTTP fetcher, primes the JWKS cache synchronously so
// startup fails fast on misconfiguration, and launches the background refresh
// goroutine.
func startCache(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	recorder *metrics.Metrics,
) (*oidc.Cache, error) {
	fetcher, err := oidc.NewHTTPFetcher(
		oidc.DefaultJWKSUpstreamURL,
		oidc.DefaultSATokenPath,
		oidc.DefaultSACAPath,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("jwks fetcher: %w", err)
	}

	cache := oidc.NewCache(fetcher, cfg.JWKSCacheTTL, cfg.JWKSCacheMaxAgeHeader, logger, recorder)

	primeCtx, cancelPrime := context.WithTimeout(ctx, cfg.StartupFetchTimeout)
	defer cancelPrime()
	if primeErr := cache.Prime(primeCtx); primeErr != nil {
		return nil, fmt.Errorf("jwks prime: %w", primeErr)
	}

	go cache.Run(ctx)
	return cache, nil
}

func buildRunnerConfig(
	cfg *config.Config,
	handler *oidc.Handler,
	setPublicReady func(bool),
	logger *slog.Logger,
	recorder *metrics.Metrics,
) (tsrunner.Config, error) {
	allowlist := netx.Middleware(netx.AllowlistConfig{
		Enabled: cfg.SourceIPAllowlistEnabled,
		CIDRs:   cfg.SourceIPAllowlistCIDRs,
	}, logger)
	wrapped := netx.AuditMiddleware(logger, recorder)(allowlist(handler.ServeMux()))

	issuerURL, err := url.Parse(cfg.IssuerURL)
	if err != nil {
		return tsrunner.Config{}, fmt.Errorf("parse issuer URL: %w", err)
	}

	return tsrunner.Config{
		Handler:            wrapped,
		FunnelAddr:         cfg.FunnelAddr,
		HTTPTimeouts:       tsrunner.DefaultHTTPTimeouts(),
		ConnContext:        netx.ConnContext,
		StartTimeout:       cfg.TSStartTimeout,
		ShutdownTimeout:    cfg.ShutdownTimeout,
		PollInterval:       cfg.TSStatusPollInterval,
		Logger:             logger,
		Metrics:            recorder,
		SetPublicReady:     setPublicReady,
		ExpectedIssuerHost: issuerURL.Hostname(),
	}, nil
}

// runProcessWithHealthServer runs the health server and the provided process
// concurrently, waits for the first to exit, and then drives a graceful
// shutdown of the other.
func runProcessWithHealthServer(
	ctx context.Context,
	stop context.CancelFunc,
	cfg *config.Config,
	handler *oidc.Handler,
	timeouts tsrunner.HTTPTimeouts,
	logger *slog.Logger,
	recorder *metrics.Metrics,
	processRunner func(context.Context) error,
) error {
	healthSrv, healthErrCh, err := startHealthServer(ctx, cfg, handler, timeouts, logger, recorder)
	if err != nil {
		return err
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- processRunner(ctx)
	}()

	var runErr error
	healthErrConsumed := false
	select {
	case runErr = <-runErrCh:
	case serveErr := <-healthErrCh:
		healthErrConsumed = true
		if serveErr != nil {
			runErr = serveErr
		}
		stop()
		runErr = firstErr(runErr, <-runErrCh)
	}

	stop()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	shutdownErr := healthSrv.Shutdown(shutdownCtx)
	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		runErr = firstErr(runErr, fmt.Errorf("health shutdown: %w", shutdownErr))
	}
	if !healthErrConsumed {
		runErr = firstErr(runErr, <-healthErrCh)
	}
	return runErr
}

func startHealthServer(
	ctx context.Context,
	cfg *config.Config,
	handler *oidc.Handler,
	timeouts tsrunner.HTTPTimeouts,
	logger *slog.Logger,
	recorder *metrics.Metrics,
) (*http.Server, <-chan error, error) {
	var listenCfg net.ListenConfig
	ln, err := listenCfg.Listen(ctx, "tcp", cfg.HealthAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("health listen %s: %w", cfg.HealthAddr, err)
	}
	srv := &http.Server{
		Handler:           handler.HealthMux(),
		ReadHeaderTimeout: timeouts.ReadHeaderTimeout,
		ReadTimeout:       timeouts.ReadTimeout,
		WriteTimeout:      timeouts.WriteTimeout,
		IdleTimeout:       timeouts.IdleTimeout,
		MaxHeaderBytes:    timeouts.MaxHeaderBytes,
	}
	errCh := make(chan error, 1)
	go func() {
		if recorder != nil {
			recorder.RecordHealthServerStart()
		}
		logx.Info(ctx, logger, "health_http", "health_server_start", "health server started",
			slog.String("addr", cfg.HealthAddr),
		)
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("health server: %w", serveErr)
			return
		}
		errCh <- nil
	}()
	return srv, errCh, nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func bootstrapLogger() *slog.Logger {
	format := logx.FormatJSON
	if parsed, err := logx.ParseFormat(os.Getenv("LOG_FORMAT")); err == nil {
		format = parsed
	}
	logger, err := logx.NewLogger(os.Stderr, format, &slog.HandlerOptions{Level: slog.LevelInfo})
	if err != nil {
		return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return logger
}

func processErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, errLeadershipLost):
		return "leadership_lost"
	default:
		return "startup_failed"
	}
}

func processErrorSummary(err error) string {
	switch processErrorKind(err) {
	case "context_canceled":
		return "context canceled"
	case "deadline_exceeded":
		return "deadline exceeded"
	case "leadership_lost":
		return "leadership lost"
	default:
		return "startup failed"
	}
}
