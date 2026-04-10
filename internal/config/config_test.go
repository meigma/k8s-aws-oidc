package config

import (
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func setProdEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ISSUER_URL", "https://oidc.example.ts.net")
	t.Setenv("TS_HOSTNAME", "oidc")
	t.Setenv("TS_STATE_SECRET", "tsnet-state")
	t.Setenv("TS_API_CLIENT_ID", "client-id")
	t.Setenv("TS_API_CLIENT_SECRET", "client-secret")
	t.Setenv("TS_TAG", "tag:oidc-proxy")
}

func TestLoad_HappyPath_Defaults(t *testing.T) {
	setProdEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.IssuerURL != "https://oidc.example.ts.net" {
		t.Errorf("IssuerURL = %q", cfg.IssuerURL)
	}
	if cfg.JWKSUpstreamURL != "https://kubernetes.default.svc/openid/v1/jwks" {
		t.Errorf("JWKSUpstreamURL default = %q", cfg.JWKSUpstreamURL)
	}
	if cfg.JWKSUpstreamTokenPath != "/var/run/secrets/kubernetes.io/serviceaccount/token" {
		t.Errorf("JWKSUpstreamTokenPath default = %q", cfg.JWKSUpstreamTokenPath)
	}
	if cfg.JWKSUpstreamCAPath != "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt" {
		t.Errorf("JWKSUpstreamCAPath default = %q", cfg.JWKSUpstreamCAPath)
	}
	if cfg.JWKSCacheTTL != 60*time.Second {
		t.Errorf("JWKSCacheTTL default = %s", cfg.JWKSCacheTTL)
	}
	if cfg.DiscoveryMaxAgeHeader != 3600*time.Second {
		t.Errorf("DiscoveryMaxAgeHeader default = %s", cfg.DiscoveryMaxAgeHeader)
	}
	if cfg.FunnelAddr != ":443" {
		t.Errorf("FunnelAddr default = %q", cfg.FunnelAddr)
	}
	if cfg.TSAPIBaseURL != "https://api.tailscale.com" {
		t.Errorf("TSAPIBaseURL default = %q", cfg.TSAPIBaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel default = %v", cfg.LogLevel)
	}
	if cfg.SourceIPAllowlistEnabled {
		t.Errorf("SourceIPAllowlistEnabled default = true")
	}
}

func TestLoad_AllExplicit(t *testing.T) {
	setProdEnv(t)
	t.Setenv("TS_API_BASE_URL", "https://api.example.com")
	t.Setenv("JWKS_UPSTREAM_URL", "https://example/openid/v1/jwks")
	t.Setenv("JWKS_UPSTREAM_TOKEN_PATH", "/tmp/token")
	t.Setenv("JWKS_UPSTREAM_CA_PATH", "/tmp/ca.crt")
	t.Setenv("JWKS_CACHE_TTL", "30s")
	t.Setenv("JWKS_CACHE_MAX_AGE_HEADER", "30s")
	t.Setenv("DISCOVERY_MAX_AGE_HEADER", "120s")
	t.Setenv("STARTUP_FETCH_TIMEOUT", "5s")
	t.Setenv("SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("TS_STATUS_POLL_INTERVAL", "10s")
	t.Setenv("FUNNEL_ADDR", ":8443")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SOURCE_IP_ALLOWLIST_ENABLED", "true")
	t.Setenv("SOURCE_IP_ALLOWLIST_CIDRS", "10.0.0.0/8, 192.168.0.0/16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.JWKSCacheTTL != 30*time.Second {
		t.Errorf("JWKSCacheTTL = %s", cfg.JWKSCacheTTL)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if !cfg.SourceIPAllowlistEnabled {
		t.Errorf("SourceIPAllowlistEnabled = false")
	}
	if len(cfg.SourceIPAllowlistCIDRs) != 2 {
		t.Fatalf("SourceIPAllowlistCIDRs len = %d", len(cfg.SourceIPAllowlistCIDRs))
	}
	if cfg.SourceIPAllowlistCIDRs[0] != netip.MustParsePrefix("10.0.0.0/8") {
		t.Errorf("CIDR[0] = %v", cfg.SourceIPAllowlistCIDRs[0])
	}
	if cfg.FunnelAddr != ":8443" {
		t.Errorf("FunnelAddr = %q", cfg.FunnelAddr)
	}
}

func TestLoad_RequiredVars(t *testing.T) {
	required := []string{
		"ISSUER_URL",
		"TS_HOSTNAME",
		"TS_STATE_SECRET",
		"TS_API_CLIENT_ID",
		"TS_API_CLIENT_SECRET",
		"TS_TAG",
	}
	for _, name := range required {
		t.Run("missing_"+name, func(t *testing.T) {
			setProdEnv(t)
			t.Setenv(name, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for missing %s", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not mention %s", err.Error(), name)
			}
		})
	}
}

func TestLoad_InvalidIssuerURL(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		errSub string
	}{
		{"http_scheme", "http://oidc.example.ts.net", "https"},
		{"trailing_slash", "https://oidc.example.ts.net/", "trailing slash"},
		{"missing_host", "https://", "missing host"},
		{"garbage", "::not a url", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setProdEnv(t)
			t.Setenv("ISSUER_URL", tc.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for %q", tc.value)
			}
			if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestLoad_InvalidTSTag(t *testing.T) {
	setProdEnv(t)
	t.Setenv("TS_TAG", "oidc-proxy")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for tag without tag: prefix")
	}
}

func TestLoad_InvalidCIDR(t *testing.T) {
	setProdEnv(t)
	t.Setenv("SOURCE_IP_ALLOWLIST_ENABLED", "true")
	t.Setenv("SOURCE_IP_ALLOWLIST_CIDRS", "not-a-cidr")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestLoad_CacheTTLTooLow(t *testing.T) {
	setProdEnv(t)
	t.Setenv("JWKS_CACHE_TTL", "1s")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for ttl below 5s")
	}
}

func TestLoad_DevModeRejectsTSVars(t *testing.T) {
	cases := []string{
		"TS_HOSTNAME",
		"TS_STATE_SECRET",
		"TS_API_CLIENT_ID",
		"TS_API_CLIENT_SECRET",
		"TS_TAG",
		"TS_API_BASE_URL",
		"FUNNEL_ADDR",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ISSUER_URL", "https://oidc.example.ts.net")
			t.Setenv("DEV_LISTEN_ADDR", "127.0.0.1:8080")
			t.Setenv(name, "value")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error when DEV_LISTEN_ADDR is set with %s", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not mention %s", err.Error(), name)
			}
		})
	}
}

func TestLoad_DevModeMinimal(t *testing.T) {
	t.Setenv("ISSUER_URL", "https://oidc.example.ts.net")
	t.Setenv("DEV_LISTEN_ADDR", "127.0.0.1:8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DevListenAddr != "127.0.0.1:8080" {
		t.Errorf("DevListenAddr = %q", cfg.DevListenAddr)
	}
}
