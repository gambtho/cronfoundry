package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsRegistered(t *testing.T) {
	// Re-registering each metric must fail with AlreadyRegisteredError —
	// proving promauto already registered them on package init.
	for _, c := range []prometheus.Collector{
		RunsStarted, RunsFinished, RunDuration, LLMTokens, LLMCost, DestPublish,
	} {
		err := prometheus.DefaultRegisterer.Register(c)
		var alreadyRegistered prometheus.AlreadyRegisteredError
		assert.ErrorAs(t, err, &alreadyRegistered, "metric should be registered: %T", c)
	}
}

func TestRunsStarted_IncrementsAndScrapes(t *testing.T) {
	RunsStarted.WithLabelValues("test-schedule").Inc()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `cronfoundry_runs_started_total{schedule="test-schedule"}`)
}

func TestHandler_PrometheusFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	ct := rec.Header().Get("Content-Type")
	assert.True(t, strings.HasPrefix(ct, "text/plain"), "got Content-Type %q", ct)
	assert.Contains(t, rec.Body.String(), "# HELP cronfoundry_runs_started_total")
	assert.Contains(t, rec.Body.String(), "# TYPE cronfoundry_runs_started_total counter")
	// No empty-label series should appear in the scrape — those would only
	// exist if something incorrectly pre-registered an empty child.
	assert.NotContains(t, rec.Body.String(), `schedule=""`)
	assert.NotContains(t, rec.Body.String(), `provider="",kind=""`)
}

func TestHandler_Disabled(t *testing.T) {
	prev := Disabled
	Disabled = true
	defer func() { Disabled = prev }()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHistogram_HasExpectedBuckets(t *testing.T) {
	RunDuration.WithLabelValues("succeeded").Observe(0.1)
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var h *dto.Histogram
	for _, mf := range mfs {
		if mf.GetName() == "cronfoundry_run_duration_seconds" {
			for _, m := range mf.GetMetric() {
				h = m.GetHistogram()
				break
			}
			break
		}
	}
	require.NotNil(t, h, "histogram not found")
	assert.Len(t, h.GetBucket(), 15)
}
