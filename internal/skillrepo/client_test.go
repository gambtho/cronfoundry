package skillrepo

import (
	"context"
	"net/http/httptest"
	"testing"
)

// stubToken returns the same fake token for any installID. Used as the
// TokenFunc in unit tests so we never need to mint a real JWT.
func stubToken(_ context.Context, _ int64) (string, error) { return "fake-token", nil }

// TestClient_PackageCompiles is a smoke test confirming the package builds
// and the constructor is callable. Real per-method tests land in Tasks 6-9.
func TestClient_PackageCompiles(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	c := New(stubToken, srv.URL)
	if c == nil {
		t.Fatalf("New returned nil")
	}
}
