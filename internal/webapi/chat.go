package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gambtho/cronfoundry/internal/chat"
	"github.com/gambtho/cronfoundry/internal/llm"
	"github.com/gambtho/cronfoundry/internal/secrets/server"
)

// ChatConfig configures the in-app assistant. The config is operator-set
// at startup (env vars in cmd/cronfoundry/serve.go); it is intentionally
// separate from per-schedule provider config so jobs and the assistant can
// use different models / API keys / budgets.
type ChatConfig struct {
	// Enabled gates the entire chat surface. When false, /api/chat/* return
	// 503 and the SPA hides the dock. Defaults to false; the operator must
	// opt in by setting the assistant env vars.
	Enabled bool
	// Provider is the llm.NewProvider name ("openai", "anthropic",
	// "copilot-enterprise", ...).
	Provider string
	// Model is the provider-specific model id (e.g. "claude-sonnet-4-5").
	Model string
	// APIKeySecret is the secret store NAME holding the API key. Used by
	// providers that authenticate with a long-lived API key (openai,
	// anthropic, azure-foundry, openrouter). Ignored when Provider is
	// "copilot-enterprise" — see CopilotPrefix.
	APIKeySecret string
	// CopilotPrefix is the secret-store prefix that holds the Copilot
	// Enterprise token bundle (e.g. "copilot" maps to
	// copilot-access-token + cached copilot-ide-token / -ide-expiry).
	// Used only when Provider == "copilot-enterprise". The operator
	// connects Copilot once via the OAuth device flow (Settings →
	// Providers); ResolveCopilotToken handles IDE-token caching on the
	// way to api.githubcopilot.com.
	CopilotPrefix string
	// MaxTurns caps the tool-using loop. 0 ⇒ chat package default.
	MaxTurns int
	// MaxTokens caps each turn's output. 0 ⇒ chat package default.
	MaxTokens int
}

// chatRequest is the JSON body of POST /api/chat/stream.
type chatRequest struct {
	Messages    []chat.Message `json:"messages"`
	PageContext string         `json:"page_context"`
}

type chatHandler struct {
	deps Deps
}

func (h *chatHandler) stream(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Chat.Enabled {
		writeErr(w, http.StatusServiceUnavailable, "chat assistant is not configured", "chat_disabled")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported", "internal")
		return
	}

	// Cap the body before decoding so a single authenticated request can't
	// force unbounded allocations. 64 KiB comfortably fits the 40-message
	// trim below at realistic chat sizes; the SPA can chunk longer payloads
	// if real usage demands more.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large", "payload_too_large")
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid body", "bad_request")
		return
	}
	if len(req.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages must be non-empty", "bad_request")
		return
	}
	// Cap conversation length to keep tokens (and abuse) bounded. The dock
	// summarizes/trims older turns client-side as well, but defense in depth.
	if len(req.Messages) > 40 {
		req.Messages = req.Messages[len(req.Messages)-40:]
	}

	claims := mustClaims(r)
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}

	provider, err := llm.NewProvider(h.deps.Chat.Provider)
	if err != nil {
		slog.Error("chat: bad provider", "provider", h.deps.Chat.Provider, "err", err)
		writeErr(w, http.StatusInternalServerError, "chat provider misconfigured", "config")
		return
	}
	tcp, ok := provider.(llm.ToolCapableProvider)
	if !ok {
		writeErr(w, http.StatusInternalServerError,
			fmt.Sprintf("provider %q does not support tool use", h.deps.Chat.Provider),
			"provider_tool_unsupported")
		return
	}

	apiKey, err := resolveChatCredentials(r.Context(), h.deps)
	if err != nil {
		// resolveChatCredentials maps known failure modes to a structured
		// error so we can surface re-auth (Copilot OAuth revoked) distinctly
		// from a missing secret or a transient mint outage.
		var ce credentialErr
		if errors.As(err, &ce) {
			writeErr(w, ce.status, ce.msg, ce.code)
			return
		}
		slog.Error("chat: credential fetch failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to load chat credentials", "internal")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sink := &sseSink{w: w, flusher: flusher}

	cfg := chat.Config{
		Provider:    tcp,
		Model:       h.deps.Chat.Model,
		APIKey:      apiKey,
		MaxTurns:    h.deps.Chat.MaxTurns,
		MaxTokens:   h.deps.Chat.MaxTokens,
		Tools:       chat.Toolbox{Queries: h.deps.Queries, OrgID: org.ID},
		Caller:      chat.Caller{Login: claims.Login, Role: claims.Role},
		PageContext: sanitizePageContext(req.PageContext),
	}

	// The Run loop produces final text plus usage. Streaming has already
	// happened via sink — final text is the same as the streamed deltas
	// concatenated, and we send a "done" event with usage stats so the UI
	// can show the cost.
	finalText, usage, err := chat.Run(r.Context(), cfg, req.Messages, sink)
	if err != nil {
		slog.Error("chat: run failed", "err", err, "actor", claims.Login)
		sink.event("error", map[string]string{"message": "I hit an error talking to the model. Try again in a moment."})
		return
	}

	sink.event("done", map[string]any{
		"final":         finalText,
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	})
}

