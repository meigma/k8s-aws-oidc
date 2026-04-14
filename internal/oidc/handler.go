package oidc

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/netx"
)

// JWKSProvider exposes the current cached JWKS bytes and a Cache-Control
// header value to serve alongside it.
type JWKSProvider interface {
	Current() (body []byte, cacheControl string)
	Ready() bool
}

// Handler serves the public OIDC routes and the separate health endpoint.
type Handler struct {
	Discovery       []byte
	DiscoveryMaxAge time.Duration
	JWKS            JWKSProvider
	PublicReady     func() bool
	Logger          *slog.Logger
	MetricsHandler  http.Handler
}

// NewHandler builds a Handler. issuer is rendered into the discovery doc
// once at construction time and never re-evaluated.
func NewHandler(
	issuer string,
	discoveryMaxAge time.Duration,
	jwks JWKSProvider,
	publicReady func() bool,
	logger *slog.Logger,
) (*Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if publicReady == nil {
		publicReady = func() bool { return true }
	}
	body, err := Render(issuer)
	if err != nil {
		return nil, err
	}
	return &Handler{
		Discovery:       body,
		DiscoveryMaxAge: discoveryMaxAge,
		JWKS:            jwks,
		PublicReady:     publicReady,
		Logger:          logger,
	}, nil
}

// ServeMux returns a stdlib mux with exactly the two public OIDC routes
// registered using Go 1.22+ method+path patterns.
func (h *Handler) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", h.handleDiscovery)
	mux.HandleFunc("GET /openid/v1/jwks", h.handleJWKS)
	return mux
}

// HealthMux returns a stdlib mux that serves only /healthz. It is intended to
// be bound on a separate, non-Funnel listener for Kubernetes probes.
func (h *Handler) HealthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleHealth)
	if h.MetricsHandler != nil {
		mux.Handle("GET /metrics", h.MetricsHandler)
	}
	return mux
}

func (h *Handler) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(h.DiscoveryMaxAge.Seconds())))
	_, _ = w.Write(h.Discovery)
}

func (h *Handler) handleJWKS(w http.ResponseWriter, r *http.Request) {
	body, cc := h.JWKS.Current()
	if !h.JWKS.Ready() || len(body) == 0 {
		netx.MarkJWKSNotReady(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "jwks not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", cc)
	_, _ = w.Write(body)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if !h.PublicReady() || !h.JWKS.Ready() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}
