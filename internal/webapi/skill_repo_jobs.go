// internal/webapi/skill_repo_jobs.go
package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/skillrepo"
	"github.com/gambtho/cronfoundry/internal/yamledit"
)

// SkillRepoClient is the subset of *skillrepo.Client that proposeJob uses.
// Declared as an interface so tests can inject a fake.
type SkillRepoClient interface {
	GetFile(ctx context.Context, installID int64, owner, repo, path, ref string) (*skillrepo.FileContents, error)
	CreateBranch(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error
	PutFile(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error
	CreatePR(ctx context.Context, installID int64, req skillrepo.PRRequest) (*skillrepo.PRResult, error)
}

// YamlAppendScheduleFunc is the function-shape of yamledit.AppendScheduleToSkill.
type YamlAppendScheduleFunc func(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error)

type proposeJobRequest struct {
	SkillPath string           `json:"skill_path"`
	Schedule  *config.Schedule `json:"schedule"`
}

type proposeJobResponse struct {
	PRURL    string `json:"pr_url"`
	PRNumber int    `json:"pr_number"`
	Branch   string `json:"branch"`
}

// resolvedConn is the small subset of repo_connection that proposeJob needs.
// Stored on Deps via testConnOverride so unit tests can bypass DB lookup.
type resolvedConn struct {
	Owner         string
	Name          string
	DefaultBranch string
	InstallID     int64
	OrgID         pgtype.UUID
	ConnID        pgtype.UUID
}

type skillRepoHandler struct{ deps Deps }

// loadConn resolves the org's single repo_connection. Returns
// pgx.ErrNoRows when no connection exists. Tests can inject
// h.deps.testConnOverride to bypass DB.
func (h *skillRepoHandler) loadConn(ctx context.Context) (*resolvedConn, error) {
	if h.deps.testConnOverride != nil {
		return h.deps.testConnOverride, nil
	}
	if h.deps.Queries == nil {
		return nil, errors.New("skill-repo: deps.Queries not configured")
	}
	org, err := h.deps.Queries.GetFirstOrganization(ctx)
	if err != nil {
		return nil, fmt.Errorf("load org: %w", err)
	}
	rows, err := h.deps.Queries.ListRepoConnections(ctx, org.ID)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	if len(rows) == 0 {
		return nil, pgx.ErrNoRows
	}
	row := rows[0] // v1: a single org has one connection (per dogfood install flow).
	return &resolvedConn{
		Owner:         row.Owner,
		Name:          row.Name,
		DefaultBranch: row.DefaultBranch,
		InstallID:     row.GithubAppInstallID,
		OrgID:         org.ID,
		ConnID:        row.ID,
	}, nil
}

func (h *skillRepoHandler) proposeJob(w http.ResponseWriter, r *http.Request) {
	var req proposeJobRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error(), "bad_request")
		return
	}
	if strings.TrimSpace(req.SkillPath) == "" {
		writeErr(w, http.StatusBadRequest, "skill_path is required", "validation")
		return
	}
	if req.Schedule == nil {
		writeErr(w, http.StatusBadRequest, "schedule is required", "validation")
		return
	}
	if strings.TrimSpace(req.Schedule.Name) == "" {
		writeErr(w, http.StatusBadRequest, "schedule.name is required", "validation")
		return
	}

	conn, err := h.loadConn(r.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "no skill repo connected; connect one first", "no_connection")
			return
		}
		writeErr(w, http.StatusInternalServerError, "load connection: "+err.Error(), "internal")
		return
	}

	const filePath = "cronfoundry.yaml"
	file, err := h.deps.SkillRepoClient.GetFile(r.Context(), conn.InstallID, conn.Owner, conn.Name, filePath, conn.DefaultBranch)
	if err != nil {
		if errors.Is(err, skillrepo.ErrFileNotFound) {
			writeErr(w, http.StatusBadRequest, "cronfoundry.yaml not found on default branch", "no_manifest")
			return
		}
		writeErr(w, http.StatusBadGateway, "github get file: "+err.Error(), "gateway")
		return
	}

	updated, err := h.deps.YamlEditAppendSchedule(file.Content, req.SkillPath, req.Schedule)
	if err != nil {
		switch {
		case errors.Is(err, yamledit.ErrSkillNotFound):
			writeErr(w, http.StatusConflict, "skill_path not in cronfoundry.yaml", "skill_not_found")
		case errors.Is(err, yamledit.ErrDuplicateScheduleName):
			writeErr(w, http.StatusConflict, "schedule name already exists under skill", "duplicate_name")
		default:
			writeErr(w, http.StatusBadRequest, "yaml edit: "+err.Error(), "validation")
		}
		return
	}

	// Belt-and-suspenders: rerun ParseManifest on the rewritten YAML.
	if _, err := config.ParseManifest(updated); err != nil {
		writeErr(w, http.StatusBadRequest, "manifest validation: "+err.Error(), "validation")
		return
	}

	branch := buildBranchName(req.Schedule.Name)
	if err := h.deps.SkillRepoClient.CreateBranch(r.Context(), conn.InstallID, conn.Owner, conn.Name, branch, file.HeadSHA); err != nil {
		if errors.Is(err, skillrepo.ErrConflict) {
			writeErr(w, http.StatusConflict, "branch already exists; retry", "branch_conflict")
			return
		}
		writeErr(w, http.StatusBadGateway, "create branch: "+err.Error(), "gateway")
		return
	}

	commitMsg := fmt.Sprintf("chore(cronfoundry): add job %s to %s", req.Schedule.Name, req.SkillPath)
	if err := h.deps.SkillRepoClient.PutFile(r.Context(), conn.InstallID, conn.Owner, conn.Name, branch, filePath, file.FileSHA, commitMsg, updated); err != nil {
		if errors.Is(err, skillrepo.ErrConflict) {
			writeErr(w, http.StatusConflict, "stale file sha; retry", "sha_conflict")
			return
		}
		writeErr(w, http.StatusBadGateway, "put file: "+err.Error(), "gateway")
		return
	}

	prBody := buildPRBody(req.SkillPath, req.Schedule)
	pr, err := h.deps.SkillRepoClient.CreatePR(r.Context(), conn.InstallID, skillrepo.PRRequest{
		Owner:  conn.Owner,
		Repo:   conn.Name,
		Branch: branch,
		Base:   conn.DefaultBranch,
		Title:  commitMsg,
		Body:   prBody,
	})
	if err != nil {
		if errors.Is(err, skillrepo.ErrPermissionRequired) {
			writePermissionRequired(w, h.deps)
			return
		}
		if errors.Is(err, skillrepo.ErrConflict) {
			writeErr(w, http.StatusConflict, "pull request already open for branch", "pr_conflict")
			return
		}
		writeErr(w, http.StatusBadGateway, "create pr: "+err.Error(), "gateway")
		return
	}

	slog.Info("skill_repo: PR opened",
		"skill_path", req.SkillPath,
		"schedule_name", req.Schedule.Name,
		"pr_url", pr.HTMLURL,
		"pr_number", pr.Number)

	writeJSON(w, http.StatusOK, proposeJobResponse{
		PRURL:    pr.HTMLURL,
		PRNumber: pr.Number,
		Branch:   branch,
	})
}

