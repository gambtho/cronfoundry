package webapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

// fakeGitHub stubs the GitHub device-code and token endpoints.
type fakeGitHub struct {
	deviceCodeResp string
	tokenResp      string
	tokenStatus    int
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.deviceCodeResp))
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte(f.tokenResp))
	})
	return mux
}

func newTestMemStore() *testMemStore {
	return &testMemStore{data: map[string]string{}}
}

type testMemStore struct {
	data map[string]string
}

func (m *testMemStore) Put(_ context.Context, name, value string) error {
	m.data[name] = value
	return nil
}

func (m *testMemStore) Get(_ context.Context, name string) (string, error) {
	v, ok := m.data[name]
	if !ok {
		return "", fmt.Errorf("not found: %s", name)
	}
	return v, nil
}

func (m *testMemStore) Delete(_ context.Context, name string) error {
	delete(m.data, name)
	return nil
}

func (m *testMemStore) List(_ context.Context) ([]string, error) {
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestCopilotConnect_StartFlow(t *testing.T) {
	gh := &fakeGitHub{
		deviceCodeResp: `{"device_code":"dev123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":300,"interval":5}`,
		tokenStatus:    http.StatusOK,
	}
	ghSrv := httptest.NewServer(gh.handler())
	defer ghSrv.Close()

	masterKey := make([]byte, 32)
	deps := webapi.Deps{
		MasterKey:     masterKey,
		AdminLogins:   []string{"alice"},
		Secrets:       newTestMemStore(),
		GitHubAPIBase: ghSrv.URL,
	}
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/copilot/connect",
		strings.NewReader(`{"prefix":"mycopilot"}`))
	req.Header.Set("Content-Type", "application/json")
	addTestSession(t, req, masterKey, "alice", "admin")
	req = withCSRF(t, req)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "ABCD-1234", resp["user_code"])
	assert.Equal(t, "https://github.com/login/device", resp["verification_uri"])
	assert.NotEmpty(t, resp["device_code"])
}

func TestCopilotConnect_PollSuccess(t *testing.T) {
	gh := &fakeGitHub{
		deviceCodeResp: `{"device_code":"dev123","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":300,"interval":1}`,
		tokenResp:      `{"access_token":"ghu_access","refresh_token":"ghu_refresh","expires_in":28800}`,
		tokenStatus:    http.StatusOK,
	}
	ghSrv := httptest.NewServer(gh.handler())
	defer ghSrv.Close()

	store := newTestMemStore()
	masterKey := make([]byte, 32)
	deps := webapi.Deps{
		MasterKey:     masterKey,
		AdminLogins:   []string{"alice"},
		Secrets:       store,
		GitHubAPIBase: ghSrv.URL,
	}
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, deps)

	// Start flow
	startReq := httptest.NewRequest(http.MethodPost, "/api/copilot/connect",
		strings.NewReader(`{"prefix":"mycopilot"}`))
	startReq.Header.Set("Content-Type", "application/json")
	addTestSession(t, startReq, masterKey, "alice", "admin")
	startReq = withCSRF(t, startReq)
	startRR := httptest.NewRecorder()
	mux.ServeHTTP(startRR, startReq)
	require.Equal(t, http.StatusOK, startRR.Code)

	// Poll
	pollReq := httptest.NewRequest(http.MethodGet, "/api/copilot/connect/dev123/poll", nil)
	addTestSession(t, pollReq, masterKey, "alice", "admin")
	pollRR := httptest.NewRecorder()
	mux.ServeHTTP(pollRR, pollReq)

	require.Equal(t, http.StatusOK, pollRR.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(pollRR.Body).Decode(&resp))
	assert.Equal(t, "success", resp["status"])

	// Secrets stored
	v, err := store.Get(context.Background(), "mycopilot-access-token")
	require.NoError(t, err)
	assert.Equal(t, "ghu_access", v)
}

func TestCopilotConnect_PollDeclined(t *testing.T) {
	gh := &fakeGitHub{
		deviceCodeResp: `{"device_code":"dev999","user_code":"ZZZZ-9999","verification_uri":"https://github.com/login/device","expires_in":300,"interval":1}`,
		tokenResp:      `{"error":"access_denied"}`,
		tokenStatus:    http.StatusOK,
	}
	ghSrv := httptest.NewServer(gh.handler())
	defer ghSrv.Close()

	masterKey := make([]byte, 32)
	deps := webapi.Deps{
		MasterKey:     masterKey,
		AdminLogins:   []string{"alice"},
		Secrets:       newTestMemStore(),
		GitHubAPIBase: ghSrv.URL,
	}
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, deps)

	startReq := httptest.NewRequest(http.MethodPost, "/api/copilot/connect",
		strings.NewReader(`{"prefix":"mycopilot"}`))
	startReq.Header.Set("Content-Type", "application/json")
	addTestSession(t, startReq, masterKey, "alice", "admin")
	startReq = withCSRF(t, startReq)
	startRR := httptest.NewRecorder()
	mux.ServeHTTP(startRR, startReq)
	require.Equal(t, http.StatusOK, startRR.Code)

	pollReq := httptest.NewRequest(http.MethodGet, "/api/copilot/connect/dev999/poll", nil)
	addTestSession(t, pollReq, masterKey, "alice", "admin")
	pollRR := httptest.NewRecorder()
	mux.ServeHTTP(pollRR, pollReq)

	require.Equal(t, http.StatusOK, pollRR.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(pollRR.Body).Decode(&resp))
	assert.Equal(t, "error", resp["status"])
	assert.Equal(t, "access_denied", resp["error"])
}
