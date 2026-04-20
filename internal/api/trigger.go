package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type runNowBody struct {
	Actor *string `json:"actor,omitempty"`
}

// ServeHTTP implements POST /internal/schedules/{id}/run-now.
//
// Inserts a pending run row with fire_reason='manual' and fire_time=NULL.
// The scheduler picks it up on the next tick and dispatches via the normal
// path. Returns {"run_id": "<uuid>"} on success.
//
// Not authenticated (NOT attached to `auth` middleware in server.go).
// Triggered by the local CLI; P3 will gate behind UI session auth. Until
// that ships we enforce a loopback-only check here: even if an operator
// binds `cronfoundry serve --addr 0.0.0.0`, remote callers are rejected
// with 403 rather than getting a free state-changing endpoint.
func (h runNowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "run-now is loopback-only", http.StatusForbidden)
		return
	}

	scheduleID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid schedule id", http.StatusBadRequest)
		return
	}

	// Decode optional body. Empty body is acceptable — Actor is optional.
	var body runNowBody
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil && err.Error() != "EOF" {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	q := dbgen.New(h.deps.Pool)
	sched, err := q.GetScheduleForTrigger(r.Context(), pgtype.UUID{Bytes: scheduleID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, "load schedule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Placeholder token hash — manual triggers don't have a JWT yet; the
	// scheduler will update this field when it dispatches. Use a random
	// value so the NOT NULL constraint is satisfied and future dispatch
	// can distinguish it from a real hash.
	placeholder, err := randomHex(32)
	if err != nil {
		http.Error(w, "gen placeholder: "+err.Error(), http.StatusInternalServerError)
		return
	}

	run, err := q.InsertRun(r.Context(), dbgen.InsertRunParams{
		OrgID:           sched.OrgID,
		ScheduleID:      sched.ScheduleID,
		SkillSha:        sched.SkillSha,
		FireTime:        pgtype.Timestamptz{}, // Valid:false -> NULL
		FireReason:      "manual",
		Actor:           body.Actor,
		RunnerTokenHash: "pending:" + placeholder,
	})
	if err != nil {
		http.Error(w, "insert run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"run_id": uuid.UUID(run.ID.Bytes).String(),
	})
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// isLoopbackRequest reports whether r.RemoteAddr is a loopback address
// (127.0.0.0/8 or ::1). An empty or malformed RemoteAddr is treated as
// non-loopback so the default is fail-closed.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Unparseable RemoteAddr — treat as untrusted.
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// "localhost" or similar — the stdlib dial resolves it before we
		// see it, so accepting the literal string is harmless but
		// unnecessary. Only accept actual IPs.
		return false
	}
	return ip.IsLoopback()
}
