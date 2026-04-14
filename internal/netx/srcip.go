// Package netx contains small networking helpers used by the binary:
// extracting the public client IP from a Tailscale Funnel connection and
// gating requests by a CIDR allowlist.
package netx

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
	"tailscale.com/ipn"
)

type srcKey struct{}
type auditKey struct{}

type auditState struct {
	decision string
}

const (
	decisionServed           = "served"
	decisionDeniedMissingSrc = "denied_missing_source"
	decisionDeniedCIDR       = "denied_cidr"
	decisionJWKSNotReady     = "jwks_not_ready"
	decisionMethodNotAllowed = "method_not_allowed"
	decisionNotFound         = "not_found"
)

// SrcFromContext returns the public client address captured by ConnContext,
// if any. The returned address is the real client IP delivered by Tailscale
// Funnel, not the relay address.
func SrcFromContext(ctx context.Context) (netip.AddrPort, bool) {
	v, ok := ctx.Value(srcKey{}).(netip.AddrPort)
	return v, ok
}

// ContextWithSrc stores a source address in a context. It is used by tests
// and by any callers that need to simulate a Funnel request without a live
// Tailscale listener.
func ContextWithSrc(ctx context.Context, addr netip.AddrPort) context.Context {
	return context.WithValue(ctx, srcKey{}, addr)
}

// ConnContext is wired into [http.Server].ConnContext. It unwraps the
// [*tls.Conn] -> *ipn.FunnelConn chain and stores the public client address
// in the request context for later retrieval by SrcFromContext.
//
// The unwrap chain is verified against tailscale source: ListenFunnel wraps
// the underlying funnel listener (which yields *ipn.FunnelConn from Accept)
// in a [*tls.Listener] (tsnet/tsnet.go:1384), so the conn passed to
// ConnContext is a [*tls.Conn] whose NetConn returns the *ipn.FunnelConn.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	tc, ok := c.(*tls.Conn)
	if !ok {
		return ctx
	}
	fc, ok := tc.NetConn().(*ipn.FunnelConn)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, srcKey{}, fc.Src)
}

func withAuditState(r *http.Request) (*http.Request, *auditState) {
	state := &auditState{}
	ctx := context.WithValue(r.Context(), auditKey{}, state)
	return r.WithContext(ctx), state
}

func setDecision(ctx context.Context, decision string) {
	state, ok := ctx.Value(auditKey{}).(*auditState)
	if !ok || state == nil {
		return
	}
	state.decision = decision
}

// MarkJWKSNotReady records a JWKS-specific audit decision on the request.
func MarkJWKSNotReady(ctx context.Context) {
	setDecision(ctx, decisionJWKSNotReady)
}

// AuditMiddleware records one structured audit event for every public request.
func AuditMiddleware(logger *slog.Logger, recorder *metrics.Metrics) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			r, state := withAuditState(r)
			rec := &auditResponseWriter{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status()
			route := routeForPath(r.URL.Path)
			decision := state.decision
			if decision == "" {
				decision = decisionFromStatus(status)
			}

			attrs := []slog.Attr{
				slog.String("route", route),
				slog.String("path", r.URL.Path),
				slog.String("method", r.Method),
				slog.Int("status", status),
				slog.Int64("latency_ms", time.Since(start).Milliseconds()),
				slog.Bool("source_present", false),
				slog.String("decision", decision),
			}
			if src, ok := SrcFromContext(r.Context()); ok {
				attrs[5] = slog.Bool("source_present", true)
				attrs = append(attrs, slog.String("source_ip", src.Addr().String()))
			}
			if recorder != nil {
				recorder.ObserveHTTPRequest(route, r.Method, decision, status, time.Since(start))
			}
			logx.Info(r.Context(), logger, "public_http", "http_request", "public request handled", attrs...)
		})
	}
}

// AllowlistConfig configures the source-IP allowlist middleware.
type AllowlistConfig struct {
	Enabled bool
	CIDRs   []netip.Prefix
}

// Middleware returns a middleware that gates requests by the configured
// allowlist. When disabled it is a pass-through; when enabled it returns 403
// if the request has no captured source address or the source is not in any
// configured CIDR (fail-closed).
func Middleware(cfg AllowlistConfig, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	cidrs := cfg.CIDRs
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			src, ok := SrcFromContext(r.Context())
			if !ok {
				setDecision(r.Context(), decisionDeniedMissingSrc)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			addr := src.Addr()
			for _, p := range cidrs {
				if p.Contains(addr) {
					next.ServeHTTP(w, r)
					return
				}
			}
			setDecision(r.Context(), decisionDeniedCIDR)
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}

func routeForPath(path string) string {
	switch path {
	case "/.well-known/openid-configuration":
		return "discovery"
	case "/openid/v1/jwks":
		return "jwks"
	default:
		return "unknown"
	}
}

func decisionFromStatus(status int) string {
	switch status {
	case http.StatusMethodNotAllowed:
		return decisionMethodNotAllowed
	case http.StatusNotFound:
		return decisionNotFound
	default:
		return decisionServed
	}
}

type auditResponseWriter struct {
	http.ResponseWriter
	code      int
	wroteCode bool
}

func (w *auditResponseWriter) WriteHeader(code int) {
	if !w.wroteCode {
		w.code = code
		w.wroteCode = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteCode {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *auditResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	rf, ok := w.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(w.ResponseWriter, r)
	}
	if !w.wroteCode {
		w.WriteHeader(http.StatusOK)
	}
	return rf.ReadFrom(r)
}

func (w *auditResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *auditResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *auditResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *auditResponseWriter) status() int {
	if w.wroteCode {
		return w.code
	}
	return http.StatusOK
}
