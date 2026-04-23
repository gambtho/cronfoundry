package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/secretstore"
)

const copilotRefreshURL = "https://github.com/login/oauth/access_token"

// CopilotTokenRefsJSON is stored on the schedule row and identifies
// which KV secrets hold the token pair for a copilot-enterprise schedule.
type CopilotTokenRefsJSON struct {
	Prefix string `json:"prefix"`
}

type copilotTokenHandlerResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"`
}

type copilotTokenHandler struct{ deps Deps }

func (h *copilotTokenHandler) get(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run id", "bad_request")
		return
	}

	run, err := h.deps.Queries.GetRunForAdmin(r.Context(), pgtype.UUID{Bytes: runID, Valid: true})
	if err != nil {
		writeErr(w, http.StatusNotFound, "run not found", "not_found")
		return
	}

	sched, err := h.deps.Queries.GetScheduleByID(r.Context(), run.ScheduleID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "schedule not found", "internal")
		return
	}

	if len(sched.CopilotTokenRefsJson) == 0 {
		writeErr(w, http.StatusBadRequest, "schedule has no copilot token refs", "bad_request")
		return
	}

	var refs CopilotTokenRefsJSON
	if err := json.Unmarshal(sched.CopilotTokenRefsJson, &refs); err != nil || refs.Prefix == "" {
		writeErr(w, http.StatusInternalServerError, "invalid copilot token refs", "internal")
		return
	}

	accessToken, expiresAt, err := ResolveCopilotToken(r.Context(), h.deps.Secrets, refs.Prefix, nil)
	if err != nil {
		slog.Error("copilot token resolve failed", "run_id", r.PathValue("id"), "error", err)
		writeErr(w, http.StatusServiceUnavailable, "copilot token refresh failed", "copilot_token_refresh")
		return
	}

	writeJSON(w, http.StatusOK, copilotTokenHandlerResponse{
		AccessToken: accessToken,
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
	})
}

// ResolveCopilotToken reads the access token for the given prefix from the
// secret store. If the token is within 60 seconds of expiry, it refreshes
// using the stored refresh token and writes the new values back.
//
// githubOverrideURL overrides the GitHub token endpoint for tests; pass nil
// to use the real GitHub endpoint.
func ResolveCopilotToken(ctx context.Context, store secretstore.SecretStore, prefix string, githubOverrideURL *string) (string, time.Time, error) {
	expiryStr, err := store.Get(ctx, prefix+"_expiry")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read expiry: %w", err)
	}

	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse expiry %q: %w", expiryStr, err)
	}
	expiry := time.Unix(expiryUnix, 0)

	if time.Until(expiry) >= 60*time.Second {
		tok, err := store.Get(ctx, prefix+"_access_token")
		if err != nil {
			return "", time.Time{}, fmt.Errorf("read access token: %w", err)
		}
		return tok, expiry, nil
	}

	// Refresh needed.
	refreshTok, err := store.Get(ctx, prefix+"_refresh_token")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read refresh token: %w", err)
	}

	tokenURL := copilotRefreshURL
	if githubOverrideURL != nil {
		tokenURL = strings.TrimRight(*githubOverrideURL, "/") + "/login/oauth/access_token"
	}

	params := url.Values{
		"client_id":     {copilotClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("refresh failed: HTTP %d", resp.StatusCode)
	}

	var tokenResp githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("refresh: malformed JSON from GitHub: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("refresh: GitHub returned no access_token (refresh token may be expired)")
	}

	newExpiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if err := store.Put(ctx, prefix+"_access_token", tokenResp.AccessToken); err != nil {
		return "", time.Time{}, fmt.Errorf("store new access token: %w", err)
	}
	if err := store.Put(ctx, prefix+"_refresh_token", tokenResp.RefreshToken); err != nil {
		return "", time.Time{}, fmt.Errorf("store new refresh token: %w", err)
	}
	if err := store.Put(ctx, prefix+"_expiry", strconv.FormatInt(newExpiry.Unix(), 10)); err != nil {
		return "", time.Time{}, fmt.Errorf("store new expiry: %w", err)
	}

	return tokenResp.AccessToken, newExpiry, nil
}
