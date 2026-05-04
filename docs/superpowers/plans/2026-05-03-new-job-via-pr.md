# Add-job / Import-job via skill-repo PR + Run-now nav — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dead `+ New job` and `Import from yaml` buttons on the Jobs dashboard with two working flows that produce a PR against the connected skill repo (form-driven and YAML-paste flows share a single backend endpoint), and fix Run-now so it navigates to the newly created run.

**Architecture:** A new admin-only endpoint `POST /api/skill-repo/jobs` orchestrates a synchronous pipeline: fetch `cronfoundry.yaml` from the connected skill repo, run a comment-preserving YAML edit through a new `internal/yamledit` package, validate the result with `config.ParseManifest`, then create a branch, commit the file, and open a PR through a new `internal/skillrepo` GitHub client. The frontend gets two new pages (`JobNew`, `JobImport`) and wires the existing buttons to navigate to them; both pages submit the same JSON body so backend validation stays unified. Run-now's existing internal endpoint already returns `{run_id}`; the public handler simply forwards it and the SPA navigates on success.

**Tech Stack:**
- **Backend:** Go 1.25, `gopkg.in/yaml.v3` (already in indirect deps; promoted to direct), `sigs.k8s.io/yaml` (existing, used to marshal the new schedule struct), `github.com/google/go-github/v74` (existing).
- **Frontend:** React 18 + TypeScript, Vite, Vitest, React Router v6, TanStack Query, `js-yaml` (new dep, ~50 KB minified).
- **Worktree:** This plan executes in `/home/tng/workspace/cronfoundry/.claude/worktrees/dogfood-dog5` on branch `spec/new-job-via-pr` (already created off `origin/main`, with the spec committed at `dc69bbd`).

---

## File Structure

**New backend files:**

| Path | Responsibility |
| --- | --- |
| `internal/yamledit/append_schedule.go` | One exported function: `AppendScheduleToSkill(yaml []byte, skillPath string, sched *config.Schedule) ([]byte, error)`. Uses `yaml.v3` Node API to inject a new `schedules:` element under the matching `SkillEntry` while preserving comments / ordering / indentation. |
| `internal/yamledit/append_schedule_test.go` | Golden-file table tests covering the eight scenarios listed in the spec's "Tests / Backend / yamledit" section. |
| `internal/yamledit/testdata/...` | YAML fixtures (input + expected output). |
| `internal/skillrepo/client.go` | Thin go-github wrapper with one struct `Client` exposing `GetFile`, `CreateBranch`, `PutFile`, `CreatePR`. Returns typed errors (`ErrPermissionRequired`, `ErrConflict`, `ErrSkillNotFound`-passes-through). Mints install tokens via `internal/github.InstallationCache`. |
| `internal/skillrepo/client_test.go` | `httptest.Server`-driven tests, one per operation, modeled on `internal/publish/githubissue_test.go`. |
| `internal/webapi/skill_repo_jobs.go` | The handler `proposeJob` + small types (`proposeJobRequest`, `proposeJobResponse`). Wires yamledit + skillrepo + audit. |
| `internal/webapi/skill_repo_jobs_test.go` | Handler tests (six scenarios listed in the spec). Uses fakes for `skillRepoClient` and `yamleditFn` injected via Deps. |

**Modified backend files:**

| Path | Change |
| --- | --- |
| `internal/webapi/server.go` | Register `POST /api/skill-repo/jobs` under `adminOnly`. Add fields to `Deps` so the handler can be wired from `cmd/cronfoundry/serve.go` (`SkillRepoClient skillRepoClient`, `YamlEditAppendSchedule yamleditFn`). |
| `internal/webapi/schedules.go` | `runNow` handler decodes the internal `/run-now` JSON response and forwards `{run_id}` with 200 OK + `Content-Type: application/json` (replacing today's empty 202). |
| `internal/webapi/schedules_test.go` | Update existing run-now tests for the new body. Add a happy-path assertion that `{run_id}` is forwarded. |
| `internal/githubapp/manifest.go` | Add `"pull_requests": "write"` to `DefaultPerms`. |
| `internal/githubapp/manifest_test.go` | Update assertion on default perms. |
| `cmd/cronfoundry/serve.go` | Construct a `skillrepo.Client` from existing `installs` cache + `pool` and inject into `webapi.Deps.SkillRepoClient`. Inject `yamledit.AppendScheduleToSkill` into `webapi.Deps.YamlEditAppendSchedule`. |
| `go.mod` / `go.sum` | Promote `gopkg.in/yaml.v3` from indirect to direct (this happens automatically once we `import` it). |

**New frontend files:**

| Path | Responsibility |
| --- | --- |
| `web/src/pages/JobNew.tsx` | Form page: skill dropdown + required fields + collapsed advanced section. POST `proposeJob`. |
| `web/src/pages/JobNew.test.tsx` | Vitest: required-field validation, submit shape, 412 CTA, 400 inline error. |
| `web/src/pages/JobImport.tsx` | Skill dropdown + textarea + js-yaml parse on submit. POST same `proposeJob`. |
| `web/src/pages/JobImport.test.tsx` | Vitest: invalid YAML inline, valid YAML serializes to same shape as form. |
| `web/src/components/forms/DestinationsField.tsx` | Repeater: type selector (`github-issue`/`slack`/`discord`/`teams`/`http`/`email`) + `when` selector. Renders type-specific subform inline. |
| `web/src/components/forms/EnvField.tsx` | k/v repeater used for both `env` and `mcp_env`. |
| `web/src/components/forms/JobSuccessCard.tsx` | Shared "PR opened" card rendered by both pages on success — shows PR number, link, "merge then sync" copy. |
| `web/src/lib/useShortcut.ts` | Tiny `useShortcut(key, handler)` hook adding a global `keydown` listener. |
| `web/src/lib/api-error.ts` | `ApiError` subclass of `Error` carrying `code`, `status`, and arbitrary extra fields (e.g. `review_url`). |

**Modified frontend files:**

| Path | Change |
| --- | --- |
| `web/src/main.tsx` | Add `/jobs/new` and `/jobs/import` routes inside the auth `<Layout>` block. Import the two new pages. |
| `web/src/pages/Jobs.tsx` | Wire `+ Add job` and `+ Import job` button onClicks to `useNavigate()`. Use `useShortcut('n', ...)` for the `N` shortcut. Update Run-now mutation to navigate to `/runs/<id>` on success. |
| `web/src/pages/Jobs.test.tsx` | Add tests for navigation on click, on `N` shortcut, and Run-now nav. |
| `web/src/pages/JobDetail.tsx` | Update Run-now mutation to navigate to `/runs/<id>` on success. |
| `web/src/pages/JobDetail.test.tsx` | Add Run-now navigation test. |
| `web/src/lib/api.ts` | Throw `ApiError` (with `code`, `status`, extras) instead of plain `Error`. Add `api.skillRepo.proposeJob`. Update `api.schedules.runNow` return type to `{ run_id: string }`. |
| `web/package.json` / `web/package-lock.json` | Add `js-yaml` and `@types/js-yaml`. |

---

## Task Ordering

We ship in two halves: **bottom-up backend** (yamledit → skillrepo → handler → wire) so each piece tests independently, then **frontend** (api-error → run-now nav → buttons → JobNew → JobImport → polish). The GitHub App manifest change is its own small task at the end so it's easy to revert if we need to.

---

## Task 1: Set up `internal/yamledit` package skeleton

**Files:**
- Create: `internal/yamledit/append_schedule.go`
- Create: `internal/yamledit/append_schedule_test.go`
- Create: `internal/yamledit/testdata/.gitkeep`
- Modify: `go.mod` (yaml.v3 promoted by import)

- [ ] **Step 1: Create the package with a stub function and exported errors**

```go
// internal/yamledit/append_schedule.go
// Package yamledit edits cronfoundry.yaml manifests with comment- and
// formatting-preserving precision via yaml.v3's Node API. Round-trips that
// go through gopkg.in/yaml.v3's *yaml.Node preserve comments, anchors,
// ordering, and source indentation; only the inserted lines change in a
// textual diff. config.ParseManifest still owns whole-manifest validation.
package yamledit

import (
	"errors"

	"github.com/gambtho/cronfoundry/internal/config"
)

// ErrSkillNotFound is returned when the requested skill_path is not present
// in the manifest's skills: list. Callers map this to HTTP 409.
var ErrSkillNotFound = errors.New("yamledit: skill_path not found in manifest")

// ErrDuplicateScheduleName is returned when a schedule with sched.Name
// already exists under the target SkillEntry. Callers map this to HTTP 409.
var ErrDuplicateScheduleName = errors.New("yamledit: schedule with this name already exists under skill")

// AppendScheduleToSkill appends sched to the schedules: sequence under the
// SkillEntry whose path matches skillPath in the manifest YAML.
//
// Preserves comments, ordering, indentation, and quoting style of the
// surrounding document — only the inserted lines change in a textual diff.
//
// The marshaled schedule omits zero-valued optional fields so the diff
// stays minimal: empty maps, nil pointers, and zero ints are not emitted.
//
// If the SkillEntry has no schedules: key, one is created.
func AppendScheduleToSkill(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error) {
	return nil, errors.New("yamledit: not implemented")
}
```

- [ ] **Step 2: Create the test file with one passing skeleton test**

```go
// internal/yamledit/append_schedule_test.go
package yamledit

import (
	"errors"
	"testing"
)

func TestAppendScheduleToSkill_NotImplemented(t *testing.T) {
	_, err := AppendScheduleToSkill([]byte("version: 1\nskills: []\n"), "skills/x", nil)
	if err == nil {
		t.Fatalf("expected error from stub, got nil")
	}
	// Stubs must surface a typed sentinel once implemented; for now any error is fine.
	if errors.Is(err, ErrSkillNotFound) || errors.Is(err, ErrDuplicateScheduleName) {
		t.Fatalf("unexpected sentinel from stub: %v", err)
	}
}
```

- [ ] **Step 3: Create testdata directory marker**

```
# internal/yamledit/testdata/.gitkeep
# Test fixtures for AppendScheduleToSkill golden tests.
```

- [ ] **Step 4: Verify package compiles and the placeholder test passes**

Run: `go test ./internal/yamledit/... -v`
Expected: PASS, one test (`TestAppendScheduleToSkill_NotImplemented`).

- [ ] **Step 5: Commit**

```bash
git add internal/yamledit/ go.mod go.sum
git commit -m "feat(yamledit): scaffold package + sentinel errors"
```

---

## Task 2: First yamledit fixture — append to a skill that already has schedules

**Files:**
- Create: `internal/yamledit/testdata/append_to_existing/input.yaml`
- Create: `internal/yamledit/testdata/append_to_existing/expected.yaml`
- Modify: `internal/yamledit/append_schedule_test.go`
- Modify: `internal/yamledit/append_schedule.go`

- [ ] **Step 1: Write the input fixture**

```yaml
# internal/yamledit/testdata/append_to_existing/input.yaml
# Starter smoke skill — adjust cron, destinations, and writeback before going live.
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: daily-smoke
        cron: "0 9 * * *"
        timezone: UTC
        provider: copilot-enterprise
        copilot_prefix: copilot
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: gambtho/skills
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

- [ ] **Step 2: Write the expected output fixture**

```yaml
# internal/yamledit/testdata/append_to_existing/expected.yaml
# Starter smoke skill — adjust cron, destinations, and writeback before going live.
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: daily-smoke
        cron: "0 9 * * *"
        timezone: UTC
        provider: copilot-enterprise
        copilot_prefix: copilot
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: gambtho/skills
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
      - name: hourly-pulse
        cron: "0 * * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: gambtho/skills
              title: "pulse"
```

- [ ] **Step 3: Replace the placeholder test with a real golden-file test**

Replace the entire `internal/yamledit/append_schedule_test.go`:

```go
package yamledit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gambtho/cronfoundry/internal/config"
)

