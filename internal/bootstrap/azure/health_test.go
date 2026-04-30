// internal/bootstrap/azure/health_test.go
package azure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitHealthy_ReturnsOnceHealthy(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/healthz", r.URL.Path)
		if atomic.AddInt32(&hits, 1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	require.NoError(t, waitHealthyAt(context.Background(), "http", host, 2*time.Second, 10*time.Millisecond))
}

func TestWaitHealthy_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	err := waitHealthyAt(context.Background(), "http", host, 50*time.Millisecond, 10*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}
