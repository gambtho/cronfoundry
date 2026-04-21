package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// ServeHTTP implements GET /internal/secrets?names=a,b,c.
//
// Each requested name must appear in the JWT's SecretRefs claim; otherwise
// the handler returns 403 without revealing whether the secret exists. This
// is the cryptographic enforcement of per-run secret scoping.
//
// Every access attempt is recorded as a run_event for audit: secret.fetched
// on success, secret.denied on out-of-scope (logged BEFORE the 403 is sent
// so the audit ordering matches reality), and secret.fetch_error when the
// store returns an error. Event persistence is best-effort — a failed insert
// is logged and swallowed so runners never fail purely because the audit
// trail hit a hiccup.
func (h secretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	allowed := make(map[string]struct{}, len(claims.SecretRefs))
	for _, n := range claims.SecretRefs {
		allowed[n] = struct{}{}
	}

	namesParam := r.URL.Query().Get("names")
	if namesParam == "" {
		http.Error(w, "missing names query parameter", http.StatusBadRequest)
		return
	}
	// Reject malformed lists up front so `names=,` or `names=%20` can't
	// partially fetch earlier tokens or write confusing empty-name events.
	rawNames := strings.Split(namesParam, ",")
	names := make([]string, 0, len(rawNames))
	for _, raw := range rawNames {
		name := strings.TrimSpace(raw)
		if name == "" {
			http.Error(w, "secret names must be non-empty", http.StatusBadRequest)
			return
		}
		names = append(names, name)
	}

	q := dbgen.New(h.deps.Pool)
	runID := pgtype.UUID{Bytes: claims.RunID, Valid: true}

	out := make(map[string]string, len(names))
	for _, name := range names {
		if _, ok := allowed[name]; !ok {
			h.logSecretEvent(r, q, runID, "warn", "secret.denied", map[string]any{
				"name":        name,
				"in_manifest": false,
			})
			http.Error(w, "secret not in token scope: "+name, http.StatusForbidden)
			return
		}
		val, err := h.deps.Secrets.Get(r.Context(), name)
		if err != nil {
			// Log the real error server-side; keep the run-event payload
			// generic so runners don't persist backend infra details (KV
			// URLs, upstream HTTP bodies) in the run timeline.
			slog.Warn("secret fetch failed", "name", name, "run_id", runID.Bytes, "err", err)
			h.logSecretEvent(r, q, runID, "error", "secret.fetch_error", map[string]any{
				"name":        name,
				"in_manifest": true,
				"error":       "secret_store_error",
			})
			http.Error(w, "load secret failed", http.StatusInternalServerError)
			return
		}
		h.logSecretEvent(r, q, runID, "info", "secret.fetched", map[string]any{
			"name":        name,
			"in_manifest": true,
		})
		out[name] = val
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// logSecretEvent inserts a run_event describing a secret access attempt.
// Failures are logged (so operators can spot audit gaps) but never surfaced
// to the caller; the HTTP response is unaffected.
func (h secretsHandler) logSecretEvent(
	r *http.Request,
	q *dbgen.Queries,
	runID pgtype.UUID,
	level, eventType string,
	payload map[string]any,
) {
	raw, _ := json.Marshal(payload)
	if err := q.InsertRunEvent(r.Context(), dbgen.InsertRunEventParams{
		RunID:       runID,
		Level:       level,
		EventType:   eventType,
		PayloadJson: raw,
	}); err != nil {
		slog.Warn("failed to persist secret run_event",
			"run_id", runID.Bytes, "event_type", eventType, "err", err)
	}
}
