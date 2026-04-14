package config

import (
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
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
	if cfg.JWKSCacheTTL != 60*time.Second {
		t.Errorf("JWKSCacheTTL default = %s", cfg.JWKSCacheTTL)
	}
	if cfg.TSStartTimeout != 30*time.Second {
		t.Errorf("TSStartTimeout default = %s", cfg.TSStartTimeout)
	}
	if cfg.HealthAddr != ":8080" {
		t.Errorf("HealthAddr default = %q", cfg.HealthAddr)
	}
	if cfg.DiscoveryMaxAgeHeader != 3600*time.Second {
		t.Errorf("DiscoveryMaxAgeHeader default = %s", cfg.DiscoveryMaxAgeHeader)
	}
	if cfg.FunnelAddr != ":443" {
		t.Errorf("FunnelAddr default = %q", cfg.FunnelAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel default = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != logx.FormatJSON {
		t.Errorf("LogFormat default = %q", cfg.LogFormat)
	}
	if cfg.SourceIPAllowlistEnabled {
		t.Errorf("SourceIPAllowlistEnabled default = true")
	}
	if cfg.LeaderElectionEnabled {
		t.Errorf("LeaderElectionEnabled default = true")
	}
	if cfg.LeaderElectionLeaseDuration != 15*time.Second {
		t.Errorf("LeaderElectionLeaseDuration default = %s", cfg.LeaderElectionLeaseDuration)
	}
	if cfg.LeaderElectionRenewDeadline != 10*time.Second {
		t.Errorf("LeaderElectionRenewDeadline default = %s", cfg.LeaderElectionRenewDeadline)
	}
	if cfg.LeaderElectionRetryPeriod != 2*time.Second {
		t.Errorf("LeaderElectionRetryPeriod default = %s", cfg.LeaderElectionRetryPeriod)
	}
}

func TestLoad_AllExplicit(t *testing.T) {
	setProdEnv(t)
	t.Setenv("JWKS_CACHE_TTL", "30s")
	t.Setenv("JWKS_CACHE_MAX_AGE_HEADER", "30s")
	t.Setenv("DISCOVERY_MAX_AGE_HEADER", "120s")
	t.Setenv("STARTUP_FETCH_TIMEOUT", "5s")
	t.Setenv("TS_START_TIMEOUT", "7s")
	t.Setenv("SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("TS_STATUS_POLL_INTERVAL", "10s")
	t.Setenv("FUNNEL_ADDR", ":8443")
	t.Setenv("HEALTH_ADDR", ":18080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("SOURCE_IP_ALLOWLIST_ENABLED", "true")
	t.Setenv("SOURCE_IP_ALLOWLIST_CIDRS", "10.0.0.0/8, 192.168.0.0/16")
	t.Setenv("LEADER_ELECTION_ENABLED", "true")
	t.Setenv("LEADER_ELECTION_LEASE_NAME", "oidc-lease")
	t.Setenv("LEADER_ELECTION_NAMESPACE", "oidc-system")
	t.Setenv("LEADER_ELECTION_IDENTITY", "oidc-pod-0")
	t.Setenv("LEADER_ELECTION_LEASE_DURATION", "21s")
	t.Setenv("LEADER_ELECTION_RENEW_DEADLINE", "14s")
	t.Setenv("LEADER_ELECTION_RETRY_PERIOD", "4s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.JWKSCacheTTL != 30*time.Second {
		t.Errorf("JWKSCacheTTL = %s", cfg.JWKSCacheTTL)
	}
	if cfg.TSStartTimeout != 7*time.Second {
		t.Errorf("TSStartTimeout = %s", cfg.TSStartTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.LogFormat != logx.FormatText {
		t.Errorf("LogFormat = %q", cfg.LogFormat)
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
	if cfg.HealthAddr != ":18080" {
		t.Errorf("HealthAddr = %q", cfg.HealthAddr)
	}
	if !cfg.LeaderElectionEnabled {
		t.Errorf("LeaderElectionEnabled = false")
	}
	if cfg.LeaderElectionLeaseName != "oidc-lease" {
		t.Errorf("LeaderElectionLeaseName = %q", cfg.LeaderElectionLeaseName)
	}
	if cfg.LeaderElectionNamespace != "oidc-system" {
		t.Errorf("LeaderElectionNamespace = %q", cfg.LeaderElectionNamespace)
	}
	if cfg.LeaderElectionIdentity != "oidc-pod-0" {
		t.Errorf("LeaderElectionIdentity = %q", cfg.LeaderElectionIdentity)
	}
	if cfg.LeaderElectionLeaseDuration != 21*time.Second {
		t.Errorf("LeaderElectionLeaseDuration = %s", cfg.LeaderElectionLeaseDuration)
	}
	if cfg.LeaderElectionRenewDeadline != 14*time.Second {
		t.Errorf("LeaderElectionRenewDeadline = %s", cfg.LeaderElectionRenewDeadline)
	}
	if cfg.LeaderElectionRetryPeriod != 4*time.Second {
		t.Errorf("LeaderElectionRetryPeriod = %s", cfg.LeaderElectionRetryPeriod)
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
		{"trailing_slash", "https://oidc.example.ts.net/", "must not contain a path"},
		{"path_segment", "https://oidc.example.ts.net/cluster-a", "must not contain a path"},
		{"deep_path", "https://oidc.example.ts.net/foo/bar", "must not contain a path"},
		{"explicit_port", "https://oidc.example.ts.net:443", "must not contain an explicit port"},
		{"query_string", "https://oidc.example.ts.net?x=1", "query string"},
		{"fragment", "https://oidc.example.ts.net#frag", "fragment"},
		{"userinfo", "https://user:pass@oidc.example.ts.net", "userinfo"},
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

func TestLoad_AllowlistEnabledRequiresCIDRs(t *testing.T) {
	setProdEnv(t)
	t.Setenv("SOURCE_IP_ALLOWLIST_ENABLED", "true")
	t.Setenv("SOURCE_IP_ALLOWLIST_CIDRS", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for enabled allowlist without CIDRs")
	}
	if !strings.Contains(err.Error(), "SOURCE_IP_ALLOWLIST_CIDRS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_RemovedEnvVarsRejected(t *testing.T) {
	cases := []string{
		"JWKS_UPSTREAM_URL",
		"TS_API_BASE_URL",
		"JWKS_UPSTREAM_TOKEN_PATH",
		"JWKS_UPSTREAM_CA_PATH",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			setProdEnv(t)
			t.Setenv(name, "value")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error when %s is set", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not mention %s", err.Error(), name)
			}
		})
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

func TestLoad_RejectsNonPositiveDurations(t *testing.T) {
	cases := []struct {
		envVar string
		value  string
	}{
		{"JWKS_CACHE_TTL", "-30s"},
		{"JWKS_CACHE_TTL", "0s"},
		{"JWKS_CACHE_MAX_AGE_HEADER", "-1s"},
		{"JWKS_CACHE_MAX_AGE_HEADER", "0s"},
		{"DISCOVERY_MAX_AGE_HEADER", "-1s"},
		{"DISCOVERY_MAX_AGE_HEADER", "0s"},
		{"STARTUP_FETCH_TIMEOUT", "-5s"},
		{"STARTUP_FETCH_TIMEOUT", "0s"},
		{"TS_START_TIMEOUT", "-5s"},
		{"TS_START_TIMEOUT", "0s"},
		{"SHUTDOWN_TIMEOUT", "-5s"},
		{"SHUTDOWN_TIMEOUT", "0s"},
		{"TS_STATUS_POLL_INTERVAL", "-1s"},
		{"TS_STATUS_POLL_INTERVAL", "0s"},
		{"LEADER_ELECTION_LEASE_DURATION", "-1s"},
		{"LEADER_ELECTION_LEASE_DURATION", "0s"},
		{"LEADER_ELECTION_RENEW_DEADLINE", "-1s"},
		{"LEADER_ELECTION_RENEW_DEADLINE", "0s"},
		{"LEADER_ELECTION_RETRY_PERIOD", "-1s"},
		{"LEADER_ELECTION_RETRY_PERIOD", "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.envVar+"_"+tc.value, func(t *testing.T) {
			setProdEnv(t)
			t.Setenv(tc.envVar, tc.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for %s=%s", tc.envVar, tc.value)
			}
			if !strings.Contains(err.Error(), tc.envVar) {
				t.Errorf("error %q does not mention %s", err.Error(), tc.envVar)
			}
		})
	}
}

func TestLoad_LeaderElectionEnabledRequiresLeaseSettings(t *testing.T) {
	setProdEnv(t)
	t.Setenv("LEADER_ELECTION_ENABLED", "true")
	t.Setenv("LEADER_ELECTION_LEASE_NAME", "")
	t.Setenv("LEADER_ELECTION_NAMESPACE", "")
	t.Setenv("LEADER_ELECTION_IDENTITY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing leader election fields")
	}
	if !strings.Contains(err.Error(), "LEADER_ELECTION_LEASE_NAME") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_LeaderElectionDerivesNamespaceAndIdentityFromPodEnv(t *testing.T) {
	setProdEnv(t)
	t.Setenv("LEADER_ELECTION_ENABLED", "true")
	t.Setenv("LEADER_ELECTION_LEASE_NAME", "oidc-lease")
	t.Setenv("POD_NAMESPACE", "oidc-system")
	t.Setenv("POD_NAME", "oidc-pod-0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LeaderElectionNamespace != "oidc-system" {
		t.Fatalf("LeaderElectionNamespace = %q", cfg.LeaderElectionNamespace)
	}
	if cfg.LeaderElectionIdentity != "oidc-pod-0" {
		t.Fatalf("LeaderElectionIdentity = %q", cfg.LeaderElectionIdentity)
	}
}

func TestLoad_LeaderElectionRejectsInvalidDurationRelationships(t *testing.T) {
	tests := []struct {
		name          string
		leaseDuration string
		renewDeadline string
		retryPeriod   string
		want          string
	}{
		{
			name:          "lease not greater than renew",
			leaseDuration: "10s",
			renewDeadline: "10s",
			retryPeriod:   "2s",
			want:          "LEADER_ELECTION_LEASE_DURATION",
		},
		{
			name:          "renew not greater than retry",
			leaseDuration: "15s",
			renewDeadline: "2s",
			retryPeriod:   "2s",
			want:          "LEADER_ELECTION_RENEW_DEADLINE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setProdEnv(t)
			t.Setenv("LEADER_ELECTION_ENABLED", "true")
			t.Setenv("LEADER_ELECTION_LEASE_NAME", "oidc-lease")
			t.Setenv("LEADER_ELECTION_NAMESPACE", "oidc-system")
			t.Setenv("LEADER_ELECTION_IDENTITY", "oidc-pod-0")
			t.Setenv("LEADER_ELECTION_LEASE_DURATION", tt.leaseDuration)
			t.Setenv("LEADER_ELECTION_RENEW_DEADLINE", tt.renewDeadline)
			t.Setenv("LEADER_ELECTION_RETRY_PERIOD", tt.retryPeriod)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestLoad_InvalidLogFormat(t *testing.T) {
	setProdEnv(t)
	t.Setenv("LOG_FORMAT", "pretty")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid log format")
	}
	if !strings.Contains(err.Error(), "LOG_FORMAT") {
		t.Fatalf("unexpected error: %v", err)
	}
}
