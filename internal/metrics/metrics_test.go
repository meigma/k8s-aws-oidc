package metrics

import (
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestNew_RegistersExpectedMetricFamilies(t *testing.T) {
	m := New(2 * time.Minute)
	m.ObserveHTTPRequest("discovery", "GET", "served", 200, 10*time.Millisecond)
	m.RecordJWKSPrimeSuccess(1)
	m.RecordJWKSRefreshFailure("fetch_failed")
	m.RecordJWKSServingStale("fetch_failed")
	m.RecordTSNetStart(resultSuccess, "")
	m.RecordTSNetStateTransition("Running")
	m.RecordPublicListenerRestart("needs_login")
	m.RecordIssuerHostVerification(resultSuccess)
	m.RecordAuthKeyMint(resultSuccess, "")
	m.RecordHealthServerStart()

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	want := []string{
		"oidc_proxy_build_info",
		"oidc_proxy_http_requests_total",
		"oidc_proxy_http_request_duration_seconds",
		"oidc_proxy_jwks_prime_total",
		"oidc_proxy_jwks_refresh_total",
		"oidc_proxy_jwks_serving_stale_total",
		"oidc_proxy_jwks_age_seconds",
		"oidc_proxy_jwks_ready",
		"oidc_proxy_jwks_kid_count",
		"oidc_proxy_tsnet_start_total",
		"oidc_proxy_tsnet_state_transitions_total",
		"oidc_proxy_public_listener_restarts_total",
		"oidc_proxy_issuer_host_verification_total",
		"oidc_proxy_auth_key_mint_total",
		"oidc_proxy_process_start_time_seconds",
		"oidc_proxy_health_server_start_total",
	}
	for _, metric := range want {
		if _, ok := metricFamilyByName(families)[metric]; !ok {
			t.Fatalf("missing metric family %q", metric)
		}
	}
}

func TestMetrics_ObserveHTTPRequest(t *testing.T) {
	m := New(2 * time.Minute)

	m.ObserveHTTPRequest("discovery", "GET", "served", 200, 25*time.Millisecond)

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	requests := metricWithLabels(t, metricFamilyByName(families)["oidc_proxy_http_requests_total"], map[string]string{
		"route":       "discovery",
		"method":      "GET",
		"decision":    "served",
		"status_code": "200",
	})
	if got := requests.GetCounter().GetValue(); got != 1 {
		t.Fatalf("requests_total = %v", got)
	}

	latency := metricWithLabels(t, metricFamilyByName(families)["oidc_proxy_http_request_duration_seconds"], map[string]string{
		"route":    "discovery",
		"method":   "GET",
		"decision": "served",
	})
	if got := latency.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("latency sample_count = %d", got)
	}
}

func TestMetrics_JWKSStateGaugesTrackFreshness(t *testing.T) {
	m := New(2 * time.Minute)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }

	m.RecordJWKSPrimeSuccess(3)

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	byName := metricFamilyByName(families)
	if got := gaugeValue(byName["oidc_proxy_jwks_ready"]); got != 1 {
		t.Fatalf("jwks_ready = %v", got)
	}
	if got := gaugeValue(byName["oidc_proxy_jwks_kid_count"]); got != 3 {
		t.Fatalf("jwks_kid_count = %v", got)
	}

	now = now.Add(3 * time.Minute)
	families, err = m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather stale: %v", err)
	}
	byName = metricFamilyByName(families)
	if got := gaugeValue(byName["oidc_proxy_jwks_ready"]); got != 0 {
		t.Fatalf("jwks_ready after expiry = %v", got)
	}
	if got := gaugeValue(byName["oidc_proxy_jwks_kid_count"]); got != 0 {
		t.Fatalf("jwks_kid_count after expiry = %v", got)
	}
}

func TestMetrics_LabelsAreSanitized(t *testing.T) {
	m := New(2 * time.Minute)

	m.RecordJWKSRefreshFailure("fetch_failed")
	m.RecordAuthKeyMint(resultFailure, "create_auth_key_failed")
	m.RecordTSNetStart(resultSuccess, "")

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), "Bearer ") {
					t.Fatalf("label %s contains bearer token material", label.GetName())
				}
				if strings.Contains(label.GetValue(), "tskey-") {
					t.Fatalf("label %s contains auth key material", label.GetName())
				}
				if strings.Contains(label.GetValue(), "/.well-known/openid-configuration") {
					t.Fatalf("label %s contains raw path", label.GetName())
				}
			}
		}
	}
}

func metricFamilyByName(families []*dto.MetricFamily) map[string]*dto.MetricFamily {
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		out[family.GetName()] = family
	}
	return out
}

func metricWithLabels(t *testing.T, family *dto.MetricFamily, labels map[string]string) *dto.Metric {
	t.Helper()
	for _, metric := range family.Metric {
		if labelsMatch(metric, labels) {
			return metric
		}
	}
	t.Fatalf("metric %q with labels %v not found", family.GetName(), labels)
	return nil
}

func labelsMatch(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.Label) != len(labels) {
		return false
	}
	for _, label := range metric.Label {
		if labels[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}

func gaugeValue(family *dto.MetricFamily) float64 {
	if len(family.Metric) == 0 {
		return 0
	}
	return family.Metric[0].GetGauge().GetValue()
}
