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

	"github.com/gambtho/cronfoundry/internal/secrets/server"
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
// secret store. Behavior:
//
//  1. If the cached access token is still inside its stated expiry window
//     (with a 60s skew margin), return it verbatim.
//  2. If expiry has passed AND a refresh token is available, exchange the
//     refresh token for a new access token + expiry, persist, and return.
//  3. If expiry has passed but NO refresh token is stored, return the cached
//     access token anyway. GitHub's public Copilot OAuth client
//     (Iv1.b507a08c87ecfe98) issues access tokens with an expires_in field
//     but does not issue refresh tokens for the device-flow grant. In
//     practice these tokens often outlive their stated expires_in; we let
//     the downstream Copilot API surface the real "expired/revoked" signal
//     via 401 to the runner. The runner's existing non-2xx handling treats
//     that as a normal run failure with a clear message, which is more
//     useful than a synthetic 503 from us. To force re-auth, an operator
//     re-runs `cronfoundry admin connect-copilot`.
//
// githubOverrideURL overrides the GitHub token endpoint for tests; pass nil
// to use the real GitHub endpoint.
func ResolveCopilotToken(ctx context.Context, store server.SecretStore, prefix string, githubOverrideURL *string) (string, time.Time, error) {
	expiryStr, err := store.Get(ctx, prefix+"-expiry")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read expiry: %w", err)
	}

	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse expiry %q: %w", expiryStr, err)
	}
	expiry := time.Unix(expiryUnix, 0)

	if time.Until(expiry) >= 60*time.Second {
		tok, err := store.Get(ctx, prefix+"-access-token")
		if err != nil {
			return "", time.Time{}, fmt.Errorf("read access token: %w", err)
		}
		return tok, expiry, nil
	}

	// Expired (or within 60s of expiry). Try to refresh, but only if we have
	// a refresh token. See header comment for why a missing refresh token
	// is a soft signal rather than a hard failure.
	refreshTok, err := store.Get(ctx, prefix+"-refresh-token")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read refresh token: %w", err)
	}
	if refreshTok == "" {
		// No refresh token stored. Return the cached access token and let
		// the downstream call decide whether it still works.
		tok, err := store.Get(ctx, prefix+"-access-token")
		if err != nil {
			return "", time.Time{}, fmt.Errorf("read access token: %w", err)
		}
		slog.Warn("copilot token: stored expiry has passed but no refresh token available; returning cached access token (Copilot may still accept it)",
			"prefix", prefix, "expiry", expiry.Format(time.RFC3339))
		return tok, expiry, nil
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
	defer func() { _ = resp.Body.Close() }()

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
	if err := store.Put(ctx, prefix+"-access-token", tokenResp.AccessToken); err != nil {
		return "", time.Time{}, fmt.Errorf("store new access token: %w", err)
	}
	if err := store.Put(ctx, prefix+"-refresh-token", tokenResp.RefreshToken); err != nil {
		return "", time.Time{}, fmt.Errorf("store new refresh token: %w", err)
	}
	if err := store.Put(ctx, prefix+"-expiry", strconv.FormatInt(newExpiry.Unix(), 10)); err != nil {
		return "", time.Time{}, fmt.Errorf("store new expiry: %w", err)
	}

	return tokenResp.AccessToken, newExpiry, nil
}