// fixture loads input + expected from testdata/<name>/{input,expected}.yaml.
func fixture(t *testing.T, name string) (input, expected []byte) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join("testdata", name, "input.yaml"))
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	exp, err := os.ReadFile(filepath.Join("testdata", name, "expected.yaml"))
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	return in, exp
}

func TestAppendScheduleToSkill_AppendToExisting(t *testing.T) {
	in, expected := fixture(t, "append_to_existing")

	sched := &config.Schedule{
		Name:     "hourly-pulse",
		Cron:     "0 * * * *",
		Timezone: "UTC",
		Provider: "copilot-enterprise",
		Model:    "gpt-5-mini",
		Destinations: []config.Destination{
			{
				GitHubIssue: &config.GitHubIssueDest{
					Repo:  "gambtho/skills",
					Title: "pulse",
				},
			},
		},
	}

	got, err := AppendScheduleToSkill(in, "skills/smoke", sched)
	if err != nil {
		t.Fatalf("AppendScheduleToSkill: %v", err)
	}
	if string(got) != string(expected) {
		t.Fatalf("output mismatch.\n--- got ---\n%s\n--- expected ---\n%s", got, expected)
	}

	// Belt-and-suspenders: the produced YAML must re-parse via config.ParseManifest.
	if _, err := config.ParseManifest(got); err != nil {
		t.Fatalf("output failed to ParseManifest: %v", err)
	}
}

func TestAppendScheduleToSkill_StubSentinelsNotReturnedWhenNotImplemented(t *testing.T) {
	// Once implemented, this is a no-op safety net — sentinels should only fire
	// from their real code paths.
	_, err := AppendScheduleToSkill([]byte("version: 1\nskills: []\n"), "skills/missing", &config.Schedule{Name: "x"})
	if err == nil {
		// Permitted once full impl lands; no-op assertion.
		return
	}
	if !errors.Is(err, ErrSkillNotFound) {
		t.Logf("note: error from missing skill: %v", err)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/yamledit/... -run TestAppendScheduleToSkill_AppendToExisting -v`
Expected: FAIL with `yamledit: not implemented`.

- [ ] **Step 5: Implement `AppendScheduleToSkill` for this fixture**

Replace the body of `AppendScheduleToSkill` in `internal/yamledit/append_schedule.go`:

```go
package yamledit

import (
	"bytes"
	"errors"
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"
	"gopkg.in/yaml.v3"

	"github.com/gambtho/cronfoundry/internal/config"
)

var ErrSkillNotFound = errors.New("yamledit: skill_path not found in manifest")
var ErrDuplicateScheduleName = errors.New("yamledit: schedule with this name already exists under skill")

func AppendScheduleToSkill(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error) {
	if sched == nil {
		return nil, fmt.Errorf("yamledit: nil schedule")
	}
	if skillPath == "" {
		return nil, fmt.Errorf("yamledit: empty skill_path")
	}

	// Parse the document while preserving comments / ordering / quoting.
	var doc yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil, fmt.Errorf("yamledit: parse: %w", err)
	}

	skillsSeq, err := findSkillsSequence(&doc)
	if err != nil {
		return nil, err
	}

	target, schedulesSeq, err := findSkillEntry(skillsSeq, skillPath)
	if err != nil {
		return nil, err
	}

	if hasScheduleNamed(schedulesSeq, sched.Name) {
		return nil, ErrDuplicateScheduleName
	}

	schedNode, err := scheduleToNode(sched)
	if err != nil {
		return nil, fmt.Errorf("yamledit: marshal schedule: %w", err)
	}

	if schedulesSeq == nil {
		// No schedules: key on this entry — create one.
		// target is the *yaml.Node for the SkillEntry (mapping).
		target.Content = append(target.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "schedules"},
			&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{schedNode}},
		)
	} else {
		schedulesSeq.Content = append(schedulesSeq.Content, schedNode)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("yamledit: marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("yamledit: close encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// findSkillsSequence returns the *yaml.Node for the top-level skills: list,
// or an error if the document shape is wrong.
func findSkillsSequence(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("yamledit: empty manifest")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yamledit: manifest root is not a mapping")
	}
	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Value == "skills" {
			if val.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("yamledit: skills: is not a sequence")
			}
			return val, nil
		}
	}
	return nil, fmt.Errorf("yamledit: manifest has no skills: key")
}

// findSkillEntry returns (skillEntryMappingNode, schedulesSeqNode_or_nil).
// schedulesSeq is nil when the SkillEntry has no schedules: key — caller
// is expected to add one.
func findSkillEntry(skillsSeq *yaml.Node, skillPath string) (*yaml.Node, *yaml.Node, error) {
	for _, entry := range skillsSeq.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		var (
			matchedPath  = false
			schedulesSeq *yaml.Node
		)
		for i := 0; i < len(entry.Content)-1; i += 2 {
			k := entry.Content[i]
			v := entry.Content[i+1]
			switch k.Value {
			case "path":
				if v.Value == skillPath {
					matchedPath = true
				}
			case "schedules":
				if v.Kind == yaml.SequenceNode {
					schedulesSeq = v
				}
			}
		}
		if matchedPath {
			return entry, schedulesSeq, nil
		}
	}
	return nil, nil, ErrSkillNotFound
}

func hasScheduleNamed(seq *yaml.Node, name string) bool {
	if seq == nil {
		return false
	}
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i < len(item.Content)-1; i += 2 {
			k := item.Content[i]
			v := item.Content[i+1]
			if k.Value == "name" && v.Value == name {
				return true
			}
		}
	}
	return false
}

// scheduleToNode marshals a *config.Schedule to a yaml.Node by routing
// through sigs.k8s.io/yaml (which honors the existing json tags), then
// re-parsing into a yaml.Node so the caller can splice into a tree.
//
// We can't go straight through yaml.v3 because config.Schedule uses
// json tags exclusively; yaml.v3 doesn't read json tags.
func scheduleToNode(s *config.Schedule) (*yaml.Node, error) {
	yamlBytes, err := sigsyaml.Marshal(s)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &n); err != nil {
		return nil, err
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) == 0 {
		return nil, fmt.Errorf("yamledit: marshaled schedule has no content")
	}
	return n.Content[0], nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./internal/yamledit/... -run TestAppendScheduleToSkill_AppendToExisting -v`
Expected: PASS.

If the byte comparison fails on whitespace (yaml.v3 sometimes drops a trailing blank line), update `expected.yaml` to match — the contract is "preserves comments and structure," not byte-perfect identity. Make sure `config.ParseManifest(got)` still passes.

- [ ] **Step 7: Commit**

```bash
git add internal/yamledit/
git commit -m "feat(yamledit): append schedule to existing skills entry"
```

---

## Task 3: yamledit — append when target skill has no `schedules:` key yet

**Files:**
- Create: `internal/yamledit/testdata/append_first_schedule/input.yaml`
- Create: `internal/yamledit/testdata/append_first_schedule/expected.yaml`
- Modify: `internal/yamledit/append_schedule_test.go`

- [ ] **Step 1: Write input fixture (skill entry with `path:` only)**

```yaml
# internal/yamledit/testdata/append_first_schedule/input.yaml
version: 1
skills:
  - path: skills/empty
```

- [ ] **Step 2: Write expected fixture**

```yaml
# internal/yamledit/testdata/append_first_schedule/expected.yaml
version: 1
skills:
  - path: skills/empty
    schedules:
      - name: hello
        cron: "*/5 * * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: gambtho/skills
              title: hello
