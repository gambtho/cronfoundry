package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/secrets/server"
)

// copilotIDETokenURL is GitHub's "internal" endpoint that mints the
// short-lived API key (the "IDE token") used for actual Copilot completions.
// The endpoint is undocumented but stable — every official Copilot client
// (vim/vscode/jetbrains plugins, plus third-party shims like LiteLLM's
// github_copilot adapter) uses this exact URL. It accepts a long-lived
// OAuth access token and returns a short-lived API key with an expires_at.
const copilotIDETokenURL = "https://api.github.com/copilot_internal/v2/token"

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

	ideToken, expiresAt, err := ResolveCopilotToken(r.Context(), h.deps.Secrets, refs.Prefix, nil)
	if err != nil {
		slog.Error("copilot token resolve failed", "run_id", r.PathValue("id"), "error", err)
		writeErr(w, http.StatusServiceUnavailable, "copilot token mint failed", "copilot_token_mint")
		return
	}

	writeJSON(w, http.StatusOK, copilotTokenHandlerResponse{
		AccessToken: ideToken,
		ExpiresAt:   expiresAt.UTC().Format(time.RFC3339),
	})
}

// ResolveCopilotToken returns a fresh Copilot IDE token (the API key sent
// to api.githubcopilot.com), minting a new one when the cached one is near
// expiry.
//
// GitHub Copilot uses a two-token system:
//
//  1. OAuth access token ("gho_..."): obtained once via device flow,
//     stored under <prefix>-access-token. Long-lived; doesn't expire on
//     its own. The empty <prefix>-refresh-token slot is a vestige of an
//     earlier (incorrect) assumption that Copilot's device flow returned
//     an OAuth refresh token — it doesn't, and we don't need one.
//
//  2. IDE token / API key: short-lived (~25-30 min), minted by GET-ing
//     https://api.github.com/copilot_internal/v2/token with the OAuth
//     token in `Authorization: token <oauth>`. Returns
//     {"token": "<api-key>", "expires_at": <unix>}. This is what
//     internal/llm/copilot.go sends as the Bearer token to
//     api.githubcopilot.com. We cache it under <prefix>-ide-token /
//     <prefix>-ide-expiry to avoid hitting GitHub on every run.
//
// On a fresh install the IDE-token cache is empty, so we mint
// unconditionally on the first call. After that we mint only when the
// cached token is within 60s of expiry.
//
// githubOverrideURL overrides the api.github.com base for tests; pass nil
// to use the real endpoint. The override applies to the IDE-token URL only;
// the path /copilot_internal/v2/token is appended.
func ResolveCopilotToken(ctx context.Context, store server.SecretStore, prefix string, githubOverrideURL *string) (string, time.Time, error) {
	if cached, exp, ok := readCachedIDEToken(ctx, store, prefix); ok {
		return cached, exp, nil
	}

	oauthToken, err := store.Get(ctx, prefix+"-access-token")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read oauth access token: %w", err)
	}
	if oauthToken == "" {
		return "", time.Time{}, fmt.Errorf("no oauth access token stored for prefix %q; run `cronfoundry admin connect-copilot`", prefix)
	}

	ideToken, expiresAt, err := mintIDEToken(ctx, oauthToken, githubOverrideURL)
	if err != nil {
		return "", time.Time{}, err
	}

	if err := writeCachedIDEToken(ctx, store, prefix, ideToken, expiresAt); err != nil {
		// Cache write failure is non-fatal — return the live token so the
		// run can proceed. We'll just re-mint on the next call.
		slog.Warn("copilot token: failed to cache IDE token; will re-mint next call",
			"prefix", prefix, "err", err)
	}

	return ideToken, expiresAt, nil
}

// readCachedIDEToken returns the cached IDE token + expiry if it exists and
// is at least 60s away from expiring. Any other condition (missing token,
// missing expiry, parse error, near-expiry) returns ok=false so the caller
// re-mints.
func readCachedIDEToken(ctx context.Context, store server.SecretStore, prefix string) (string, time.Time, bool) {
	tok, err := store.Get(ctx, prefix+"-ide-token")
	if err != nil || tok == "" {
		return "", time.Time{}, false
	}
	expStr, err := store.Get(ctx, prefix+"-ide-expiry")
	if err != nil || expStr == "" {
		return "", time.Time{}, false
	}
	expUnix, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	exp := time.Unix(expUnix, 0)
	if time.Until(exp) < 60*time.Second {
		return "", time.Time{}, false
	}
	return tok, exp, true
}

func writeCachedIDEToken(ctx context.Context, store server.SecretStore, prefix, token string, expiresAt time.Time) error {
	if err := store.Put(ctx, prefix+"-ide-token", token); err != nil {
		return fmt.Errorf("put ide token: %w", err)
	}
	if err := store.Put(ctx, prefix+"-ide-expiry", strconv.FormatInt(expiresAt.Unix(), 10)); err != nil {
		return fmt.Errorf("put ide expiry: %w", err)
	}
	return nil
}

type copilotIDETokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func mintIDEToken(ctx context.Context, oauthToken string, overrideBase *string) (string, time.Time, error) {
	url := copilotIDETokenURL
	if overrideBase != nil {
		url = strings.TrimRight(*overrideBase, "/") + "/copilot_internal/v2/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build mint request: %w", err)
	}
	// Mirror the headers the official Copilot clients (and LiteLLM's adapter)
	// send. GitHub's copilot_internal endpoint rejects requests that don't
	// look like a Copilot client.
	req.Header.Set("Authorization", "token "+oauthToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.85.1")
	req.Header.Set("Editor-Plugin-Version", "copilot/1.155.0")
	req.Header.Set("User-Agent", "GithubCopilot/1.155.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mint request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("mint failed: HTTP %d (oauth token may be revoked; re-run `cronfoundry admin connect-copilot`)", resp.StatusCode)
	}

	var body copilotIDETokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", time.Time{}, fmt.Errorf("mint: malformed JSON from GitHub: %w", err)
	}
	if body.Token == "" {
		return "", time.Time{}, fmt.Errorf("mint: GitHub returned no token")
	}
	return body.Token, time.Unix(body.ExpiresAt, 0), nil
}
