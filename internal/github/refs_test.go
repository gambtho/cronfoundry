package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBranchHead_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/myorg/myrepo/branches/main", r.URL.Path)
		assert.Equal(t, "token ghs_abc", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":   "main",
			"commit": map[string]any{"sha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		})
	}))
	defer srv.Close()

	sha, err := GetBranchHead(context.Background(), srv.Client(), srv.URL, "ghs_abc", "myorg", "myrepo", "main")
	require.NoError(t, err)
	assert.Equal(t, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", sha)
}

func TestGetBranchHead_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := GetBranchHead(context.Background(), srv.Client(), srv.URL, "ghs_abc", "o", "r", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestGetBranchHead_EmptyShaIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commit": map[string]any{"sha": ""},
		})
	}))
	defer srv.Close()

	_, err := GetBranchHead(context.Background(), srv.Client(), srv.URL, "tok", "o", "r", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}
