package tsrunner

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// BackendStateRunning is the BackendState string reported by tsnet's
// LocalClient when the node is fully connected.
const BackendStateRunning = "Running"

// BackendStateNeedsLogin is the BackendState string reported when tsnet has
// no usable identity and needs an auth key.
const BackendStateNeedsLogin = "NeedsLogin"

const (
	defaultStartTimeout    = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultPollInterval    = 15 * time.Second

	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 10 * time.Second
	httpWriteTimeout      = 10 * time.Second
	httpIdleTimeout       = 60 * time.Second
	httpMaxHeaderBytes    = 8 * 1024

	backoffBase   = 100 * time.Millisecond
	backoffMax    = 30 * time.Second
	backoffFactor = 2
)

// tsnetServer is the small subset of *tsnet.Server we use, kept narrow so
// tests can substitute a fake without dragging in the real Tailscale stack.
type tsnetServer interface {
	// Start brings the node up. It must respect ctx cancellation; the runner
	// always wraps the call in a per-attempt timeout context.
	Start(ctx context.Context) error
	// ListenFunnel returns the public TLS listener for the node.
	ListenFunnel(network, addr string) (net.Listener, error)
	// BackendState returns the current ipn backend state string. The runner
	// only cares about "Running" and "NeedsLogin".
	BackendState(ctx context.Context) (string, error)
	// SetAuthKey configures the auth key to use on the next Start. It must
	// be safe to call only before Start (the real adapter recreates the
	// underlying tsnet.Server when reauth is required).
	SetAuthKey(key string)
	// Close releases all resources held by the node.
	Close() error
}

// ServerFactory builds a fresh tsnetServer. The runner calls it on every
// reauth so that the underlying tsnet.Server's identity state can be reset
// cleanly without trying to mutate a running instance.
type ServerFactory func() tsnetServer

