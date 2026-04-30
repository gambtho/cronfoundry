package githubapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServer_HappyPath(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": 99,
			"slug": "cf-test",
			"client_id": "Iv23liXX",
			"client_secret": "cs",
			"webhook_secret": "ws",
			"pem": "-----BEGIN RSA PRIVATE KEY-----\nABC\n-----END RSA PRIVATE KEY-----\n"
		}`))
	}))
	defer gh.Close()

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state")
	pemDir := filepath.Join(dir, "pems")

	s, err := NewServer(Options{
		Port:            0,
		StateFile:       stateFile,
		PEMDir:          pemDir,
		Converter:       NewConverter(gh.URL, gh.Client()),
		ManifestInput:   ManifestInput{Name: "cf-test"},
		Timeout:         5 * time.Second,
		SkipBrowserOpen: true,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	go func() {
		state := s.State()
		client := &http.Client{Timeout: 2 * time.Second}
		_, _ = client.Get(s.URL() + "/callback?code=abc&state=" + url.QueryEscape(state))
		_, _ = client.Get(s.URL() + "/installed?installation_id=4242&setup_action=install")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.AppID != 99 || result.Slug != "cf-test" || result.InstallationID != 4242 {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(result.PEMPath, pemDir) {
		t.Errorf("pem path = %s, want under %s", result.PEMPath, pemDir)
	}
}

func TestServer_StateMismatchRejected(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("conversion endpoint should NOT be hit on state mismatch")
	}))
	defer gh.Close()

	s, _ := NewServer(Options{
		Port:            0,
		StateFile:       filepath.Join(t.TempDir(), "state"),
		PEMDir:          t.TempDir(),
		Converter:       NewConverter(gh.URL, gh.Client()),
		ManifestInput:   ManifestInput{Name: "x"},
		Timeout:         500 * time.Millisecond,
		SkipBrowserOpen: true,
	})
	go func() {
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Get(s.URL() + "/callback?code=abc&state=WRONG")
		if err == nil && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Run(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
