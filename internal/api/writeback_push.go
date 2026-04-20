package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/writeback"
)

type writebackPushBody struct {
	CommitSHA string `json:"commit_sha"`
	// RepoRoot is the runner-side absolute path of the clone.
	// The serve process pushes from here so the App private key never leaves it.
	// Caller must ensure this path remains valid until the request completes
	// (i.e. do not remove the clone directory before the HTTP response).
	RepoRoot string `json:"repo_root"`
}

// writebackPushHandler implements POST /internal/runs/{id}/writeback-push.
//
// The runner has already committed the <memory> block locally; this endpoint
// mints an installation token and performs the actual git push. Kept
// server-side so the App private key never leaves the serve process.
type writebackPushHandler struct{ deps Deps }

func (h writebackPushHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if ClaimsFromContext(r.Context()).RunID != urlRunID {
		http.Error(w, "token run_id mismatch", http.StatusForbidden)
		return
	}

	var body writebackPushBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.CommitSHA == "" {
		http.Error(w, "commit_sha required", http.StatusBadRequest)
		return
	}
	if body.RepoRoot == "" {
		http.Error(w, "repo_root required", http.StatusBadRequest)
		return
	}
	// Validate that RepoRoot is within the OS temp directory to prevent path
	// traversal from a compromised runner subprocess.
	tmpDir := os.TempDir()
	absRoot, err := filepath.Abs(body.RepoRoot)
	if err != nil || !strings.HasPrefix(absRoot+string(filepath.Separator), tmpDir+string(filepath.Separator)) {
		http.Error(w, "repo_root outside allowed prefix", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(absRoot); err != nil {
		http.Error(w, "repo_root not readable: "+err.Error(), http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	cfg, err := q.GetRunWritebackConfig(r.Context(), pgtype.UUID{Bytes: urlRunID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, "load run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Reject the request if writeback is not enabled for this schedule.
	var wbCfg config.WritebackConfig
	if len(cfg.WritebackJson) > 0 {
		_ = json.Unmarshal(cfg.WritebackJson, &wbCfg)
	}
	if !wbCfg.Enabled {
		http.Error(w, "writeback not enabled for this run", http.StatusBadRequest)
		return
	}

	tok, err := h.deps.Installations.Token(r.Context(), cfg.GithubAppInstallID)
	if err != nil {
		slog.Error("writeback_push: mint install token failed",
			"run_id", urlRunID, "install_id", cfg.GithubAppInstallID, "err", err)
		http.Error(w, "could not authenticate with GitHub", http.StatusBadGateway)
		return
	}

	pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", tok, cfg.Owner, cfg.RepoName)

	writer := writeback.New()
	if err := writer.PushToURL(absRoot, pushURL); err != nil {
		slog.Error("writeback_push: push failed",
			"run_id", urlRunID, "owner", cfg.Owner, "repo", cfg.RepoName, "err", err)
		http.Error(w, "push failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
