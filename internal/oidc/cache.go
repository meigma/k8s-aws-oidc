package oidc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meigma/k8s-aws-oidc/internal/logx"
	"github.com/meigma/k8s-aws-oidc/internal/metrics"
)

// Cache holds the most recently fetched, validated, marshaled JWKS bytes
// and refreshes them in the background. On refresh failure, the previous
// good value is retained only for a bounded stale window; once that window
// expires the cache fails closed until a fresh JWKS is fetched.
type Cache struct {
	fetcher Fetcher
	ttl     time.Duration
	maxAge  time.Duration
	logger  *slog.Logger
	metrics *metrics.Metrics
	now     func() time.Time

	mu        sync.RWMutex
	current   []byte
	updatedAt time.Time
	ready     bool
}

// NewCache constructs a Cache. ttl controls how often Run refreshes; maxAge
// is the value placed in the Cache-Control: public, max-age=N header that
// Current returns alongside the body.
func NewCache(f Fetcher, ttl, maxAge time.Duration, logger *slog.Logger, recorder *metrics.Metrics) *Cache {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cache{
		fetcher: f,
		ttl:     ttl,
		maxAge:  maxAge,
		logger:  logger,
		metrics: recorder,
		now:     time.Now,
	}
}

// Prime does one synchronous fetch and stores the result. A failure here
// is intended to fail startup; once Prime succeeds, refresh failures are
// non-fatal and the previous value is retained.
func (c *Cache) Prime(ctx context.Context) error {
	jwks, err := c.fetcher.Fetch(ctx)
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordJWKSPrimeFailure()
		}
		logJWKSFailure(ctx, c.logger, slog.LevelError, "jwks_prime_failure", err)
		return err
	}
	body, err := jwks.Marshal()
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordJWKSPrimeFailure()
		}
		logx.Error(ctx, c.logger, "jwks_cache", "jwks_prime_failure", "jwks cache update failed",
			slog.String("error_kind", "marshal_failed"),
			slog.String("error", "marshal jwks failed"),
		)
		return err
	}
	c.store(body)
	if c.metrics != nil {
		c.metrics.RecordJWKSPrimeSuccess(len(jwks.Keys))
	}
	logJWKSSuccess(ctx, c.logger, "jwks_prime_success", jwks)
	return nil
}

// Run blocks until ctx is cancelled, refreshing the cache every ttl. Refresh
// errors are logged at WARN and the previous value is retained.
func (c *Cache) Run(ctx context.Context) {
	t := time.NewTicker(c.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.refresh(ctx); err != nil {
				errorKind := jwksErrorKind(err)
				if c.metrics != nil {
					c.metrics.RecordJWKSRefreshFailure(errorKind)
				}
				logJWKSFailure(ctx, c.logger, slog.LevelWarn, "jwks_refresh_failure", err)
				if ok, remaining := c.remainingFreshness(); ok {
					if c.metrics != nil {
						c.metrics.RecordJWKSServingStale(errorKind)
					}
					logx.Warn(ctx, c.logger, "jwks_cache", "jwks_serving_stale", "serving stale jwks",
						slog.Int64("stale_remaining_seconds", int64(remaining.Seconds())),
					)
				}
			}
		}
	}
}

func (c *Cache) refresh(ctx context.Context) error {
	jwks, err := c.fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	body, err := jwks.Marshal()
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordJWKSRefreshFailure("marshal_failed")
		}
		logx.Warn(ctx, c.logger, "jwks_cache", "jwks_refresh_failure", "jwks cache update failed",
			slog.String("error_kind", "marshal_failed"),
			slog.String("error", "marshal jwks failed"),
		)
		return err
	}
	c.store(body)
	if c.metrics != nil {
		c.metrics.RecordJWKSRefreshSuccess(len(jwks.Keys))
	}
	logJWKSSuccess(ctx, c.logger, "jwks_refresh_success", jwks)
	return nil
}

func (c *Cache) store(body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = body
	c.updatedAt = c.now()
	c.ready = true
}

// Current returns the current JWKS bytes and the Cache-Control header value.
// The returned slice must be treated as read-only by the caller.
func (c *Cache) Current() ([]byte, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ready, remaining := c.freshnessLocked()
	if !ready {
		return nil, "no-store"
	}
	return c.current, fmt.Sprintf("public, max-age=%d", int(remaining.Seconds()))
}

// Ready reports whether the cache has at least one good value.
func (c *Cache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ready, _ := c.freshnessLocked()
	return ready
}

func (c *Cache) remainingFreshness() (bool, time.Duration) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.freshnessLocked()
}

// freshnessLocked reports whether the current cache entry is still usable and,
// if so, how much client-visible freshness remains. Callers must hold c.mu.
//
// We allow one refresh interval of stale serving beyond maxAge so a single
// missed refresh does not immediately flip the service unhealthy. The
// Cache-Control header is capped at maxAge and then reduced as the value ages,
// so downstream caches never extend the total lifetime past the bounded stale
// window.
func (c *Cache) freshnessLocked() (bool, time.Duration) {
	if !c.ready || len(c.current) == 0 || c.updatedAt.IsZero() {
		return false, 0
	}
	age := max(c.now().Sub(c.updatedAt), 0)
	staleWindow := c.maxAge + c.ttl
	remaining := staleWindow - age
	if remaining <= 0 {
		return false, 0
	}
	if remaining > c.maxAge {
		remaining = c.maxAge
	}
	return true, remaining
}

// ErrCacheNotReady is returned by callers that need a sentinel for the
// not-yet-primed condition. Reserved for handler use; the cache itself
// returns nil bytes when not ready.
var ErrCacheNotReady = errors.New("jwks cache not ready")
