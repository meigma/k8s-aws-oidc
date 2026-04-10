package tsrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"tailscale.com/ipn/store/kubestore"
	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

// RealServerConfig configures the production tsnetServer adapter.
type RealServerConfig struct {
	Hostname    string
	StateSecret string
	Logger      *slog.Logger
}

// NewRealServerFactory returns a ServerFactory that builds production tsnet
// servers backed by the kubestore state Secret. Each call constructs a fresh
// *tsnet.Server so the runner's reauth path can replace identity cleanly.
func NewRealServerFactory(cfg RealServerConfig) ServerFactory {
	return func() tsnetServer {
		return &realServer{cfg: cfg}
	}
}

// realServer adapts *tsnet.Server to the tsnetServer interface.
//
// We deliberately defer constructing the underlying *tsnet.Server until Start
// is called: the kubestore client wants to reach the kube apiserver, and any
// failure should land inside Start (which the runner already retries) rather
// than at factory time (which is unrecoverable from the runner's perspective).
type realServer struct {
	cfg     RealServerConfig
	authKey string
	srv     *tsnet.Server
}

func (r *realServer) Start(ctx context.Context) error {
	if r.srv == nil {
		st, err := kubestore.New(logger.Discard, r.cfg.StateSecret)
		if err != nil {
			return fmt.Errorf("kubestore: %w", err)
		}
		r.srv = &tsnet.Server{
			Hostname:  r.cfg.Hostname,
			Store:     st,
			AuthKey:   r.authKey,
			Logf:      slogToTailscaleLogf(r.cfg.Logger),
			Ephemeral: false,
		}
	}
	if _, err := r.srv.Up(ctx); err != nil {
		return err
	}
	return nil
}

func (r *realServer) ListenFunnel(network, addr string) (net.Listener, error) {
	if r.srv == nil {
		return nil, errors.New("tsnet server not started")
	}
	return r.srv.ListenFunnel(network, addr)
}

func (r *realServer) CertDomains(ctx context.Context) ([]string, error) {
	if r.srv == nil {
		return nil, nil
	}
	lc, err := r.srv.LocalClient()
	if err != nil {
		return nil, err
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	return st.CertDomains, nil
}

func (r *realServer) BackendState(ctx context.Context) (string, error) {
	if r.srv == nil {
		return "", nil
	}
	lc, err := r.srv.LocalClient()
	if err != nil {
		return "", err
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return "", err
	}
	return st.BackendState, nil
}

func (r *realServer) SetAuthKey(key string) {
	r.authKey = key
	if r.srv != nil {
		r.srv.AuthKey = key
	}
}

func (r *realServer) Close() error {
	if r.srv == nil {
		return nil
	}
	err := r.srv.Close()
	r.srv = nil
	return err
}

// slogToTailscaleLogf adapts an [*slog.Logger] to Tailscale's logger.Logf
// printf-style signature. Levels are flattened to Debug because tsnet's
// internal logs are noisy and not interesting at higher levels.
func slogToTailscaleLogf(l *slog.Logger) logger.Logf {
	if l == nil {
		return logger.Discard
	}
	return func(format string, args ...any) {
		l.Debug(fmt.Sprintf(format, args...), slog.String("source", "tsnet"))
	}
}

// Compile-time assertion: realServer implements tsnetServer.
var _ tsnetServer = (*realServer)(nil)
