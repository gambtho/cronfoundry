package githubapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConvert_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/app-manifests/") || !strings.HasSuffix(r.URL.Path, "/conversions") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": 12345,
			"slug": "cronfoundry-tng",
			"client_id": "Iv23liabcdef",
			"client_secret": "shh",
			"webhook_secret": "wh-secret",
			"pem": "-----BEGIN RSA PRIVATE KEY-----\nA\n-----END RSA PRIVATE KEY-----\n",
			"owner": {"login": "tng"}
		}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	got, err := c.Convert(context.Background(), "code-abc")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.ID != 12345 || got.Slug != "cronfoundry-tng" || got.ClientID != "Iv23liabcdef" {
		t.Errorf("unexpected result: %+v", got)
	}
	if got.WebhookSecret != "wh-secret" || !strings.Contains(got.PEM, "BEGIN RSA") {
		t.Errorf("missing secret/pem: %+v", got)
	}
}

func TestConvert_NameTaken422(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Name has already been taken"}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	_, err := c.Convert(context.Background(), "code")
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Errorf("err = %v, want 422", err)
	}
}

func TestConvert_5xxRetriesOnce(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"slug":"s","client_id":"x","client_secret":"y","webhook_secret":"z","pem":"p"}`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	c.RetryDelay = 10 * time.Millisecond
	if _, err := c.Convert(context.Background(), "code"); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestConvert_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewConverter(srv.URL, srv.Client())
	if _, err := c.Convert(context.Background(), "code"); err == nil {
		t.Error("expected decode error, got nil")
	}
}