// HTTPTimeouts collects the [http.Server] timeout knobs the runner needs.
type HTTPTimeouts struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// DefaultHTTPTimeouts returns the hardened timeouts the runner uses by
// default. ReadHeaderTimeout is the most important — it bounds Slowloris.
func DefaultHTTPTimeouts() HTTPTimeouts {
	return HTTPTimeouts{
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

// Config configures Run.
type Config struct {
	Handler         http.Handler
	FunnelAddr      string
	HTTPTimeouts    HTTPTimeouts
	ConnContext     func(context.Context, net.Conn) context.Context
	StartTimeout    time.Duration
	ShutdownTimeout time.Duration
	PollInterval    time.Duration
	Logger          *slog.Logger
}

func (c *Config) defaults() {
	if c.HTTPTimeouts == (HTTPTimeouts{}) {
		c.HTTPTimeouts = DefaultHTTPTimeouts()
	}
	if c.FunnelAddr == "" {
		c.FunnelAddr = ":443"
	}
	if c.StartTimeout == 0 {
		c.StartTimeout = defaultStartTimeout
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	if c.PollInterval == 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Run brings up tsnet, serves the configured HTTP handler over Funnel, and
// reactively re-mints an auth key if the backend state ever flips to
// NeedsLogin. It blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config, factory ServerFactory, minter AuthKeyMinter) error {
	cfg.defaults()
	logger := cfg.Logger

	var server tsnetServer
	defer func() {
		if server != nil {
			_ = server.Close()
		}
	}()

	backoff := backoffState{base: backoffBase, max: backoffMax}

	for {
		if ctx.Err() != nil {
			return nil
		}
		if server == nil {
			server = factory()
		}

		state, startErr := startAndProbe(ctx, server, cfg.StartTimeout)
		next := decideNext(state, startErr)

		switch next {
		case actionServe:
			backoff.reset()
			if err := serveOnce(ctx, cfg, server); err != nil {
				logger.ErrorContext(ctx, "serve loop ended", "error", err.Error())
			}
			_ = server.Close()
			server = nil
		case actionReauth:
			server = handleReauth(ctx, logger, factory, minter, server, &backoff)
		case actionRetry:
			logger.ErrorContext(ctx, "tsnet start failed", "error", errString(startErr), "state", state)
			if err := backoff.sleep(ctx); err != nil {
				return nil
			}
			_ = server.Close()
			server = nil
		}
	}
}

type loopAction int

const (
	actionServe loopAction = iota
	actionReauth
	actionRetry
)

func startAndProbe(ctx context.Context, server tsnetServer, timeout time.Duration) (string, error) {
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startErr := server.Start(startCtx)
	state, _ := server.BackendState(ctx)
	return state, startErr
}

func decideNext(state string, startErr error) loopAction {
	if state == BackendStateNeedsLogin {
		return actionReauth
	}
	if startErr != nil || state != BackendStateRunning {
		return actionRetry
	}
	return actionServe
}

// handleReauth mints a fresh auth key, closes the current server, and returns
// a new tsnetServer with the key applied. On mint failure it backs off and
// returns the existing server unchanged so the outer loop will retry.
func handleReauth(
	ctx context.Context,
	logger *slog.Logger,
	factory ServerFactory,
	minter AuthKeyMinter,
	server tsnetServer,
	backoff *backoffState,
) tsnetServer {
	logger.InfoContext(ctx, "tsnet needs login; minting fresh auth key")
	key, mintErr := minter.Mint(ctx)
	if mintErr != nil {
		logger.ErrorContext(ctx, "auth key mint failed", "error", mintErr.Error())
		_ = backoff.sleep(ctx)
		return server
	}
	backoff.reset()
	_ = server.Close()
	fresh := factory()
	fresh.SetAuthKey(key)
	return fresh
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// serveOnce wires up the [http.Server] on a Funnel listener and blocks until
// ctx is cancelled or the watcher detects a flip out of Running.
func serveOnce(ctx context.Context, cfg Config, server tsnetServer) error {
	logger := cfg.Logger

	ln, listenErr := server.ListenFunnel("tcp", cfg.FunnelAddr)
	if listenErr != nil {
		return listenErr
	}

	srv := &http.Server{
		Handler:           cfg.Handler,
		ReadHeaderTimeout: cfg.HTTPTimeouts.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPTimeouts.ReadTimeout,
		WriteTimeout:      cfg.HTTPTimeouts.WriteTimeout,
		IdleTimeout:       cfg.HTTPTimeouts.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTPTimeouts.MaxHeaderBytes,
		ConnContext:       cfg.ConnContext,
		ErrorLog:          nil,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	flipped := watchForLoginNeeded(watchCtx, server, cfg.PollInterval, logger)

	select {
	case <-ctx.Done():
		shutdown(ctx, srv, cfg.ShutdownTimeout, logger)
		<-serveErr
		return nil
	case <-flipped:
		logger.WarnContext(ctx, "tsnet backend flipped to NeedsLogin; restarting")
		shutdown(ctx, srv, cfg.ShutdownTimeout, logger)
		<-serveErr
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// watchForLoginNeeded polls BackendState and returns a channel that closes
// the first time the backend reports NeedsLogin. The channel never receives
// a value; callers select on it to detect the transition.
func watchForLoginNeeded(
	ctx context.Context,
	server tsnetServer,
	interval time.Duration,
	logger *slog.Logger,
) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		defer close(out)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				state, err := server.BackendState(ctx)
				if err != nil {
					logger.WarnContext(ctx, "backend state poll failed", "error", err.Error())
					continue
				}
				if state == BackendStateNeedsLogin {
					return
				}
			}
		}
	}()
	return out
}

func shutdown(ctx context.Context, srv *http.Server, timeout time.Duration, logger *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.WarnContext(ctx, "http shutdown error", "error", err.Error())
	}
}

// backoffState implements exponential backoff with a cap. It is intentionally
// tiny — no jitter, no decorrelated retries — because the runner is its only
// caller and the surrounding state machine is the simpler thing to test.
type backoffState struct {
	base    time.Duration
	max     time.Duration
	current atomic.Int64
}

func (b *backoffState) sleep(ctx context.Context) error {
	d := time.Duration(b.current.Load())
	if d == 0 {
		d = b.base
	}
	next := min(d*backoffFactor, b.max)
	b.current.Store(int64(next))

	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (b *backoffState) reset() { b.current.Store(0) }
