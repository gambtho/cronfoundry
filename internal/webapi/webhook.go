package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	gh "github.com/gambtho/cronfoundry/internal/github"
)

// RepoSyncer resolves a repo_connection by ID and triggers a single sync pass.
// The concrete implementation in serve.go wraps sync.Poller.SyncOne.
type RepoSyncer interface {
	SyncOne(ctx context.Context, connID pgtype.UUID) error
}

type webhookHandler struct {
	deps   Deps
	secret []byte
	syncer RepoSyncer
}

func (h *webhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	// Respond 200 to ping so GitHub reports the hook healthy.
	if event == "ping" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if event != "push" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(h.secret) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "webhook secret not configured", "config")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body", "bad_request")
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if err := gh.VerifyWebhookSignature(h.secret, body, sig); err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid signature", "unauthorized")
		return
	}

	var p gh.PushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "parse payload", "bad_request")
		return
	}
	owner := p.Repository.Owner.Login
	name := p.Repository.Name
	if owner == "" || name == "" {
		writeErr(w, http.StatusBadRequest, "missing repo coords", "bad_request")
		return
	}
	// Only resync pushes to the default branch. Other refs don't change
	// which schedules run.
	if p.Repository.DefaultBranch != "" && p.Ref != "refs/heads/"+p.Repository.DefaultBranch {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load org", "internal")
		return
	}
	conn, err := h.deps.Queries.GetRepoConnectionByOwnerName(r.Context(), dbgen.GetRepoConnectionByOwnerNameParams{
		OrgID: org.ID,
		Owner: owner,
		Name:  name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Webhook for a repo we don't track — log and 200 so GitHub
			// doesn't mark the hook unhealthy.
			slog.Info("webhook: unknown repo, ignoring", "owner", owner, "name", name)
			w.WriteHeader(http.StatusOK)
			return
		}
		writeErr(w, http.StatusInternalServerError, "lookup conn", "internal")
		return
	}

	if err := h.syncer.SyncOne(r.Context(), conn.ID); err != nil {
		slog.Warn("webhook: sync failed", "conn_id", conn.ID, "err", err)
		writeErr(w, http.StatusInternalServerError, "sync failed", "sync_error")
		return
	}
	w.WriteHeader(http.StatusOK)
}
