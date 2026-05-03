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
	"github.com/gambtho/cronfoundry/internal/yamledit"
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

const sampleManifest = `version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: existing
        cron: "0 9 * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: o/r
              title: t
`

func TestProposeJob_HappyPath(t *testing.T) {
	const (
		owner = "o"
		repo  = "r"
	)
	calls := struct {
		getFile      int
		createBranch int
		putFile      int
		createPR     int
	}{}
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, p, ref string) (*skillrepo.FileContents, error) {
			calls.getFile++
			if p != "cronfoundry.yaml" {
				t.Errorf("expected cronfoundry.yaml, got %s", p)
			}
			return &skillrepo.FileContents{
				Content: []byte(sampleManifest),
				FileSHA: "filesha",
				HeadSHA: "headsha",
			}, nil
		},
		createBranch: func(_ context.Context, _ int64, _, _, branch, sha string) error {
			calls.createBranch++
			if !strings.HasPrefix(branch, "cronfoundry/add-job-newjob-") {
				t.Errorf("branch: %s", branch)
			}
			if sha != "headsha" {
				t.Errorf("sha: %s", sha)
			}
			return nil
		},
		putFile: func(_ context.Context, _ int64, _, _, _, _, sha, msg string, content []byte) error {
			calls.putFile++
			if sha != "filesha" {
				t.Errorf("file sha: %s", sha)
			}
			if !bytes.Contains(content, []byte("newjob")) {
				t.Errorf("expected new job in content; got: %s", content)
			}
			if !strings.Contains(msg, "newjob") {
				t.Errorf("commit msg: %s", msg)
			}
			return nil
		},
		createPR: func(_ context.Context, _ int64, req skillrepo.PRRequest) (*skillrepo.PRResult, error) {
			calls.createPR++
			if req.Base != "main" {
				t.Errorf("base: %s", req.Base)
			}
			return &skillrepo.PRResult{HTMLURL: "https://github.com/o/r/pull/9", Number: 9}, nil
		},
	}
	yamlFn := YamlAppendScheduleFunc(func(b []byte, p string, s *config.Schedule) ([]byte, error) {
		// For the happy-path test, return a small but valid manifest with the
		// new schedule's name visible so the PutFile assertion can find it.
		out := []byte(`version: 1
skills:
  - path: ` + p + `
    schedules:
      - name: ` + s.Name + `
        cron: "0 9 * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: o/r
              title: ` + s.Name + `
`)
		return out, nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride: &resolvedConn{
			Owner:         owner,
			Name:          repo,
			DefaultBranch: "main",
			InstallID:     12345,
		},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/smoke",
		Schedule:  &config.Schedule{Name: "newjob"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", w.Code, w.Body.String())
	}
	var got proposeJobResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.PRURL != "https://github.com/o/r/pull/9" || got.PRNumber != 9 {
		t.Errorf("response: %+v", got)
	}
	if calls.getFile != 1 || calls.createBranch != 1 || calls.putFile != 1 || calls.createPR != 1 {
		t.Errorf("call counts: %+v", calls)
	}
}

func TestProposeJob_412_PermissionRequired(t *testing.T) {
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, _, _ string) (*skillrepo.FileContents, error) {
			return &skillrepo.FileContents{Content: []byte(sampleManifest), FileSHA: "f", HeadSHA: "h"}, nil
		},
		createBranch: func(_ context.Context, _ int64, _, _, _, _ string) error { return nil },
		putFile: func(_ context.Context, _ int64, _, _, _, _, _, _ string, _ []byte) error { return nil },
		createPR: func(_ context.Context, _ int64, _ skillrepo.PRRequest) (*skillrepo.PRResult, error) {
			return nil, skillrepo.ErrPermissionRequired
		},
	}
	yamlFn := YamlAppendScheduleFunc(func(b []byte, _ string, _ *config.Schedule) ([]byte, error) {
		// Return the same input as a valid manifest so ParseManifest passes.
		return b, nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		GitHubAppSlug:          "cronfoundry-test",
		testConnOverride: &resolvedConn{
			Owner:         "o",
			Name:          "r",
			DefaultBranch: "main",
			InstallID:     1,
		},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/smoke",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "permission_required" {
		t.Errorf("code: %q", body["code"])
	}
	if !strings.Contains(body["review_url"], "cronfoundry-test") {
		t.Errorf("review_url should include slug: %q", body["review_url"])
	}
}

func TestProposeJob_409_SkillNotFound(t *testing.T) {
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, _, _ string) (*skillrepo.FileContents, error) {
			return &skillrepo.FileContents{Content: []byte(sampleManifest), FileSHA: "f", HeadSHA: "h"}, nil
		},
	}
	yamlFn := YamlAppendScheduleFunc(func(_ []byte, _ string, _ *config.Schedule) ([]byte, error) {
		return nil, yamledit.ErrSkillNotFound
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride: &resolvedConn{
			Owner:         "o",
			Name:          "r",
			DefaultBranch: "main",
			InstallID:     1,
		},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/missing",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestProposeJob_400_ParseManifestFails(t *testing.T) {
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, _, _ string) (*skillrepo.FileContents, error) {
			return &skillrepo.FileContents{Content: []byte(sampleManifest), FileSHA: "f", HeadSHA: "h"}, nil
		},
	}
	// Return clearly invalid YAML so config.ParseManifest errors.
	yamlFn := YamlAppendScheduleFunc(func(_ []byte, _ string, _ *config.Schedule) ([]byte, error) {
		return []byte("\t\tnot a manifest at all"), nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride: &resolvedConn{
			Owner:         "o",
			Name:          "r",
			DefaultBranch: "main",
			InstallID:     1,
		},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/smoke",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestProposeJob_502_OnGitHubError(t *testing.T) {
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, _, _ string) (*skillrepo.FileContents, error) {
			return nil, errors.New("boom")
		},
	}
	yamlFn := YamlAppendScheduleFunc(func(b []byte, _ string, _ *config.Schedule) ([]byte, error) { return b, nil })
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride: &resolvedConn{
			Owner:         "o",
			Name:          "r",
			DefaultBranch: "main",
			InstallID:     1,
		},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/smoke",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}
