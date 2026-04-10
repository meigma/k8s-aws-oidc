package oidc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeCAPEM(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	cert := srv.Certificate()
	block := &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}
	if err := os.WriteFile(caPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return caPath
}

//nolint:unparam // value parameter is constant in current callers but kept for clarity at call sites
func writeTokenFile(t *testing.T, value string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "token")
	if err := os.WriteFile(p, []byte(value), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return p
}

func TestHTTPFetcher_HappyPath(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer t1" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(validJWKS(t))
	}))
	defer srv.Close()

	caPath := writeCAPEM(t, srv)
	tokenPath := writeTokenFile(t, "t1")

	f, err := NewHTTPFetcher(srv.URL, tokenPath, caPath, nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}

	jwks, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(jwks.Keys) != 1 || jwks.Keys[0].Kid != "k1" {
		t.Errorf("unexpected JWKS: %+v", jwks)
	}
}

func TestHTTPFetcher_TokenReReadOnEachRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(validJWKS(t))
	}))
	defer srv.Close()

	caPath := writeCAPEM(t, srv)
	tokenPath := writeTokenFile(t, "t1")

	f, err := NewHTTPFetcher(srv.URL, tokenPath, caPath, nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}

	if _, fetchErr := f.Fetch(context.Background()); fetchErr != nil {
		t.Fatalf("first Fetch: %v", fetchErr)
	}
	if writeErr := os.WriteFile(tokenPath, []byte("t2"), 0o600); writeErr != nil {
		t.Fatalf("rewrite token: %v", writeErr)
	}
	if _, fetchErr := f.Fetch(context.Background()); fetchErr != nil {
		t.Fatalf("second Fetch: %v", fetchErr)
	}

	if len(seen) != 2 {
		t.Fatalf("seen = %v", seen)
	}
	if seen[0] != "Bearer t1" {
		t.Errorf("first request: %q", seen[0])
	}
	if seen[1] != "Bearer t2" {
		t.Errorf("second request: %q", seen[1])
	}
}

func TestHTTPFetcher_WrongCAFails(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(validJWKS(t))
	}))
	defer srv.Close()

	// Pin the fetcher to an unrelated, freshly-generated self-signed CA so
	// the upstream's built-in httptest cert does not chain to it.
	caPath := writeUnrelatedCA(t)
	tokenPath := writeTokenFile(t, "t1")

	f, err := NewHTTPFetcher(srv.URL, tokenPath, caPath, nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}
	_, err = f.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected TLS verification error")
	}
}

func writeUnrelatedCA(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if writeErr := os.WriteFile(
		caPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		0o600,
	); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	return caPath
}

func TestHTTPFetcher_RejectsNon200(t *testing.T) {
	cases := []int{
		http.StatusInternalServerError,
		http.StatusNotFound,
		http.StatusUnauthorized,
	}
	for _, code := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()
			f, err := NewHTTPFetcher(srv.URL, writeTokenFile(t, "t1"), writeCAPEM(t, srv), nil)
			if err != nil {
				t.Fatalf("NewHTTPFetcher: %v", err)
			}
			_, err = f.Fetch(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestHTTPFetcher_RejectsRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	f, err := NewHTTPFetcher(srv.URL, writeTokenFile(t, "t1"), writeCAPEM(t, srv), nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}
	_, err = f.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected non-200 error from redirect")
	}
}

func TestHTTPFetcher_BodyTooLarge(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		// Stream a body that exceeds the cap.
		buf := bytes.Repeat([]byte("a"), MaxJWKSBodyBytes+10)
		_, _ = io.Copy(w, bytes.NewReader(buf))
	}))
	defer srv.Close()

	f, err := NewHTTPFetcher(srv.URL, writeTokenFile(t, "t1"), writeCAPEM(t, srv), nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}
	_, err = f.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want body-cap error, got %v", err)
	}
}

func TestHTTPFetcher_HonorsContextCancel(t *testing.T) {
	var hit atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	f, err := NewHTTPFetcher(srv.URL, writeTokenFile(t, "t1"), writeCAPEM(t, srv), nil)
	if err != nil {
		t.Fatalf("NewHTTPFetcher: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = f.Fetch(ctx)
	if err == nil {
		t.Fatal("expected ctx cancel error")
	}
}

func TestNewHTTPFetcher_BadCA(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a pem block"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewHTTPFetcher("https://example", "/tmp/token", caPath, nil)
	if err == nil {
		t.Fatal("expected CA parse error")
	}
}

// Sanity-check that [x509.NewCertPool] produces a usable pool when fed a real
// httptest cert (catches changes that break our PEM serialization helper).
func TestWriteCAPEM_Loadable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	pemBytes, err := os.ReadFile(writeCAPEM(t, srv))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("AppendCertsFromPEM failed")
	}
}

// Catch [json.Marshal] regressions in test helpers.
func TestValidJWKSHelper(t *testing.T) {
	body := validJWKS(t)
	var got JWKS
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Keys) != 1 {
		t.Fatalf("len = %d", len(got.Keys))
	}
}
