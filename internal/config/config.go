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
)

// Config is the validated runtime configuration.
type Config struct {
	IssuerURL         string
	TSHostname        string
	TSStateSecret     string
	TSAPIClientID     string
	TSAPIClientSecret string
	TSTag             string
	TSAPIBaseURL      string

	JWKSUpstreamURL       string
	JWKSUpstreamTokenPath string
	JWKSUpstreamCAPath    string
	JWKSCacheTTL          time.Duration
	JWKSCacheMaxAgeHeader time.Duration
	DiscoveryMaxAgeHeader time.Duration

	SourceIPAllowlistEnabled bool
	SourceIPAllowlistCIDRs   []netip.Prefix

	FunnelAddr           string
	LogLevel             slog.Level
	StartupFetchTimeout  time.Duration
	ShutdownTimeout      time.Duration
	TSStatusPollInterval time.Duration

	DevListenAddr string
}

const (
	defaultJWKSCacheTTL         = 60 * time.Second
	defaultJWKSCacheMaxAge      = 60 * time.Second
	defaultDiscoveryMaxAge      = 3600 * time.Second
	defaultStartupFetchTimeout  = 30 * time.Second
	defaultShutdownTimeout      = 10 * time.Second
	defaultTSStatusPollInterval = 15 * time.Second
	minJWKSCacheTTL             = 5 * time.Second
)

// tsEnvVars are all environment variables that imply real-tailnet operation.
// If any of these are set together with DEV_LISTEN_ADDR, Load rejects the
// configuration to prevent accidentally enabling the dev path in production.
//
//nolint:gochecknoglobals // immutable lookup table; clearer as a package var than recreated per call
var tsEnvVars = []string{
	"TS_HOSTNAME",
	"TS_STATE_SECRET",
	"TS_API_CLIENT_ID",
	"TS_API_CLIENT_SECRET",
	"TS_TAG",
	"TS_API_BASE_URL",
	"FUNNEL_ADDR",
}

// Load reads the configuration from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		IssuerURL:         os.Getenv("ISSUER_URL"),
		TSHostname:        os.Getenv("TS_HOSTNAME"),
		TSStateSecret:     os.Getenv("TS_STATE_SECRET"),
		TSAPIClientID:     os.Getenv("TS_API_CLIENT_ID"),
		TSAPIClientSecret: os.Getenv("TS_API_CLIENT_SECRET"),
		TSTag:             os.Getenv("TS_TAG"),
		TSAPIBaseURL:      envDefault("TS_API_BASE_URL", "https://api.tailscale.com"),
		JWKSUpstreamURL:   envDefault("JWKS_UPSTREAM_URL", "https://kubernetes.default.svc/openid/v1/jwks"),
		JWKSUpstreamTokenPath: envDefault(
			"JWKS_UPSTREAM_TOKEN_PATH",
			"/var/run/secrets/kubernetes.io/serviceaccount/token",
		),
		JWKSUpstreamCAPath: envDefault(
			"JWKS_UPSTREAM_CA_PATH",
			"/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		),
		FunnelAddr:    envDefault("FUNNEL_ADDR", ":443"),
		DevListenAddr: os.Getenv("DEV_LISTEN_ADDR"),
	}

	var err error
	if cfg.JWKSCacheTTL, err = envDuration("JWKS_CACHE_TTL", defaultJWKSCacheTTL); err != nil {
		return nil, err
	}
	if cfg.JWKSCacheMaxAgeHeader, err = envDuration("JWKS_CACHE_MAX_AGE_HEADER", defaultJWKSCacheMaxAge); err != nil {
		return nil, err
	}
	if cfg.DiscoveryMaxAgeHeader, err = envDuration("DISCOVERY_MAX_AGE_HEADER", defaultDiscoveryMaxAge); err != nil {
		return nil, err
	}
	if cfg.StartupFetchTimeout, err = envDuration("STARTUP_FETCH_TIMEOUT", defaultStartupFetchTimeout); err != nil {
		return nil, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", defaultShutdownTimeout); err != nil {
		return nil, err
	}
	if cfg.TSStatusPollInterval, err = envDuration("TS_STATUS_POLL_INTERVAL", defaultTSStatusPollInterval); err != nil {
		return nil, err
	}

	if cfg.LogLevel, err = parseLogLevel(envDefault("LOG_LEVEL", "info")); err != nil {
		return nil, err
	}

	cfg.SourceIPAllowlistEnabled = envBool("SOURCE_IP_ALLOWLIST_ENABLED")
	if raw := os.Getenv("SOURCE_IP_ALLOWLIST_CIDRS"); raw != "" {
		for item := range strings.SplitSeq(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			p, perr := netip.ParsePrefix(item)
			if perr != nil {
				return nil, fmt.Errorf("SOURCE_IP_ALLOWLIST_CIDRS: invalid CIDR %q: %w", item, perr)
			}
			cfg.SourceIPAllowlistCIDRs = append(cfg.SourceIPAllowlistCIDRs, p)
		}
	}

	if vErr := cfg.validate(); vErr != nil {
		return nil, vErr
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if err := c.validateDevModeMutex(); err != nil {
		return err
	}
	if err := c.validateIssuerURL(); err != nil {
		return err
	}
	if c.DevListenAddr == "" {
		if err := c.validateProdRequired(); err != nil {
			return err
		}
	}
	if c.JWKSCacheTTL < minJWKSCacheTTL {
		return fmt.Errorf("JWKS_CACHE_TTL: must be >= %s, got %s", minJWKSCacheTTL, c.JWKSCacheTTL)
	}
	return nil
}

func (c *Config) validateDevModeMutex() error {
	if c.DevListenAddr == "" {
		return nil
	}
	var setVars []string
	for _, name := range tsEnvVars {
		if os.Getenv(name) != "" {
			setVars = append(setVars, name)
		}
	}
	if len(setVars) > 0 {
		return fmt.Errorf("DEV_LISTEN_ADDR is set together with production env vars %v: refusing to start", setVars)
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
	if strings.HasSuffix(c.IssuerURL, "/") {
		return errors.New("ISSUER_URL: must not have trailing slash")
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
