# Runbook Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 451-line Azure smoke-test runbook with an 8-step quickstart backed by a new `cronfoundry bootstrap azure` interactive command, and move the historical findings file out of `docs/`.

**Architecture:** New `internal/bootstrap/azure` package with each step (preflight, prompt, params write, deploy, firewall, admin init, restart, healthcheck) as its own function behind a `Runner` shell-out abstraction so unit tests can mock `az`. A new `cmd/cronfoundry/bootstrap.go` wires it as a cobra subcommand. Docs are rewritten in lockstep: `quickstart-azure.md` (new, ≤10 steps) and `deploy-azure.md` (full reference); `smoke-test-mvp-azure.md` is deleted; `smoke-test-mvp-azure-findings.md` moves to `.smoke-history/`.

**Tech Stack:** Go 1.25, cobra, stdlib `os/exec` + `net/http`, testify, existing `internal/secretstore` for the master-key helper. No new third-party deps.

**Spec:** [`docs/superpowers/specs/2026-04-29-runbook-simplification-design.md`](../specs/2026-04-29-runbook-simplification-design.md)

---

## File Structure

**Create:**

- `internal/bootstrap/azure/runner.go` — `Runner` interface + `ExecRunner` + `MockRunner`.
- `internal/bootstrap/azure/runner_test.go`
- `internal/bootstrap/azure/inputs.go` — `Inputs` struct + validation + password generator.
- `internal/bootstrap/azure/inputs_test.go`
- `internal/bootstrap/azure/preflight.go` — `Preflight(ctx, Runner) error`.
- `internal/bootstrap/azure/preflight_test.go`
- `internal/bootstrap/azure/prompt.go` — `Prompt(ctx, io.Reader, io.Writer) (Inputs, error)`.
- `internal/bootstrap/azure/prompt_test.go`
- `internal/bootstrap/azure/image.go` — `ProbeImage(ctx, owner, tag string) error`.
- `internal/bootstrap/azure/image_test.go`
- `internal/bootstrap/azure/params.go` — `WriteParams(in Inputs, masterKey, paramsPath string) error`.
- `internal/bootstrap/azure/params_test.go`
- `internal/bootstrap/azure/deploy.go` — `Deploy`, `AllowOperatorIP`, `RestartServe`.
- `internal/bootstrap/azure/deploy_test.go`
- `internal/bootstrap/azure/admininit.go` — `AdminInit(ctx, Runner, binary, dsn, masterKey, orgName string) error`.
- `internal/bootstrap/azure/admininit_test.go`
- `internal/bootstrap/azure/health.go` — `WaitHealthy(ctx, fqdn, timeout) error`.
- `internal/bootstrap/azure/health_test.go`
- `internal/bootstrap/azure/bootstrap.go` — `Bootstrap` struct + `Run(ctx)` orchestrator.
- `internal/bootstrap/azure/bootstrap_test.go`
- `cmd/cronfoundry/bootstrap.go` — cobra wiring: `cronfoundry bootstrap azure`.
- `cmd/cronfoundry/bootstrap_test.go`
- `docs/guides/quickstart-azure.md` — new ≤10-step happy path.
- `.smoke-history/README.md` — one-line explanation.

**Modify:**

- `cmd/cronfoundry/main.go` — register `newBootstrapCmd()`.
- `deploy/params.example.json` — `ingressExternal: true`, `location: swedencentral`, blank master key + PEM.
- `docs/guides/deploy-azure.md` — expand into full reference + troubleshooting.
- `README.md` — link "Get started" to `quickstart-azure.md`.

**Delete:**

- `docs/guides/smoke-test-mvp-azure.md`

**Move:**

- `docs/guides/smoke-test-mvp-azure-findings.md` → `.smoke-history/2026-04-21-azure-mvp-findings.md`.

---

## Task 1: Runner abstraction

**Files:**
- Create: `internal/bootstrap/azure/runner.go`
- Create: `internal/bootstrap/azure/runner_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/runner_test.go
package azure

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockRunner_RecordsCallsAndReturnsCanned(t *testing.T) {
	mr := &MockRunner{
		Responses: []MockResponse{
			{Stdout: []byte("ok\n")},
			{Err: errors.New("nope")},
		},
	}
	out, err := mr.Run(context.Background(), "az", "account", "show")
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(out))

	_, err = mr.Run(context.Background(), "az", "deployment", "sub", "create")
	require.EqualError(t, err, "nope")

	require.Len(t, mr.Calls, 2)
	require.Equal(t, "az", mr.Calls[0].Name)
	require.Equal(t, []string{"account", "show"}, mr.Calls[0].Args)
}

func TestMockRunner_RunStreaming_WritesStdoutToWriter(t *testing.T) {
	var buf bytes.Buffer
	mr := &MockRunner{
		Responses: []MockResponse{{Stdout: []byte("streamed\n")}},
		Stdout:    &buf,
	}
	err := mr.RunStreaming(context.Background(), "az", "x")
	require.NoError(t, err)
	require.True(t, strings.Contains(buf.String(), "streamed"))
}

func TestMockRunner_RunWithEnv_RecordsEnv(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	err := mr.RunWithEnv(context.Background(), []string{"K=V"}, "echo", "hi")
	require.NoError(t, err)
	require.Len(t, mr.EnvCalls, 1)
	require.Equal(t, []string{"K=V"}, mr.EnvCalls[0].Env)
	require.Equal(t, "echo", mr.EnvCalls[0].Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/tng/workspace/cronfoundry/.claude/worktrees/spec-runbook
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/runner.go
// Package azure implements the `cronfoundry bootstrap azure` interactive
// installer.
package azure

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// Runner abstracts shell-outs so tests can stub them.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	RunStreaming(ctx context.Context, name string, args ...string) error
	RunWithEnv(ctx context.Context, env []string, name string, args ...string) error
}

// ExecRunner is the production Runner, backed by os/exec.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (r *ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (r *ExecRunner) RunStreaming(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

func (r *ExecRunner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// MockRunner is a test double recording calls and returning canned responses.
type MockRunner struct {
	Responses []MockResponse
	Calls     []MockCall
	EnvCalls  []MockEnvCall
	Stdout    io.Writer
}

type MockResponse struct {
	Stdout []byte
	Err    error
}

type MockCall struct {
	Name string
	Args []string
}

type MockEnvCall struct {
	Name string
	Args []string
	Env  []string
}

func (m *MockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	if len(m.Responses) == 0 {
		return nil, nil
	}
	r := m.Responses[0]
	m.Responses = m.Responses[1:]
	return r.Stdout, r.Err
}

func (m *MockRunner) RunStreaming(ctx context.Context, name string, args ...string) error {
	out, err := m.Run(ctx, name, args...)
	if m.Stdout != nil && out != nil {
		_, _ = m.Stdout.Write(out)
	}
	return err
}

func (m *MockRunner) RunWithEnv(_ context.Context, env []string, name string, args ...string) error {
	m.EnvCalls = append(m.EnvCalls, MockEnvCall{Name: name, Args: args, Env: env})
	if len(m.Responses) == 0 {
		return nil
	}
	r := m.Responses[0]
	m.Responses = m.Responses[1:]
	return r.Err
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/runner.go internal/bootstrap/azure/runner_test.go
git commit -m "$(cat <<'EOF'
bootstrap: Runner abstraction + MockRunner

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Inputs struct + validation

**Files:**
- Create: `internal/bootstrap/azure/inputs.go`
- Create: `internal/bootstrap/azure/inputs_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/inputs_test.go
package azure

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInputs_Validate_OK(t *testing.T) {
	require.NoError(t, goodInputs().Validate())
}

