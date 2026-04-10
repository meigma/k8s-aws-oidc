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
	"sync/atomic"
	"syscall"

	"github.com/meigma/k8s-aws-oidc/internal/config"
	"github.com/meigma/k8s-aws-oidc/internal/netx"
	"github.com/meigma/k8s-aws-oidc/internal/oidc"
	"github.com/meigma/k8s-aws-oidc/internal/tsrunner"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cache, err := startCache(ctx, cfg, logger)
	if err != nil {
		return err
	}

	var publicReady atomic.Bool
	handler, err := oidc.NewHandler(cfg.IssuerURL, cfg.DiscoveryMaxAgeHeader, cache, publicReady.Load, logger)
	if err != nil {
		return fmt.Errorf("handler: %w", err)
	}

	runnerCfg, err := buildRunnerConfig(cfg, handler, publicReady.Store, logger)
	if err != nil {
		return err
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
	}

	logger.InfoContext(ctx, "starting tsnet runner",
		"hostname", cfg.TSHostname,
		"funnel_addr", cfg.FunnelAddr,
		"issuer", cfg.IssuerURL,
	)

	return serveAll(ctx, stop, cfg, handler, runnerCfg, factory, minter, logger)
}

// startCache builds the HTTP fetcher, primes the JWKS cache synchronously so
// startup fails fast on misconfiguration, and launches the background refresh
// goroutine.
func startCache(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*oidc.Cache, error) {
	fetcher, err := oidc.NewHTTPFetcher(
		oidc.DefaultJWKSUpstreamURL,
		oidc.DefaultSATokenPath,
		oidc.DefaultSACAPath,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("jwks fetcher: %w", err)
	}

	cache := oidc.NewCache(fetcher, cfg.JWKSCacheTTL, cfg.JWKSCacheMaxAgeHeader, logger)

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
) (tsrunner.Config, error) {
	allowlist := netx.Middleware(netx.AllowlistConfig{
		Enabled: cfg.SourceIPAllowlistEnabled,
		CIDRs:   cfg.SourceIPAllowlistCIDRs,
	}, logger)
	wrapped := allowlist(handler.ServeMux())

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
		SetPublicReady:     setPublicReady,
		ExpectedIssuerHost: issuerURL.Hostname(),
	}, nil
}

// serveAll runs the health server and the tsnet runner concurrently, waits
// for the first to exit, and then drives a graceful shutdown of the other.
func serveAll(
	ctx context.Context,
	stop context.CancelFunc,
	cfg *config.Config,
	handler *oidc.Handler,
	runnerCfg tsrunner.Config,
	factory tsrunner.ServerFactory,
	minter tsrunner.AuthKeyMinter,
	logger *slog.Logger,
) error {
	healthSrv, healthErrCh, err := startHealthServer(ctx, cfg, handler, runnerCfg.HTTPTimeouts, logger)
	if err != nil {
		return err
	}

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- tsrunner.Run(ctx, runnerCfg, factory, minter)
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
		logger.InfoContext(ctx, "starting health server", "addr", cfg.HealthAddr)
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
