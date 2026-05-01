package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectCopilotCmd_PrintsUserCodeAndStoresToken(t *testing.T) {
	var pollHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"device_code": "dev-abc",
			"user_code": "WDJB-MJHT",
			"verification_uri": "https://github.com/login/device",
			"expires_in": 900,
			"interval": 5
		}`))
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// First call returns pending, second returns the token.
		if atomic.AddInt32(&pollHits, 1) == 1 {
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"access_token": "ghu_test",
			"refresh_token": "refresh-test",
			"expires_in": 3600
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	stored := map[string]string{}
	deps := connectCopilotDeps{
		DeviceCodeURL: srv.URL + "/login/device/code",
		TokenURL:      srv.URL + "/login/oauth/access_token",
		ClientID:      "cronfoundry",
		Scope:         "copilot",
		PollInterval:  0,
		HTTPClient:    srv.Client(),
		StoreTokens: func(ctx context.Context, prefix, accessToken, refreshToken string, expiresIn int) error {
			stored["prefix"] = prefix
			stored["accessToken"] = accessToken
			stored["refreshToken"] = refreshToken
			stored["expiresIn"] = time.Duration(expiresIn).String()
			return nil
		},
	}

	cmd := newAdminConnectCopilotCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--prefix", "copilot", "--timeout", "30s"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, cmd.ExecuteContext(ctx))

	assert.Contains(t, out.String(), "WDJB-MJHT")
	assert.True(t, strings.Contains(out.String(), "https://github.com/login/device"))
	assert.Equal(t, "copilot", stored["prefix"])
	assert.Equal(t, "ghu_test", stored["accessToken"])
	assert.Equal(t, "refresh-test", stored["refreshToken"])
	assert.GreaterOrEqual(t, atomic.LoadInt32(&pollHits), int32(2))
}