var branchSafe = regexp.MustCompile(`[^a-z0-9-]+`)

func buildBranchName(scheduleName string) string {
	safe := branchSafe.ReplaceAllString(strings.ToLower(scheduleName), "-")
	safe = strings.Trim(safe, "-")
	return fmt.Sprintf("cronfoundry/add-job-%s-%d", safe, time.Now().Unix())
}

func buildPRBody(skillPath string, s *config.Schedule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Adds a new schedule **%s** to `%s` in cronfoundry.yaml.\n\n", s.Name, skillPath)
	fmt.Fprintf(&b, "- cron: `%s` (%s)\n", s.Cron, defaultStr(s.Timezone, "UTC"))
	fmt.Fprintf(&b, "- provider: %s, model: %s\n", s.Provider, s.Model)
	if len(s.Destinations) > 0 {
		fmt.Fprintf(&b, "- destinations: %d\n", len(s.Destinations))
	}
	if s.Writeback != nil && s.Writeback.Enabled {
		fmt.Fprintf(&b, "- writeback: %s (%s)\n", s.Writeback.Path, s.Writeback.Mode)
	}
	b.WriteString("\nGenerated via the CronFoundry dashboard.")
	return b.String()
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// writePermissionRequired surfaces 412 with a CTA URL pointing at the
// App's permissions-review page.
func writePermissionRequired(w http.ResponseWriter, deps Deps) {
	slug := deps.GitHubAppSlug
	if slug == "" {
		slug = "cronfoundry"
	}
	reviewURL := fmt.Sprintf("https://github.com/settings/apps/%s/permissions", slug)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPreconditionFailed)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      "github app missing pull_requests:write permission",
		"code":       "permission_required",
		"review_url": reviewURL,
	})
}