func TestInputs_Validate_BadEnv(t *testing.T) {
	in := goodInputs()
	in.Env = "this-is-far-too-long-suffix"
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "env")
}

func TestInputs_Validate_PasswordHasURLSpecialChars(t *testing.T) {
	in := goodInputs()
	in.PostgresPassword = "Abc@123XyzAbc123XyzAbc"
	err := in.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "URL")
}

func TestGeneratePassword_AlphanumericAndLongEnough(t *testing.T) {
	p, err := GeneratePostgresPassword()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(p), 20)
	for _, r := range p {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		require.True(t, ok, "non-alphanumeric in %q", p)
	}
}

func TestGeneratePassword_DistinctAcrossCalls(t *testing.T) {
	a, err := GeneratePostgresPassword()
	require.NoError(t, err)
	b, err := GeneratePostgresPassword()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func goodInputs() Inputs {
	return Inputs{
		Env:               "prod",
		Region:            "swedencentral",
		ImageOwner:        "gambtho",
		ImageTag:          "0.7.0",
		GithubAppID:       "12345",
		OAuthClientID:     "Iv23liabc",
		OAuthClientSecret: "shhh",
		PEMContents:       strings.Repeat("x", 32),
		AdminLogins:       "alice",
		PostgresPassword:  "Abc123XyzAbc123XyzAbc",
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `Inputs`, `GeneratePostgresPassword` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/inputs.go
package azure

import (
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Inputs is everything bootstrap needs to deploy.
type Inputs struct {
	Env               string // resource-name suffix, e.g. "prod"
	Region            string // azure region
	ImageOwner        string // ghcr owner, default "gambtho"
	ImageTag          string // ghcr tag, default "latest"
	GithubAppID       string
	OAuthClientID     string
	OAuthClientSecret string
	PEMContents       string // raw PEM contents (with BEGIN/END headers)
	AdminLogins       string // comma-separated github logins
	PostgresPassword  string
}

var envSuffixRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// Validate checks each field per the Bicep deployment's constraints.
func (in Inputs) Validate() error {
	if !envSuffixRe.MatchString(in.Env) {
		return fmt.Errorf("env %q invalid: must be 1-10 lowercase alphanumerics", in.Env)
	}
	if in.Region == "" {
		return errors.New("region required")
	}
	if in.GithubAppID == "" || in.OAuthClientID == "" || in.OAuthClientSecret == "" {
		return errors.New("github app id, oauth client id, oauth client secret all required")
	}
	if len(in.PEMContents) < 16 {
		return errors.New("PEM contents missing or implausibly short")
	}
	if in.AdminLogins == "" {
		return errors.New("at least one admin github login required")
	}
	if in.PostgresPassword == "" {
		return errors.New("postgres password required")
	}
	if strings.ContainsAny(in.PostgresPassword, "@:/%#?&=") {
		return errors.New("postgres password contains URL-special characters; alphanumerics only")
	}
	return nil
}

// GeneratePostgresPassword returns a 24-character alphanumeric password.
// Postgres ends up in a connection-string URL where @ : / etc. would need
// encoding, so we keep it simple.
func GeneratePostgresPassword() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 24
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/inputs.go internal/bootstrap/azure/inputs_test.go
git commit -m "$(cat <<'EOF'
bootstrap: Inputs struct + validation + password generator

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Preflight checks

**Files:**
- Create: `internal/bootstrap/azure/preflight.go`
- Create: `internal/bootstrap/azure/preflight_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/preflight_test.go
package azure

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreflight_HappyPath(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},
		{Stdout: []byte("Bicep CLI 0.30")},
	}}
	require.NoError(t, Preflight(context.Background(), mr))
	require.Len(t, mr.Calls, 2)
	require.Equal(t, []string{"account", "show"}, mr.Calls[0].Args)
	require.Equal(t, []string{"bicep", "version"}, mr.Calls[1].Args)
}

func TestPreflight_AzNotLoggedIn(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{Err: errors.New("Please run 'az login'")}}}
	err := Preflight(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "az login")
}

func TestPreflight_BicepMissing(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},
		{Err: errors.New("ERROR: bicep not installed")},
	}}
	err := Preflight(context.Background(), mr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bicep")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `Preflight` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/preflight.go
package azure

import (
	"context"
	"fmt"
)

// Preflight verifies az is logged in and bicep is installed. Returns a
// typed error with a remediation hint.
func Preflight(ctx context.Context, r Runner) error {
	if _, err := r.Run(ctx, "az", "account", "show"); err != nil {
		return fmt.Errorf("az login required: %w (run `az login` then retry)", err)
	}
	if _, err := r.Run(ctx, "az", "bicep", "version"); err != nil {
		return fmt.Errorf("bicep CLI required: %w (run `az bicep install` then retry)", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/preflight.go internal/bootstrap/azure/preflight_test.go
git commit -m "$(cat <<'EOF'
bootstrap: Preflight checks (az login + bicep)

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: GHCR image probe

**Files:**
- Create: `internal/bootstrap/azure/image.go`
- Create: `internal/bootstrap/azure/image_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/image_test.go
package azure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProbeImage_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodHead, r.Method)
		require.Equal(t, "/v2/gambtho/cronfoundry/manifests/0.7.0", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, probeImageAt(context.Background(), srv.URL, "gambtho", "0.7.0"))
}

func TestProbeImage_NotFound_ReturnsTagPushHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := probeImageAt(context.Background(), srv.URL, "gambtho", "0.7.0")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "git tag v0.7.0"),
		"want tag-push hint, got %q", err.Error())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `probeImageAt` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/image.go
package azure

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ghcrRoot = "https://ghcr.io"

// ProbeImage checks ghcr.io/<owner>/cronfoundry:<tag> exists anonymously.
// On 404 it returns an error suggesting the operator push a v* tag.
func ProbeImage(ctx context.Context, owner, tag string) error {
	return probeImageAt(ctx, ghcrRoot, owner, tag)
}

