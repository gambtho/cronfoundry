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

func TestClient_CreateBranch_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/git/refs") || r.Method != "POST" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ref"] != "refs/heads/feat-x" {
			t.Errorf("ref: got %v", body["ref"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": body["ref"]})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	if err := c.CreateBranch(context.Background(), 1, "o", "r", "feat-x", "deadbeef"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
}

func TestClient_CreateBranch_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Reference already exists"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.CreateBranch(context.Background(), 1, "o", "r", "feat-x", "deadbeef")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestClient_PutFile_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/contents/") || r.Method != "PUT" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "newsha"},
			"commit":  map[string]any{"sha": "commitsha"},
		})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.PutFile(context.Background(), 1, "o", "r", "feat-x", "cronfoundry.yaml", "oldsha", "msg", []byte("body"))
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
}

func TestClient_PutFile_StaleSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"is at <sha>"}`, http.StatusConflict)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.PutFile(context.Background(), 1, "o", "r", "feat-x", "cronfoundry.yaml", "stalesha", "msg", []byte("body"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestClient_CreatePR_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls") || r.Method != "POST" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/o/r/pull/42",
			"number":   42,
		})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	pr, err := c.CreatePR(context.Background(), 1, PRRequest{
		Owner: "o", Repo: "r", Branch: "feat-x", Base: "main", Title: "t", Body: "b",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.HTMLURL != "https://github.com/o/r/pull/42" || pr.Number != 42 {
		t.Errorf("got %+v", pr)
	}
}

func TestClient_CreatePR_PermissionRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.CreatePR(context.Background(), 1, PRRequest{Owner: "o", Repo: "r", Branch: "x", Base: "main", Title: "t", Body: "b"})
	if !errors.Is(err, ErrPermissionRequired) {
		t.Fatalf("want ErrPermissionRequired, got %v", err)
	}
}

func TestClient_CreatePR_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"A pull request already exists"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.CreatePR(context.Background(), 1, PRRequest{Owner: "o", Repo: "r", Branch: "x", Base: "main", Title: "t", Body: "b"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
