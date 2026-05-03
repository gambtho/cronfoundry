package skillrepo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubToken returns the same fake token for any installID.
func stubToken(_ context.Context, _ int64) (string, error) { return "fake-token", nil }

func TestClient_GetFile_Happy(t *testing.T) {
	const (
		owner   = "ownr"
		repo    = "rp"
		path    = "cronfoundry.yaml"
		ref     = "main"
		fileSha = "abc123"
		headSha = "deadbeef"
		body    = "version: 1\nskills: []\n"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"+owner+"/"+repo+"/contents/"+path):
			payload := map[string]any{
				"type":     "file",
				"sha":      fileSha,
				"path":     path,
				"content":  base64.StdEncoding.EncodeToString([]byte(body)),
				"encoding": "base64",
			}
			_ = json.NewEncoder(w).Encode(payload)
		case strings.HasPrefix(r.URL.Path, "/repos/"+owner+"/"+repo+"/branches/"+ref):
			payload := map[string]any{
				"name":   ref,
				"commit": map[string]string{"sha": headSha},
			}
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, 500)
		}
	}))
	defer srv.Close()

	c := New(stubToken, srv.URL)
	got, err := c.GetFile(context.Background(), 42, owner, repo, path, ref)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(got.Content) != body {
		t.Errorf("content: got %q, want %q", got.Content, body)
	}
	if got.FileSHA != fileSha {
		t.Errorf("file sha: got %q, want %q", got.FileSHA, fileSha)
	}
	if got.HeadSHA != headSha {
		t.Errorf("head sha: got %q, want %q", got.HeadSHA, headSha)
	}
}

func TestClient_GetFile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.GetFile(context.Background(), 1, "o", "r", "x.yaml", "main")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("want ErrFileNotFound, got %v", err)
	}
}
