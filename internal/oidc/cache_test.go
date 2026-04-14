package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/metrics"
)

type fakeFetcher struct {
	mu     sync.Mutex
	body   *JWKS
	err    error
	calls  atomic.Int32
	onCall func(call int32)
}

func (f *fakeFetcher) Fetch(_ context.Context) (*JWKS, error) {
	n := f.calls.Add(1)
	if f.onCall != nil {
		f.onCall(n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	cp := *f.body
	return &cp, nil
}

func (f *fakeFetcher) setBody(b *JWKS) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = b
}

func (f *fakeFetcher) setErr(e error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = e
}

func mustValidJWKS(t *testing.T, kid string) *JWKS {
	t.Helper()
	return &JWKS{Keys: []JWK{{Kid: kid, Kty: "RSA", Alg: "RS256", Use: "sig", N: validN, E: validE}}}
}

func TestCache_Prime_Success(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	c := NewCache(f, time.Minute, 60*time.Second, nil, nil)
	if c.Ready() {
		t.Fatal("Ready before Prime")
	}
	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if !c.Ready() {
		t.Fatal("not Ready after Prime")
	}
	body, cc := c.Current()
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	if cc != "public, max-age=60" {
		t.Errorf("cache-control = %q", cc)
	}
}

func TestCache_CurrentBoundsFreshnessByAge(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	now := time.Unix(1_700_000_000, 0)
	c := NewCache(f, 20*time.Second, 60*time.Second, nil, nil)
	c.now = func() time.Time { return now }

	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	now = now.Add(75 * time.Second)
	_, cc := c.Current()
	if cc != "public, max-age=5" {
		t.Fatalf("cache-control = %q, want public, max-age=5", cc)
	}
}

func TestCache_Prime_Failure(t *testing.T) {
	f := &fakeFetcher{}
	f.setErr(errors.New("boom"))

	c := NewCache(f, time.Minute, 60*time.Second, nil, nil)
	err := c.Prime(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if c.Ready() {
		t.Error("Ready after failed Prime")
	}
}

func TestCache_Run_RefreshUpdates(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	c := NewCache(f, 20*time.Millisecond, 60*time.Second, nil, nil)
	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	first, _ := c.Current()

	// Switch to a different body so the next refresh produces different bytes.
	f.setBody(mustValidJWKS(t, "k2"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, _ := c.Current()
		if string(body) != string(first) {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("cache did not update after refresh tick")
}

func TestCache_Run_RefreshFailureRetainsStale(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	c := NewCache(f, 20*time.Millisecond, 60*time.Second, nil, nil)
	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	wantBody, _ := c.Current()

	f.setErr(errors.New("upstream down"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	<-done

	gotBody, _ := c.Current()
	if string(gotBody) != string(wantBody) {
		t.Fatalf("stale not retained\ngot:  %s\nwant: %s", gotBody, wantBody)
	}
	if !c.Ready() {
		t.Error("Ready flipped to false after refresh failure")
	}
}

func TestCache_ExpiresAfterBoundedStaleWindow(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	now := time.Unix(1_700_000_000, 0)
	c := NewCache(f, 20*time.Second, 60*time.Second, nil, nil)
	c.now = func() time.Time { return now }

	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	now = now.Add(81 * time.Second)
	body, cc := c.Current()
	if body != nil {
		t.Fatalf("body = %q, want nil", string(body))
	}
	if cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
	if c.Ready() {
		t.Fatal("Ready = true after stale window elapsed")
	}
}

func TestCache_ConcurrentReadDuringRefresh(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	c := NewCache(f, 5*time.Millisecond, 60*time.Second, nil, nil)
	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	go c.Run(ctx)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for ctx.Err() == nil {
				_, _ = c.Current()
				_ = c.Ready()
			}
		})
	}
	wg.Wait()
}

func TestCache_Run_ExitsOnContextCancel(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k1"))

	c := NewCache(f, 50*time.Millisecond, 60*time.Second, nil, nil)
	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit within 1s of cancel")
	}
}

func TestCache_LogsLifecycleEvents(t *testing.T) {
	f := &fakeFetcher{}
	f.setBody(mustValidJWKS(t, "k2"))

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	recorder := metrics.New(60*time.Second + 20*time.Millisecond)
	c := NewCache(f, 20*time.Millisecond, 60*time.Second, logger, recorder)

	if err := c.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	f.setErr(errors.New("supersecret-do-not-log"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	c.Run(ctx)

	records := decodeCacheJSONLogs(t, &buf)
	events := eventSet(records)
	for _, want := range []string{
		"jwks_prime_success",
		"jwks_refresh_failure",
		"jwks_serving_stale",
	} {
		if _, ok := events[want]; !ok {
			t.Fatalf("missing event %q in %v", want, events)
		}
	}
	prime := events["jwks_prime_success"]
	if got := int(prime["kid_count"].(float64)); got != 1 {
		t.Fatalf("kid_count = %d", got)
	}
	if strings.Contains(buf.String(), "supersecret-do-not-log") {
		t.Fatal("log buffer contains raw fetch error")
	}
	if strings.Contains(buf.String(), `"n":"`) {
		t.Fatal("log buffer contains jwks modulus")
	}
	metricsBody := scrapeCacheMetrics(t, recorder)
	for _, want := range []string{
		`oidc_proxy_jwks_prime_total{result="success"} 1`,
		`oidc_proxy_jwks_refresh_total{error_kind="fetch_failed",result="failure"}`,
		`oidc_proxy_jwks_serving_stale_total{error_kind="fetch_failed"}`,
		"oidc_proxy_jwks_ready 1",
		"oidc_proxy_jwks_kid_count 1",
	} {
		if !strings.Contains(metricsBody, want) {
			t.Fatalf("metrics body missing %q\n%s", want, metricsBody)
		}
	}
}

func TestCache_PrimeFailure_LogIsSanitized(t *testing.T) {
	f := &fakeFetcher{}
	f.setErr(errors.New("Bearer very-secret-token"))

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	recorder := metrics.New(61 * time.Second)
	c := NewCache(f, time.Minute, 60*time.Second, logger, recorder)

	if err := c.Prime(context.Background()); err == nil {
		t.Fatal("expected prime error")
	}

	records := decodeCacheJSONLogs(t, &buf)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0]
	if got := record["event"]; got != "jwks_prime_failure" {
		t.Fatalf("event = %v", got)
	}
	if strings.Contains(buf.String(), "very-secret-token") {
		t.Fatal("prime failure log contains secret")
	}
	metricsBody := scrapeCacheMetrics(t, recorder)
	if !strings.Contains(metricsBody, `oidc_proxy_jwks_prime_total{result="failure"} 1`) {
		t.Fatalf("metrics body missing prime failure counter\n%s", metricsBody)
	}
	if strings.Contains(metricsBody, "very-secret-token") {
		t.Fatal("metrics body contains secret")
	}
}

func decodeCacheJSONLogs(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
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

func eventSet(records []map[string]any) map[string]map[string]any {
	events := make(map[string]map[string]any, len(records))
	for _, record := range records {
		event, _ := record["event"].(string)
		events[event] = record
	}
	return events
}

func scrapeCacheMetrics(t *testing.T, recorder *metrics.Metrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics scrape status = %d", rr.Code)
	}
	return rr.Body.String()
}