// credentialErr is returned by resolveChatCredentials when the operator
// needs to take a specific action (re-auth, configure a missing secret).
// Mapping it to writeErr at one place keeps the handler readable and the
// failure-mode → HTTP-code mapping consistent.
type credentialErr struct {
	status int
	code   string
	msg    string
}

func (e credentialErr) Error() string { return e.msg }

// resolveChatCredentials returns a usable bearer token for the configured
// chat provider. For long-lived-API-key providers (openai, anthropic,
// azure-foundry, openrouter) it reads APIKeySecret from the secret store.
// For copilot-enterprise it resolves the short-lived IDE token via the same
// OAuth-mint path the runner uses. Both paths return tokens with no
// per-request user identity attached — chat is a single-tenant assistant,
// not a per-user proxy to GitHub Copilot.
func resolveChatCredentials(ctx context.Context, deps Deps) (string, error) {
	if deps.Chat.Provider == "copilot-enterprise" {
		if deps.Chat.CopilotPrefix == "" {
			return "", credentialErr{
				status: http.StatusServiceUnavailable,
				code:   "chat_disabled",
				msg:    "chat copilot prefix is not configured (set CRONFOUNDRY_CHAT_COPILOT_PREFIX)",
			}
		}
		token, _, err := ResolveCopilotToken(ctx, deps.Secrets, deps.Chat.CopilotPrefix, nil)
		if err != nil {
			if errors.Is(err, ErrCopilotReauthRequired) {
				return "", credentialErr{
					status: http.StatusUnauthorized,
					code:   "copilot_token_reauth",
					msg:    "copilot oauth token revoked or missing; reconnect from Settings → Providers",
				}
			}
			slog.Error("chat: copilot token resolve failed", "err", err)
			return "", credentialErr{
				status: http.StatusServiceUnavailable,
				code:   "copilot_token_mint",
				msg:    "copilot token mint failed",
			}
		}
		return token, nil
	}

	if deps.Chat.APIKeySecret == "" {
		return "", credentialErr{
			status: http.StatusServiceUnavailable,
			code:   "chat_disabled",
			msg:    "chat api key secret is not configured",
		}
	}
	apiKey, err := deps.Secrets.Get(ctx, deps.Chat.APIKeySecret)
	if err != nil {
		if errors.Is(err, server.ErrNotFound) {
			return "", credentialErr{
				status: http.StatusServiceUnavailable,
				code:   "chat_disabled",
				msg:    fmt.Sprintf("chat api key secret %q is not set", deps.Chat.APIKeySecret),
			}
		}
		return "", err
	}
	return apiKey, nil
}

// sanitizePageContext bounds the size of the operator-supplied context so
// a malicious or buggy client can't blow up the system prompt. We also
// drop control characters that would disrupt the prompt rendering.
func sanitizePageContext(s string) string {
	if len(s) > 256 {
		s = s[:256]
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			out = append(out, []byte(string(r))...)
		} else if r == ' ' || r == '\t' {
			out = append(out, byte(r))
		}
	}
	return string(out)
}

// sseSink emits chat.Sink events as Server-Sent Events on the response
// stream. The event names — "token", "tool_start", "tool_end", "error",
// "done" — are the wire contract consumed by the React dock.
type sseSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseSink) Token(delta string) {
	s.event("token", map[string]string{"delta": delta})
}

func (s *sseSink) ToolStart(name string, input json.RawMessage) {
	s.event("tool_start", map[string]any{"name": name, "input": input})
}

func (s *sseSink) ToolEnd(name string, errMsg string) {
	payload := map[string]any{"name": name}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	s.event("tool_end", payload)
}

func (s *sseSink) event(name string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, body)
	s.flusher.Flush()
}

// info handles GET /api/chat/info. It returns whether chat is enabled so
// the SPA can hide the dock when the operator hasn't configured it.
func (h *chatHandler) info(w http.ResponseWriter, r *http.Request) {
	_ = r // unused; included for handler signature consistency
	type out struct {
		Enabled bool   `json:"enabled"`
		Model   string `json:"model,omitempty"`
	}
	o := out{Enabled: h.deps.Chat.Enabled}
	if h.deps.Chat.Enabled {
		o.Model = h.deps.Chat.Model
	}
	writeJSON(w, http.StatusOK, o)
}