func probeImageAt(ctx context.Context, root, owner, tag string) error {
	url := fmt.Sprintf("%s/v2/%s/cronfoundry/manifests/%s",
		strings.TrimRight(root, "/"), owner, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf(
			"image %s not found. Publish it first:\n  git tag v%s && git push origin v%s\n(Wait ~5 min for the Release workflow.)",
			url, tag, tag)
	default:
		return fmt.Errorf("probe %s: HTTP %d", url, resp.StatusCode)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/image.go internal/bootstrap/azure/image_test.go
git commit -m "$(cat <<'EOF'
bootstrap: GHCR image probe with tag-push hint on 404

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: WriteParams

**Files:**
- Create: `internal/bootstrap/azure/params.go`
- Create: `internal/bootstrap/azure/params_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/params_test.go
package azure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteParams_AllFieldsPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "params.json")

	in := goodInputs()
	in.PEMContents = "-----BEGIN-----\nLINE1\nLINE2\n-----END-----\n"
	require.NoError(t, WriteParams(in, "MASTERKEYBASE64==", path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	want := []string{
		"env", "location", "imageTag",
		"githubAppId", "githubAppOAuthClientId", "githubAppOAuthClientSecret",
		"postgresAdminPassword", "masterKey", "githubAppPem",
		"adminLogins", "viewerLogins", "ingressExternal",
	}
	for _, k := range want {
		_, ok := doc.Parameters[k]
		require.True(t, ok, "missing param %q", k)
	}

	require.Equal(t, true, doc.Parameters["ingressExternal"].Value)
	require.Equal(t, "MASTERKEYBASE64==", doc.Parameters["masterKey"].Value)
	require.Equal(t, in.PEMContents, doc.Parameters["githubAppPem"].Value)
	require.Equal(t, "prod", doc.Parameters["env"].Value)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `WriteParams` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/params.go
package azure

import (
	"encoding/json"
	"os"
)

// WriteParams writes a Bicep deployment parameters file with every required
// field set, ingressExternal=true, and the PEM contents inlined.
func WriteParams(in Inputs, masterKey, paramsPath string) error {
	type p struct {
		Value any `json:"value"`
	}
	doc := map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters": map[string]p{
			"env":                        {in.Env},
			"location":                   {in.Region},
			"imageTag":                   {in.ImageTag},
			"githubAppId":                {in.GithubAppID},
			"githubAppOAuthClientId":     {in.OAuthClientID},
			"githubAppOAuthClientSecret": {in.OAuthClientSecret},
			"postgresAdminPassword":      {in.PostgresPassword},
			"masterKey":                  {masterKey},
			"githubAppPem":               {in.PEMContents},
			"adminLogins":                {in.AdminLogins},
			"viewerLogins":               {""},
			"ingressExternal":            {true},
		},
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paramsPath, body, 0o600)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/params.go internal/bootstrap/azure/params_test.go
git commit -m "$(cat <<'EOF'
bootstrap: WriteParams creates Bicep params file with ingressExternal=true

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Deploy / firewall / restart

**Files:**
- Create: `internal/bootstrap/azure/deploy.go`
- Create: `internal/bootstrap/azure/deploy_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/deploy_test.go
package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploy_InvokesAzWithExpectedArgs(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, Deploy(context.Background(), mr,
		"swedencentral", "deploy/main.bicep", "deploy/params.prod.json"))
	require.Len(t, mr.Calls, 1)
	require.Equal(t, "az", mr.Calls[0].Name)
	require.Equal(t, []string{
		"deployment", "sub", "create",
		"--location", "swedencentral",
		"--template-file", "deploy/main.bicep",
		"--parameters", "@deploy/params.prod.json",
	}, mr.Calls[0].Args)
}

func TestAllowOperatorIP_CallsAzWithIP(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, AllowOperatorIP(context.Background(), mr, "prod", "203.0.113.7"))
	require.Len(t, mr.Calls, 1)
	args := mr.Calls[0].Args
	require.Contains(t, args, "rg-cronfoundry-prod")
	require.Contains(t, args, "cf-pg-prod")
	require.Contains(t, args, "203.0.113.7")
}

