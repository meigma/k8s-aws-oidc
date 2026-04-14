// Package config loads and validates the runtime configuration from environment
// variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
)

// Config is the validated runtime configuration.
type Config struct {
	IssuerURL         string
	TSHostname        string
	TSStateSecret     string
	TSAPIClientID     string
	TSAPIClientSecret string
	TSTag             string
	HealthAddr        string

	LeaderElectionEnabled       bool
	LeaderElectionLeaseName     string
	LeaderElectionNamespace     string
	LeaderElectionIdentity      string
	LeaderElectionLeaseDuration time.Duration
	LeaderElectionRenewDeadline time.Duration
	LeaderElectionRetryPeriod   time.Duration

	JWKSCacheTTL          time.Duration
	JWKSCacheMaxAgeHeader time.Duration
	DiscoveryMaxAgeHeader time.Duration

	SourceIPAllowlistEnabled bool
	SourceIPAllowlistCIDRs   []netip.Prefix

	FunnelAddr           string
	LogFormat            logx.Format
	LogLevel             slog.Level
	StartupFetchTimeout  time.Duration
	TSStartTimeout       time.Duration
	ShutdownTimeout      time.Duration
	TSStatusPollInterval time.Duration
}

type durationTarget struct {
	name         string
	defaultValue time.Duration
	target       *time.Duration
}

const (
	defaultJWKSCacheTTL         = 60 * time.Second
	defaultJWKSCacheMaxAge      = 60 * time.Second
	defaultDiscoveryMaxAge      = 3600 * time.Second
	defaultStartupFetchTimeout  = 30 * time.Second
	defaultTSStartTimeout       = 30 * time.Second
	defaultShutdownTimeout      = 10 * time.Second
	defaultTSStatusPollInterval = 15 * time.Second
	defaultLeaderLeaseDuration  = 15 * time.Second
	defaultLeaderRenewDeadline  = 10 * time.Second
	defaultLeaderRetryPeriod    = 2 * time.Second
	minJWKSCacheTTL             = 5 * time.Second
)

// removedEnvVars are no longer supported because the service must only talk
// to the in-cluster apiserver JWKS endpoint and the production Tailscale API.
//
//nolint:gochecknoglobals // immutable lookup table; clearer as a package var than recreated per call
var removedEnvVars = []string{
	"JWKS_UPSTREAM_URL",
	"TS_API_BASE_URL",
	"JWKS_UPSTREAM_TOKEN_PATH",
	"JWKS_UPSTREAM_CA_PATH",
	"DEV_LISTEN_ADDR",
}

// Load reads the configuration from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		IssuerURL:               os.Getenv("ISSUER_URL"),
		TSHostname:              os.Getenv("TS_HOSTNAME"),
		TSStateSecret:           os.Getenv("TS_STATE_SECRET"),
		TSAPIClientID:           os.Getenv("TS_API_CLIENT_ID"),
		TSAPIClientSecret:       os.Getenv("TS_API_CLIENT_SECRET"),
		TSTag:                   os.Getenv("TS_TAG"),
		HealthAddr:              envDefault("HEALTH_ADDR", ":8080"),
		FunnelAddr:              envDefault("FUNNEL_ADDR", ":443"),
		LeaderElectionEnabled:   envBool("LEADER_ELECTION_ENABLED"),
		LeaderElectionLeaseName: os.Getenv("LEADER_ELECTION_LEASE_NAME"),
		LeaderElectionNamespace: envDefault("LEADER_ELECTION_NAMESPACE", os.Getenv("POD_NAMESPACE")),
		LeaderElectionIdentity:  envDefault("LEADER_ELECTION_IDENTITY", os.Getenv("POD_NAME")),
	}

	if err := loadDurations(cfg); err != nil {
		return nil, err
	}
	if err := loadLogging(cfg); err != nil {
		return nil, err
	}
	if err := loadAllowlist(cfg); err != nil {
		return nil, err
	}

	if vErr := cfg.validate(); vErr != nil {
		return nil, vErr
	}
	return cfg, nil
}