```

- [ ] **Step 3: Add the test**

Append to `internal/yamledit/append_schedule_test.go`:

```go
func TestAppendScheduleToSkill_AppendFirstSchedule(t *testing.T) {
	in, expected := fixture(t, "append_first_schedule")
	sched := &config.Schedule{
		Name:     "hello",
		Cron:     "*/5 * * * *",
		Timezone: "UTC",
		Provider: "copilot-enterprise",
		Model:    "gpt-5-mini",
		Destinations: []config.Destination{
			{GitHubIssue: &config.GitHubIssueDest{Repo: "gambtho/skills", Title: "hello"}},
		},
	}
	got, err := AppendScheduleToSkill(in, "skills/empty", sched)
	if err != nil {
		t.Fatalf("AppendScheduleToSkill: %v", err)
	}
	if string(got) != string(expected) {
		t.Fatalf("mismatch\n---got---\n%s\n---want---\n%s", got, expected)
	}
	if _, err := config.ParseManifest(got); err != nil {
		t.Fatalf("ParseManifest on output: %v", err)
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/yamledit/... -run TestAppendScheduleToSkill_AppendFirstSchedule -v`
Expected: PASS (the implementation already covered this branch).

- [ ] **Step 5: Commit**

```bash
git add internal/yamledit/
git commit -m "test(yamledit): cover skill entry without schedules: key"
```

---

## Task 4: yamledit — error cases (missing skill, duplicate name)

**Files:**
- Modify: `internal/yamledit/append_schedule_test.go`

- [ ] **Step 1: Add tests for both sentinels**

Append to `internal/yamledit/append_schedule_test.go`:

```go
func TestAppendScheduleToSkill_SkillNotFound(t *testing.T) {
	in := []byte(`version: 1
skills:
  - path: skills/exists
`)
	_, err := AppendScheduleToSkill(in, "skills/nope", &config.Schedule{Name: "x"})
	if !errors.Is(err, ErrSkillNotFound) {
		t.Fatalf("want ErrSkillNotFound, got %v", err)
	}
}

func TestAppendScheduleToSkill_DuplicateName(t *testing.T) {
	in := []byte(`version: 1
skills:
  - path: skills/dup
    schedules:
      - name: same
        cron: "0 0 * * *"
        timezone: UTC
        provider: copilot-enterprise
        model: gpt-5-mini
        destinations:
          - github-issue:
              repo: x/y
              title: t
`)
	_, err := AppendScheduleToSkill(in, "skills/dup", &config.Schedule{Name: "same"})
	if !errors.Is(err, ErrDuplicateScheduleName) {
		t.Fatalf("want ErrDuplicateScheduleName, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/yamledit/... -run "SkillNotFound|DuplicateName" -v`
Expected: PASS.

- [ ] **Step 3: Remove the old `_StubSentinelsNotReturnedWhenNotImplemented` test**

Delete `TestAppendScheduleToSkill_StubSentinelsNotReturnedWhenNotImplemented` from `internal/yamledit/append_schedule_test.go` — it's no longer meaningful now that the function is implemented.

- [ ] **Step 4: Run the full yamledit suite**

Run: `go test ./internal/yamledit/... -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/yamledit/
git commit -m "test(yamledit): cover missing skill + duplicate schedule errors"
```

---

## Task 5: Set up `internal/skillrepo` package skeleton with the four operations

**Files:**
- Create: `internal/skillrepo/client.go`
- Create: `internal/skillrepo/client_test.go`

- [ ] **Step 1: Define Client + types + sentinel errors (stub bodies)**

```go
// internal/skillrepo/client.go
// Package skillrepo composes go-github calls into the specific "open a PR
// with a single-file change" pipeline used by POST /api/skill-repo/jobs.
// Lower-level transport and JWT/install-token primitives live in
// internal/github; skillrepo is the call-flow layer above them.
package skillrepo

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v74/github"

	"github.com/gambtho/cronfoundry/internal/github"
)

// ErrPermissionRequired is returned when the GitHub App installation lacks
// a permission needed to complete the operation (e.g. pull_requests:write).
// Callers map this to HTTP 412.
var ErrPermissionRequired = errors.New("skillrepo: github app missing required permission")

// ErrConflict is returned for 409/422 responses on branch creation
// (already exists), file PUT (sha mismatch), or PR open (PR already exists
// for branch). Callers map this to HTTP 409.
var ErrConflict = errors.New("skillrepo: github reported a conflict")

// ErrFileNotFound is returned when GetFile receives a 404 (missing
// cronfoundry.yaml on the default branch). Callers map this to HTTP 400
// with a clear message.
var ErrFileNotFound = errors.New("skillrepo: file not found on default branch")

// FileContents bundles what GetFile returns: file blob + the head commit
// sha of the branch the file is on. The caller passes the file sha back
// to PutFile and the head sha to CreateBranch.
type FileContents struct {
	Content   []byte
	FileSHA   string // for "If-Match"-style PUT
	HeadSHA   string // commit sha the file was read at
}

// PRRequest is the structured input to CreatePR.
type PRRequest struct {
	Owner    string
	Repo     string
	Branch   string
	Base     string
	Title    string
	Body     string
}

// PRResult is what CreatePR returns on success.
type PRResult struct {
	HTMLURL string
	Number  int
}

// Client is the stateless wrapper. Each method mints a fresh install token
// via Installations and constructs a per-call go-github client. We don't
// cache *gh.Client because token refresh would invalidate it.
type Client struct {
	Installations *github.InstallationCache
	BaseURL       string // default "https://api.github.com"
	HTTPClient    *http.Client // optional override
}

// New constructs a Client with sensible defaults applied.
func New(installs *github.InstallationCache, baseURL string) *Client {
	return &Client{Installations: installs, BaseURL: baseURL}
}

// gitHubClient mints an install token and returns a configured go-github
// client. Internal helper.
func (c *Client) gitHubClient(ctx context.Context, installID int64) (*gh.Client, error) {
	token, err := c.Installations.Token(ctx, installID)
	if err != nil {
		return nil, fmt.Errorf("skillrepo: install token: %w", err)
	}
	httpClient := c.HTTPClient
	cli := gh.NewClient(httpClient).WithAuthToken(token)
	if c.BaseURL != "" && c.BaseURL != "https://api.github.com" {
		// gh.Client.BaseURL must end in trailing slash and have /api/v3 etc.
		// For our test stubs we just point it at the test server URL.
		base, err := cli.BaseURL.Parse(c.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("skillrepo: parse base url: %w", err)
		}
		cli.BaseURL = base
	}
	return cli, nil
}

// GetFile fetches cronfoundry.yaml at the default branch's HEAD.
func (c *Client) GetFile(ctx context.Context, installID int64, owner, repo, path, ref string) (*FileContents, error) {
	return nil, errors.New("skillrepo: GetFile not implemented")
}

// CreateBranch creates a new ref pointing at fromSHA. Returns ErrConflict
// if the branch already exists.
func (c *Client) CreateBranch(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error {
	return errors.New("skillrepo: CreateBranch not implemented")
}

// PutFile creates or updates a file on the named branch. fileSHA must
// match the file's current sha on the branch (use FileContents.FileSHA
// from GetFile). Returns ErrConflict on stale sha.
func (c *Client) PutFile(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error {
	return errors.New("skillrepo: PutFile not implemented")
}

// CreatePR opens a PR. Returns ErrPermissionRequired if the App lacks
// pull_requests:write. Returns ErrConflict if a PR is already open for
// the same branch.
func (c *Client) CreatePR(ctx context.Context, installID int64, req PRRequest) (*PRResult, error) {
	return nil, errors.New("skillrepo: CreatePR not implemented")
}
```

- [ ] **Step 2: Create the test file with one passing skeleton check**

```go
// internal/skillrepo/client_test.go
package skillrepo

import (
	"context"
	"net/http/httptest"
	"testing"
)

// fakeInstalls satisfies the small subset of InstallationCache.Token that
// skillrepo uses, so tests don't need a real JWT-signing path.
//
// We can't import the concrete InstallationCache type for tests because
// it contains unexported fields; instead skillrepo's Client only depends
// on the public Token method. (See note in Task 9 for any future seam
// refactor — for v1 we use a real InstallationCache wired with a tiny
// stub PEM, generated at test setup.)
//
// For now, this test stub just confirms the package compiles.
func TestClient_PackageCompiles(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	c := New(nil, srv.URL)
	_ = c
	_ = context.Background()
}
```

- [ ] **Step 3: Verify package compiles**

Run: `go test ./internal/skillrepo/... -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/skillrepo/
git commit -m "feat(skillrepo): scaffold Client + sentinel errors"
```

---

## Task 6: skillrepo — implement GetFile + test against httptest stub

**Files:**
- Modify: `internal/skillrepo/client.go`
- Modify: `internal/skillrepo/client_test.go`

- [ ] **Step 1: Add a test InstallationCache helper**

In a `helpers_test.go`-style block at the top of `client_test.go`, set up a way to mint a real `InstallationCache` whose `Token` call returns a stub. The cleanest path is to expose a small constructor in `internal/github` for tests, but to avoid touching that package we'll simply use a function variable for the token. Refactor `Client` to use a pluggable token source:

```go
// in client.go, replace the gitHubClient method:

// TokenFunc returns an installation token for installID. Wired in
// production from internal/github.InstallationCache.Token; tests inject
// a stub.
type TokenFunc func(ctx context.Context, installID int64) (string, error)

type Client struct {
	Token   TokenFunc
	BaseURL string // default "https://api.github.com"
	HTTPClient *http.Client
}

func New(token TokenFunc, baseURL string) *Client {
	return &Client{Token: token, BaseURL: baseURL}
}

func (c *Client) gitHubClient(ctx context.Context, installID int64) (*gh.Client, error) {
	tok, err := c.Token(ctx, installID)
	if err != nil {
		return nil, fmt.Errorf("skillrepo: token: %w", err)
	}
	httpClient := c.HTTPClient
	cli := gh.NewClient(httpClient).WithAuthToken(tok)
	if c.BaseURL != "" {
		u := c.BaseURL
		if u[len(u)-1] != '/' {
			u += "/"
		}
		base, err := cli.BaseURL.Parse(u)
		if err != nil {
			return nil, fmt.Errorf("skillrepo: parse base url: %w", err)
		}
		cli.BaseURL = base
	}
	return cli, nil
}
```

Drop the `Installations` field and the `internal/github` import.

- [ ] **Step 2: Implement GetFile**

```go
// in client.go
func (c *Client) GetFile(ctx context.Context, installID int64, owner, repo, path, ref string) (*FileContents, error) {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return nil, err
	}
	opts := &gh.RepositoryContentGetOptions{Ref: ref}
	fileC, _, resp, err := cli.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("skillrepo: GetContents: %w", err)
	}
	if fileC == nil {
		// GetContents returns dirContents when path is a directory; we want a file.
		return nil, fmt.Errorf("skillrepo: %s is not a file", path)
	}
	content, err := fileC.GetContent()
	if err != nil {
		return nil, fmt.Errorf("skillrepo: decode content: %w", err)
	}
	// Look up the head commit sha of ref.
	branch, _, err := cli.Repositories.GetBranch(ctx, owner, repo, ref, 0)
	if err != nil {
		return nil, fmt.Errorf("skillrepo: GetBranch: %w", err)
	}
	headSHA := ""
	if branch != nil && branch.Commit != nil && branch.Commit.SHA != nil {
		headSHA = *branch.Commit.SHA
	}
	fileSHA := ""
	if fileC.SHA != nil {
		fileSHA = *fileC.SHA
	}
	return &FileContents{Content: []byte(content), FileSHA: fileSHA, HeadSHA: headSHA}, nil
}
```

- [ ] **Step 3: Write the test**

Replace `client_test.go`:

```go
package skillrepo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubToken returns the same fake token for any installID.
func stubToken(_ context.Context, _ int64) (string, error) { return "fake-token", nil }

func TestClient_GetFile_Happy(t *testing.T) {
	const (
		owner   = "ownr"
		repo    = "rp"
		path    = "cronfoundry.yaml"
		ref     = "main"
		fileSha = "abc123"
		headSha = "deadbeef"
		body    = "version: 1\nskills: []\n"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"+owner+"/"+repo+"/contents/"+path):
			payload := map[string]any{
				"type":    "file",
				"sha":     fileSha,
				"path":    path,
				"content": base64.StdEncoding.EncodeToString([]byte(body)),
				"encoding": "base64",
			}
			_ = json.NewEncoder(w).Encode(payload)
		case strings.HasPrefix(r.URL.Path, "/repos/"+owner+"/"+repo+"/branches/"+ref):
			payload := map[string]any{
				"name": ref,
				"commit": map[string]string{"sha": headSha},
			}
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, 500)
		}
	}))
	defer srv.Close()

	c := New(stubToken, srv.URL)
	got, err := c.GetFile(context.Background(), 42, owner, repo, path, ref)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(got.Content) != body {
		t.Errorf("content: got %q, want %q", got.Content, body)
	}
	if got.FileSHA != fileSha {
		t.Errorf("file sha: got %q, want %q", got.FileSHA, fileSha)
	}
	if got.HeadSHA != headSha {
		t.Errorf("head sha: got %q, want %q", got.HeadSHA, headSha)
	}
}

func TestClient_GetFile_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.GetFile(context.Background(), 1, "o", "r", "x.yaml", "main")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("want ErrFileNotFound, got %v", err)
	}
}
```

Add `"errors"` to the test file's imports.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/skillrepo/... -v`
Expected: PASS, three tests (PackageCompiles, GetFile_Happy, GetFile_NotFound).

- [ ] **Step 5: Commit**

```bash
git add internal/skillrepo/
git commit -m "feat(skillrepo): implement GetFile + tests"
```

---

## Task 7: skillrepo — implement CreateBranch + test

**Files:**
- Modify: `internal/skillrepo/client.go`
- Modify: `internal/skillrepo/client_test.go`

- [ ] **Step 1: Implement CreateBranch**

```go
// in client.go
func (c *Client) CreateBranch(ctx context.Context, installID int64, owner, repo, branch, fromSHA string) error {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return err
	}
	ref := &gh.Reference{
		Ref:    gh.Ptr("refs/heads/" + branch),
		Object: &gh.GitObject{SHA: gh.Ptr(fromSHA)},
	}
	_, resp, err := cli.Git.CreateRef(ctx, owner, repo, ref)
	if err != nil {
		if resp != nil && resp.StatusCode == 422 {
			return ErrConflict
		}
		return fmt.Errorf("skillrepo: CreateRef: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Write tests**

Append to `client_test.go`:

```go
func TestClient_CreateBranch_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/git/refs") || r.Method != "POST" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ref"] != "refs/heads/feat-x" {
			t.Errorf("ref: got %v", body["ref"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": body["ref"]})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	if err := c.CreateBranch(context.Background(), 1, "o", "r", "feat-x", "deadbeef"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
}

func TestClient_CreateBranch_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Reference already exists"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.CreateBranch(context.Background(), 1, "o", "r", "feat-x", "deadbeef")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/skillrepo/... -run CreateBranch -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/skillrepo/
git commit -m "feat(skillrepo): implement CreateBranch + tests"
```

---

## Task 8: skillrepo — implement PutFile + test

**Files:**
- Modify: `internal/skillrepo/client.go`
- Modify: `internal/skillrepo/client_test.go`

- [ ] **Step 1: Implement PutFile**

```go
// in client.go
func (c *Client) PutFile(ctx context.Context, installID int64, owner, repo, branch, path, fileSHA, message string, content []byte) error {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return err
	}
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		Content: content,
		SHA:     gh.Ptr(fileSHA),
		Branch:  gh.Ptr(branch),
	}
	_, resp, err := cli.Repositories.UpdateFile(ctx, owner, repo, path, opts)
	if err != nil {
		if resp != nil && (resp.StatusCode == 409 || resp.StatusCode == 422) {
			return ErrConflict
		}
		return fmt.Errorf("skillrepo: UpdateFile: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Write tests**

Append to `client_test.go`:

```go
func TestClient_PutFile_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/contents/") || r.Method != "PUT" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "newsha"},
			"commit":  map[string]any{"sha": "commitsha"},
		})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.PutFile(context.Background(), 1, "o", "r", "feat-x", "cronfoundry.yaml", "oldsha", "msg", []byte("body"))
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
}

func TestClient_PutFile_StaleSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"is at <sha>"}`, http.StatusConflict)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	err := c.PutFile(context.Background(), 1, "o", "r", "feat-x", "cronfoundry.yaml", "stalesha", "msg", []byte("body"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/skillrepo/... -run PutFile -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/skillrepo/
git commit -m "feat(skillrepo): implement PutFile + tests"
```

---

## Task 9: skillrepo — implement CreatePR + test (incl. permission missing)

**Files:**
- Modify: `internal/skillrepo/client.go`
- Modify: `internal/skillrepo/client_test.go`

- [ ] **Step 1: Implement CreatePR**

```go
// in client.go
func (c *Client) CreatePR(ctx context.Context, installID int64, req PRRequest) (*PRResult, error) {
	cli, err := c.gitHubClient(ctx, installID)
	if err != nil {
		return nil, err
	}
	pr, resp, err := cli.PullRequests.Create(ctx, req.Owner, req.Repo, &gh.NewPullRequest{
		Title: gh.Ptr(req.Title),
		Body:  gh.Ptr(req.Body),
		Head:  gh.Ptr(req.Branch),
		Base:  gh.Ptr(req.Base),
	})
	if err != nil {
		if resp != nil {
			if resp.StatusCode == http.StatusForbidden {
				// 403 from /pulls when the App is missing pull_requests:write
				return nil, ErrPermissionRequired
			}
			if resp.StatusCode == http.StatusUnprocessableEntity {
				// 422 commonly means "PR already exists for this branch"
				return nil, ErrConflict
			}
		}
		return nil, fmt.Errorf("skillrepo: CreatePR: %w", err)
	}
	return &PRResult{
		HTMLURL: ptrStr(pr.HTMLURL),
		Number:  ptrInt(pr.Number),
	}, nil
}

func ptrStr(p *string) string { if p == nil { return "" }; return *p }
func ptrInt(p *int) int       { if p == nil { return 0 }; return *p }
```

- [ ] **Step 2: Write tests**

Append to `client_test.go`:

```go
func TestClient_CreatePR_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls") || r.Method != "POST" {
			http.Error(w, "unexpected: "+r.URL.Path, 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/o/r/pull/42",
			"number":   42,
		})
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	pr, err := c.CreatePR(context.Background(), 1, PRRequest{
		Owner: "o", Repo: "r", Branch: "feat-x", Base: "main", Title: "t", Body: "b",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.HTMLURL != "https://github.com/o/r/pull/42" || pr.Number != 42 {
		t.Errorf("got %+v", pr)
	}
}

func TestClient_CreatePR_PermissionRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.CreatePR(context.Background(), 1, PRRequest{Owner: "o", Repo: "r", Branch: "x", Base: "main", Title: "t", Body: "b"})
	if !errors.Is(err, ErrPermissionRequired) {
		t.Fatalf("want ErrPermissionRequired, got %v", err)
	}
}

func TestClient_CreatePR_AlreadyExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"A pull request already exists"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	c := New(stubToken, srv.URL)
	_, err := c.CreatePR(context.Background(), 1, PRRequest{Owner: "o", Repo: "r", Branch: "x", Base: "main", Title: "t", Body: "b"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
```

- [ ] **Step 3: Run the full skillrepo suite**

Run: `go test ./internal/skillrepo/... -v`
Expected: PASS, all tests (PackageCompiles, GetFile x2, CreateBranch x2, PutFile x2, CreatePR x3).

- [ ] **Step 4: Commit**

```bash
git add internal/skillrepo/
git commit -m "feat(skillrepo): implement CreatePR + permission/conflict tests"
```

---

## Task 10: webapi handler — set up file, types, and route registration

**Files:**
- Create: `internal/webapi/skill_repo_jobs.go`
- Modify: `internal/webapi/server.go`

- [ ] **Step 1: Add fields to `Deps` so the handler is wirable**

In `internal/webapi/server.go`, add to the `Deps` struct (right after `Syncer RepoSyncer`):

```go
	// SkillRepoClient handles GitHub round-trips for `POST /api/skill-repo/jobs`.
	// Injected from cmd/cronfoundry/serve.go as a *skillrepo.Client.
	// In tests we substitute a fake satisfying the skillRepoClient interface
	// declared in skill_repo_jobs.go.
	SkillRepoClient SkillRepoClient
	// YamlEditAppendSchedule is the YAML editor used by the proposeJob handler.
	// Wired from internal/yamledit.AppendScheduleToSkill in production; tests
	// inject a function-typed fake.
	YamlEditAppendSchedule YamlAppendScheduleFunc
```

- [ ] **Step 2: Create the handler file with types and a stub**

```go
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

	"github.com/gambtho/cronfoundry/internal/audit"
	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
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

type skillRepoHandler struct{ deps Deps }

func (h *skillRepoHandler) proposeJob(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "skill-repo proposeJob not yet implemented", "internal")
}
```

- [ ] **Step 3: Register the route**

In `internal/webapi/server.go`, near the schedules block:

```go
	// Skill-repo PR pipeline (POST /api/skill-repo/jobs)
	srh := &skillRepoHandler{deps: deps}
	mux.Handle("POST /api/skill-repo/jobs", adminOnly(http.HandlerFunc(srh.proposeJob)))
```

- [ ] **Step 4: Verify the package compiles**

Run: `go build ./...`
Expected: succeeds. (`go test` will fail to build cmd/cronfoundry until Task 19 wires the new Deps fields, so don't run the full suite yet — only build.)

If `go test ./internal/webapi/...` fails because existing tests construct `webapi.Deps{}` literals that don't have the new fields, that's fine — those tests will continue to compile because the new fields are optional (zero-valued is OK for the not-yet-implemented path).

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/skill_repo_jobs.go internal/webapi/server.go
git commit -m "feat(webapi): scaffold POST /api/skill-repo/jobs handler"
```

---

## Task 11: webapi handler — request body validation tests + impl

**Files:**
- Create: `internal/webapi/skill_repo_jobs_test.go`
- Modify: `internal/webapi/skill_repo_jobs.go`

- [ ] **Step 1: Write the validation tests**

```go
// internal/webapi/skill_repo_jobs_test.go
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

// proposeJobReq does an authenticated POST to the handler under test.
// The session/csrf middleware is skipped here — we call the handler directly.
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
)
```

- [ ] **Step 2: Run the tests, expect three failures**

Run: `go test ./internal/webapi/... -run TestProposeJob -v`
Expected: All four fail (501 from the stub, expected 400).

- [ ] **Step 3: Implement validation in `proposeJob`**

Replace `proposeJob` in `internal/webapi/skill_repo_jobs.go`:

```go
func (h *skillRepoHandler) proposeJob(w http.ResponseWriter, r *http.Request) {
	var req proposeJobRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
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

	// Pipeline gets implemented in subsequent tasks.
	writeErr(w, http.StatusNotImplemented, "pipeline not yet implemented", "internal")
}
```

- [ ] **Step 4: Run the tests, all four pass**

Run: `go test ./internal/webapi/... -run TestProposeJob -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/skill_repo_jobs.go internal/webapi/skill_repo_jobs_test.go
git commit -m "feat(webapi): proposeJob input validation"
```

---

## Task 12: webapi handler — happy-path pipeline (no audit yet)

**Files:**
- Modify: `internal/webapi/skill_repo_jobs.go`
- Modify: `internal/webapi/skill_repo_jobs_test.go`

- [ ] **Step 1: Add the happy-path test**

Append to `skill_repo_jobs_test.go`:

```go
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

// fakeQueries satisfies the smallest surface proposeJob touches.
// Replaced in Task 14 once the audit log path is added.
type fakeQueries struct{}

func (fakeQueries) GetFirstOrganization(_ context.Context) (interface{ GetID() any }, error) {
	return nil, nil
}

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
	// resolveConn is a per-test stub; in Task 13 we wire DB-backed lookup.
	yamlFn := YamlAppendScheduleFunc(func(b []byte, p string, s *config.Schedule) ([]byte, error) {
		return append(b, []byte("\n# inserted "+s.Name+"\n")...), nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
	}}
	// Stub the connection-resolution path; for now the handler will read it
	// from a deps field we add in this task.
	h.deps.testConnOverride = &resolvedConn{
		Owner:         owner,
		Name:          repo,
		DefaultBranch: "main",
		InstallID:     12345,
	}
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
```

- [ ] **Step 2: Add the connection-resolver seam in the handler**

In `internal/webapi/skill_repo_jobs.go`, add helper types and a `resolveConn` shim:

```go
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
	row := rows[0] // v1: a single org has one connection (per the dogfood install flow).
	return &resolvedConn{
		Owner: row.Owner, Name: row.Name, DefaultBranch: row.DefaultBranch,
		InstallID: row.GithubAppInstallID, OrgID: org.ID, ConnID: row.ID,
	}, nil
}
```

Add `testConnOverride *resolvedConn` to the `Deps` struct in `internal/webapi/server.go` (with a `// for unit tests only` comment). Production code never sets it.

- [ ] **Step 3: Implement the happy path in `proposeJob`**

Replace the body after validation with:

```go
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
		Owner: conn.Owner, Repo: conn.Name, Branch: branch, Base: conn.DefaultBranch,
		Title: commitMsg, Body: prBody,
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

	writeJSON(w, http.StatusOK, proposeJobResponse{
		PRURL: pr.HTMLURL, PRNumber: pr.Number, Branch: branch,
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
	slug := deps.GitHubAppSlug // populated from cmd/cronfoundry/serve.go
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
```

Add a `GitHubAppSlug string` field to `Deps` in `server.go` so `writePermissionRequired` can construct the URL. Wired in Task 19.

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/webapi/... -run TestProposeJob_HappyPath -v`
Expected: PASS.

(The validation tests from Task 11 still pass because the validation happens before the connection-resolver step.)

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/skill_repo_jobs.go internal/webapi/skill_repo_jobs_test.go internal/webapi/server.go
git commit -m "feat(webapi): proposeJob happy-path pipeline (no audit yet)"
```

---

## Task 13: webapi handler — error mapping tests (412 / 409 / 400 / 502)

**Files:**
- Modify: `internal/webapi/skill_repo_jobs_test.go`

- [ ] **Step 1: Add the four error-mapping tests**

Append to `skill_repo_jobs_test.go`:

```go
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
		return b, nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		GitHubAppSlug:          "cronfoundry-test",
		testConnOverride:       &resolvedConn{Owner: "o", Name: "r", DefaultBranch: "main", InstallID: 1},
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
		return nil, yamleditErrSkillNotFound() // shim defined below
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride:       &resolvedConn{Owner: "o", Name: "r", DefaultBranch: "main", InstallID: 1},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/missing",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}

// yamleditErrSkillNotFound is just a clean alias so the test reads naturally.
func yamleditErrSkillNotFound() error {
	return errors.New("yamledit: skill_path not found in manifest")
}

func TestProposeJob_400_ParseManifestFails(t *testing.T) {
	fakeClient := &fakeSkillRepoClient{
		getFile: func(_ context.Context, _ int64, _, _, _, _ string) (*skillrepo.FileContents, error) {
			return &skillrepo.FileContents{Content: []byte(sampleManifest), FileSHA: "f", HeadSHA: "h"}, nil
		},
	}
	// Return clearly invalid YAML so ParseManifest errors.
	yamlFn := YamlAppendScheduleFunc(func(_ []byte, _ string, _ *config.Schedule) ([]byte, error) {
		return []byte("not: a: valid: manifest:::"), nil
	})
	h := &skillRepoHandler{deps: Deps{
		SkillRepoClient:        fakeClient,
		YamlEditAppendSchedule: yamlFn,
		testConnOverride:       &resolvedConn{Owner: "o", Name: "r", DefaultBranch: "main", InstallID: 1},
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
		testConnOverride:       &resolvedConn{Owner: "o", Name: "r", DefaultBranch: "main", InstallID: 1},
	}}
	w := proposeJobReq(t, http.HandlerFunc(h.proposeJob), proposeJobRequest{
		SkillPath: "skills/smoke",
		Schedule:  &config.Schedule{Name: "x"},
	})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}
```

Update the `errors.Is(err, yamledit.ErrSkillNotFound)` check in the handler to also match the test's plain-error shim — actually, simpler: change the test's `yamlFn` to return `yamledit.ErrSkillNotFound` directly. Replace `yamleditErrSkillNotFound()` with `yamledit.ErrSkillNotFound` (and add the import to the test file).

- [ ] **Step 2: Run the tests, all four pass**

Run: `go test ./internal/webapi/... -run TestProposeJob -v`
Expected: PASS, all eight (4 validation + 1 happy + 4 error mappings).

- [ ] **Step 3: Commit**

```bash
git add internal/webapi/skill_repo_jobs_test.go
git commit -m "test(webapi): proposeJob error mappings (412/409/400/502)"
```

---

## Task 14: webapi handler — audit log on success

**Files:**
- Modify: `internal/webapi/skill_repo_jobs.go`
- Modify: `internal/webapi/skill_repo_jobs_test.go`

- [ ] **Step 1: Add the audit-emitted assertion to the happy-path test**

Modify `TestProposeJob_HappyPath` to inject a fake `audit.Logger` (we follow the pattern other handlers use — see `internal/webapi/schedules.go::clearOverrides` calling `auditLog`). Since `auditLog` just calls `audit.Log(ctx, q, entry)` with `h.deps.Queries`, a unit test typically passes a stub Queries that records calls.

Inspect `internal/webapi/audit.go` (or wherever `auditLog` lives) and follow the same pattern an existing handler test uses. If no audit-log unit test exists, skip the audit assertion in the unit test and instead add a `slog.Info` line in `proposeJob` and assert via captured logs (using `slog.NewLogLogger` redirected to a `bytes.Buffer`).

- [ ] **Step 2: Add audit-log emission to `proposeJob`**

Just before `writeJSON` at the end of `proposeJob`, insert:

```go
	// Audit: one entry per successful PR open.
	idCopy := uuidUUID(conn.ConnID)
	auditLog(r.Context(), h.deps.Queries, mustClaims(r).Login, audit.Entry{
		OrgID:      conn.OrgID,
		Action:     "schedule.proposed",
		TargetKind: "repo_connection",
		TargetID:   &idCopy,
		Detail: map[string]any{
			"skill_path":     req.SkillPath,
			"schedule_name":  req.Schedule.Name,
			"pr_url":         pr.HTMLURL,
			"pr_number":      pr.Number,
			"branch":         branch,
		},
	})
	slog.Info("skill_repo: PR opened",
		"actor", mustClaims(r).Login,
		"skill_path", req.SkillPath,
		"schedule_name", req.Schedule.Name,
		"pr_url", pr.HTMLURL,
		"pr_number", pr.Number)
```

`uuidUUID` is whatever helper the codebase already uses — match the pattern in `schedules.go::clearOverrides`.

In tests, since `mustClaims(r)` requires session context, set up a fake claims context for the happy-path test:

```go
// helper at top of test file
func withTestActor(r *http.Request, login string) *http.Request {
	ctx := context.WithValue(r.Context(), claimsContextKey, &sessionClaims{Login: login, Role: "admin"})
	return r.WithContext(ctx)
}
```

Then in `proposeJobReq` use `httptest.NewRequest`-then-`withTestActor` before `h.ServeHTTP`. **Verify** that `claimsContextKey` and `sessionClaims` are exported or test-accessible — if not, model after how `schedules_test.go` injects claims (read it first; if it does the same trick, copy it).

- [ ] **Step 3: Run the tests; all should still pass**

Run: `go test ./internal/webapi/... -run TestProposeJob -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/webapi/skill_repo_jobs.go internal/webapi/skill_repo_jobs_test.go
git commit -m "feat(webapi): proposeJob writes audit log on success"
```

---

## Task 15: Run-now handler — forward `{run_id}` instead of empty 202

**Files:**
- Modify: `internal/webapi/schedules.go`
- Modify: `internal/webapi/schedules_test.go`

- [ ] **Step 1: Update the existing run-now test (or add one) to assert the JSON body**

Find the run-now happy-path test in `internal/webapi/schedules_test.go` (search `runNow` or `run-now`). Modify or add to assert:
- response Content-Type is `application/json`
- status is 200 (not 202)
- body parses to `{"run_id": "<uuid>"}` matching what the internal endpoint stub returned

```go
// example structure — adapt to whatever the existing test scaffolding provides.
func TestSchedules_RunNow_ForwardsRunID(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "abc-123"})
	}))
	defer internalSrv.Close()

	h := &schedulesHandler{deps: Deps{APIBaseURL: internalSrv.URL}}
	r := httptest.NewRequest("POST", "/api/schedules/00000000-0000-0000-0000-000000000001/run-now", nil)
	r.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	r = withTestActor(r, "alice") // helper from Task 14
	w := httptest.NewRecorder()
	h.runNow(w, r)
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: %s", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["run_id"] != "abc-123" {
		t.Errorf("run_id: %q", body["run_id"])
	}
}

func TestSchedules_RunNow_BadGateway_OnEmptyRunID(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer internalSrv.Close()
	h := &schedulesHandler{deps: Deps{APIBaseURL: internalSrv.URL}}
	r := httptest.NewRequest("POST", "/api/schedules/00000000-0000-0000-0000-000000000001/run-now", nil)
	r.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	r = withTestActor(r, "alice")
	w := httptest.NewRecorder()
	h.runNow(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run, expect failures (handler still does empty 202)**

Run: `go test ./internal/webapi/... -run RunNow -v`
Expected: FAIL.

- [ ] **Step 3: Update the runNow handler**

In `internal/webapi/schedules.go`, replace the tail of the `runNow` function (currently lines 281-289):

```go
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		writeErr(w, resp.StatusCode, "trigger failed", "trigger_error")
		return
	}
	var trigger struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&trigger); err != nil || trigger.RunID == "" {
		slog.Error("runNow: internal endpoint returned no run_id",
			"actor", actor, "schedule_id", idStr, "decode_err", err)
		writeErr(w, http.StatusBadGateway, "trigger returned no run_id", "gateway")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"run_id": trigger.RunID})
```

- [ ] **Step 4: Run again, all pass**

Run: `go test ./internal/webapi/... -run RunNow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/schedules.go internal/webapi/schedules_test.go
git commit -m "feat(webapi): run-now forwards run_id from internal endpoint"
```

---

## Task 16: GitHub App manifest — add `pull_requests:write`

**Files:**
- Modify: `internal/githubapp/manifest.go`
- Modify: `internal/githubapp/manifest_test.go`

- [ ] **Step 1: Update the test to expect the new permission**

Find the existing assertion on `DefaultPerms` in `internal/githubapp/manifest_test.go` and add:

```go
	if got, want := m.DefaultPerms["pull_requests"], "write"; got != want {
		t.Errorf("pull_requests perm: got %q, want %q", got, want)
	}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/githubapp/... -v`
Expected: FAIL.

- [ ] **Step 3: Add the permission**

In `internal/githubapp/manifest.go` (around line 72-76):

```go
		DefaultPerms: map[string]string{
			"contents":      "write",
			"issues":        "write",
			"metadata":      "read",
			"pull_requests": "write",
		},
```

- [ ] **Step 4: Run, all pass**

Run: `go test ./internal/githubapp/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/
git commit -m "feat(githubapp): request pull_requests:write in app manifest"
```

---

## Task 17: Wire skillrepo + yamledit into `cmd/cronfoundry/serve.go`

**Files:**
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Wire the new Deps fields**

Find the `webapi.RegisterRoutes(mux, webapi.Deps{...})` call in `cmd/cronfoundry/serve.go` and add:

```go
		SkillRepoClient: skillrepo.New(installs.Token, ghBaseURL),
		YamlEditAppendSchedule: yamledit.AppendScheduleToSkill,
		GitHubAppSlug: os.Getenv("CRONFOUNDRY_GITHUB_APP_SLUG"), // set in Bicep / quickstart step 17
```

Add imports for `internal/skillrepo` and `internal/yamledit` at the top of the file.

`installs` is the existing `*github.InstallationCache`. Its `Token` method matches `skillrepo.TokenFunc`.

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/cronfoundry/`
Expected: succeeds.

If `CRONFOUNDRY_GITHUB_APP_SLUG` env var isn't already set in the deployment, that's fine — the 412 handler falls back to the default `"cronfoundry"` slug, and dog5's slug happens to be `cronfoundry-tng`. Set the env var in Bicep / quickstart in a follow-up commit if desired (out of scope for this PR).

- [ ] **Step 3: Run the full backend test suite**

Run: `go test ./...`
Expected: PASS, no new failures.

- [ ] **Step 4: Commit**

```bash
git add cmd/cronfoundry/serve.go
git commit -m "feat(serve): wire skillrepo + yamledit into webapi Deps"
```

---

## Task 18: Frontend — `ApiError` class so 412 review_url survives the network layer

**Files:**
- Create: `web/src/lib/api-error.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Add ApiError**

```ts
// web/src/lib/api-error.ts
/**
 * ApiError carries the structured error JSON envelope our backend returns.
 *
 * `code` is the machine-readable enum (e.g. "permission_required",
 * "validation"). `extras` holds any additional fields the backend included
 * (most notably `review_url` for 412).
 *
 * Plain `Error.message` is set to the human-readable error string for
 * backwards-compat with existing `try/catch (e)` consumers.
 */
export class ApiError extends Error {
  status: number
  code: string
  extras: Record<string, unknown>

  constructor(message: string, status: number, code: string, extras: Record<string, unknown> = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.extras = extras
  }
}

/** Type guard — the ApiError class crosses bundler boundaries cleanly. */
export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError
}
```

- [ ] **Step 2: Update `apiFetch` to throw `ApiError`**

In `web/src/lib/api.ts`, modify the failure path:

```ts
import { ApiError } from './api-error'

// ...
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    const message = (body as { error?: string }).error ?? res.statusText
    const code = (body as { code?: string }).code ?? 'unknown'
    const extras: Record<string, unknown> = { ...body }
    delete extras.error
    delete extras.code
    throw new ApiError(message, res.status, code, extras)
  }
```

- [ ] **Step 3: Run the existing frontend tests to confirm no regression**

```bash
cd web && pnpm test
```

Expected: PASS, same number of tests as before.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/api-error.ts web/src/lib/api.ts
git commit -m "feat(web): ApiError class carries error code + extras"
```

---

## Task 19: Frontend — wire `+ Add job` and `+ Import job` button onClicks + `N` shortcut

**Files:**
- Create: `web/src/lib/useShortcut.ts`
- Modify: `web/src/pages/Jobs.tsx`
- Modify: `web/src/pages/Jobs.test.tsx`
- Modify: `web/src/main.tsx`
- Create: `web/src/pages/JobNew.tsx` (placeholder)
- Create: `web/src/pages/JobImport.tsx` (placeholder)

- [ ] **Step 1: Add the shortcut hook**

```ts
// web/src/lib/useShortcut.ts
import { useEffect } from 'react'

/**
 * Registers a global `keydown` listener that fires `handler` when `key`
 * is pressed (case-insensitive) outside of typing contexts (input/textarea/contenteditable).
 *
 * Removes the listener on unmount; safe to call from any page.
 */
export function useShortcut(key: string, handler: () => void) {
  useEffect(() => {
    const lower = key.toLowerCase()
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== lower) return
      const t = e.target as HTMLElement | null
      if (t) {
        const tag = t.tagName?.toLowerCase()
        if (tag === 'input' || tag === 'textarea' || t.isContentEditable) return
      }
      e.preventDefault()
      handler()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [key, handler])
}
```

- [ ] **Step 2: Create placeholder pages so routes don't 404**

```tsx
// web/src/pages/JobNew.tsx
export default function JobNew() {
  return <div>Job new (placeholder)</div>
}
```

```tsx
// web/src/pages/JobImport.tsx
export default function JobImport() {
  return <div>Job import (placeholder)</div>
}
```

- [ ] **Step 3: Add routes in main.tsx**

```tsx
// web/src/main.tsx — inside the <Layout> block
import JobNew from './pages/JobNew'
import JobImport from './pages/JobImport'

// ...
<Route path="/jobs/new" element={<JobNew />} />
<Route path="/jobs/import" element={<JobImport />} />
```

- [ ] **Step 4: Wire button onClicks in Jobs.tsx**

Replace the existing buttons block (around line 170-175):

```tsx
import { useNavigate } from 'react-router-dom'
import { useShortcut } from '../lib/useShortcut'

// ...inside Jobs():
const navigate = useNavigate()
useShortcut('n', () => navigate('/jobs/new'))

// ...inside <PageHeader actions=>:
<Button variant="primary" shortcut="N" onClick={() => navigate('/jobs/new')}>+ Add job</Button>
<Button onClick={() => navigate('/jobs/import')}>+ Import job</Button>
```

- [ ] **Step 5: Update the existing Jobs.test.tsx**

Add a navigation test:

```tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import Jobs from './Jobs'
// ... reuse existing api mock

describe('Jobs page nav buttons', () => {
  it('+ Add job navigates to /jobs/new', async () => {
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <Routes>
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/jobs/new" element={<div data-testid="new-page" />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('+ Add job'))
    expect(await screen.findByTestId('new-page')).toBeInTheDocument()
  })

  it('+ Import job navigates to /jobs/import', async () => {
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <Routes>
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/jobs/import" element={<div data-testid="import-page" />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('+ Import job'))
    expect(await screen.findByTestId('import-page')).toBeInTheDocument()
  })

  it('pressing N navigates to /jobs/new', async () => {
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <Routes>
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/jobs/new" element={<div data-testid="new-page" />} />
        </Routes>
      </MemoryRouter>,
    )
    await userEvent.keyboard('n')
    expect(await screen.findByTestId('new-page')).toBeInTheDocument()
  })
})
```

If `@testing-library/user-event` isn't already a dep, replace with a `fireEvent.keyDown(window, { key: 'n' })` call.

- [ ] **Step 6: Run the frontend tests**

```bash
cd web && pnpm test
```

Expected: PASS, including the new tests.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/useShortcut.ts web/src/pages/Jobs.tsx web/src/pages/Jobs.test.tsx web/src/pages/JobNew.tsx web/src/pages/JobImport.tsx web/src/main.tsx
git commit -m "feat(web): wire + Add job / + Import job buttons + N shortcut"
```

---

## Task 20: Frontend — Run-now navigates to `/runs/<id>` on success

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/Jobs.tsx`
- Modify: `web/src/pages/JobDetail.tsx`
- Modify: `web/src/pages/Jobs.test.tsx`
- Modify: `web/src/pages/JobDetail.test.tsx`

- [ ] **Step 1: Update `runNow` return type**

```ts
// web/src/lib/api.ts — schedules section
runNow: (id: string) =>
  apiFetch<{ run_id: string }>(`/api/schedules/${id}/run-now`, { method: 'POST' }),
```

- [ ] **Step 2: Update mutation in Jobs.tsx**

```tsx
const navigate = useNavigate() // already added in Task 19
const runNow = useMutation({
  mutationFn: api.schedules.runNow,
  onSuccess: (data) => {
    qc.invalidateQueries({ queryKey: ['runs'] })
    navigate(`/runs/${data.run_id}`)
  },
})
```

- [ ] **Step 3: Same change in JobDetail.tsx**

```tsx
import { useNavigate } from 'react-router-dom'
const navigate = useNavigate()
const runNow = useMutation({
  mutationFn: api.schedules.runNow,
  onSuccess: (data) => {
    qc.invalidateQueries({ queryKey: ['runs'] })
    navigate(`/runs/${data.run_id}`)
  },
})
```

- [ ] **Step 4: Update tests in Jobs.test.tsx**

Add:

```tsx
it('Run-now navigates to /runs/<id>', async () => {
  ;(api.schedules.runNow as any).mockResolvedValue({ run_id: 'run-xyz' })
  ;(api.schedules.list as any).mockResolvedValue([{ id: 's1', name: 'job', cron: '0 9 * * *', enabled: true, /* ...minimal Schedule shape */ }])
  ;(api.runs.list as any).mockResolvedValue([])
  render(
    <MemoryRouter initialEntries={['/jobs']}>
      <QueryClientProvider client={new QueryClient()}>
        <Routes>
          <Route path="/jobs" element={<Jobs />} />
          <Route path="/runs/:id" element={<div data-testid="run-page" />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
  fireEvent.click(await screen.findByLabelText('Run now'))
  expect(await screen.findByTestId('run-page')).toBeInTheDocument()
})
```

Adjust the mock Schedule shape to match `web/src/lib/types.ts:Schedule`. The exact button selector depends on how Run-now renders (look at the existing Jobs test for the pattern).

- [ ] **Step 5: Same kind of test in JobDetail.test.tsx**

```tsx
it('Run-now navigates to /runs/<id>', async () => {
  ;(api.schedules.runNow as any).mockResolvedValue({ run_id: 'run-xyz' })
  // ...stub other reads as the existing test does
  render(/* ... */)
  fireEvent.click(await screen.findByText('Run now'))
  expect(await screen.findByTestId('run-page')).toBeInTheDocument()
})
```

- [ ] **Step 6: Run, all pass**

```bash
cd web && pnpm test
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/api.ts web/src/pages/Jobs.tsx web/src/pages/JobDetail.tsx web/src/pages/Jobs.test.tsx web/src/pages/JobDetail.test.tsx
git commit -m "feat(web): Run-now navigates to /runs/<id> on success"
```

---

## Task 21: Frontend — `proposeJob` API client + JobNew skeleton form

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/JobNew.tsx`
- Modify: `web/src/pages/JobNew.test.tsx`

- [ ] **Step 1: Add `proposeJob` to the API client**

```ts
// web/src/lib/api.ts
import type { Schedule } from './types'

export interface ProposeJobRequest {
  skill_path: string
  schedule: Partial<Schedule> & { name: string; cron: string; provider: string; model: string }
}

export interface ProposeJobResponse {
  pr_url: string
  pr_number: number
  branch: string
}

// ... extend the api object:
skillRepo: {
  proposeJob: (req: ProposeJobRequest) =>
    apiFetch<ProposeJobResponse>('/api/skill-repo/jobs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
},
```

- [ ] **Step 2: Replace the JobNew placeholder with a minimal form**

```tsx
// web/src/pages/JobNew.tsx
import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api, type ProposeJobRequest } from '../lib/api'
import { isApiError } from '../lib/api-error'
import { Button, Card, Input, PageHeader, Topbar } from '../components/ui'

export default function JobNew() {
  const navigate = useNavigate()
  const skillsQ = useQuery({ queryKey: ['skills'], queryFn: api.skills.list })

  const [skillPath, setSkillPath] = useState('')
  const [name, setName] = useState('')
  const [cron, setCron] = useState('')
  const [timezone, setTimezone] = useState('UTC')
  const [provider, setProvider] = useState('copilot-enterprise')
  const [model, setModel] = useState('gpt-5-mini')

  const [submitError, setSubmitError] = useState<string | null>(null)
  const [reviewURL, setReviewURL] = useState<string | null>(null)

  const propose = useMutation({
    mutationFn: api.skillRepo.proposeJob,
    onSuccess: (data) => {
      navigate(`/jobs?pr=${data.pr_number}`) // simple success: route back with a query string
      // Task 23 replaces this with a proper success card.
    },
    onError: (err) => {
      if (isApiError(err) && err.code === 'permission_required') {
        setReviewURL((err.extras.review_url as string) ?? null)
      }
      setSubmitError(err instanceof Error ? err.message : String(err))
    },
  })

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitError(null)
    setReviewURL(null)
    if (!skillPath || !name || !cron || !provider || !model) {
      setSubmitError('skill, name, cron, provider, and model are required')
      return
    }
    const req: ProposeJobRequest = {
      skill_path: skillPath,
      schedule: { name, cron, timezone, provider, model, destinations: [] },
    }
    propose.mutate(req)
  }

  return (
    <>
      <Topbar>{/* existing chrome */}</Topbar>
      <div className="w-full max-w-[820px] px-6 pb-16 pt-7">
        <PageHeader title="+ Add job" subtitle="propose a new schedule by opening a PR against the connected skill repo" />
        <form onSubmit={onSubmit} className="grid grid-cols-1 gap-3">
          {submitError && (
            <Card><p className="text-accent-red">{submitError}</p>
              {reviewURL && (
                <a href={reviewURL} target="_blank" rel="noreferrer" className="underline">
                  Review the GitHub App permissions →
                </a>
              )}
            </Card>
          )}
          <label>Skill
            <select value={skillPath} onChange={(e) => setSkillPath(e.target.value)} required>
              <option value="">— select a skill —</option>
              {(skillsQ.data ?? []).map(s => (
                <option key={s.id} value={s.path}>{s.path}</option>
              ))}
            </select>
          </label>
          <label>Name <Input value={name} onChange={(e) => setName(e.target.value)} required /></label>
          <label>Cron <Input value={cron} onChange={(e) => setCron(e.target.value)} required placeholder="0 9 * * *" /></label>
          <label>Timezone <Input value={timezone} onChange={(e) => setTimezone(e.target.value)} /></label>
          <label>Provider <Input value={provider} onChange={(e) => setProvider(e.target.value)} required /></label>
          <label>Model <Input value={model} onChange={(e) => setModel(e.target.value)} required /></label>
          <Button type="submit" variant="primary" disabled={propose.isPending}>
            {propose.isPending ? 'Opening PR…' : 'Open PR'}
          </Button>
        </form>
      </div>
    </>
  )
}
```

(Destinations / writeback / advanced fields land in Task 22.)

- [ ] **Step 3: Write a basic JobNew test**

```tsx
// web/src/pages/JobNew.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobNew from './JobNew'

vi.mock('../lib/api', () => ({
  api: {
    skills: { list: vi.fn().mockResolvedValue([{ id: '1', path: 'skills/smoke', name: 'smoke', repo_id:'r', current_sha:'', updated_at:'', owner:'o', repo_name:'r' }]) },
    skillRepo: { proposeJob: vi.fn() },
  },
}))

const { api } = await import('../lib/api')

beforeEach(() => {
  vi.clearAllMocks()
})

function renderJobNew() {
  return render(
    <MemoryRouter initialEntries={['/jobs/new']}>
      <QueryClientProvider client={new QueryClient()}>
        <Routes>
          <Route path="/jobs/new" element={<JobNew />} />
          <Route path="/jobs" element={<div data-testid="back" />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('JobNew', () => {
  it('blocks submit when required fields missing', async () => {
    renderJobNew()
    fireEvent.submit(await screen.findByRole('button', { name: /open pr/i }))
    expect(api.skillRepo.proposeJob).not.toHaveBeenCalled()
  })

  it('serializes the form to ProposeJobRequest and navigates back on success', async () => {
    ;(api.skillRepo.proposeJob as any).mockResolvedValue({ pr_url: 'u', pr_number: 1, branch: 'b' })
    renderJobNew()
    fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'newjob' } })
    fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '0 9 * * *' } })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    await waitFor(() => expect(api.skillRepo.proposeJob).toHaveBeenCalled())
    const arg = (api.skillRepo.proposeJob as any).mock.calls[0][0]
    expect(arg.skill_path).toBe('skills/smoke')
    expect(arg.schedule.name).toBe('newjob')
    expect(arg.schedule.cron).toBe('0 9 * * *')
  })

  it('renders 412 review_url CTA on permission_required', async () => {
    const { ApiError } = await import('../lib/api-error')
    ;(api.skillRepo.proposeJob as any).mockRejectedValue(new ApiError('missing perm', 412, 'permission_required', { review_url: 'https://github.com/settings/apps/cf/permissions' }))
    renderJobNew()
    fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'x' } })
    fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '0 9 * * *' } })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    expect(await screen.findByText(/Review the GitHub App permissions/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 4: Run frontend tests**

```bash
cd web && pnpm test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/pages/JobNew.tsx web/src/pages/JobNew.test.tsx
git commit -m "feat(web): JobNew minimal form + proposeJob API client"
```

---

## Task 22: Frontend — DestinationsField + EnvField + Writeback + Advanced section

**Files:**
- Create: `web/src/components/forms/DestinationsField.tsx`
- Create: `web/src/components/forms/EnvField.tsx`
- Modify: `web/src/pages/JobNew.tsx`
- Modify: `web/src/pages/JobNew.test.tsx`

- [ ] **Step 1: Implement DestinationsField as a controlled repeater**

```tsx
// web/src/components/forms/DestinationsField.tsx
import { useState } from 'react'
import { Button, Input } from '../ui'

type DestType = 'github-issue' | 'slack' | 'discord' | 'teams' | 'http' | 'email'
type When = 'always' | 'on_success' | 'on_failure'

export interface DestinationsValue {
  type: DestType
  when?: When
  // type-specific fields stored in `fields`; serialization below maps them
  // back to the cronfoundry.yaml shape.
  fields: Record<string, string>
}

export interface DestinationsFieldProps {
  value: DestinationsValue[]
  onChange: (v: DestinationsValue[]) => void
}

const FIELDS_BY_TYPE: Record<DestType, string[]> = {
  'github-issue': ['repo', 'title', 'labels'],
  slack:         ['url'],
  discord:       ['url'],
  teams:         ['url'],
  http:          ['url', 'method', 'headers'],
  email:         ['to', 'subject'],
}

export function DestinationsField({ value, onChange }: DestinationsFieldProps) {
  const update = (i: number, next: Partial<DestinationsValue>) => {
    onChange(value.map((v, idx) => (idx === i ? { ...v, ...next } : v)))
  }
  return (
    <div className="grid gap-2">
      {value.map((d, i) => (
        <div key={i} className="border p-2 rounded">
          <select value={d.type} onChange={(e) => update(i, { type: e.target.value as DestType, fields: {} })}>
            {Object.keys(FIELDS_BY_TYPE).map(t => <option key={t} value={t}>{t}</option>)}
          </select>
          <select value={d.when ?? 'always'} onChange={(e) => update(i, { when: e.target.value as When })}>
            <option value="always">always</option>
            <option value="on_success">on success</option>
            <option value="on_failure">on failure</option>
          </select>
          {FIELDS_BY_TYPE[d.type].map((f) => (
            <label key={f} className="block">
              {f}
              <Input value={d.fields[f] ?? ''} onChange={(e) => update(i, { fields: { ...d.fields, [f]: e.target.value } })} />
            </label>
          ))}
          <Button onClick={() => onChange(value.filter((_, idx) => idx !== i))}>remove</Button>
        </div>
      ))}
      <Button onClick={() => onChange([...value, { type: 'github-issue', fields: {} }])}>+ add destination</Button>
    </div>
  )
}

/** Serializes the DestinationsValue array to the JSON shape expected by config.Destination[]. */
export function serializeDestinations(value: DestinationsValue[]) {
  return value.map((d) => {
    const out: Record<string, unknown> = { when: d.when ?? 'always' }
    switch (d.type) {
      case 'github-issue':
        out['github-issue'] = {
          repo: d.fields.repo,
          title: d.fields.title,
          labels: d.fields.labels ? d.fields.labels.split(',').map(s => s.trim()) : undefined,
        }
        break
      case 'slack':
      case 'discord':
      case 'teams':
        out[d.type] = { url: d.fields.url }
        break
      case 'http':
        out.http = {
          url: d.fields.url,
          method: d.fields.method || 'POST',
          headers: d.fields.headers ? Object.fromEntries(d.fields.headers.split(',').map(kv => kv.split('=').map(s => s.trim())).filter(([k]) => !!k)) : undefined,
        }
        break
      case 'email':
        out.email = { to: d.fields.to, subject: d.fields.subject }
        break
    }
    return out
  })
}
```

- [ ] **Step 2: Implement EnvField (k/v repeater)**

```tsx
// web/src/components/forms/EnvField.tsx
import { Input, Button } from '../ui'

export interface EnvFieldProps {
  value: Array<{ key: string; value: string }>
  onChange: (v: Array<{ key: string; value: string }>) => void
}

export function EnvField({ value, onChange }: EnvFieldProps) {
  return (
    <div className="grid gap-1">
      {value.map((kv, i) => (
        <div key={i} className="flex gap-1">
          <Input value={kv.key} onChange={(e) => onChange(value.map((v, j) => j === i ? { ...v, key: e.target.value } : v))} placeholder="KEY" />
          <Input value={kv.value} onChange={(e) => onChange(value.map((v, j) => j === i ? { ...v, value: e.target.value } : v))} placeholder="value" />
          <Button onClick={() => onChange(value.filter((_, j) => j !== i))}>x</Button>
        </div>
      ))}
      <Button onClick={() => onChange([...value, { key: '', value: '' }])}>+</Button>
    </div>
  )
}

export function serializeEnv(value: Array<{ key: string; value: string }>) {
  const out: Record<string, { value: string }> = {}
  for (const { key, value: v } of value) {
    if (key) out[key] = { value: v }
  }
  return Object.keys(out).length ? out : undefined
}
```

- [ ] **Step 3: Plug the new fields + advanced toggle into JobNew**

In `JobNew.tsx`:

```tsx
import { DestinationsField, serializeDestinations, type DestinationsValue } from '../components/forms/DestinationsField'
import { EnvField, serializeEnv } from '../components/forms/EnvField'

// inside JobNew():
const [destinations, setDestinations] = useState<DestinationsValue[]>([])
const [writebackEnabled, setWritebackEnabled] = useState(false)
const [writebackPath, setWritebackPath] = useState('memory.md')
const [writebackMode, setWritebackMode] = useState<'append'|'replace'>('append')
const [advanced, setAdvanced] = useState(false)
const [overlapPolicy, setOverlapPolicy] = useState('skip_if_running')
const [timeoutSec, setTimeoutSec] = useState<number | ''>('')
const [maxTurns, setMaxTurns] = useState<number | ''>('')
const [copilotPrefix, setCopilotPrefix] = useState('')
const [env, setEnv] = useState<Array<{key:string; value:string}>>([])
const [mcpEnv, setMcpEnv] = useState<Array<{key:string; value:string}>>([])
```

Update `onSubmit` to include the new values:

```tsx
const req: ProposeJobRequest = {
  skill_path: skillPath,
  schedule: {
    name, cron, timezone, provider, model,
    destinations: serializeDestinations(destinations) as any,
    writeback: writebackEnabled ? { enabled: true, path: writebackPath, mode: writebackMode } as any : undefined,
    overlap_policy: advanced ? overlapPolicy : undefined,
    timeout_sec: advanced && timeoutSec !== '' ? Number(timeoutSec) : undefined,
    max_turns: advanced && maxTurns !== '' ? Number(maxTurns) : undefined,
    copilot_prefix: advanced && copilotPrefix ? copilotPrefix : undefined,
    env: advanced ? serializeEnv(env) : undefined,
    mcp_env: advanced ? (Object.keys(serializeEnv(mcpEnv) ?? {}).length ? { default: serializeEnv(mcpEnv) } : undefined) : undefined,
  } as any,
}
```

Add the destination + writeback + advanced sections to the form JSX. The exact markup mirrors the basic-fields pattern.

- [ ] **Step 4: Update tests for serialization**

In `JobNew.test.tsx`, add:

```tsx
it('serializes destinations and writeback into the request', async () => {
  ;(api.skillRepo.proposeJob as any).mockResolvedValue({ pr_url:'u', pr_number:1, branch:'b' })
  renderJobNew()
  // fill basics
  fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
  fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'newjob' } })
  fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '0 9 * * *' } })
  // add a github-issue destination
  fireEvent.click(screen.getByText(/\+ add destination/i))
  fireEvent.change(screen.getByLabelText(/repo/i), { target: { value: 'o/r' } })
  fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 't' } })
  fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
  await waitFor(() => expect(api.skillRepo.proposeJob).toHaveBeenCalled())
  const arg = (api.skillRepo.proposeJob as any).mock.calls[0][0]
  expect(arg.schedule.destinations).toHaveLength(1)
  expect(arg.schedule.destinations[0]['github-issue'].repo).toBe('o/r')
})
```

- [ ] **Step 5: Run tests**

```bash
cd web && pnpm test
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/forms/ web/src/pages/JobNew.tsx web/src/pages/JobNew.test.tsx
git commit -m "feat(web): JobNew destinations / writeback / advanced fields"
```

---

## Task 23: Frontend — JobSuccessCard shared by JobNew and JobImport

**Files:**
- Create: `web/src/components/forms/JobSuccessCard.tsx`
- Modify: `web/src/pages/JobNew.tsx`
- Modify: `web/src/pages/JobNew.test.tsx`

- [ ] **Step 1: Add the shared card**

```tsx
// web/src/components/forms/JobSuccessCard.tsx
import { Card } from '../ui'

interface Props {
  prURL: string
  prNumber: number
  branch: string
}

export function JobSuccessCard({ prURL, prNumber, branch }: Props) {
  return (
    <Card>
      <h3>PR #{prNumber} opened</h3>
      <p>
        Branch <code>{branch}</code>. Merge it on GitHub and the schedule will appear after the next sync (~60s after the merge push).
      </p>
      <a href={prURL} target="_blank" rel="noreferrer" className="underline">
        View PR →
      </a>
    </Card>
  )
}
```

- [ ] **Step 2: Replace the navigate-on-success with rendering the card**

```tsx
// in JobNew.tsx
const [success, setSuccess] = useState<{ pr_url: string; pr_number: number; branch: string } | null>(null)
const propose = useMutation({
  mutationFn: api.skillRepo.proposeJob,
  onSuccess: (data) => setSuccess(data),
  onError: (err) => { /* same as before */ },
})

// in JSX, before the form:
{success && <JobSuccessCard prURL={success.pr_url} prNumber={success.pr_number} branch={success.branch} />}
```

- [ ] **Step 3: Update JobNew tests**

Update the success-path test to assert the card renders instead of navigating:

```tsx
it('shows JobSuccessCard with PR link on success', async () => {
  ;(api.skillRepo.proposeJob as any).mockResolvedValue({ pr_url: 'https://gh/x/y/pull/9', pr_number: 9, branch: 'b' })
  renderJobNew()
  // fill required + submit
  fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
  fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'x' } })
  fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '0 9 * * *' } })
  fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
  expect(await screen.findByText(/PR #9 opened/)).toBeInTheDocument()
})
```

- [ ] **Step 4: Run tests**

```bash
cd web && pnpm test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/forms/JobSuccessCard.tsx web/src/pages/JobNew.tsx web/src/pages/JobNew.test.tsx
git commit -m "feat(web): JobSuccessCard with PR link"
```

---

## Task 24: Frontend — JobImport page (paste YAML, parse with js-yaml, share endpoint)

**Files:**
- Modify: `web/package.json` (add js-yaml)
- Modify: `web/src/pages/JobImport.tsx`
- Modify: `web/src/pages/JobImport.test.tsx` (or create if absent)

- [ ] **Step 1: Add js-yaml**

```bash
cd web && pnpm add js-yaml @types/js-yaml
```

- [ ] **Step 2: Implement JobImport**

```tsx
// web/src/pages/JobImport.tsx
import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import yaml from 'js-yaml'
import { api, type ProposeJobRequest } from '../lib/api'
import { isApiError } from '../lib/api-error'
import { Button, Card, PageHeader, Topbar } from '../components/ui'
import { JobSuccessCard } from '../components/forms/JobSuccessCard'

export default function JobImport() {
  const skillsQ = useQuery({ queryKey: ['skills'], queryFn: api.skills.list })
  const [skillPath, setSkillPath] = useState('')
  const [text, setText] = useState('')
  const [parseError, setParseError] = useState<string | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [reviewURL, setReviewURL] = useState<string | null>(null)
  const [success, setSuccess] = useState<{pr_url:string; pr_number:number; branch:string} | null>(null)

  const propose = useMutation({
    mutationFn: api.skillRepo.proposeJob,
    onSuccess: (data) => setSuccess(data),
    onError: (err) => {
      if (isApiError(err) && err.code === 'permission_required') {
        setReviewURL((err.extras.review_url as string) ?? null)
      }
      setSubmitError(err instanceof Error ? err.message : String(err))
    },
  })

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setParseError(null); setSubmitError(null); setReviewURL(null)
    if (!skillPath) { setSubmitError('skill is required'); return }
    let parsed: any
    try { parsed = yaml.load(text) } catch (err) {
      setParseError(err instanceof Error ? err.message : String(err)); return
    }
    if (!parsed || typeof parsed !== 'object') {
      setParseError('YAML must be an object'); return
    }
    if (!parsed.name) {
      setParseError('YAML must contain a `name:` field'); return
    }
    const req: ProposeJobRequest = { skill_path: skillPath, schedule: parsed as any }
    propose.mutate(req)
  }

  return (
    <>
      <Topbar />
      <div className="w-full max-w-[820px] px-6 pb-16 pt-7">
        <PageHeader title="+ Import job" subtitle="paste a single Schedule YAML object — opens a PR against the connected skill repo" />
        {success && <JobSuccessCard prURL={success.pr_url} prNumber={success.pr_number} branch={success.branch} />}
        <form onSubmit={onSubmit} className="grid gap-3">
          {parseError && <Card><p className="text-accent-red">YAML: {parseError}</p></Card>}
          {submitError && (
            <Card>
              <p className="text-accent-red">{submitError}</p>
              {reviewURL && <a href={reviewURL} target="_blank" rel="noreferrer" className="underline">Review the GitHub App permissions →</a>}
            </Card>
          )}
          <label>Skill
            <select value={skillPath} onChange={(e) => setSkillPath(e.target.value)} required>
              <option value="">— select a skill —</option>
              {(skillsQ.data ?? []).map(s => <option key={s.id} value={s.path}>{s.path}</option>)}
            </select>
          </label>
          <label>Schedule YAML
            <textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={20}
              className="font-mono w-full"
              placeholder={"name: hourly-pulse\ncron: \"0 * * * *\"\ntimezone: UTC\nprovider: copilot-enterprise\nmodel: gpt-5-mini\ndestinations:\n  - github-issue:\n      repo: o/r\n      title: pulse"}
            />
          </label>
          <Button type="submit" variant="primary" disabled={propose.isPending}>
            {propose.isPending ? 'Opening PR…' : 'Open PR'}
          </Button>
        </form>
      </div>
    </>
  )
}
```

- [ ] **Step 3: Test it**

```tsx
// web/src/pages/JobImport.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobImport from './JobImport'

vi.mock('../lib/api', () => ({
  api: {
    skills: { list: vi.fn().mockResolvedValue([{ id:'1', path:'skills/smoke', name:'smoke', repo_id:'r', current_sha:'', updated_at:'', owner:'o', repo_name:'r' }]) },
    skillRepo: { proposeJob: vi.fn() },
  },
}))
const { api } = await import('../lib/api')

beforeEach(() => vi.clearAllMocks())

function renderImport() {
  return render(
    <MemoryRouter initialEntries={['/jobs/import']}>
      <QueryClientProvider client={new QueryClient()}>
        <Routes><Route path="/jobs/import" element={<JobImport />} /></Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('JobImport', () => {
  it('shows YAML parse error inline', async () => {
    renderImport()
    fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/schedule yaml/i), { target: { value: 'name: x\n  : :: badly indented' } })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    expect(await screen.findByText(/^YAML:/)).toBeInTheDocument()
    expect(api.skillRepo.proposeJob).not.toHaveBeenCalled()
  })

  it('parses valid YAML and submits the same shape as JobNew', async () => {
    ;(api.skillRepo.proposeJob as any).mockResolvedValue({ pr_url:'u', pr_number:1, branch:'b' })
    renderImport()
    fireEvent.change(await screen.findByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/schedule yaml/i), {
      target: { value: 'name: hourly\ncron: "0 * * * *"\ntimezone: UTC\nprovider: copilot-enterprise\nmodel: gpt-5-mini\n' },
    })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    await waitFor(() => expect(api.skillRepo.proposeJob).toHaveBeenCalled())
    const arg = (api.skillRepo.proposeJob as any).mock.calls[0][0]
    expect(arg.skill_path).toBe('skills/smoke')
    expect(arg.schedule.name).toBe('hourly')
    expect(arg.schedule.cron).toBe('0 * * * *')
  })
})
```

- [ ] **Step 4: Run tests**

```bash
cd web && pnpm test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/src/pages/JobImport.tsx web/src/pages/JobImport.test.tsx
git commit -m "feat(web): JobImport page using js-yaml"
```

---

## Task 25: Push, open PR, manual smoke check on dog5

**Files:** none

- [ ] **Step 1: Run the full test suite (backend + frontend)**

```bash
go test ./...
cd web && pnpm test && cd ..
```

Expected: PASS, no skips, no flakes.

- [ ] **Step 2: Build the frontend so the embedded `web/dist/` reflects the new code**

```bash
cd web && pnpm build && cd ..
```

This produces fresh `web/dist/assets/...` files; commit them with the rest.

- [ ] **Step 3: Commit the dist bundle**

```bash
git add web/dist/
git commit -m "build(web): bundle add-job / import-job + run-now nav UI"
```

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin spec/new-job-via-pr
gh pr create \
  --title "feat: add-job / import-job via skill-repo PR + run-now nav" \
  --body "$(cat <<'EOF'
## Summary
- Replaces the dead `+ New job` and `Import from yaml` buttons on `Jobs.tsx` with two working flows. `+ Add job` is form-driven; `+ Import job` accepts a single-schedule YAML snippet. Both submit to a single new `POST /api/skill-repo/jobs` endpoint that fetches `cronfoundry.yaml` from the connected skill repo, runs a comment-preserving append via the new `internal/yamledit` package, validates with `config.ParseManifest`, then opens a PR via the new `internal/skillrepo` go-github wrapper.
- Adds `pull_requests:write` to the GitHub App's default permissions. Existing installs see GitHub's "review permission updates" prompt; the 412 error path on the new endpoint surfaces a CTA pointing at the App's permissions-review page.
- Run-now now returns `{run_id}` from the public endpoint and the SPA navigates to `/runs/<id>` on success.

## Test plan
- [ ] Backend unit tests pass (yamledit + skillrepo + webapi handler).
- [ ] Frontend tests pass (Jobs nav, JobNew, JobImport, JobDetail Run-now nav).
- [ ] Manual smoke on dog5: accept the new GitHub App permission prompt, open `/jobs/new`, fill the form for an existing skill, confirm a PR is opened in `gambtho/skills` and that merging it produces a new schedule on the dashboard after the next sync.
- [ ] Manual smoke: click Run-now on the resulting schedule, confirm navigation to the new run page.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Roll dog5 to a tag containing this PR (after merge)**

After PR merges:

```bash
git fetch origin main
git tag -a v0.7.16 origin/main -m "v0.7.16: + Add job / + Import job via skill-repo PR + run-now nav"
git push origin v0.7.16
# wait ~4 min for amd64 image
TAG=0.7.16
az containerapp update -n cf-serve-dog5 -g rg-cronfoundry-dog5 \
  --image "ghcr.io/gambtho/cronfoundry:$TAG" \
  --set-env-vars "AZURE_CAE_JOB_IMAGE=ghcr.io/gambtho/cronfoundry:$TAG" -o none
az containerapp job update -n cf-runner-dog5 -g rg-cronfoundry-dog5 \
  --image "ghcr.io/gambtho/cronfoundry:$TAG" -o none
```

Verify state with `az containerapp show ... --query "{img:.., runner:.., prov:..}"` per the dogfood-round prompt's "All three values must move together" rule.

---

## Self-review

**Spec coverage check:** Walking the spec's section list against the plan tasks:

- "User-visible flows / Add job" → Tasks 19, 21, 22, 23.
- "User-visible flows / Import job" → Task 24.
- "User-visible flows / Run-now navigation" → Tasks 15, 20.
- "Backend architecture / new endpoint" → Tasks 10–14.
- "Backend architecture / pipeline" → Task 12 (happy) + Task 13 (error mappings) + Task 14 (audit).
- "Module layout" → Tasks 1–4 (yamledit), 5–9 (skillrepo), 10 (handler).
- "yamledit.AppendScheduleToSkill contract" → Tasks 1–4.
- "Run-now backend change" → Task 15.
- "GitHub App manifest update" → Task 16.
- "Frontend / routes" → Task 19.
- "Frontend / API client additions" → Tasks 18, 20, 21.
- "Frontend / pages" → Tasks 21–24.
- "Frontend / Run-now wiring" → Task 20.
- "Frontend / new dep" (js-yaml) → Task 24.
- "Validation strategy" → Tasks 11, 12, 13.
- "Concurrency, conflicts, known limitations" → encoded in Tasks 12 (no locking, branch-name uses unix-ts) and 13 (409 mapping for stale sha / branch / PR conflicts).
- "Audit and observability" → Task 14.
- "Tests / Backend / yamledit" → Tasks 2, 3, 4.
- "Tests / Backend / skillrepo" → Tasks 6, 7, 8, 9.
- "Tests / Backend / webapi handler" → Tasks 11, 12, 13, 14.
- "Tests / Frontend" → Tasks 19, 20, 21, 22, 24.
- "Sequencing and PR shape" → Task 25.

**Sharp edges from spec called out in plan:**
- yaml.v3 vs json tags — Task 2 step 5 explains the `sigsyaml.Marshal → yaml.Unmarshal` round-trip.
- `<Button shortcut>` not actually wiring keydown — Task 19 step 1 adds `useShortcut`.
- 412 vs install-config-page subtleties — Task 9 maps 403 → ErrPermissionRequired; Task 12 surfaces it via `writePermissionRequired`. The spec note about the install-config page (vs permissions page) is a follow-up; v1 only wires the permissions URL.
- Comment header preservation — Task 2's fixture deliberately includes the `# Starter smoke skill — adjust cron, destinations, and writeback before going live.` header.

**Type / signature consistency check:** `proposeJobRequest`, `proposeJobResponse`, `ProposeJobRequest` (TS), `ProposeJobResponse` (TS), `ApiError`, `isApiError`, `useShortcut`, `serializeDestinations`, `serializeEnv`, `JobSuccessCard`, `SkillRepoClient` interface, `YamlAppendScheduleFunc`, `resolvedConn`, `buildBranchName`, `writePermissionRequired` — all defined in their first task and reused without rename in later tasks.

**No-placeholder check:** every "Step N: Implement" carries the actual code; tests carry the actual fixture and assertion. The only intentional non-code references are documentation links in the PR body.
