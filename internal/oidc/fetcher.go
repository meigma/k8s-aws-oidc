package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	fetchClientTimeout   = 10 * time.Second
	fetchTLSHandshake    = 5 * time.Second
	fetchIdleConnTimeout = 90 * time.Second
	fetchExpectContinue  = 1 * time.Second
	fetchMaxIdleConns    = 4
)

// Fetcher fetches and validates a JWKS from an upstream source.
type Fetcher interface {
	Fetch(ctx context.Context) (*JWKS, error)
}

// HTTPFetcher fetches a JWKS from a Kubernetes apiserver-style endpoint
// using a service-account bearer token (re-read on every request to honor
// kubelet projected-token rotation) and a pinned in-cluster CA bundle.
type HTTPFetcher struct {
	url       string
	tokenPath string
	client    *http.Client
	logger    *slog.Logger
}

// NewHTTPFetcher constructs an HTTPFetcher. The CA at caPath is loaded once
// at construction; the token at tokenPath is re-read on every request.
func NewHTTPFetcher(url, tokenPath, caPath string, logger *slog.Logger) (*HTTPFetcher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA %q: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA %q: no PEM blocks", caPath)
	}

	base := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          fetchMaxIdleConns,
		IdleConnTimeout:       fetchIdleConnTimeout,
		TLSHandshakeTimeout:   fetchTLSHandshake,
		ExpectContinueTimeout: fetchExpectContinue,
	}

	client := &http.Client{
		Transport: &bearerTokenTransport{base: base, tokenPath: tokenPath},
		Timeout:   fetchClientTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &HTTPFetcher{
		url:       url,
		tokenPath: tokenPath,
		client:    client,
		logger:    logger,
	}, nil
}

// Fetch retrieves the JWKS from the configured upstream and returns the
// validated, re-emit-ready value.
func (f *HTTPFetcher) Fetch(ctx context.Context) (*JWKS, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/jwk-set+json, application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", f.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", f.url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxJWKSBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > MaxJWKSBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", MaxJWKSBodyBytes)
	}

	jwks, err := ParseAndValidate(body)
	if err != nil {
		return nil, err
	}
	return jwks, nil
}

// bearerTokenTransport re-reads the bearer token from disk on every request,
// so kubelet projected-token rotation is honored without caching stale tokens.
type bearerTokenTransport struct {
	base      http.RoundTripper
	tokenPath string
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// tokenPath is set at construction from a config-supplied path that is
	// expected to be the in-cluster service account token mount. We re-read
	// it on every request to honor kubelet projected-token rotation.
	//nolint:gosec // G703: tokenPath is operator-controlled config, not request input
	tok, err := os.ReadFile(t.tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read token %q: %w", t.tokenPath, err)
	}
	tok = trimTrailingNewline(tok)
	if len(tok) == 0 {
		return nil, errors.New("token file is empty")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+string(tok))
	return t.base.RoundTrip(clone)
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