func loadDurations(cfg *Config) error {
	settings := []durationTarget{
		{name: "JWKS_CACHE_TTL", defaultValue: defaultJWKSCacheTTL, target: &cfg.JWKSCacheTTL},
		{name: "JWKS_CACHE_MAX_AGE_HEADER", defaultValue: defaultJWKSCacheMaxAge, target: &cfg.JWKSCacheMaxAgeHeader},
		{name: "DISCOVERY_MAX_AGE_HEADER", defaultValue: defaultDiscoveryMaxAge, target: &cfg.DiscoveryMaxAgeHeader},
		{name: "STARTUP_FETCH_TIMEOUT", defaultValue: defaultStartupFetchTimeout, target: &cfg.StartupFetchTimeout},
		{name: "TS_START_TIMEOUT", defaultValue: defaultTSStartTimeout, target: &cfg.TSStartTimeout},
		{name: "SHUTDOWN_TIMEOUT", defaultValue: defaultShutdownTimeout, target: &cfg.ShutdownTimeout},
		{name: "TS_STATUS_POLL_INTERVAL", defaultValue: defaultTSStatusPollInterval, target: &cfg.TSStatusPollInterval},
		{
			name:         "LEADER_ELECTION_LEASE_DURATION",
			defaultValue: defaultLeaderLeaseDuration,
			target:       &cfg.LeaderElectionLeaseDuration,
		},
		{
			name:         "LEADER_ELECTION_RENEW_DEADLINE",
			defaultValue: defaultLeaderRenewDeadline,
			target:       &cfg.LeaderElectionRenewDeadline,
		},
		{
			name:         "LEADER_ELECTION_RETRY_PERIOD",
			defaultValue: defaultLeaderRetryPeriod,
			target:       &cfg.LeaderElectionRetryPeriod,
		},
	}

	for _, setting := range settings {
		value, err := envDuration(setting.name, setting.defaultValue)
		if err != nil {
			return err
		}
		*setting.target = value
	}
	return nil
}

func loadLogging(cfg *Config) error {
	var err error
	if cfg.LogLevel, err = parseLogLevel(envDefault("LOG_LEVEL", "info")); err != nil {
		return err
	}
	if cfg.LogFormat, err = logx.ParseFormat(envDefault("LOG_FORMAT", string(logx.FormatJSON))); err != nil {
		return err
	}
	return nil
}

func loadAllowlist(cfg *Config) error {
	cfg.SourceIPAllowlistEnabled = envBool("SOURCE_IP_ALLOWLIST_ENABLED")
	raw := os.Getenv("SOURCE_IP_ALLOWLIST_CIDRS")
	if raw == "" {
		return nil
	}
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return fmt.Errorf("SOURCE_IP_ALLOWLIST_CIDRS: invalid CIDR %q: %w", item, err)
		}
		cfg.SourceIPAllowlistCIDRs = append(cfg.SourceIPAllowlistCIDRs, prefix)
	}
	return nil
}

func (c *Config) validate() error {
	if err := validateRemovedEnvVars(); err != nil {
		return err
	}
	if err := c.validateIssuerURL(); err != nil {
		return err
	}
	if err := c.validateProdRequired(); err != nil {
		return err
	}
	if err := c.validateAllowlist(); err != nil {
		return err
	}
	if err := c.validateLeaderElection(); err != nil {
		return err
	}
	if err := c.validateDurations(); err != nil {
		return err
	}
	return nil
}

func validateRemovedEnvVars() error {
	for _, name := range removedEnvVars {
		if os.Getenv(name) != "" {
			return fmt.Errorf("%s is no longer configurable; remove it from the environment", name)
		}
	}
	return nil
}

// validateDurations rejects zero/negative values for every duration field.
// [time.NewTicker] panics on non-positive durations and Cache-Control headers
// must not be negative, so these need to fail at config-load time rather than
// crash a serving process or emit malformed responses.
func (c *Config) validateDurations() error {
	checks := []struct {
		name string
		val  time.Duration
	}{
		{"JWKS_CACHE_TTL", c.JWKSCacheTTL},
		{"JWKS_CACHE_MAX_AGE_HEADER", c.JWKSCacheMaxAgeHeader},
		{"DISCOVERY_MAX_AGE_HEADER", c.DiscoveryMaxAgeHeader},
		{"STARTUP_FETCH_TIMEOUT", c.StartupFetchTimeout},
		{"TS_START_TIMEOUT", c.TSStartTimeout},
		{"SHUTDOWN_TIMEOUT", c.ShutdownTimeout},
		{"TS_STATUS_POLL_INTERVAL", c.TSStatusPollInterval},
		{"LEADER_ELECTION_LEASE_DURATION", c.LeaderElectionLeaseDuration},
		{"LEADER_ELECTION_RENEW_DEADLINE", c.LeaderElectionRenewDeadline},
		{"LEADER_ELECTION_RETRY_PERIOD", c.LeaderElectionRetryPeriod},
	}
	for _, ch := range checks {
		if ch.val <= 0 {
			return fmt.Errorf("%s: must be positive, got %s", ch.name, ch.val)
		}
	}
	if c.JWKSCacheTTL < minJWKSCacheTTL {
		return fmt.Errorf("JWKS_CACHE_TTL: must be >= %s, got %s", minJWKSCacheTTL, c.JWKSCacheTTL)
	}
	return nil
}

