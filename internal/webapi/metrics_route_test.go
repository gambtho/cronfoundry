package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gambtho/cronfoundry/internal/metrics"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestMetricsRouteServed(t *testing.T) {
	old := metrics.Disabled
	metrics.Disabled = false
	defer func() { metrics.Disabled = old }()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey: make([]byte, 32),
	})

	// Touch the counter so the metric family appears in /metrics output.
	// CounterVecs only emit HELP/TYPE lines after a label combo is observed.
	metrics.RunsStarted.WithLabelValues("test").Add(0)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cronfoundry_runs_started_total")
}
