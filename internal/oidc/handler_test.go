package oidc

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubProvider struct {
	body  []byte
	cc    string
	ready bool
}

func (s *stubProvider) Current() ([]byte, string) { return s.body, s.cc }
func (s *stubProvider) Ready() bool               { return s.ready }

func newTestHandler(t *testing.T, p JWKSProvider, publicReady func() bool) *Handler {
	t.Helper()
	h, err := NewHandler(
		"https://oidc.example.ts.net",
		3600*time.Second,
		p,
		publicReady,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestHandler_Discovery(t *testing.T) {
	p := &stubProvider{body: []byte(`{"keys":[]}`), cc: "public, max-age=60", ready: true}
	h := newTestHandler(t, p, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header = %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	want, _ := Render("https://oidc.example.ts.net")
	if !bytes.Equal(body, want) {
		t.Errorf("body mismatch")
	}
}

func TestHandler_JWKS_Ready(t *testing.T) {
	p := &stubProvider{body: []byte(`{"keys":[{"kid":"k1"}]}`), cc: "public, max-age=60", ready: true}
	h := newTestHandler(t, p, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/openid/v1/jwks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/jwk-set+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"keys":[{"kid":"k1"}]}` {
		t.Errorf("body = %s", body)
	}
}

func TestHandler_JWKS_NotReady(t *testing.T) {
	p := &stubProvider{ready: false}
	h := newTestHandler(t, p, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/openid/v1/jwks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestHandler_Health(t *testing.T) {
	p := &stubProvider{ready: false}
	publicReady := false
	h := newTestHandler(t, p, func() bool { return publicReady })
	srv := httptest.NewServer(h.HealthMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("not-ready status = %d", resp.StatusCode)
	}

	p.ready = true
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("jwks-ready/public-not-ready status = %d", resp.StatusCode)
	}

	publicReady = true
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ready status = %d", resp.StatusCode)
	}
}

func TestHandler_PostReturns405(t *testing.T) {
	h := newTestHandler(t, &stubProvider{ready: true}, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/.well-known/openid-configuration", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d (want 405)", resp.StatusCode)
	}
}

func TestHandler_UnknownPathReturns404(t *testing.T) {
	h := newTestHandler(t, &stubProvider{ready: true}, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestHandler_PublicMuxDoesNotExposeHealth(t *testing.T) {
	h := newTestHandler(t, &stubProvider{ready: true}, nil)
	srv := httptest.NewServer(h.ServeMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d (want 404)", resp.StatusCode)
	}
}

func TestHandler_HEAD(t *testing.T) {
	p := &stubProvider{body: []byte(`{"keys":[]}`), cc: "public, max-age=60", ready: true}
	h := newTestHandler(t, p, nil)
	publicSrv := httptest.NewServer(h.ServeMux())
	defer publicSrv.Close()
	healthSrv := httptest.NewServer(h.HealthMux())
	defer healthSrv.Close()

	for _, target := range []struct {
		base string
		path string
	}{
		{base: publicSrv.URL, path: "/.well-known/openid-configuration"},
		{base: publicSrv.URL, path: "/openid/v1/jwks"},
		{base: healthSrv.URL, path: "/healthz"},
	} {
		req, _ := http.NewRequest(http.MethodHead, target.base+target.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HEAD %s: %v", target.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s: status = %d", target.path, resp.StatusCode)
		}
	}
}

func TestHandler_LogSecretLeakCanary(t *testing.T) {
	const secret = "supersecret-do-not-leak"

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h, err := NewHandler(
		"https://oidc.example.ts.net",
		60*time.Second,
		&stubProvider{
			body:  []byte(`{"keys":[{"kid":"k","n":"shouldnotleak"}]}`),
			cc:    "public, max-age=60",
			ready: true,
		},
		nil,
		logger,
	)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	publicSrv := httptest.NewServer(h.ServeMux())
	defer publicSrv.Close()
	healthSrv := httptest.NewServer(h.HealthMux())
	defer healthSrv.Close()

	for _, target := range []struct {
		base string
		path string
	}{
		{base: publicSrv.URL, path: "/.well-known/openid-configuration"},
		{base: publicSrv.URL, path: "/openid/v1/jwks"},
		{base: healthSrv.URL, path: "/healthz"},
	} {
		resp, gerr := http.Get(target.base + target.path)
		if gerr != nil {
			t.Fatalf("GET %s: %v", target.path, gerr)
		}
		resp.Body.Close()
	}

	if bytes.Contains(buf.Bytes(), []byte(secret)) {
		t.Errorf("log buffer contains secret literal")
	}
	if bytes.Contains(buf.Bytes(), []byte(`"n":"`)) {
		t.Errorf("log buffer contains JWKS n field")
	}
}
