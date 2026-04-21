package webapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

type fakeSyncer struct {
	called bool
	id     pgtype.UUID
	err    error
}

func (f *fakeSyncer) SyncOne(_ context.Context, id pgtype.UUID) error {
	f.called = true
	f.id = id
	return f.err
}

func signBody(body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookHandler_PingReturns200(t *testing.T) {
	h := &webhookHandler{secret: []byte("s")}
	req := httptest.NewRequest("POST", "/webhook/github", nil)
	req.Header.Set("X-GitHub-Event", "ping")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestWebhookHandler_NonPushReturns204(t *testing.T) {
	h := &webhookHandler{secret: []byte("s")}
	req := httptest.NewRequest("POST", "/webhook/github", nil)
	req.Header.Set("X-GitHub-Event", "issue_comment")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestWebhookHandler_MissingSecret503(t *testing.T) {
	h := &webhookHandler{secret: nil}
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestWebhookHandler_InvalidSignature401(t *testing.T) {
	h := &webhookHandler{secret: []byte("s")}
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestWebhookHandler_NonDefaultBranchReturns204(t *testing.T) {
	h := &webhookHandler{secret: []byte("s")}
	body := []byte(`{
		"ref":"refs/heads/topic-branch",
		"repository":{"name":"w","owner":{"login":"a"},"default_branch":"main"}
	}`)
	req := httptest.NewRequest("POST", "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signBody(body, []byte("s")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}
