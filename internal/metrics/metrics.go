package metrics

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace      = "oidc_proxy"
	resultSuccess  = "success"
	resultFailure  = "failure"
	errorKindNone  = "none"
	buildInfoValue = 1
)

// Metrics owns the service's Prometheus registry and typed instrumentation
// helpers. It is safe for concurrent use.
type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler
	now      func() time.Time

	jwksStaleWindow time.Duration
	jwksUpdatedAt   atomic.Int64
	jwksKidCount    atomic.Int64
	jwksHasValue    atomic.Bool
	leader          atomic.Bool
	publicReady     atomic.Bool

	httpRequestsTotal       *prometheus.CounterVec
	httpRequestDuration     *prometheus.HistogramVec
	jwksPrimeTotal          *prometheus.CounterVec
	jwksRefreshTotal        *prometheus.CounterVec
	jwksServingStaleTotal   *prometheus.CounterVec
	tsnetStartTotal         *prometheus.CounterVec
	tsnetStateTransitions   *prometheus.CounterVec
	publicListenerRestarts  *prometheus.CounterVec
	issuerHostVerification  *prometheus.CounterVec
	authKeyMintTotal        *prometheus.CounterVec
	leaderTransitionsTotal  *prometheus.CounterVec
	processStartTimeSeconds prometheus.Gauge
	healthServerStartTotal  prometheus.Counter
}

// New constructs a metrics registry with runtime/process collectors and the
// service-specific metric families.
func New(jwksStaleWindow time.Duration) *Metrics {
	m := &Metrics{
		registry:        prometheus.NewRegistry(),
		now:             time.Now,
		jwksStaleWindow: jwksStaleWindow,
	}
	m.initCollectors()
	m.registerCollectors()
	m.handler = promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return m
}

func (m *Metrics) initCollectors() {
	m.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Total number of public HTTP requests handled by the bridge.",
		},
		[]string{"route", "method", "decision", "status_code"},
	)
	m.httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "Latency of public HTTP requests handled by the bridge.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"route", "method", "decision"},
	)
	m.jwksPrimeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jwks_prime_total",
			Help:      "Total number of JWKS cache prime attempts.",
		},
		[]string{"result"},
	)
	m.jwksRefreshTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jwks_refresh_total",
			Help:      "Total number of JWKS cache refresh attempts.",
		},
		[]string{"result", "error_kind"},
	)
	m.jwksServingStaleTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "jwks_serving_stale_total",
			Help:      "Total number of times the bridge served stale JWKS data.",
		},
		[]string{"error_kind"},
	)
	m.tsnetStartTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tsnet_start_total",
			Help:      "Total number of tsnet start attempts.",
		},
		[]string{"result", "error_kind"},
	)
	m.tsnetStateTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tsnet_state_transitions_total",
			Help:      "Total number of observed tsnet backend state transitions.",
		},
		[]string{"state"},
	)
	m.publicListenerRestarts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "public_listener_restarts_total",
			Help:      "Total number of public listener restarts.",
		},
		[]string{"reason"},
	)
	m.issuerHostVerification = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "issuer_host_verification_total",
			Help:      "Total number of issuer host verification outcomes.",
		},
		[]string{"result"},
	)
	m.authKeyMintTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "auth_key_mint_total",
			Help:      "Total number of auth key mint attempts.",
		},
		[]string{"result", "error_kind"},
	)
	m.leaderTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "leader_election_transitions_total",
			Help:      "Total number of leadership state transitions observed by this pod.",
		},
		[]string{"state"},
	)
	m.processStartTimeSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "process_start_time_seconds",
			Help:      "Unix time when the bridge process started.",
		},
	)
	m.healthServerStartTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "health_server_start_total",
			Help:      "Total number of times the internal health/metrics server started.",
		},
	)
}

func (m *Metrics) registerCollectors() {
	buildInfo := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "build_info",
			Help:      "Build information for the running bridge process.",
		},
		[]string{"version", "go_version"},
	)
	buildInfo.WithLabelValues(buildVersion(), runtime.Version()).Set(buildInfoValue)
	m.processStartTimeSeconds.Set(float64(m.now().Unix()))

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.httpRequestsTotal,
		m.httpRequestDuration,
		m.jwksPrimeTotal,
		m.jwksRefreshTotal,
		m.jwksServingStaleTotal,
		m.tsnetStartTotal,
		m.tsnetStateTransitions,
		m.publicListenerRestarts,
		m.issuerHostVerification,
		m.authKeyMintTotal,
		m.leaderTransitionsTotal,
		m.processStartTimeSeconds,
		m.healthServerStartTotal,
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "jwks_age_seconds",
				Help:      "Age of the currently served JWKS payload in seconds.",
			},
			m.jwksAgeSeconds,
		),
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "jwks_ready",
				Help:      "Whether the JWKS cache currently has a serveable value.",
			},
			m.jwksReadyValue,
		),
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "jwks_keys",
				Help:      "Number of JWK keys in the currently served JWKS payload.",
			},
			m.jwksKidCountValue,
		),
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "leader",
				Help:      "Whether this pod is the current leader for the public Funnel endpoint.",
			},
			m.leaderValue,
		),
		prometheus.NewGaugeFunc(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "public_ready",
				Help:      "Whether this pod is actively serving the public Funnel listener.",
			},
			m.publicReadyValue,
		),
	)
}

// Handler returns the Prometheus scrape handler for the registry.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

// Registry returns the custom metrics registry.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// ObserveHTTPRequest records the request counter and latency histogram for one
// public HTTP request.
func (m *Metrics) ObserveHTTPRequest(route, method, decision string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	statusCode := strconv.Itoa(status)
	m.httpRequestsTotal.WithLabelValues(route, method, decision, statusCode).Inc()
	m.httpRequestDuration.WithLabelValues(route, method, decision).Observe(duration.Seconds())
}

