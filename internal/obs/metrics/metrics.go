// Package metrics owns the Prometheus registry and all gatewayd-specific
// instruments. The registry is created explicitly (not via the global default)
// so tests can spin up isolated instances without cross-contamination, and so
// the production binary controls exactly what is exposed.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics bundles the registry and every instrument the gateway records.
// A nil *Metrics is safe for all recording methods (no-op), which keeps
// wiring in handlers simple when metrics are disabled in tests.
type Metrics struct {
	registry *prometheus.Registry

	requestsTotal   *prometheus.CounterVec
	ttftSeconds     prometheus.Histogram
	costRublesTotal prometheus.Counter
	providerErrors  *prometheus.CounterVec
	cacheHitRatio   prometheus.Gauge
}

// New creates a registry pre-populated with Go runtime and process collectors,
// then registers the gateway's own instruments.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,

		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gw_requests_total",
			Help: "Total LLM requests handled, partitioned by provider, model and HTTP status.",
		}, []string{"provider", "model", "status"}),

		// TTFT is measured per streamed request from the moment the upstream
		// call starts until the first SSE chunk reaches the client. Buckets are
		// tuned for LLM latencies: sub-100ms is cache-fast, 10s+ is a degraded
		// upstream. The top bucket must comfortably exceed any plausible TTFT,
		// otherwise p99 is silently reported as +Inf .
		ttftSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gw_ttft_seconds",
			Help:    "Time to first token for streamed completions, in seconds.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30},
		}),

		costRublesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gw_cost_rubles_total",
			Help: "Cumulative upstream cost of LLM calls, in Russian rubles.",
		}),

		providerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gw_provider_errors_total",
			Help: "Total provider-level failures, partitioned by provider and error class.",
		}, []string{"provider", "class"}),

		cacheHitRatio: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gw_cache_hit_ratio",
			Help: "Exact-cache hit ratio as a fraction in [0,1], updated on every cache access.",
		}),
	}

	reg.MustRegister(
		m.requestsTotal,
		m.ttftSeconds,
		m.costRublesTotal,
		m.providerErrors,
		m.cacheHitRatio,
	)

	// Pre-initialize vectors with zero values. Prometheus does not export a
	// CounterVec until at least one label combination is observed. Without
	// this, a freshly started gateway would expose an empty /metrics endpoint
	// for these vectors until the first request — breaking dashboards and the
	// "all metrics present" acceptance criterion.
	m.requestsTotal.WithLabelValues("", "", "0")
	m.providerErrors.WithLabelValues("", "")

	return m
}

// Registry exposes the underlying registry for callers that need to register
// additional collectors (e.g. test utilities or future adapters).
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return prometheus.NewRegistry()
	}
	return m.registry
}

// Handler serves the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry(), promhttp.HandlerOpts{
		// Surface scrape errors instead of silently returning partial output.
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
}

// IncRequests records one completed request. status is the HTTP status code
// as a string (e.g. "200", "402", "502") so PromQL can filter by class.
func (m *Metrics) IncRequests(provider, model, status string) {
	if m == nil {
		return
	}
	m.requestsTotal.WithLabelValues(provider, model, status).Inc()
}

// ObserveTTFT records the time to first token for a streamed request.
// Call this once, exactly when the first SSE chunk is written to the client.
func (m *Metrics) ObserveTTFT(seconds float64) {
	if m == nil {
		return
	}
	m.ttftSeconds.Observe(seconds)
}

// AddCostRubles accumulates upstream cost. Call it with the cost of a single
// request after usage is known; streaming requests should call it once the
// final usage fragment arrives.
func (m *Metrics) AddCostRubles(rubles float64) {
	if m == nil || rubles <= 0 {
		return
	}
	m.costRublesTotal.Add(rubles)
}

// IncProviderError records a provider-level failure. class should be a small,
// bounded set such as "timeout", "rate_limit", "5xx", "decode" — never a raw
// error string, which would explode label cardinality .
func (m *Metrics) IncProviderError(provider, class string) {
	if m == nil {
		return
	}
	m.providerErrors.WithLabelValues(provider, class).Inc()
}

// SetCacheHitRatio updates the cache hit ratio gauge. The caller computes the
// ratio from hits/(hits+misses) and passes it here; the gauge is a snapshot,
// so dashboards should also rely on the underlying counters where available.
func (m *Metrics) SetCacheHitRatio(ratio float64) {
	if m == nil {
		return
	}
	m.cacheHitRatio.Set(ratio)
}