func (c *Config) validateLeaderElection() error {
	if !c.LeaderElectionEnabled {
		return nil
	}
	if c.LeaderElectionLeaseName == "" {
		return errors.New("LEADER_ELECTION_LEASE_NAME is required when LEADER_ELECTION_ENABLED=true")
	}
	if c.LeaderElectionNamespace == "" {
		return errors.New("LEADER_ELECTION_NAMESPACE is required when LEADER_ELECTION_ENABLED=true")
	}
	if c.LeaderElectionIdentity == "" {
		return errors.New("LEADER_ELECTION_IDENTITY is required when LEADER_ELECTION_ENABLED=true")
	}
	if c.LeaderElectionLeaseDuration <= c.LeaderElectionRenewDeadline {
		return fmt.Errorf(
			"LEADER_ELECTION_LEASE_DURATION must be greater than LEADER_ELECTION_RENEW_DEADLINE (%s <= %s)",
			c.LeaderElectionLeaseDuration,
			c.LeaderElectionRenewDeadline,
		)
	}
	if c.LeaderElectionRenewDeadline <= c.LeaderElectionRetryPeriod {
		return fmt.Errorf(
			"LEADER_ELECTION_RENEW_DEADLINE must be greater than LEADER_ELECTION_RETRY_PERIOD (%s <= %s)",
			c.LeaderElectionRenewDeadline,
			c.LeaderElectionRetryPeriod,
		)
	}
	return nil
}

func (c *Config) validateIssuerURL() error {
	if c.IssuerURL == "" {
		return errors.New("ISSUER_URL is required")
	}
	u, err := url.Parse(c.IssuerURL)
	if err != nil {
		return fmt.Errorf("ISSUER_URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("ISSUER_URL: scheme must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("ISSUER_URL: missing host")
	}
	if u.Port() != "" {
		return fmt.Errorf("ISSUER_URL: must not contain an explicit port, got %q", u.Port())
	}
	// The handler only serves the two well-known paths at the root, so the
	// issuer URL must be host-only. Anything else (path/query/fragment/userinfo)
	// produces a discovery document that advertises endpoints this process
	// will never serve, breaking AWS-side validation in a way that looks fine
	// at config-load time.
	if u.Path != "" {
		return fmt.Errorf("ISSUER_URL: must not contain a path, got %q", u.Path)
	}
	if u.RawQuery != "" {
		return errors.New("ISSUER_URL: must not contain a query string")
	}
	if u.Fragment != "" {
		return errors.New("ISSUER_URL: must not contain a fragment")
	}
	if u.User != nil {
		return errors.New("ISSUER_URL: must not contain userinfo")
	}
	return nil
}

func (c *Config) validateAllowlist() error {
	if c.SourceIPAllowlistEnabled && len(c.SourceIPAllowlistCIDRs) == 0 {
		return errors.New("SOURCE_IP_ALLOWLIST_CIDRS is required when SOURCE_IP_ALLOWLIST_ENABLED=true")
	}
	return nil
}

func (c *Config) validateProdRequired() error {
	if c.TSHostname == "" {
		return errors.New("TS_HOSTNAME is required")
	}
	if c.TSStateSecret == "" {
		return errors.New("TS_STATE_SECRET is required")
	}
	if c.TSAPIClientID == "" {
		return errors.New("TS_API_CLIENT_ID is required")
	}
	if c.TSAPIClientSecret == "" {
		return errors.New("TS_API_CLIENT_SECRET is required")
	}
	if c.TSTag == "" {
		return errors.New("TS_TAG is required")
	}
	if !strings.HasPrefix(c.TSTag, "tag:") {
		return fmt.Errorf("TS_TAG: must start with %q, got %q", "tag:", c.TSTag)
	}
	return nil
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return slog.Level(n), nil
	}
	return 0, fmt.Errorf("LOG_LEVEL: unrecognized %q", s)
}