// RecordJWKSPrimeSuccess records a successful JWKS cache prime and updates the
// current JWKS state gauges.
func (m *Metrics) RecordJWKSPrimeSuccess(kidCount int) {
	if m == nil {
		return
	}
	m.jwksPrimeTotal.WithLabelValues(resultSuccess).Inc()
	m.updateJWKSState(kidCount)
}

// RecordJWKSPrimeFailure records a failed JWKS cache prime attempt.
func (m *Metrics) RecordJWKSPrimeFailure() {
	if m == nil {
		return
	}
	m.jwksPrimeTotal.WithLabelValues(resultFailure).Inc()
}

// RecordJWKSRefreshSuccess records a successful JWKS cache refresh.
func (m *Metrics) RecordJWKSRefreshSuccess(kidCount int) {
	if m == nil {
		return
	}
	m.jwksRefreshTotal.WithLabelValues(resultSuccess, errorKindNone).Inc()
	m.updateJWKSState(kidCount)
}

// RecordJWKSRefreshFailure records a failed JWKS cache refresh attempt.
func (m *Metrics) RecordJWKSRefreshFailure(errorKind string) {
	if m == nil {
		return
	}
	m.jwksRefreshTotal.WithLabelValues(resultFailure, normalizeErrorKind(errorKind)).Inc()
}

// RecordJWKSServingStale records that stale JWKS data was served.
func (m *Metrics) RecordJWKSServingStale(errorKind string) {
	if m == nil {
		return
	}
	m.jwksServingStaleTotal.WithLabelValues(normalizeErrorKind(errorKind)).Inc()
}

// RecordTSNetStart records the result of a tsnet start attempt.
func (m *Metrics) RecordTSNetStart(result, errorKind string) {
	if m == nil {
		return
	}
	m.tsnetStartTotal.WithLabelValues(result, normalizeErrorKind(errorKind)).Inc()
}

// RecordTSNetStateTransition records a tsnet backend state transition.
func (m *Metrics) RecordTSNetStateTransition(state string) {
	if m == nil || strings.TrimSpace(state) == "" {
		return
	}
	m.tsnetStateTransitions.WithLabelValues(state).Inc()
}

// RecordPublicListenerRestart records a public listener restart reason.
func (m *Metrics) RecordPublicListenerRestart(reason string) {
	if m == nil || strings.TrimSpace(reason) == "" {
		return
	}
	m.publicListenerRestarts.WithLabelValues(reason).Inc()
}

// RecordIssuerHostVerification records the outcome of issuer host verification.
func (m *Metrics) RecordIssuerHostVerification(result string) {
	if m == nil || strings.TrimSpace(result) == "" {
		return
	}
	m.issuerHostVerification.WithLabelValues(result).Inc()
}

// RecordAuthKeyMint records the result of an auth key mint attempt.
func (m *Metrics) RecordAuthKeyMint(result, errorKind string) {
	if m == nil {
		return
	}
	m.authKeyMintTotal.WithLabelValues(result, normalizeErrorKind(errorKind)).Inc()
}

// RecordLeaderElectionTransition records one leadership transition for this pod.
func (m *Metrics) RecordLeaderElectionTransition(state string) {
	if m == nil || strings.TrimSpace(state) == "" {
		return
	}
	m.leaderTransitionsTotal.WithLabelValues(state).Inc()
}

// RecordHealthServerStart records one internal health/metrics server start.
func (m *Metrics) RecordHealthServerStart() {
	if m == nil {
		return
	}
	m.healthServerStartTotal.Inc()
}

// SetLeader updates the leader gauge source state.
func (m *Metrics) SetLeader(v bool) {
	if m == nil {
		return
	}
	m.leader.Store(v)
}

// SetPublicReady updates the public-ready gauge source state.
func (m *Metrics) SetPublicReady(v bool) {
	if m == nil {
		return
	}
	m.publicReady.Store(v)
}

func (m *Metrics) updateJWKSState(kidCount int) {
	m.jwksUpdatedAt.Store(m.now().UnixNano())
	m.jwksKidCount.Store(int64(kidCount))
	m.jwksHasValue.Store(true)
}

func (m *Metrics) jwksAgeSeconds() float64 {
	if m == nil || !m.jwksHasValue.Load() {
		return 0
	}
	updatedAt := m.jwksUpdatedAt.Load()
	if updatedAt == 0 {
		return 0
	}
	age := m.now().Sub(time.Unix(0, updatedAt)).Seconds()
	if age < 0 {
		return 0
	}
	return age
}

func (m *Metrics) jwksReadyValue() float64 {
	if m == nil || !m.jwksHasValue.Load() {
		return 0
	}
	if m.jwksStaleWindow <= 0 {
		return 0
	}
	if m.jwksAgeSeconds() > m.jwksStaleWindow.Seconds() {
		return 0
	}
	return 1
}

func (m *Metrics) jwksKidCountValue() float64 {
	if m.jwksReadyValue() == 0 {
		return 0
	}
	return float64(m.jwksKidCount.Load())
}

func (m *Metrics) leaderValue() float64 {
	if m == nil || !m.leader.Load() {
		return 0
	}
	return 1
}

func (m *Metrics) publicReadyValue() float64 {
	if m == nil || !m.publicReady.Load() {
		return 0
	}
	return 1
}

func normalizeErrorKind(errorKind string) string {
	if strings.TrimSpace(errorKind) == "" {
		return errorKindNone
	}
	return strings.TrimSpace(errorKind)
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if strings.TrimSpace(info.Main.Version) == "" {
		return "devel"
	}
	return info.Main.Version
}
