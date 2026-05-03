package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/skillrepo"
)

// fakeSkillRepoClient lets tests assert calls and inject errors.
type fakeSkillRepoClient struct {
	getFile      func(ctx context.Context, installID int64, owner, repo, path, ref string) (*skillrepo.FileContents, error)
	createBranch func(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error
	putFile      func(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error
	createPR     func(ctx context.Context, installID int64, req skillrepo.PRRequest) (*skillrepo.PRResult, error)
}

func (f *fakeSkillRepoClient) GetFile(ctx context.Context, installID int64, owner, repo, path, ref string) (*skillrepo.FileContents, error) {
	return f.getFile(ctx, installID, owner, repo, path, ref)
}
func (f *fakeSkillRepoClient) CreateBranch(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error {
	return f.createBranch(ctx, installID, owner, repo, branch, fromSHA)
}
func (f *fakeSkillRepoClient) PutFile(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error {
	return f.putFile(ctx, installID, owner, repo, branch, path, fileSHA, message, content)
}
func (f *fakeSkillRepoClient) CreatePR(ctx context.Context, installID int64, req skillrepo.PRRequest) (*skillrepo.PRResult, error) {
	return f.createPR(ctx, installID, req)
}

// proposeJobReq does an authenticated-style POST to the handler under test.
// We call the handler directly, bypassing session/csrf middleware.
func proposeJobReq(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/skill-repo/jobs", bytes.NewReader(buf))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestProposeJob_RejectsEmptySkillPath(t *testing.T) {
	h := &skillRepoHandler{deps: Deps{}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "skill_path") {
		t.Errorf("body should mention skill_path: %s", w.Body.String())
	}
}

func TestProposeJob_RejectsEmptyScheduleName(t *testing.T) {
	h := &skillRepoHandler{deps: Deps{}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/x",
		Schedule:  &config.Schedule{Name: ""},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestProposeJob_RejectsNilSchedule(t *testing.T) {
	h := &skillRepoHandler{deps: Deps{}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), map[string]any{
		"skill_path": "skills/x",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

func TestProposeJob_RejectsMalformedJSON(t *testing.T) {
	h := &skillRepoHandler{deps: Deps{}}
	r := httptest.NewRequest("POST", "/api/skill-repo/jobs", strings.NewReader("{not json"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.proposeJob(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", w.Code)
	}
}

// silence "imported and not used" until later tasks reference these.
var (
	_ = errors.New
	_ = context.Background
	_ = (*fakeSkillRepoClient)(nil)
)
