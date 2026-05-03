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
	// Provider is the llm.NewProvider name ("openai", "anthropic", ...).
	Provider string
	// Model is the provider-specific model id (e.g. "claude-sonnet-4-5").
	Model string
	// APIKeySecret is the secret store NAME holding the API key. Resolved
	// per-request through deps.Secrets so rotations take effect immediately.
	APIKeySecret string
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

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	apiKey, err := h.deps.Secrets.Get(r.Context(), h.deps.Chat.APIKeySecret)
	if err != nil {
		if errors.Is(err, server.ErrNotFound) {
			writeErr(w, http.StatusServiceUnavailable,
				fmt.Sprintf("chat api key secret %q is not set", h.deps.Chat.APIKeySecret),
				"chat_disabled")
			return
		}
		slog.Error("chat: secret fetch failed", "err", err)
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

// chatStreamingClient is unused in production — kept here as a doc anchor
// for the SSE wire format. The browser EventSource API uses GET only, so
// the dock uses fetch + ReadableStream against POST /api/chat/stream.
var _ = context.Background