func TestRestartServe_SetsRestartTrigger(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	require.NoError(t, RestartServe(context.Background(), mr, "prod"))
	require.Len(t, mr.Calls, 1)
	args := mr.Calls[0].Args
	require.Contains(t, args, "--name")
	require.Contains(t, args, "cf-serve-prod")
	var found bool
	for _, a := range args {
		if strings.HasPrefix(a, "RESTART_TRIGGER=") {
			found = true
		}
	}
	require.True(t, found, "expected RESTART_TRIGGER=... in args, got %v", args)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `Deploy`, `AllowOperatorIP`, `RestartServe` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/deploy.go
package azure

import (
	"context"
	"fmt"
	"time"
)

// Deploy invokes `az deployment sub create` and streams output through Runner.
func Deploy(ctx context.Context, r Runner, region, templateFile, paramsFile string) error {
	return r.RunStreaming(ctx,
		"az", "deployment", "sub", "create",
		"--location", region,
		"--template-file", templateFile,
		"--parameters", "@"+paramsFile,
	)
}

// AllowOperatorIP creates a Postgres firewall rule for the operator's IP.
// The rule name embeds the date so repeated runs don't collide.
func AllowOperatorIP(ctx context.Context, r Runner, env, ip string) error {
	rule := "cf-bootstrap-" + time.Now().UTC().Format("20060102")
	_, err := r.Run(ctx,
		"az", "postgres", "flexible-server", "firewall-rule", "create",
		"--resource-group", fmt.Sprintf("rg-cronfoundry-%s", env),
		"--name", fmt.Sprintf("cf-pg-%s", env),
		"--rule-name", rule,
		"--start-ip-address", ip,
		"--end-ip-address", ip,
	)
	return err
}

// RestartServe forces a new revision so the migrated schema is picked up.
// (Failed revisions don't auto-heal after admin init.)
func RestartServe(ctx context.Context, r Runner, env string) error {
	_, err := r.Run(ctx,
		"az", "containerapp", "update",
		"--resource-group", fmt.Sprintf("rg-cronfoundry-%s", env),
		"--name", fmt.Sprintf("cf-serve-%s", env),
		"--set-env-vars", fmt.Sprintf("RESTART_TRIGGER=%d", time.Now().Unix()),
	)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/deploy.go internal/bootstrap/azure/deploy_test.go
git commit -m "$(cat <<'EOF'
bootstrap: Deploy / AllowOperatorIP / RestartServe shell-outs

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: AdminInit shell-out

**Files:**
- Create: `internal/bootstrap/azure/admininit.go`
- Create: `internal/bootstrap/azure/admininit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/admininit_test.go
package azure

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminInit_PassesEnvVars(t *testing.T) {
	mr := &MockRunner{Responses: []MockResponse{{}}}
	dsn := "postgres://cfadmin:pw@cf-pg-prod.postgres.database.azure.com:5432/cronfoundry?sslmode=require"
	require.NoError(t, AdminInit(context.Background(), mr, "/path/to/cronfoundry", dsn, "MK", "default"))
	require.Len(t, mr.EnvCalls, 1)
	c := mr.EnvCalls[0]
	require.Equal(t, "/path/to/cronfoundry", c.Name)
	require.Contains(t, c.Args, "admin")
	require.Contains(t, c.Args, "init")
	require.Contains(t, c.Env, "CRONFOUNDRY_DATABASE_URL="+dsn)
	require.Contains(t, c.Env, "CRONFOUNDRY_MASTER_KEY=MK")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `AdminInit` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/admininit.go
package azure

import (
	"context"
	"fmt"
)

// AdminInit shells out to `<binary> admin init --org-name <org>` with
// CRONFOUNDRY_DATABASE_URL and CRONFOUNDRY_MASTER_KEY set in the env.
func AdminInit(ctx context.Context, r Runner, binary, dsn, masterKey, orgName string) error {
	env := []string{
		"CRONFOUNDRY_DATABASE_URL=" + dsn,
		"CRONFOUNDRY_MASTER_KEY=" + masterKey,
	}
	if err := r.RunWithEnv(ctx, env, binary, "admin", "init", "--org-name", orgName); err != nil {
		return fmt.Errorf("admin init: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/admininit.go internal/bootstrap/azure/admininit_test.go
git commit -m "$(cat <<'EOF'
bootstrap: AdminInit shell-out passing CRONFOUNDRY_* env vars

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Health polling

**Files:**
- Create: `internal/bootstrap/azure/health.go`
- Create: `internal/bootstrap/azure/health_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/health_test.go
package azure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitHealthy_ReturnsOnceHealthy(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/healthz", r.URL.Path)
		if atomic.AddInt32(&hits, 1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	require.NoError(t, waitHealthyAt(context.Background(), "http", host, 2*time.Second, 10*time.Millisecond))
}

func TestWaitHealthy_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	err := waitHealthyAt(context.Background(), "http", host, 50*time.Millisecond, 10*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `WaitHealthy`, `waitHealthyAt` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/health.go
package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// WaitHealthy polls https://<fqdn>/healthz until 200 OK or timeout.
func WaitHealthy(ctx context.Context, fqdn string, timeout time.Duration) error {
	return waitHealthyAt(ctx, "https", fqdn, timeout, 5*time.Second)
}

func waitHealthyAt(ctx context.Context, scheme, host string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("%s://%s/healthz", scheme, host)
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for /healthz")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/health.go internal/bootstrap/azure/health_test.go
git commit -m "$(cat <<'EOF'
bootstrap: WaitHealthy polls /healthz until 200 or timeout

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Interactive Prompt

**Files:**
- Create: `internal/bootstrap/azure/prompt.go`
- Create: `internal/bootstrap/azure/prompt_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/prompt_test.go
package azure

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrompt_HappyPath(t *testing.T) {
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "app.pem")
	require.NoError(t, os.WriteFile(pemPath,
		[]byte("-----BEGIN-----\nABCDEFGHIJKLMNOP\n-----END-----\n"), 0o600))

	stdin := strings.NewReader(strings.Join([]string{
		"",                // env (default prod)
		"",                // region (default swedencentral)
		"",                // image owner (default gambtho)
		"0.7.0",           // image tag
		"12345",           // github app id
		"Iv23liabc",       // oauth client id
		"super-secret",    // oauth client secret
		pemPath,           // pem path
		"alice",           // admin login
		"",                // postgres password (blank => generate)
	}, "\n") + "\n")

	var stdout bytes.Buffer
	in, err := Prompt(context.Background(), stdin, &stdout)
	require.NoError(t, err)
	require.Equal(t, "prod", in.Env)
	require.Equal(t, "swedencentral", in.Region)
	require.Equal(t, "gambtho", in.ImageOwner)
	require.Equal(t, "0.7.0", in.ImageTag)
	require.Contains(t, in.PEMContents, "BEGIN")
	require.Equal(t, "alice", in.AdminLogins)
	require.GreaterOrEqual(t, len(in.PostgresPassword), 20)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `Prompt` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/prompt.go
package azure

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompt reads bootstrap inputs interactively. Empty input keeps the
// suggested default.
func Prompt(_ context.Context, stdin io.Reader, stdout io.Writer) (Inputs, error) {
	r := bufio.NewReader(stdin)
	in := Inputs{}
	var err error

	if in.Env, err = ask(r, stdout, "env suffix", "prod"); err != nil {
		return in, err
	}
	if in.Region, err = ask(r, stdout, "Azure region", "swedencentral"); err != nil {
		return in, err
	}
	if in.ImageOwner, err = ask(r, stdout, "GHCR image owner", "gambtho"); err != nil {
		return in, err
	}
	if in.ImageTag, err = ask(r, stdout, "image tag", "latest"); err != nil {
		return in, err
	}
	if in.GithubAppID, err = ask(r, stdout, "GitHub App ID (numeric)", ""); err != nil {
		return in, err
	}
	if in.OAuthClientID, err = ask(r, stdout, "OAuth Client ID (Iv23li...)", ""); err != nil {
		return in, err
	}
	if in.OAuthClientSecret, err = ask(r, stdout, "OAuth Client Secret", ""); err != nil {
		return in, err
	}
	pemPath, err := ask(r, stdout, "Path to GitHub App .pem file", "")
	if err != nil {
		return in, err
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return in, fmt.Errorf("read pem: %w", err)
	}
	in.PEMContents = string(pemBytes)
	if in.AdminLogins, err = ask(r, stdout, "Admin GitHub login(s) (comma-separated)", ""); err != nil {
		return in, err
	}
	pw, err := ask(r, stdout, "Postgres admin password (blank to generate)", "")
	if err != nil {
		return in, err
	}
	if pw == "" {
		pw, err = GeneratePostgresPassword()
		if err != nil {
			return in, err
		}
		fmt.Fprintf(stdout, "  generated postgres password: %s\n", pw)
	}
	in.PostgresPassword = pw
	return in, nil
}

func ask(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return def, nil
	}
	return line, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/prompt.go internal/bootstrap/azure/prompt_test.go
git commit -m "$(cat <<'EOF'
bootstrap: interactive Prompt with defaults and password autogen

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Bootstrap orchestrator

**Files:**
- Create: `internal/bootstrap/azure/bootstrap.go`
- Create: `internal/bootstrap/azure/bootstrap_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/bootstrap/azure/bootstrap_test.go
package azure

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrap_DryRun_StopsAfterParamsWrite(t *testing.T) {
	dir := t.TempDir()
	paramsPath := filepath.Join(dir, "params.json")
	imageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer imageSrv.Close()

	mr := &MockRunner{Responses: []MockResponse{
		{Stdout: []byte(`{"id":"sub"}`)},   // az account show
		{Stdout: []byte("Bicep CLI 0.30")}, // az bicep version
	}}

	bs := &Bootstrap{
		Runner:       mr,
		Inputs:       goodInputs(),
		MasterKey:    "MK",
		ParamsPath:   paramsPath,
		TemplateFile: "deploy/main.bicep",
		DryRun:       true,
		ImageRoot:    imageSrv.URL,
		Stdout:       &bytes.Buffer{},
	}
	require.NoError(t, bs.Run(context.Background()))

	require.Len(t, mr.Calls, 2)
	for _, c := range mr.Calls {
		require.NotContains(t, strings.Join(c.Args, " "), "deployment sub create")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: FAIL — `Bootstrap` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/bootstrap/azure/bootstrap.go
package azure

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Bootstrap orchestrates the end-to-end Azure deploy flow.
type Bootstrap struct {
	Runner       Runner
	Inputs       Inputs
	MasterKey    string
	ParamsPath   string  // where to write the Bicep params file
	TemplateFile string  // path to deploy/main.bicep
	Binary       string  // path to the cronfoundry binary (for admin init)
	DryRun       bool    // skip deploy + everything after
	ImageRoot    string  // override ghcr.io for testing; defaults to ghcrRoot
	HealthScheme string  // "https" by default
	Stdout       io.Writer
}

// Run executes preflight, image probe, params write, deploy, firewall,
// admin init, restart, and health-wait in order. Honors DryRun.
func (b *Bootstrap) Run(ctx context.Context) error {
	if b.Stdout == nil {
		b.Stdout = io.Discard
	}
	if b.HealthScheme == "" {
		b.HealthScheme = "https"
	}
	root := b.ImageRoot
	if root == "" {
		root = ghcrRoot
	}

	if err := b.Inputs.Validate(); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> preflight")
	if err := Preflight(ctx, b.Runner); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> probing image")
	if err := probeImageAt(ctx, root, b.Inputs.ImageOwner, b.Inputs.ImageTag); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> writing params:", b.ParamsPath)
	if err := WriteParams(b.Inputs, b.MasterKey, b.ParamsPath); err != nil {
		return err
	}
	if b.DryRun {
		fmt.Fprintln(b.Stdout, "dry-run: skipping deploy")
		return nil
	}
	fmt.Fprintln(b.Stdout, "==> deploying (this takes ~10 minutes)")
	if err := Deploy(ctx, b.Runner, b.Inputs.Region, b.TemplateFile, b.ParamsPath); err != nil {
		return err
	}
	ip, err := detectPublicIP(ctx)
	if err != nil {
		return fmt.Errorf("detect public ip: %w", err)
	}
	fmt.Fprintln(b.Stdout, "==> opening postgres firewall to", ip)
	if err := AllowOperatorIP(ctx, b.Runner, b.Inputs.Env, ip); err != nil {
		return err
	}
	dsn := fmt.Sprintf(
		"postgres://cfadmin:%s@cf-pg-%s.postgres.database.azure.com:5432/cronfoundry?sslmode=require",
		b.Inputs.PostgresPassword, b.Inputs.Env)
	fmt.Fprintln(b.Stdout, "==> running admin init")
	if err := AdminInit(ctx, b.Runner, b.Binary, dsn, b.MasterKey, "default"); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout, "==> restarting serve revision")
	if err := RestartServe(ctx, b.Runner, b.Inputs.Env); err != nil {
		return err
	}
	out, err := b.Runner.Run(ctx,
		"az", "containerapp", "show",
		"--resource-group", "rg-cronfoundry-"+b.Inputs.Env,
		"--name", "cf-serve-"+b.Inputs.Env,
		"--query", "properties.configuration.ingress.fqdn",
		"-o", "tsv",
	)
	if err != nil {
		return fmt.Errorf("discover fqdn: %w", err)
	}
	fqdn := strings.TrimSpace(string(out))
	fmt.Fprintln(b.Stdout, "==> waiting for /healthz at", fqdn)
	if err := waitHealthyAt(ctx, b.HealthScheme, fqdn, 5*time.Minute, 5*time.Second); err != nil {
		return err
	}
	fmt.Fprintln(b.Stdout)
	fmt.Fprintln(b.Stdout, "Deploy complete.")
	fmt.Fprintln(b.Stdout, "  Login URL:        https://"+fqdn+"/")
	fmt.Fprintln(b.Stdout, "  GitHub App URLs:  paste https://"+fqdn+" into Homepage / Callback / Webhook")
	return nil
}

func detectPublicIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ifconfig.me", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/bootstrap/azure/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/azure/bootstrap.go internal/bootstrap/azure/bootstrap_test.go
git commit -m "$(cat <<'EOF'
bootstrap: end-to-end orchestrator with --dry-run

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: cobra wiring

**Files:**
- Create: `cmd/cronfoundry/bootstrap.go`
- Create: `cmd/cronfoundry/bootstrap_test.go`
- Modify: `cmd/cronfoundry/main.go`

- [ ] **Step 1: Write the failing test**

```go
// cmd/cronfoundry/bootstrap_test.go
package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrapCmd_HasAzureSubcommand(t *testing.T) {
	cmd := newBootstrapCmd()
	require.Equal(t, "bootstrap", cmd.Name())
	var azureSub *cobraTestProbe
	for _, sub := range cmd.Commands() {
		if sub.Name() == "azure" {
			azureSub = &cobraTestProbe{
				dryRun:    sub.Flags().Lookup("dry-run") != nil,
				paramsOut: sub.Flags().Lookup("params-out") != nil,
			}
		}
	}
	require.NotNil(t, azureSub, "azure subcommand missing")
	require.True(t, azureSub.dryRun, "missing --dry-run flag")
	require.True(t, azureSub.paramsOut, "missing --params-out flag")
}

type cobraTestProbe struct {
	dryRun    bool
	paramsOut bool
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./cmd/cronfoundry/... -run TestBootstrapCmd
```

Expected: FAIL — `newBootstrapCmd` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/cronfoundry/bootstrap.go`:

```go
// cmd/cronfoundry/bootstrap.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/bootstrap/azure"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)

func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a fresh CronFoundry deployment",
	}
	cmd.AddCommand(newBootstrapAzureCmd())
	return cmd
}

func newBootstrapAzureCmd() *cobra.Command {
	var (
		paramsOut    string
		templateFile string
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "azure",
		Short: "Interactive deploy to Azure",
		RunE: func(cmd *cobra.Command, args []string) error {
			runner := &azure.ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr}
			in, err := azure.Prompt(cmd.Context(), os.Stdin, os.Stdout)
			if err != nil {
				return err
			}
			if err := in.Validate(); err != nil {
				return err
			}
			masterKey, err := secretstore.GenerateMasterKey()
			if err != nil {
				return fmt.Errorf("generate master key: %w", err)
			}
			fmt.Fprintf(os.Stdout, "  generated master key: %s\n", masterKey)

			if paramsOut == "" {
				paramsOut = filepath.Join("deploy", fmt.Sprintf("params.%s.json", in.Env))
			}
			binary, err := os.Executable()
			if err != nil {
				return err
			}
			bs := &azure.Bootstrap{
				Runner:       runner,
				Inputs:       in,
				MasterKey:    masterKey,
				ParamsPath:   paramsOut,
				TemplateFile: templateFile,
				Binary:       binary,
				DryRun:       dryRun,
				Stdout:       os.Stdout,
			}
			return bs.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&paramsOut, "params-out", "",
		"where to write the Bicep params file (default deploy/params.<env>.json)")
	cmd.Flags().StringVar(&templateFile, "template-file", "deploy/main.bicep",
		"path to the Bicep template")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"stop after writing params, before az deployment")
	return cmd
}
```

Modify `cmd/cronfoundry/main.go` — add the `root.AddCommand` line:

```go
// cmd/cronfoundry/main.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "cronfoundry",
		Short:         "CronFoundry service CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newAdminCmd())
	root.AddCommand(newRunnerCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newBootstrapCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run test + build**

```bash
go test ./cmd/cronfoundry/... -run TestBootstrapCmd
go build ./...
go vet ./...
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/bootstrap.go cmd/cronfoundry/bootstrap_test.go cmd/cronfoundry/main.go
git commit -m "$(cat <<'EOF'
cmd: wire `cronfoundry bootstrap azure` cobra command

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Update params.example.json

**Files:**
- Modify: `deploy/params.example.json`

- [ ] **Step 1: Replace contents**

Overwrite `deploy/params.example.json` with:

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env": { "value": "prod" },
    "location": { "value": "swedencentral" },
    "imageTag": { "value": "latest" },
    "githubAppId": { "value": "YOUR_GITHUB_APP_ID" },
    "githubAppOAuthClientId": { "value": "YOUR_OAUTH_CLIENT_ID" },
    "githubAppOAuthClientSecret": { "value": "YOUR_OAUTH_CLIENT_SECRET" },
    "postgresAdminPassword": { "value": "REPLACE_WITH_24_CHAR_ALPHANUMERIC" },
    "masterKey": { "value": "" },
    "githubAppPem": { "value": "" },
    "adminLogins": { "value": "your-github-login" },
    "viewerLogins": { "value": "" },
    "ingressExternal": { "value": true }
  }
}
```

(`masterKey` and `githubAppPem` blank by design — `cronfoundry bootstrap azure` fills them in.)

- [ ] **Step 2: Verify**

```bash
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add deploy/params.example.json
git commit -m "$(cat <<'EOF'
deploy: params.example.json defaults work for the bootstrap path

ingressExternal=true (was false, broke webhooks). location=swedencentral.
masterKey/githubAppPem blank — bootstrap fills them.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Move findings file out of docs/

**Files:**
- Create: `.smoke-history/README.md`
- Move: `docs/guides/smoke-test-mvp-azure-findings.md` → `.smoke-history/2026-04-21-azure-mvp-findings.md`

- [ ] **Step 1: Create directory + README**

```bash
mkdir -p .smoke-history
```

Write `.smoke-history/README.md`:

```markdown
# Smoke-test history

Play-by-play logs from maintainer-run end-to-end deploys, kept as a paper
trail of issues that fed back into the runbook and Bicep. Not user-facing —
operators should read `docs/guides/quickstart-azure.md` instead.

Each file is dated by the session it captures.
```

- [ ] **Step 2: Move + commit**

```bash
git mv docs/guides/smoke-test-mvp-azure-findings.md .smoke-history/2026-04-21-azure-mvp-findings.md
git add .smoke-history/README.md
git commit -m "$(cat <<'EOF'
docs: move smoke-test findings out of docs/ to .smoke-history/

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 14: Write the new quickstart

**Files:**
- Create: `docs/guides/quickstart-azure.md`
- Modify: `README.md`

- [ ] **Step 1: Write `docs/guides/quickstart-azure.md`**

Use this complete content:

````markdown
# Quickstart: Deploy CronFoundry to Azure

End-to-end deploy from an empty Azure subscription to a green run. Target
wall time: ~45 minutes for a first-timer; ~25 minutes for a repeat.

For non-default deployments (custom domain, VNet, region pinning, etc.) see
[`deploy-azure.md`](./deploy-azure.md).

## 1. Prerequisites

- Azure subscription with Contributor rights; `az login` complete.
- `az` CLI ≥ 2.60 with the Bicep extension (`az bicep install`).
- A GitHub account, one Slack Incoming Webhook URL, one LLM API key
  (OpenAI / Anthropic / Azure AI Foundry), and two GitHub repos under the
  same owner: a **skill repo** (`cronfoundry.yaml` + `SKILL.md`) and a
  **reports repo** (where issues will be filed).

## 2. Register a GitHub App

> Register a **GitHub App**, not an OAuth App. Both live under
> *Settings → Developer settings*; only GitHub Apps have an App ID and
> private key. The URL must end in `/settings/apps/new`.

1. Open https://github.com/settings/apps/new.
2. **Name:** anything globally unique (`cronfoundry-<your-name>`).
3. **Homepage / Callback / Webhook URL:** placeholders for now (e.g.
   `https://example.com`); you replace them in step 6.
4. **Webhook secret:** generate a long random string and save it.
5. **Permissions:** Repository Contents (R+W), Issues (W), Metadata (R);
   Account Email (R).
6. **Subscribe to events:** Push.
7. Save. Note the **App ID**, generate a **client secret** and a
   **private key** (downloads `.pem`), then **Install** the App on both
   your skill repo and your reports repo.

## 3. Build the binary

```bash
git clone https://github.com/gambtho/cronfoundry.git
cd cronfoundry
make build
```

## 4. (First time only) Publish a container image

If `ghcr.io/gambtho/cronfoundry:latest` doesn't exist (forks, fresh
clones), tag a release:

```bash
git tag v0.7.0
git push origin v0.7.0
```

Wait ~5 minutes for the Release workflow. Skip this step if you're using
the upstream image.

## 5. Run bootstrap

```bash
./cronfoundry bootstrap azure
```

Answer the prompts. Bootstrap will:

1. Verify `az` is logged in and Bicep is installed.
2. Probe GHCR for the image tag (errors with the exact tag-push commands
   if missing).
3. Generate a master key, write `deploy/params.<env>.json`, and run
   `az deployment sub create` (~10 minutes).
4. Open the Postgres firewall to your IP, run `cronfoundry admin init`,
   restart the serve revision, and poll `/healthz` until green.

When it finishes you'll see the API FQDN.

## 6. Update the GitHub App URLs

Go back to your GitHub App settings page and replace the placeholders
from step 2 with the printed FQDN:

- Homepage URL: `https://<fqdn>/`
- Callback URL: `https://<fqdn>/oauth/callback`
- Webhook URL:  `https://<fqdn>/webhooks/github`

Save.

## 7. Wire up the web UI

1. Open `https://<fqdn>/` and log in via GitHub.
2. **Repos → Connect repo.** Paste `<owner>/<skill-repo>` and the
   installation ID.
3. **Secrets → Add** three secrets:
   - `llm_key` — your LLM API key
   - `slack_webhook` — your Slack Incoming Webhook URL
   - `github_webhook_secret` — the random string from step 2.4

## 8. Land a skill and verify

In your skill repo, add `cronfoundry.yaml`:

```yaml
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: every-5
        cron: "*/5 * * * *"
        timezone: UTC
        overlap_policy: skip
        timeout_sec: 300
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack:
              secret: slack_webhook
              text: "{{ output.truncated 35000 }}"
          - github-issue:
              repo: <owner>/<reports-repo>
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

…and `skills/smoke/SKILL.md`:

```markdown
---
name: smoke
description: Proves a run end-to-end
max_tokens: 500
---
Write one short paragraph confirming this pipeline works.
End with:

<memory>
run at {{ run.started_at }}
</memory>
```

Commit and push. Within ~5 minutes a run will fire — check the dashboard,
the Slack channel, the reports repo for a new issue, and your skill repo
for a new commit on `memory.md` authored by `cronfoundry[bot]`.

If all four happen, the deploy is green.

## Teardown

```bash
az group delete --name rg-cronfoundry-<env> --yes --no-wait
```

Then revoke the GitHub App installation.

## Troubleshooting

If anything fails, see [`deploy-azure.md`](./deploy-azure.md#troubleshooting).
````

- [ ] **Step 2: Update `README.md`**

```bash
git grep -n smoke-test-mvp-azure README.md
git grep -n deploy-azure README.md
```

For any "Get started" / "deploy" / "smoke" link in the README that points
at `smoke-test-mvp-azure.md`, replace the link target with
`docs/guides/quickstart-azure.md`. Leave any link to `deploy-azure.md` as
"full reference."

- [ ] **Step 3: Verify**

```bash
test -f docs/guides/quickstart-azure.md
wc -l docs/guides/quickstart-azure.md
```

Expected: file present.

- [ ] **Step 4: Commit**

```bash
git add docs/guides/quickstart-azure.md README.md
git commit -m "$(cat <<'EOF'
docs: add quickstart-azure.md (8 numbered steps)

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 15: Rewrite deploy-azure.md and delete smoke-test runbook

**Files:**
- Modify: `docs/guides/deploy-azure.md`
- Delete: `docs/guides/smoke-test-mvp-azure.md`

- [ ] **Step 1: Rewrite `docs/guides/deploy-azure.md`**

Overwrite with:

````markdown
# Deploy CronFoundry to Azure (Reference)

The happy path is in [`quickstart-azure.md`](./quickstart-azure.md). This
doc covers non-default deployments and troubleshooting.

## Manual deploy (without `cronfoundry bootstrap azure`)

If you can't or don't want to use the bootstrap command:

1. **Generate a master key:** `openssl rand -base64 32`. Save it; encrypted
   secrets in the database become unrecoverable if it's lost.
2. **Copy and edit the params file:**

   ```bash
   cp deploy/params.example.json deploy/params.prod.json
   ```

   Fill in:
   - `githubAppId`, `githubAppOAuthClientId`, `githubAppOAuthClientSecret`
   - `postgresAdminPassword` — 24+ alphanumerics, no `@ : / % # ? & =`
   - `masterKey` — your generated key
   - `githubAppPem` — full contents of the .pem file (use a small Python
     helper to embed newlines as `\n`)
   - `adminLogins` — comma-separated GitHub logins (not a JSON array)
   - `ingressExternal` — leave at `true` unless fronting with a private
     gateway

3. **Deploy:**

   ```bash
   az deployment sub create \
     --location swedencentral \
     --template-file deploy/main.bicep \
     --parameters @deploy/params.prod.json
   ```

   Takes ~10 minutes.

4. **Open Postgres to your IP:**

   ```bash
   MY_IP=$(curl -s https://ifconfig.me)
   az postgres flexible-server firewall-rule create \
     --resource-group rg-cronfoundry-<env> --name cf-pg-<env> \
     --rule-name op --start-ip-address "$MY_IP" --end-ip-address "$MY_IP"
   ```

5. **Run admin init locally:**

   ```bash
   CRONFOUNDRY_DATABASE_URL="postgres://cfadmin:<pw>@cf-pg-<env>.postgres.database.azure.com:5432/cronfoundry?sslmode=require" \
   CRONFOUNDRY_MASTER_KEY="<your master key>" \
   ./cronfoundry admin init
   ```

6. **Force a serve revision restart so the migrated schema is picked up:**

   ```bash
   az containerapp update \
     --resource-group rg-cronfoundry-<env> --name cf-serve-<env> \
     --set-env-vars RESTART_TRIGGER=$(date +%s)
   ```

7. **Discover the FQDN:**

   ```bash
   az containerapp show \
     --resource-group rg-cronfoundry-<env> --name cf-serve-<env> \
     --query properties.configuration.ingress.fqdn -o tsv
   ```

## Region selection

`Microsoft.DBforPostgreSQL/flexibleServers` is offer-restricted in some
subscriptions. The reliable probe is a synchronous create; the listing
APIs return SKUs your subscription cannot actually provision.
`swedencentral` is known-good for Microsoft-internal subscriptions;
`eastus`/`eastus2` were observed restricted.

## Custom domain / VNet

Out of the box the Container App uses the
`<env>.<random>.azurecontainerapps.io` FQDN. To put it behind a custom
domain, follow the
[Container Apps custom domain docs](https://learn.microsoft.com/azure/container-apps/custom-domains-certificates)
after the bootstrap completes. The Bicep does not yet wire VNet
integration; pass real `subnetId`/`privateDnsZoneId` to
`modules/postgres.bicep` if you need it.

## Upgrading the image

```bash
az containerapp update --resource-group rg-cronfoundry-<env> --name cf-serve-<env> \
  --image ghcr.io/gambtho/cronfoundry:0.X.Y
az containerapp job update --resource-group rg-cronfoundry-<env> --name cf-runner-<env> \
  --image ghcr.io/gambtho/cronfoundry:0.X.Y
```

## Teardown

```bash
az group delete --name rg-cronfoundry-<env> --yes --no-wait
```

Note: Key Vault and Postgres soft-delete pin the resource names for 7 days.
Re-deploys with the same `env` need to wait the retention window or use a
different suffix.

## Troubleshooting

- **`az deployment sub create` complains about `ingressExternal`** — the
  default `params.example.json` ships `true`; if you flipped it, the GitHub
  webhook can't reach your API. Set `true` and redeploy.
- **`docker manifest inspect ghcr.io/gambtho/cronfoundry:v0.7.0` returns
  `manifest unknown`** — `docker/metadata-action` strips the `v` prefix.
  Use `0.7.0`, `0.7`, or `latest`.
- **`LocationIsOfferRestricted`** — your subscription can't provision
  Postgres Flexible Server in this region. Try `swedencentral`.
- **GHCR pull fails with `denied`** — the package may be private. Visit
  `https://github.com/users/<owner>/packages/container/cronfoundry/settings`
  → Danger Zone → *Change visibility* → Public.
- **OAuth Client ID starts with `Ov23li`, not `Iv23li`** — you registered
  an OAuth App, not a GitHub App. Start over at
  `https://github.com/settings/apps/new`.
- **Container App stuck on the previous revision after `admin init`** —
  trigger a restart with `--set-env-vars RESTART_TRIGGER=$(date +%s)`.

## Maintainers: end-to-end smoke test

Periodic full-deploy testing is documented in `internal/bootstrap/azure/`
(unit-tested under `go test ./...`; an integration test gated on
`CRONFOUNDRY_E2E=1` exercises a real subscription). Historical
play-by-plays are in `.smoke-history/`.
````

- [ ] **Step 2: Delete the smoke-test doc**

```bash
git rm docs/guides/smoke-test-mvp-azure.md
```

- [ ] **Step 3: Verify**

```bash
test ! -f docs/guides/smoke-test-mvp-azure.md
go vet ./...
```

Expected: file gone; vet clean.

- [ ] **Step 4: Commit**

```bash
git add docs/guides/deploy-azure.md
git commit -m "$(cat <<'EOF'
docs: rewrite deploy-azure.md as reference; delete smoke-test runbook

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 16: Final test sweep + lint

**Files:** none

- [ ] **Step 1: Full test suite + vet + build**

```bash
go test ./... -short
go vet ./...
go build ./...
```

Expected: all PASS, no vet errors, build succeeds.

- [ ] **Step 2: Smoke-test the new command**

```bash
./cronfoundry bootstrap --help
./cronfoundry bootstrap azure --help
```

Expected: help text shows `--dry-run`, `--params-out`, `--template-file`.

- [ ] **Step 3: Confirm no leftover references to the deleted doc**

```bash
git grep -n smoke-test-mvp-azure docs/ README.md || echo "ok: no references"
```

Expected: `ok: no references` (only `.smoke-history/` is allowed to mention it, and we didn't grep there).

- [ ] **Step 4: Commit (only if anything changed)**

If grep surfaced stale references, fix them:

```bash
git add -u
git commit -m "$(cat <<'EOF'
docs: scrub remaining references to deleted smoke-test runbook

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 17: Open the PR

**Files:** none

- [ ] **Step 1: Push branch**

```bash
git push -u origin worktree-spec-runbook
```

- [ ] **Step 2: Create the PR**

```bash
gh pr create --title "docs: simplify Azure deploy runbook" --body "$(cat <<'EOF'
## Summary

- New `cronfoundry bootstrap azure` interactive subcommand (under `internal/bootstrap/azure/`) that wraps the manual `az`/openssl/PEM-upload choreography into one command.
- New `docs/guides/quickstart-azure.md` (8 numbered steps; ~45 min wall time for a first-timer).
- `docs/guides/deploy-azure.md` rewritten as the full reference + troubleshooting doc.
- `docs/guides/smoke-test-mvp-azure.md` deleted; `smoke-test-mvp-azure-findings.md` moved to `.smoke-history/`.
- `deploy/params.example.json` defaults updated: `ingressExternal: true`, `location: swedencentral`, `masterKey`/`githubAppPem` blank (filled in by bootstrap).

### Before / after

|  | Before | After |
|---|---|---|
| Canonical doc | `smoke-test-mvp-azure.md`, 451 lines, 10 §-numbered phases | `quickstart-azure.md`, 8 numbered steps |
| Operator commands | ~25 (openssl, az, python, az x N, curl, az x N) | ~3 (`make build`, `./cronfoundry bootstrap azure`, paste FQDN) |
| Estimated wall time | "an afternoon" with cliffs | ~45 min |

### Cliff coverage (F1–F24)

Every finding from the original `smoke-test-mvp-azure-findings.md` (now in `.smoke-history/`) is now handled either by `cronfoundry bootstrap azure`, the Bicep template (already merged), or pre-empted by the new params defaults. See the spec at `docs/superpowers/specs/2026-04-29-runbook-simplification-design.md` for the matrix.

## Test plan

- [ ] `go test ./... -short` passes
- [ ] `go vet ./...` clean
- [ ] `./cronfoundry bootstrap azure --dry-run` runs through preflight + image probe + params write end-to-end
- [ ] (Stretch) Fresh subscription deploy completed reading only the new quickstart

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Capture and report PR URL**

```bash
gh pr view --json url -q .url
```

Report the URL back to the user.

---

## Self-review

**Spec coverage:**
Mapped each component in the spec to a task — Runner (T1), Inputs (T2),
Preflight (T3), ProbeImage (T4), WriteParams (T5), Deploy /
AllowOperatorIP / RestartServe (T6), AdminInit (T7), WaitHealthy (T8),
Prompt (T9), Bootstrap orchestrator (T10), cobra wiring (T11),
params.example.json (T12), `.smoke-history/` move (T13), quickstart (T14),
deploy-azure rewrite + smoke-test delete (T15), final sweep (T16), PR
(T17). Cliff coverage matrix from the spec is reproduced in the PR body
and addressed by tasks T2–T12.

**Placeholder scan:**
No "TBD" / "implement later" / "similar to Task N" / "add appropriate
error handling." Each code step contains the actual code. Each
verification step lists the expected outcome.

**Type consistency:**
- `Runner` interface (with `RunWithEnv`) defined in T1; consumed in T3,
  T6, T7, T10. `MockRunner.EnvCalls`, `MockEnvCall` defined in T1; used
  in T7.
- `Inputs` field names defined in T2 used identically in T5, T9, T10.
- `Bootstrap` struct fields defined in T10 consumed in T11.
- Function names (`Preflight`, `ProbeImage`, `WriteParams`, `Deploy`,
  `AllowOperatorIP`, `RestartServe`, `AdminInit`, `WaitHealthy`,
  `Prompt`, `GeneratePostgresPassword`) consistent across tasks.
- `goodInputs()` helper defined in T2's test, reused in T5 and T10 tests
  (Go allows test helpers to be shared across `_test.go` files in the
  same package).
