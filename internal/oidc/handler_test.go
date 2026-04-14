package oidc

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/metrics"
	"github.com/meigma/k8s-aws-oidc/internal/netx"
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

func TestHandler_MetricsExposedOnlyOnHealthMux(t *testing.T) {
	h := newTestHandler(t, &stubProvider{ready: true}, nil)
	recorder := metrics.New(time.Minute)
	h.MetricsHandler = recorder.Handler()

	publicSrv := httptest.NewServer(h.ServeMux())
	defer publicSrv.Close()
	healthSrv := httptest.NewServer(h.HealthMux())
	defer healthSrv.Close()

	resp, err := http.Get(healthSrv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health metrics status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "oidc_proxy_process_start_time_seconds") {
		t.Fatalf("metrics body missing process_start_time metric")
	}

	resp, err = http.Get(publicSrv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET public /metrics: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("public metrics status = %d, want 404", resp.StatusCode)
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

func TestHandler_PublicAuditEvents(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		path              string
		provider          *stubProvider
		allowlist         netx.AllowlistConfig
		src               string
		wantStatus        int
		wantRoute         string
		wantDecision      string
		wantSourcePresent bool
		wantSourceIP      string
	}{
		{
			name:              "discovery served",
			method:            http.MethodGet,
			path:              "/.well-known/openid-configuration",
			provider:          &stubProvider{body: []byte(`{"keys":[]}`), cc: "public, max-age=60", ready: true},
			wantStatus:        http.StatusOK,
			wantRoute:         "discovery",
			wantDecision:      "served",
			wantSourcePresent: false,
		},
		{
			name:              "jwks served",
			method:            http.MethodGet,
			path:              "/openid/v1/jwks",
			provider:          &stubProvider{body: []byte(`{"keys":[{"kid":"k1"}]}`), cc: "public, max-age=60", ready: true},
			wantStatus:        http.StatusOK,
			wantRoute:         "jwks",
			wantDecision:      "served",
			wantSourcePresent: false,
		},
		{
			name:              "jwks not ready",
			method:            http.MethodGet,
			path:              "/openid/v1/jwks",
			provider:          &stubProvider{ready: false},
			wantStatus:        http.StatusServiceUnavailable,
			wantRoute:         "jwks",
			wantDecision:      "jwks_not_ready",
			wantSourcePresent: false,
		},
		{
			name:   "allowlist missing source",
			method: http.MethodGet,
			path:   "/.well-known/openid-configuration",
			provider: &stubProvider{
				body:  []byte(`{"keys":[]}`),
				cc:    "public, max-age=60",
				ready: true,
			},
			allowlist:         netx.AllowlistConfig{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
			wantStatus:        http.StatusForbidden,
			wantRoute:         "discovery",
			wantDecision:      "denied_missing_source",
			wantSourcePresent: false,
		},
		{
			name:   "allowlist blocked cidr",
			method: http.MethodGet,
			path:   "/.well-known/openid-configuration",
			provider: &stubProvider{
				body:  []byte(`{"keys":[]}`),
				cc:    "public, max-age=60",
				ready: true,
			},
			allowlist:         netx.AllowlistConfig{Enabled: true, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
			src:               "8.8.8.8:42000",
			wantStatus:        http.StatusForbidden,
			wantRoute:         "discovery",
			wantDecision:      "denied_cidr",
			wantSourcePresent: true,
			wantSourceIP:      "8.8.8.8",
		},
		{
			name:              "unknown path",
			method:            http.MethodGet,
			path:              "/nope",
			provider:          &stubProvider{ready: true},
			wantStatus:        http.StatusNotFound,
			wantRoute:         "unknown",
			wantDecision:      "not_found",
			wantSourcePresent: false,
		},
		{
			name:              "method not allowed",
			method:            http.MethodPost,
			path:              "/.well-known/openid-configuration",
			provider:          &stubProvider{ready: true},
			wantStatus:        http.StatusMethodNotAllowed,
			wantRoute:         "discovery",
			wantDecision:      "method_not_allowed",
			wantSourcePresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			recorder := metrics.New(time.Minute)
			h, err := NewHandler(
				"https://oidc.example.ts.net",
				60*time.Second,
				tt.provider,
				nil,
				logger,
			)
			if err != nil {
				t.Fatalf("NewHandler: %v", err)
			}
			h.MetricsHandler = recorder.Handler()
			wrapped := netx.AuditMiddleware(logger, recorder)(netx.Middleware(tt.allowlist, logger)(h.ServeMux()))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.src != "" {
				req = req.WithContext(netx.ContextWithSrc(req.Context(), netip.MustParseAddrPort(tt.src)))
			}
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			records := decodeJSONRecords(t, &buf)
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			record := records[0]
			if got := record["event"]; got != "http_request" {
				t.Fatalf("event = %v", got)
			}
			if got := record["component"]; got != "public_http" {
				t.Fatalf("component = %v", got)
			}
			if got := record["route"]; got != tt.wantRoute {
				t.Fatalf("route = %v", got)
			}
			if got := record["path"]; got != tt.path {
				t.Fatalf("path = %v", got)
			}
			if got := record["method"]; got != tt.method {
				t.Fatalf("method = %v", got)
			}
			if got := int(record["status"].(float64)); got != tt.wantStatus {
				t.Fatalf("status attr = %d", got)
			}
			if got := record["decision"]; got != tt.wantDecision {
				t.Fatalf("decision = %v", got)
			}
			if got := record["source_present"]; got != tt.wantSourcePresent {
				t.Fatalf("source_present = %v", got)
			}
			if tt.wantSourceIP == "" {
				if _, ok := record["source_ip"]; ok {
					t.Fatalf("unexpected source_ip = %v", record["source_ip"])
				}
			} else if got := record["source_ip"]; got != tt.wantSourceIP {
				t.Fatalf("source_ip = %v", got)
			}
			if _, ok := record["latency_ms"]; !ok {
				t.Fatal("missing latency_ms")
			}

			metricsBody := scrapeMetrics(t, recorder.Handler())
			wantCounter := `oidc_proxy_http_requests_total{decision="` + tt.wantDecision + `",method="` + tt.method + `",route="` + tt.wantRoute + `",status_code="` + strconv.Itoa(tt.wantStatus) + `"} 1`
			if !strings.Contains(metricsBody, wantCounter) {
				t.Fatalf("metrics missing counter %q\n%s", wantCounter, metricsBody)
			}
			wantHistogram := `oidc_proxy_http_request_duration_seconds_count{decision="` + tt.wantDecision + `",method="` + tt.method + `",route="` + tt.wantRoute + `"} 1`
			if !strings.Contains(metricsBody, wantHistogram) {
				t.Fatalf("metrics missing histogram count %q\n%s", wantHistogram, metricsBody)
			}
			if strings.Contains(metricsBody, tt.path) {
				t.Fatalf("metrics body leaked raw path %q", tt.path)
			}
			if tt.wantSourceIP != "" && strings.Contains(metricsBody, tt.wantSourceIP) {
				t.Fatalf("metrics body leaked source ip %q", tt.wantSourceIP)
			}
		})
	}
}

func scrapeMetrics(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics scrape status = %d", rr.Code)
	}
	return rr.Body.String()
}

func decodeJSONRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal log: %v", err)
		}
		out = append(out, record)
	}
	return out
}
