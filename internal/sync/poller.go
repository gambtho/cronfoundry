package sync

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
)

// PollerConfig bundles the poller's collaborators. Only Pool, OrgID, and
// Installations are required; the rest have sensible defaults.
//
// OrgID is retained for the P2c scheduler loop — it'll be used to list the
// connections belonging to this org ("list connections for this org"
// queries). It is NOT used for per-row upserts in SyncOne; each
// repo_connection row carries its own OrgID and is the source of truth for
// downstream skill/schedule writes to prevent cross-tenant writes if someone
// ever hands SyncOne a connID from a different org.
type PollerConfig struct {
	Pool          *pgxpool.Pool
	OrgID         pgtype.UUID
	Installations *github.InstallationCache
	GitHubBaseURL string                          // default: "https://api.github.com"
	HTTPClient    *http.Client                    // default: http.DefaultClient
	CloneURLFor   func(owner, name string) string // default: x-access-token HTTPS
}

// Poller orchestrates repo-sync passes. P2c will add a Run() loop that
// schedules SyncOne against all due repo_connection rows on a ticker.
type Poller struct {
	cfg PollerConfig
}

// NewPoller constructs a Poller with sensible defaults.
func NewPoller(cfg PollerConfig) *Poller {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.GitHubBaseURL == "" {
		cfg.GitHubBaseURL = "https://api.github.com"
	}
	if cfg.CloneURLFor == nil {
		cfg.CloneURLFor = func(owner, name string) string {
			return fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
		}
	}
	return &Poller{cfg: cfg}
}

// SyncOne runs a single sync pass for the named repo_connection.
//
// Flow:
//  1. Load the repo_connection row.
//  2. Fetch an installation token.
//  3. HEAD-check the default branch via the GitHub API.
//  4. If the SHA matches last_synced_head_sha, touch last_synced_at and return.
//  5. Otherwise shallow-clone the repo at the new SHA into a tempdir.
//  6. Parse cronfoundry.yaml + SKILL.md files.
//  7. Upsert skill + schedule rows.
//  8. Mark the repo_connection as synced at the new SHA.
//
// Errors at any step are recorded in last_sync_error and surfaced to the
// caller; the row's last_synced_at is still advanced so a broken repo
// doesn't back up the queue.
func (p *Poller) SyncOne(ctx context.Context, connID pgtype.UUID) error {
	q := dbgen.New(p.cfg.Pool)

	row, err := q.GetRepoConnection(ctx, connID)
	if err != nil {
		return fmt.Errorf("sync: SyncOne: load conn: %w", err)
	}

	token, err := p.cfg.Installations.Token(ctx, row.GithubAppInstallID)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("install token: %w", err))
	}

	headSHA, err := github.GetBranchHead(
		ctx, p.cfg.HTTPClient, p.cfg.GitHubBaseURL, token,
		row.Owner, row.Name, row.DefaultBranch,
	)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("head check: %w", err))
	}

	// Cheap path: SHA unchanged.
	if row.LastSyncedHeadSha != nil && *row.LastSyncedHeadSha == headSHA {
		shaCopy := headSHA
		if err := q.MarkRepoSyncedOK(ctx, dbgen.MarkRepoSyncedOKParams{
			ID:                connID,
			LastSyncedHeadSha: &shaCopy,
		}); err != nil {
			return fmt.Errorf("sync: mark unchanged: %w", err)
		}
		return nil
	}

	// Expensive path: clone, parse, upsert.
	tmp, err := os.MkdirTemp("", "cronfoundry-clone-")
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("tempdir: %w", err))
	}
	// PlainClone refuses non-empty dirs; hand CloneAtSHA a fresh subpath.
	cloneDest := tmp + "/repo"
	defer func() { _ = os.RemoveAll(tmp) }()

	cloneURL := p.cfg.CloneURLFor(row.Owner, row.Name)
	if err := github.CloneAtSHA(ctx, cloneURL, token, headSHA, cloneDest); err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("clone: %w", err))
	}

	manifest, skills, err := LoadManifest(cloneDest)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("load manifest: %w", err))
	}

	// Use the connection row's own OrgID, not p.cfg.OrgID: the row is the
	// source of truth for which tenant owns these skills/schedules.
	if err := UpsertSkillsAndSchedules(ctx, p.cfg.Pool, row.OrgID, connID, manifest, skills, headSHA); err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("upsert: %w", err))
	}

	shaCopy := headSHA
	if err := q.MarkRepoSyncedOK(ctx, dbgen.MarkRepoSyncedOKParams{
		ID:                connID,
		LastSyncedHeadSha: &shaCopy,
	}); err != nil {
		return fmt.Errorf("sync: mark synced ok: %w", err)
	}
	return nil
}

// markErr persists the error to last_sync_error and returns it wrapped.
// last_synced_at is still advanced so we retry on the normal cadence rather
// than tight-looping on a broken repo. If persisting the error itself fails,
// log the failure — swallowing it silently would mask operator-visible state.
func (p *Poller) markErr(ctx context.Context, q *dbgen.Queries, connID pgtype.UUID, err error) error {
	msg := err.Error()
	if markErr := q.MarkRepoSyncError(ctx, dbgen.MarkRepoSyncErrorParams{
		ID:            connID,
		LastSyncError: &msg,
	}); markErr != nil {
		slog.Warn("sync: failed to persist sync error",
			"conn_id", connID,
			"original_err", err,
			"persist_err", markErr)
	}
	return fmt.Errorf("sync: SyncOne: %w", err)
}
