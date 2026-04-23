package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type copilotConnectRequest struct {
	Prefix string `json:"prefix"`
}

type copilotConnectResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
}

type copilotDeviceState struct {
	prefix    string
	expiresAt time.Time
}

type copilotConnectHandler struct {
	deps  Deps
	mu    sync.Mutex
	flows map[string]*copilotDeviceState // keyed by deviceCode
}

func (h *copilotConnectHandler) githubBase() string {
	if h.deps.GitHubAPIBase != "" {
		return strings.TrimRight(h.deps.GitHubAPIBase, "/")
	}
	return "https://github.com"
}

func (h *copilotConnectHandler) startFlow(w http.ResponseWriter, r *http.Request) {
	var req copilotConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prefix == "" {
		writeErr(w, http.StatusBadRequest, "prefix is required", "bad_request")
		return
	}

	body := strings.NewReader("client_id=cronfoundry&scope=copilot")
	httpReq, _ := http.NewRequestWithContext(r.Context(),
		http.MethodPost, h.githubBase()+"/login/device/code", body)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "failed to contact GitHub", "upstream_error")
		return
	}
	defer resp.Body.Close()

	var ghResp githubDeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil || ghResp.DeviceCode == "" {
		writeErr(w, http.StatusBadGateway, "unexpected response from GitHub", "upstream_error")
		return
	}

	h.mu.Lock()
	if h.flows == nil {
		h.flows = map[string]*copilotDeviceState{}
	}
	h.flows[ghResp.DeviceCode] = &copilotDeviceState{
		prefix:    req.Prefix,
		expiresAt: time.Now().Add(time.Duration(ghResp.ExpiresIn) * time.Second),
	}
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, copilotConnectResponse{
		DeviceCode:      ghResp.DeviceCode,
		UserCode:        ghResp.UserCode,
		VerificationURI: ghResp.VerificationURI,
		ExpiresIn:       ghResp.ExpiresIn,
	})
}

func (h *copilotConnectHandler) poll(w http.ResponseWriter, r *http.Request) {
	deviceCode := r.PathValue("device_code")

	h.mu.Lock()
	state, ok := h.flows[deviceCode]
	h.mu.Unlock()

	if !ok {
		writeErr(w, http.StatusNotFound, "unknown device_code — start a new flow", "not_found")
		return
	}
	if time.Now().After(state.expiresAt) {
		h.mu.Lock()
		delete(h.flows, deviceCode)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "error": "expired"})
		return
	}

	body := strings.NewReader(
		"client_id=cronfoundry&device_code=" + deviceCode + "&grant_type=urn:ietf:params:oauth:grant-type:device_code")
	req, _ := http.NewRequestWithContext(r.Context(),
		http.MethodPost, h.githubBase()+"/login/oauth/access_token", body)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "failed to contact GitHub", "upstream_error")
		return
	}
	defer resp.Body.Close()

	var tokenResp githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		writeErr(w, http.StatusBadGateway, "unexpected response from GitHub", "upstream_error")
		return
	}

	switch tokenResp.Error {
	case "":
		// success — fall through
	case "authorization_pending", "slow_down":
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	default:
		h.mu.Lock()
		delete(h.flows, deviceCode)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "error": tokenResp.Error})
		return
	}

	prefix := state.prefix
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if err := h.deps.Secrets.Put(r.Context(), prefix+"_access_token", tokenResp.AccessToken); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to store access token", "internal")
		return
	}
	if err := h.deps.Secrets.Put(r.Context(), prefix+"_refresh_token", tokenResp.RefreshToken); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to store refresh token", "internal")
		return
	}
	if err := h.deps.Secrets.Put(r.Context(), prefix+"_expiry", fmt.Sprintf("%d", expiresAt.Unix())); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to store expiry", "internal")
		return
	}

	h.mu.Lock()
	delete(h.flows, deviceCode)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
}
