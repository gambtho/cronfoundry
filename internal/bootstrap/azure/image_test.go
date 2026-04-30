// internal/bootstrap/azure/image_test.go
package azure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProbeImage_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodHead, r.Method)
		require.Equal(t, "/v2/gambtho/cronfoundry/manifests/0.7.0", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, probeImageAt(context.Background(), srv.URL, "gambtho", "0.7.0"))
}

func TestProbeImage_NotFound_ReturnsTagPushHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := probeImageAt(context.Background(), srv.URL, "gambtho", "0.7.0")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "git tag v0.7.0"),
		"want tag-push hint, got %q", err.Error())
}
