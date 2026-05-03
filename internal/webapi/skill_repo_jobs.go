// internal/webapi/skill_repo_jobs.go
package webapi

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/skillrepo"
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

func (h *skillRepoHandler) proposeJob(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "skill-repo proposeJob not yet implemented", "internal")
}
