// Package netx contains small networking helpers used by the binary:
// extracting the public client IP from a Tailscale Funnel connection and
// gating requests by a CIDR allowlist.
package netx

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/netip"

	"tailscale.com/ipn"
)

type srcKey struct{}

// SrcFromContext returns the public client address captured by ConnContext,
// if any. The returned address is the real client IP delivered by Tailscale
// Funnel, not the relay address.
func SrcFromContext(ctx context.Context) (netip.AddrPort, bool) {
	v, ok := ctx.Value(srcKey{}).(netip.AddrPort)
	return v, ok
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
				logger.Warn("source-ip allowlist: missing source address; denying")
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
			logger.Warn("source-ip allowlist: address not allowed", "src", addr.String())
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}
