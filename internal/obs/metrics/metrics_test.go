package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scrape hits the /metrics handler and returns the exposition body as a string.
// Going through the real handler (rather than the registry) verifies that every
// instrument is actually wired into the scrape output — the exact thing the
// acceptance criterion "curl /metrics — все метрики на месте" checks.
func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape: status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("scrape: read body: %v", err)
	}
	return string(body)
}

func TestNew_AllMetricsPresent(t *testing.T) {
	m := New()
	body := scrape(t, m)

	// Each instrument must appear in the exposition by name. This is the
	// acceptance test for П36: all five metrics are on /metrics.
	want := []string{
		"gw_requests_total",
		"gw_ttft_seconds",
		"gw_cost_rubles_total",
		"gw_provider_errors_total",
		"gw_cache_hit_ratio",
	}
	for _, name := range want {
		if !strings.Contains(body, name) {
			t.Errorf("metric %q missing from /metrics output", name)
		}
	}
}

func TestIncRequests_LabelValues(t *testing.T) {
	m := New()
	m.IncRequests("ollama", "llama3.2:3b", "200")
	m.IncRequests("ollama", "llama3.2:3b", "200")
	m.IncRequests("anthropic", "claude-3", "502")

	body := scrape(t, m)

	// Two increments for the same label set collapse to value 2.
	if !strings.Contains(body, `gw_requests_total{model="llama3.2:3b",provider="ollama",status="200"} 2`) {
		t.Errorf("expected 2 requests for ollama/llama3.2:3b/200, body:\n%s", body)
	}
	if !strings.Contains(body, `gw_requests_total{model="claude-3",provider="anthropic",status="502"} 1`) {
		t.Errorf("expected 1 request for anthropic/claude-3/502, body:\n%s", body)
	}
}

func TestObserveTTFT_Histogram(t *testing.T) {
	m := New()
	m.ObserveTTFT(0.3)
	m.ObserveTTFT(1.5)

	body := scrape(t, m)

	// Histogram must expose _count and _sum and bucket boundaries.
	for _, frag := range []string{
		"gw_ttft_seconds_bucket",
		"gw_ttft_seconds_sum",
		"gw_ttft_seconds_count 2",
	} {
		if !strings.Contains(body, frag) {
			t.Errorf("TTFT histogram missing fragment %q, body:\n%s", frag, body)
		}
	}
	// 0.3 and 1.5 both land in the +Inf bucket; sum must be 1.8.
	if !strings.Contains(body, "gw_ttft_seconds_sum 1.8") {
		t.Errorf("expected TTFT sum 1.8, body:\n%s", body)
	}
}

func TestObserveTTFT_ZeroIsObserved(t *testing.T) {
	// A cached stream can produce TTFT ≈ 0; it must still be counted, not dropped.
	m := New()
	m.ObserveTTFT(0)

	body := scrape(t, m)
	if !strings.Contains(body, "gw_ttft_seconds_count 1") {
		t.Errorf("expected TTFT count 1 for zero observation, body:\n%s", body)
	}
}

func TestAddCostRubles_Accumulates(t *testing.T) {
	m := New()
	m.AddCostRubles(0.25)
	m.AddCostRubles(0.75)

	body := scrape(t, m)
	if !strings.Contains(body, "gw_cost_rubles_total 1") {
		t.Errorf("expected cost total 1, body:\n%s", body)
	}
}

func TestAddCostRubles_IgnoresNonPositive(t *testing.T) {
	// Zero/negative cost must not create a time series or move the counter.
	m := New()
	m.AddCostRubles(0)
	m.AddCostRubles(-5)

	body := scrape(t, m)
	if !strings.Contains(body, "gw_cost_rubles_total 0") {
		t.Errorf("expected cost total 0, body:\n%s", body)
	}
}

func TestIncProviderError_Labels(t *testing.T) {
	m := New()
	m.IncProviderError("anthropic", "timeout")
	m.IncProviderError("anthropic", "timeout")
	m.IncProviderError("ollama", "5xx")

	body := scrape(t, m)
	if !strings.Contains(body, `gw_provider_errors_total{class="timeout",provider="anthropic"} 2`) {
		t.Errorf("expected 2 timeouts for anthropic, body:\n%s", body)
	}
	if !strings.Contains(body, `gw_provider_errors_total{class="5xx",provider="ollama"} 1`) {
		t.Errorf("expected 1 5xx for ollama, body:\n%s", body)
	}
}

func TestSetCacheHitRatio(t *testing.T) {
	m := New()
	m.SetCacheHitRatio(0.75)

	body := scrape(t, m)
	if !strings.Contains(body, "gw_cache_hit_ratio 0.75") {
		t.Errorf("expected cache hit ratio 0.75, body:\n%s", body)
	}
}

func TestNilMetrics_NoPanic(t *testing.T) {
	// A nil *Metrics must be a safe no-op so callers can disable metrics
	// without branching at every call site.
	var m *Metrics
	m.IncRequests("p", "m", "200")
	m.ObserveTTFT(0.1)
	m.AddCostRubles(1)
	m.IncProviderError("p", "timeout")
	m.SetCacheHitRatio(0.5)

	// Handler must still return something usable.
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("nil handler: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nil handler: status = %d", resp.StatusCode)
	}
}

func TestNew_RegistriesAreIsolated(t *testing.T) {
	// Two instances must not share collectors; otherwise New() would panic on
	// duplicate registration in tests that build more than one gateway.
	a := New()
	b := New()

	a.IncRequests("p", "m", "200")
	bodyB := scrape(t, b)
	if strings.Contains(bodyB, `gw_requests_total{model="m",provider="p",status="200"}`) {
		t.Errorf("registry b leaked series from registry a")
	}
}
