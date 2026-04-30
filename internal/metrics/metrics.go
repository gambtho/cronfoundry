// Package metrics defines all Prometheus metrics emitted by cronfoundry
// and exposes a /metrics handler. Producers import this package and call
// the package-level vars directly, e.g.
//   metrics.RunsStarted.WithLabelValues("my-schedule").Inc()
//
// Set Disabled=true to make Handler() return 404. Increments still apply
// to the underlying counters — the toggle is reversible without restart.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Disabled, when true, makes Handler() return 404. Set at startup from
// CRONFOUNDRY_METRICS_DISABLED.
var Disabled bool

// runDurationBuckets extends Prometheus DefBuckets (0.005s … 10s) with
// 30/60/300/600 to capture long-running LLM calls.
var runDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 600,
}

const namespace = "cronfoundry"

var RunsStarted = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "runs_started_total",
	Help:      "Total runs dispatched by the scheduler.",
}, []string{"schedule"})

var RunsFinished = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "runs_finished_total",
	Help:      "Total runs that reached a terminal state, partitioned by status (succeeded|partial_failure|failed).",
}, []string{"status"})

var RunDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: namespace,
	Name:      "run_duration_seconds",
	Help:      "Wall-clock run duration from dispatch to finalize, partitioned by status.",
	Buckets:   runDurationBuckets,
}, []string{"status"})

var LLMTokens = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "llm_tokens_total",
	Help:      "Total LLM tokens, partitioned by provider and kind (input|output).",
}, []string{"provider", "kind"})

var LLMCost = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "llm_cost_usd_total",
	Help:      "Estimated LLM cost in USD, partitioned by provider.",
}, []string{"provider"})

var DestPublish = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace,
	Name:      "destination_publish_total",
	Help:      "Total destination publish attempts, partitioned by type and result (ok|error).",
}, []string{"type", "result"})

// Handler returns the /metrics HTTP handler. When Disabled is true, returns 404.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Disabled {
			http.NotFound(w, r)
			return
		}
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
}
