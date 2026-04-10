package netx

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func ctxWithSrc(addr netip.AddrPort) context.Context {
	return context.WithValue(context.Background(), srcKey{}, addr)
}

func newOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_Disabled_PassThrough(t *testing.T) {
	mw := Middleware(AllowlistConfig{Enabled: false}, nil)
	h := mw(newOK())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("disabled = %d", rr.Code)
	}
}

func TestMiddleware_Enabled_Match(t *testing.T) {
	mw := Middleware(AllowlistConfig{
		Enabled: true,
		CIDRs:   []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}, nil)
	h := mw(newOK())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ctxWithSrc(netip.MustParseAddrPort("10.1.2.3:42000")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("match = %d", rr.Code)
	}
}

func TestMiddleware_Enabled_Reject(t *testing.T) {
	mw := Middleware(AllowlistConfig{
		Enabled: true,
		CIDRs:   []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}, nil)
	h := mw(newOK())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ctxWithSrc(netip.MustParseAddrPort("8.8.8.8:42000")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("reject = %d", rr.Code)
	}
}

func TestMiddleware_Enabled_MissingSrc_FailsClosed(t *testing.T) {
	mw := Middleware(AllowlistConfig{
		Enabled: true,
		CIDRs:   []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}, nil)
	h := mw(newOK())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("missing src = %d (want 403)", rr.Code)
	}
}

func TestMiddleware_Enabled_MultipleCIDRs(t *testing.T) {
	mw := Middleware(AllowlistConfig{
		Enabled: true,
		CIDRs: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("192.168.0.0/16"),
		},
	}, nil)
	h := mw(newOK())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(ctxWithSrc(netip.MustParseAddrPort("192.168.5.5:42000")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("second-cidr = %d", rr.Code)
	}
}

func TestSrcFromContext(t *testing.T) {
	addr := netip.MustParseAddrPort("1.2.3.4:5678")
	ctx := ctxWithSrc(addr)
	got, ok := SrcFromContext(ctx)
	if !ok {
		t.Fatal("not found")
	}
	if got != addr {
		t.Errorf("got %v want %v", got, addr)
	}

	if _, found := SrcFromContext(context.Background()); found {
		t.Error("empty ctx returned ok")
	}
}

// ConnContext is exercised end-to-end in the runner integration with tsnet
// (out of scope for unit tests). Here we verify it returns the input context
// unchanged for non-FunnelConn inputs (e.g., the dev plain HTTP path).
func TestConnContext_PlainNetConn_PassThrough(t *testing.T) {
	ctx := context.Background()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	got := ConnContext(ctx, a)
	if got != ctx {
		t.Error("plain net.Conn should pass through unchanged")
	}
}
