package oidc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	// DefaultJWKSUpstreamURL is the only upstream JWKS endpoint the service
	// uses in production.
	DefaultJWKSUpstreamURL = "https://kubernetes.default.svc/openid/v1/jwks"
	// DefaultSATokenPath is the in-cluster projected service-account token.
	// #nosec G101 -- filesystem path to a projected token file, not a secret literal
	DefaultSATokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	// DefaultSACAPath is the in-cluster projected Kubernetes CA bundle.
	DefaultSACAPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	fetchClientTimeout   = 10 * time.Second
	fetchTLSHandshake    = 5 * time.Second
	fetchIdleConnTimeout = 90 * time.Second
	fetchExpectContinue  = 1 * time.Second
	fetchMaxIdleConns    = 4
)

type fetchErrorKind string

const (
	fetchErrBuildRequest fetchErrorKind = "request_build"
	fetchErrUpstream     fetchErrorKind = "upstream_request"
	fetchErrUpstreamCode fetchErrorKind = "upstream_status"
	fetchErrReadBody     fetchErrorKind = "body_read"
	fetchErrBodyTooLarge fetchErrorKind = "body_too_large"
	fetchErrJWKSInvalid  fetchErrorKind = "jwks_invalid"
	fetchErrTokenRead    fetchErrorKind = "token_read"
	fetchErrTokenEmpty   fetchErrorKind = "token_empty"
)

type fetchError struct {
	kind          fetchErrorKind
	url           string
	statusCode    int
	bodySizeBytes int
	err           error
}

func (e *fetchError) Error() string {
	switch e.kind {
	case fetchErrBuildRequest:
		return fmt.Sprintf("build request: %v", e.err)
	case fetchErrUpstream:
		return fmt.Sprintf("fetch %s: %v", e.url, e.err)
	case fetchErrUpstreamCode:
		return fmt.Sprintf("fetch %s: status %d", e.url, e.statusCode)
	case fetchErrReadBody:
		return fmt.Sprintf("read body: %v", e.err)
	case fetchErrBodyTooLarge:
		return fmt.Sprintf("response body exceeds %d bytes", e.bodySizeBytes)
	case fetchErrJWKSInvalid:
		if e.err == nil {
			return "jwks validation failed"
		}
		return e.err.Error()
	case fetchErrTokenRead:
		return fmt.Sprintf("read token %q: %v", e.url, e.err)
	case fetchErrTokenEmpty:
		return "token file is empty"
	default:
		if e.err != nil {
			return e.err.Error()
		}
		return "fetch failed"
	}
}

func (e *fetchError) Unwrap() error { return e.err }

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
	// #nosec G304 -- paths are fixed in production and parameterized here only for tests/composition
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
		return nil, &fetchError{kind: fetchErrBuildRequest, url: f.url, err: err}
	}
	req.Header.Set("Accept", "application/jwk-set+json, application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, &fetchError{kind: fetchErrUpstream, url: f.url, err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &fetchError{kind: fetchErrUpstreamCode, url: f.url, statusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxJWKSBodyBytes+1))
	if err != nil {
		return nil, &fetchError{kind: fetchErrReadBody, url: f.url, err: err}
	}
	if len(body) > MaxJWKSBodyBytes {
		return nil, &fetchError{kind: fetchErrBodyTooLarge, url: f.url, bodySizeBytes: len(body)}
	}

	jwks, err := ParseAndValidate(body)
	if err != nil {
		return nil, &fetchError{kind: fetchErrJWKSInvalid, url: f.url, err: err}
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
	// expected to be the in-cluster service account token mount during
	// production startup. We re-read it on every request to honor kubelet
	// projected-token rotation.
	// #nosec G304,G703 -- path is fixed in production and parameterized here only for tests/composition
	tok, err := os.ReadFile(t.tokenPath)
	if err != nil {
		return nil, &fetchError{kind: fetchErrTokenRead, url: t.tokenPath, err: err}
	}
	tok = trimTrailingNewline(tok)
	if len(tok) == 0 {
		return nil, &fetchError{kind: fetchErrTokenEmpty, url: t.tokenPath}
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
