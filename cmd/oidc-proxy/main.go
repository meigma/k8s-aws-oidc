// Command oidc-proxy serves a minimal OIDC discovery + JWKS endpoint
// publicly via Tailscale Funnel, fetching the JWKS from the in-cluster
// Kubernetes apiserver.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	fetcher, err := oidc.NewHTTPFetcher(cfg.JWKSUpstreamURL, cfg.JWKSUpstreamTokenPath, cfg.JWKSUpstreamCAPath, logger)
	if err != nil {
		return fmt.Errorf("jwks fetcher: %w", err)
	}

	cache := oidc.NewCache(fetcher, cfg.JWKSCacheTTL, cfg.JWKSCacheMaxAgeHeader, logger)

	primeCtx, cancelPrime := context.WithTimeout(ctx, cfg.StartupFetchTimeout)
	if primeErr := cache.Prime(primeCtx); primeErr != nil {
		cancelPrime()
		return fmt.Errorf("jwks prime: %w", primeErr)
	}
	cancelPrime()

	go cache.Run(ctx)

	handler, err := oidc.NewHandler(cfg.IssuerURL, cfg.DiscoveryMaxAgeHeader, cache, logger)
	if err != nil {
		return fmt.Errorf("handler: %w", err)
	}

	mux := handler.ServeMux()
	allowlist := netx.Middleware(netx.AllowlistConfig{
		Enabled: cfg.SourceIPAllowlistEnabled,
		CIDRs:   cfg.SourceIPAllowlistCIDRs,
	}, logger)
	wrapped := allowlist(mux)

	if cfg.DevListenAddr != "" {
		return runDev(ctx, cfg, wrapped, logger)
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
		BaseURL:      cfg.TSAPIBaseURL,
		Logger:       logger,
	}

	runnerCfg := tsrunner.Config{
		Handler:         wrapped,
		FunnelAddr:      cfg.FunnelAddr,
		HTTPTimeouts:    tsrunner.DefaultHTTPTimeouts(),
		ConnContext:     netx.ConnContext,
		StartTimeout:    cfg.StartupFetchTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
		PollInterval:    cfg.TSStatusPollInterval,
		Logger:          logger,
	}

	logger.InfoContext(ctx, "starting tsnet runner",
		"hostname", cfg.TSHostname,
		"funnel_addr", cfg.FunnelAddr,
		"issuer", cfg.IssuerURL,
	)
	return tsrunner.Run(ctx, runnerCfg, factory, minter)
}

func runDev(ctx context.Context, cfg *config.Config, handler http.Handler, logger *slog.Logger) error {
	logger.WarnContext(ctx, "DEV MODE: serving plain HTTP, no tailnet, no TLS", "addr", cfg.DevListenAddr)

	timeouts := tsrunner.DefaultHTTPTimeouts()
	srv := &http.Server{
		Addr:              cfg.DevListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: timeouts.ReadHeaderTimeout,
		ReadTimeout:       timeouts.ReadTimeout,
		WriteTimeout:      timeouts.WriteTimeout,
		IdleTimeout:       timeouts.IdleTimeout,
		MaxHeaderBytes:    timeouts.MaxHeaderBytes,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
