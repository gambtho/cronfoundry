# Multi-Platform Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AKS (K8s Jobs) and Fly.io (Fly Machines) as first-class deploy targets by wiring composable adapters for job dispatch, secrets, and runner authentication — leaving the existing Azure Container Apps path entirely unchanged.

**Architecture:** Split `internal/cloud/` into three independent adapter interfaces (`JobDispatcher`, `SecretStore`, `IdentityProvider`), add `internal/jobdispatch/k8sjobs` and `internal/jobdispatch/flymachines`, extend the existing `secretstore.SecretStore` interface with a `Ref()` method, add runner API-key auth alongside the existing bearer-token path, and add `deploy/aks/` and `deploy/fly/` directories. All wiring happens in `cmd/cronfoundry/serve.go` via env-var detection.

**Tech Stack:** Go 1.25, `k8s.io/client-go` (K8s Jobs), Fly Machines REST API (HTTP), existing `pgxpool` + `dbgen` (Postgres secret store already implemented), `cobra` for CLI wiring.

---

## File Map

### New files
- `internal/cloud/interfaces.go` — `JobDispatcher`, `SecretStore`, `IdentityProvider` interfaces + `DispatchRequest`
- `internal/jobdispatch/k8sjobs/dispatcher.go` — K8s Jobs adapter
- `internal/jobdispatch/k8sjobs/dispatcher_test.go`
- `internal/jobdispatch/flymachines/dispatcher.go` — Fly Machines adapter
- `internal/jobdispatch/flymachines/dispatcher_test.go`
- `internal/jobdispatch/flymachines/client.go` — HTTP client interface + real impl
- `internal/db/migrations/20260422000003_encrypted_secret_ref.sql` — add `ref` column to existing secret table (Fly.io needs a stable opaque ref)
- `deploy/aks/chart/Chart.yaml`
- `deploy/aks/chart/values.yaml`
- `deploy/aks/chart/templates/deployment-api.yaml`
- `deploy/aks/chart/templates/deployment-scheduler.yaml`
- `deploy/aks/chart/templates/serviceaccount.yaml`
- `deploy/aks/chart/templates/configmap.yaml`
- `deploy/aks/README.md`
- `deploy/fly/fly.api.toml`
- `deploy/fly/fly.runner.toml`
- `deploy/fly/README.md`

### Modified files
- `internal/cloud/dispatcher.go` — update `JobDispatcher` interface to match new `DispatchRequest` shape (rename `DispatchSpec` → `DispatchRequest`, keep backward compat)
- `internal/cloud/azure/dispatcher.go` — adapt to `DispatchRequest`
- `internal/cloud/azure/dispatcher_test.go` — update test types
- `internal/cloud/subprocess.go` — adapt to `DispatchRequest`
- `internal/cloud/subprocess_test.go` — update test types
- `internal/api/auth.go` — add `requireBearerOrAPIKey` middleware
- `internal/api/server.go` — switch `/internal` routes to use new middleware; add `RunnerAPIKey` to `Deps`
- `cmd/cronfoundry/serve.go` — add `buildJobDispatcher` cases for K8s/Fly, `buildSecretStore` case for Fly, env-var wiring

---

## Task 1: Rename `DispatchSpec` → `DispatchRequest`

The existing `cloud.DispatchSpec` type has the right shape but an inconsistent name vs. the spec. Rename before adding new adapters so all adapters share the same type name.

**Files:**
- Modify: `internal/cloud/dispatcher.go`
- Modify: `internal/cloud/azure/dispatcher.go`
- Modify: `internal/cloud/azure/dispatcher_test.go`
- Modify: `internal/cloud/subprocess.go`
- Modify: `internal/cloud/subprocess_test.go`
- Modify: `internal/scheduler/tick.go` (uses `cloud.DispatchSpec`)

- [ ] **Step 1: Run existing tests to confirm baseline**

```bash
go test ./internal/cloud/... ./internal/scheduler/... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 2: Rename the type**

In `internal/cloud/dispatcher.go`, change `DispatchSpec` to `DispatchRequest` everywhere (the struct definition and the `Dispatch` method signature):

```go
// DispatchRequest describes a single job to run.
type DispatchRequest struct {
	BinaryPath string
	Args       []string
	Env        []string
}

// JobDispatcher dispatches one-shot jobs and returns a Handle for supervision.
type JobDispatcher interface {
	Dispatch(ctx context.Context, spec DispatchRequest) (Handle, error)
}
```

- [ ] **Step 3: Update callers**

```bash
# Find all remaining references to DispatchSpec
grep -rn "DispatchSpec" /home/tng/workspace/cronfoundry/.worktrees/multicloud --include="*.go"
```

In each file listed, replace `cloud.DispatchSpec` → `cloud.DispatchRequest` and `DispatchSpec{` → `DispatchRequest{`.

- [ ] **Step 4: Verify it compiles and tests pass**

```bash
go build ./... && go test ./internal/cloud/... ./internal/scheduler/... -v 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/dispatcher.go internal/cloud/azure/dispatcher.go internal/cloud/azure/dispatcher_test.go internal/cloud/subprocess.go internal/cloud/subprocess_test.go internal/scheduler/tick.go
git commit -m "refactor(cloud): rename DispatchSpec → DispatchRequest"
```

---

## Task 2: K8s Jobs dispatcher — interface + tests

Add the `k8sjobs` package with a testable interface seam and tests written against a fake K8s client. The real k8s client (requires `k8s.io/client-go`) is wired in Task 3.

**Files:**
- Create: `internal/jobdispatch/k8sjobs/dispatcher.go`
- Create: `internal/jobdispatch/k8sjobs/dispatcher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/jobdispatch/k8sjobs/dispatcher_test.go`:

```go
package k8sjobs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/jobdispatch/k8sjobs"
)

type fakeK8sClient struct {
	created []k8sjobs.JobSpec
	err     error
}

func (f *fakeK8sClient) CreateJob(ctx context.Context, spec k8sjobs.JobSpec) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, spec)
	return nil
}

func TestDispatch_CreatesJob(t *testing.T) {
	fake := &fakeK8sClient{}
	cfg := k8sjobs.Config{
		Namespace:      "cronfoundry",
		RunnerImage:    "ghcr.io/cronfoundry/runner:v1.0.0",
		ServiceAccount: "cf-runner",
	}
	d := k8sjobs.NewDispatcher(fake, cfg)

	req := cloud.DispatchRequest{
		Args: []string{"runner"},
		Env:  []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"},
	}
	h, err := d.Dispatch(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Len(t, fake.created, 1)

	job := fake.created[0]
	require.Equal(t, "cronfoundry", job.Namespace)
	require.Equal(t, "ghcr.io/cronfoundry/runner:v1.0.0", job.Image)
	require.Equal(t, "cf-runner", job.ServiceAccount)
	require.Equal(t, []string{"runner"}, job.Args)
	require.Equal(t, []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"}, job.Env)
}

func TestDispatch_PropagatesClientError(t *testing.T) {
	fake := &fakeK8sClient{err: context.DeadlineExceeded}
	cfg := k8sjobs.Config{Namespace: "cronfoundry", RunnerImage: "img:v1"}
	d := k8sjobs.NewDispatcher(fake, cfg)

	_, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
```

- [ ] **Step 2: Run test to confirm it fails (package doesn't exist)**

```bash
go test ./internal/jobdispatch/k8sjobs/... 2>&1 | head -10
```
Expected: compile error, package not found.

- [ ] **Step 3: Write the implementation**

Create `internal/jobdispatch/k8sjobs/dispatcher.go`:

```go
// Package k8sjobs implements cloud.JobDispatcher using Kubernetes batch/v1 Jobs.
package k8sjobs

import (
	"context"
	"fmt"

	"github.com/gambtho/cronfoundry/internal/cloud"
)

// Config holds the static parameters for the K8s Jobs dispatcher.
type Config struct {
	Namespace      string
	RunnerImage    string
	ServiceAccount string // K8s ServiceAccount with workload identity annotation
}

// JobSpec is the neutral shape passed to K8sClient.CreateJob.
type JobSpec struct {
	Namespace      string
	Image          string
	ServiceAccount string
	Args           []string
	Env            []string
	// Name is generated by the dispatcher; callers do not set it.
	Name string
}

// K8sClient is the testability seam over the real Kubernetes client.
type K8sClient interface {
	CreateJob(ctx context.Context, spec JobSpec) error
}

// Dispatcher implements cloud.JobDispatcher using Kubernetes Jobs.
type Dispatcher struct {
	client K8sClient
	cfg    Config
}

// NewDispatcher returns a Dispatcher. Panics if client is nil.
func NewDispatcher(client K8sClient, cfg Config) *Dispatcher {
	if client == nil {
		panic("k8sjobs: NewDispatcher: client must not be nil")
	}
	return &Dispatcher{client: client, cfg: cfg}
}

// Compile-time interface check.
var _ cloud.JobDispatcher = (*Dispatcher)(nil)

// Dispatch creates a K8s Job and returns immediately. The returned Handle
// has PID()=0 and Wait()/Kill() not implemented (observe via kubectl/k8s API).
func (d *Dispatcher) Dispatch(ctx context.Context, req cloud.DispatchRequest) (cloud.Handle, error) {
	spec := JobSpec{
		Namespace:      d.cfg.Namespace,
		Image:          d.cfg.RunnerImage,
		ServiceAccount: d.cfg.ServiceAccount,
		Args:           append([]string{}, req.Args...),
		Env:            append([]string{}, req.Env...),
	}
	if err := d.client.CreateJob(ctx, spec); err != nil {
		return nil, fmt.Errorf("k8sjobs: dispatch: %w", err)
	}
	return &k8sHandle{}, nil
}

type k8sHandle struct{}

var _ cloud.Handle = (*k8sHandle)(nil)

func (h *k8sHandle) PID() int   { return 0 }
func (h *k8sHandle) Wait() error { return fmt.Errorf("k8sjobs: Wait not implemented: observe via kubectl") }
func (h *k8sHandle) Kill() error { return fmt.Errorf("k8sjobs: Kill not implemented: delete Job via kubectl") }
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/jobdispatch/k8sjobs/... -v 2>&1 | tail -15
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobdispatch/k8sjobs/
git commit -m "feat(k8sjobs): add K8s Jobs dispatcher with fake client tests"
```

---

## Task 3: K8s Jobs dispatcher — real client

Add the real `k8s.io/client-go` implementation that `serve.go` will use in production. This requires adding the dependency.

**Files:**
- Create: `internal/jobdispatch/k8sjobs/client_real.go`

- [ ] **Step 1: Add k8s.io/client-go dependency**

```bash
go get k8s.io/client-go@v0.32.0
go get k8s.io/api@v0.32.0
go get k8s.io/apimachinery@v0.32.0
go mod tidy
```

- [ ] **Step 2: Write the real client**

Create `internal/jobdispatch/k8sjobs/client_real.go`:

```go
package k8sjobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// RealK8sClient wraps a kubernetes.Clientset to satisfy K8sClient.
type RealK8sClient struct {
	cs *kubernetes.Clientset
}

// NewInClusterClient builds a RealK8sClient from the in-cluster service account config.
func NewInClusterClient() (*RealK8sClient, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8sjobs: in-cluster config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8sjobs: kubernetes client: %w", err)
	}
	return &RealK8sClient{cs: cs}, nil
}

// Compile-time interface check.
var _ K8sClient = (*RealK8sClient)(nil)

// CreateJob creates a batch/v1 Job with TTLSecondsAfterFinished=300 for auto-cleanup.
func (c *RealK8sClient) CreateJob(ctx context.Context, spec JobSpec) error {
	ttl := int32(300)
	backoff := int32(0)
	completions := int32(1)

	envVars := make([]corev1.EnvVar, 0, len(spec.Env))
	for _, kv := range spec.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("k8sjobs: malformed env entry %q (missing '=')", kv)
		}
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	name := fmt.Sprintf("cf-runner-%d", time.Now().UnixMilli())
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
		},
		Spec: batchv1.JobSpec{
			Completions:             &completions,
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: spec.ServiceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "runner",
							Image: spec.Image,
							Args:  spec.Args,
							Env:   envVars,
						},
					},
				},
			},
		},
	}
	_, err := c.cs.BatchV1().Jobs(spec.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("k8sjobs: create job %q: %w", name, err)
	}
	return nil
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/jobdispatch/k8sjobs/...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/jobdispatch/k8sjobs/client_real.go go.mod go.sum
git commit -m "feat(k8sjobs): add real in-cluster Kubernetes client"
```

---

## Task 4: Fly Machines dispatcher — interface + tests

Add the `flymachines` package with a testable client interface and tests against a fake.

**Files:**
- Create: `internal/jobdispatch/flymachines/client.go`
- Create: `internal/jobdispatch/flymachines/dispatcher.go`
- Create: `internal/jobdispatch/flymachines/dispatcher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/jobdispatch/flymachines/dispatcher_test.go`:

```go
package flymachines_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/jobdispatch/flymachines"
)

type fakeFlyClient struct {
	created []flymachines.CreateMachineRequest
	err     error
}

func (f *fakeFlyClient) CreateMachine(ctx context.Context, req flymachines.CreateMachineRequest) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, req)
	return nil
}

func TestDispatch_CreatesMachine(t *testing.T) {
	fake := &fakeFlyClient{}
	cfg := flymachines.Config{
		App:   "cronfoundry-runner",
		Image: "registry.fly.io/cronfoundry-runner:v1.0.0",
	}
	d := flymachines.NewDispatcher(fake, cfg)

	req := cloud.DispatchRequest{
		Args: []string{"runner"},
		Env:  []string{"RUN_ID=abc123", "SCHEDULE_ID=s1"},
	}
	h, err := d.Dispatch(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Len(t, fake.created, 1)

	m := fake.created[0]
	require.Equal(t, "cronfoundry-runner", m.AppName)
	require.Equal(t, "registry.fly.io/cronfoundry-runner:v1.0.0", m.Image)
	require.True(t, m.AutoDestroy)
	require.Equal(t, []string{"runner"}, m.Args)
	require.Equal(t, map[string]string{"RUN_ID": "abc123", "SCHEDULE_ID": "s1"}, m.Env)
}

func TestDispatch_PropagatesClientError(t *testing.T) {
	fake := &fakeFlyClient{err: context.DeadlineExceeded}
	cfg := flymachines.Config{App: "app", Image: "img:v1"}
	d := flymachines.NewDispatcher(fake, cfg)

	_, err := d.Dispatch(context.Background(), cloud.DispatchRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/jobdispatch/flymachines/... 2>&1 | head -10
```
Expected: compile error.

- [ ] **Step 3: Write the client interface**

Create `internal/jobdispatch/flymachines/client.go`:

```go
// Package flymachines implements cloud.JobDispatcher using the Fly Machines API.
package flymachines

import "context"

// CreateMachineRequest is the neutral shape for creating a Fly Machine.
type CreateMachineRequest struct {
	AppName     string
	Image       string
	AutoDestroy bool
	Args        []string
	Env         map[string]string
}

// FlyClient is the testability seam over the Fly Machines REST API.
type FlyClient interface {
	CreateMachine(ctx context.Context, req CreateMachineRequest) error
}
```

- [ ] **Step 4: Write the dispatcher**

Create `internal/jobdispatch/flymachines/dispatcher.go`:

```go
package flymachines

import (
	"context"
	"fmt"
	"strings"

	"github.com/gambtho/cronfoundry/internal/cloud"
)

// Config holds static parameters for the Fly Machines dispatcher.
type Config struct {
	App   string // Fly app name for the runner (pre-created)
	Image string // Registry image reference
}

// Dispatcher implements cloud.JobDispatcher using Fly Machines.
type Dispatcher struct {
	client FlyClient
	cfg    Config
}

// NewDispatcher returns a Dispatcher. Panics if client is nil.
func NewDispatcher(client FlyClient, cfg Config) *Dispatcher {
	if client == nil {
		panic("flymachines: NewDispatcher: client must not be nil")
	}
	return &Dispatcher{client: client, cfg: cfg}
}

// Compile-time interface check.
var _ cloud.JobDispatcher = (*Dispatcher)(nil)

// Dispatch creates an auto-destroy Fly Machine and returns immediately.
func (d *Dispatcher) Dispatch(ctx context.Context, req cloud.DispatchRequest) (cloud.Handle, error) {
	env := make(map[string]string, len(req.Env))
	for _, kv := range req.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("flymachines: malformed env entry %q (missing '=')", kv)
		}
		env[k] = v
	}
	mr := CreateMachineRequest{
		AppName:     d.cfg.App,
		Image:       d.cfg.Image,
		AutoDestroy: true,
		Args:        append([]string{}, req.Args...),
		Env:         env,
	}
	if err := d.client.CreateMachine(ctx, mr); err != nil {
		return nil, fmt.Errorf("flymachines: dispatch: %w", err)
	}
	return &flyHandle{}, nil
}

type flyHandle struct{}

var _ cloud.Handle = (*flyHandle)(nil)

func (h *flyHandle) PID() int    { return 0 }
func (h *flyHandle) Wait() error { return fmt.Errorf("flymachines: Wait not implemented: observe via flyctl") }
func (h *flyHandle) Kill() error { return fmt.Errorf("flymachines: Kill not implemented: stop via flyctl") }
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/jobdispatch/flymachines/... -v 2>&1 | tail -15
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobdispatch/flymachines/
git commit -m "feat(flymachines): add Fly Machines dispatcher with fake client tests"
```

---

## Task 5: Fly Machines dispatcher — real HTTP client

Add the real HTTP client that calls the Fly Machines REST API.

**Files:**
- Create: `internal/jobdispatch/flymachines/client_real.go`

- [ ] **Step 1: Write the real client**

Create `internal/jobdispatch/flymachines/client_real.go`:

```go
package flymachines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const flyMachinesBaseURL = "https://api.machines.dev/v1"

// RealFlyClient calls the Fly Machines REST API.
type RealFlyClient struct {
	apiToken string
	baseURL  string // overridable for testing; production uses flyMachinesBaseURL
	http     *http.Client
}

// NewRealFlyClient returns a FlyClient backed by the Fly Machines API.
// apiToken is the Fly API token (FLY_API_TOKEN env var).
func NewRealFlyClient(apiToken string) *RealFlyClient {
	return &RealFlyClient{
		apiToken: apiToken,
		baseURL:  flyMachinesBaseURL,
		http:     &http.Client{},
	}
}

// Compile-time interface check.
var _ FlyClient = (*RealFlyClient)(nil)

type flyCreateMachineBody struct {
	Config flyMachineConfig `json:"config"`
}

type flyMachineConfig struct {
	Image       string            `json:"image"`
	AutoDestroy bool              `json:"auto_destroy"`
	Env         map[string]string `json:"env,omitempty"`
	Init        flyMachineInit    `json:"init,omitempty"`
}

type flyMachineInit struct {
	Cmd []string `json:"cmd,omitempty"`
}

// CreateMachine calls POST /v1/apps/{app}/machines with auto_destroy: true.
func (c *RealFlyClient) CreateMachine(ctx context.Context, req CreateMachineRequest) error {
	body := flyCreateMachineBody{
		Config: flyMachineConfig{
			Image:       req.Image,
			AutoDestroy: true,
			Env:         req.Env,
			Init:        flyMachineInit{Cmd: req.Args},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("flymachines: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/apps/%s/machines", c.baseURL, req.AppName)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("flymachines: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("flymachines: POST machines: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("flymachines: POST machines: unexpected status %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/jobdispatch/flymachines/...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/jobdispatch/flymachines/client_real.go
git commit -m "feat(flymachines): add real Fly Machines HTTP client"
```

---

## Task 6: Runner API-key authentication

Add `X-Runner-Key` header auth to `/internal` endpoints for Fly.io (which has no managed identity). Only enabled when `CRONFOUNDRY_RUNNER_API_KEY` is set.

**Files:**
- Modify: `internal/api/auth.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/auth_test.go` (existing file — append new test function):

```go
func TestRequireBearerOrAPIKey_AcceptsAPIKey(t *testing.T) {
	called := false
	handler := requireBearerOrAPIKey(nil, nil, "super-secret-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	req.Header.Set("X-Runner-Key", "super-secret-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, called)
}

func TestRequireBearerOrAPIKey_RejectsWrongAPIKey(t *testing.T) {
	handler := requireBearerOrAPIKey(nil, nil, "super-secret-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	req.Header.Set("X-Runner-Key", "wrong-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearerOrAPIKey_RejectsNoCredentials(t *testing.T) {
	handler := requireBearerOrAPIKey(nil, nil, "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/runs/1/context", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/api/... -run TestRequireBearerOrAPIKey 2>&1 | head -15
```
Expected: compile error (`requireBearerOrAPIKey` not defined).

- [ ] **Step 3: Add `requireBearerOrAPIKey` to `internal/api/auth.go`**

Append to the end of `internal/api/auth.go`:

```go
// requireBearerOrAPIKey is like requireBearer but also accepts an X-Runner-Key
// header when runnerAPIKey is non-empty. Use for deployments (e.g. Fly.io)
// that have no managed identity service. When runnerAPIKey is empty, only the
// Bearer path is active (identical behavior to requireBearer).
func requireBearerOrAPIKey(signer *token.Signer, pool *pgxpool.Pool, runnerAPIKey string) func(http.Handler) http.Handler {
	bearerMiddleware := requireBearer(signer, pool)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if runnerAPIKey != "" && r.Header.Get("X-Runner-Key") == runnerAPIKey {
				next.ServeHTTP(w, r)
				return
			}
			bearerMiddleware(next).ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Add `RunnerAPIKey` to `Deps` in `internal/api/server.go`**

In `internal/api/server.go`, update the `Deps` struct:

```go
type Deps struct {
	Pool          *pgxpool.Pool
	Signer        *token.Signer
	Secrets       secretstore.SecretStore
	Installations *github.InstallationCache
	RunnerAPIKey  string // optional; when set, X-Runner-Key is accepted on /internal routes
}
```

And update `RegisterRoutes` to use the new middleware:

```go
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	auth := requireBearerOrAPIKey(deps.Signer, deps.Pool, deps.RunnerAPIKey)

	mux.Handle("GET /internal/runs/{id}/context", auth(runContextHandler{deps}))
	mux.Handle("GET /internal/secrets", auth(secretsHandler{deps}))
	mux.Handle("GET /internal/repos/{id}/clone-url", auth(cloneURLHandler{deps}))
	mux.Handle("POST /internal/runs/{id}/events", auth(eventsHandler{deps}))
	mux.Handle("POST /internal/runs/{id}/finalize", auth(finalizeHandler{deps}))
	mux.Handle("POST /internal/runs/{id}/writeback-push", auth(writebackPushHandler{deps}))

	mux.Handle("POST /internal/schedules/{id}/run-now", runNowHandler{deps})
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/api/... -v 2>&1 | tail -20
```
Expected: all PASS including the three new tests.

- [ ] **Step 6: Commit**

```bash
git add internal/api/auth.go internal/api/server.go
git commit -m "feat(api): add X-Runner-Key auth for deployments without managed identity"
```

---

## Task 7: Wire adapters in `serve.go`

Extend `buildJobDispatcher` and `buildSecretStore` in `cmd/cronfoundry/serve.go` to detect and wire the K8s and Fly.io adapters from env vars.

**Files:**
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Review current `buildJobDispatcher` and `buildSecretStore`**

Read the bottom of `cmd/cronfoundry/serve.go` (already done during exploration). The current logic:
- `buildJobDispatcher`: checks `AZURE_CAE_RESOURCE_GROUP` + `AZURE_CAE_JOB_NAME` + `AZURE_SUBSCRIPTION_ID` → Azure; else subprocess.
- `buildSecretStore`: checks `AZURE_KEYVAULT_URL` → KeyVault; else Postgres.

- [ ] **Step 2: Add K8s and Fly.io dispatch cases**

Replace the `buildJobDispatcher` function:

```go
func buildJobDispatcher() (cloud.JobDispatcher, error) {
	// Fly.io
	if app := os.Getenv("FLY_RUNNER_APP"); app != "" {
		token := os.Getenv("FLY_API_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("FLY_RUNNER_APP is set but FLY_API_TOKEN is missing")
		}
		image := os.Getenv("FLY_RUNNER_IMAGE")
		if image == "" {
			return nil, fmt.Errorf("FLY_RUNNER_APP is set but FLY_RUNNER_IMAGE is missing")
		}
		client := flymachines.NewRealFlyClient(token)
		return flymachines.NewDispatcher(client, flymachines.Config{App: app, Image: image}), nil
	}

	// AKS / Kubernetes
	if ns := os.Getenv("K8S_RUNNER_NAMESPACE"); ns != "" {
		image := os.Getenv("K8S_RUNNER_IMAGE")
		if image == "" {
			return nil, fmt.Errorf("K8S_RUNNER_NAMESPACE is set but K8S_RUNNER_IMAGE is missing")
		}
		sa := os.Getenv("K8S_RUNNER_SERVICE_ACCOUNT")
		if sa == "" {
			sa = "cf-runner"
		}
		k8sClient, err := k8sjobs.NewInClusterClient()
		if err != nil {
			return nil, fmt.Errorf("k8s in-cluster client: %w", err)
		}
		return k8sjobs.NewDispatcher(k8sClient, k8sjobs.Config{
			Namespace:      ns,
			RunnerImage:    image,
			ServiceAccount: sa,
		}), nil
	}

	// Azure Container Apps Jobs
	rg := os.Getenv("AZURE_CAE_RESOURCE_GROUP")
	jobName := os.Getenv("AZURE_CAE_JOB_NAME")
	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if rg != "" && jobName != "" && subID != "" {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("azure credential: %w", err)
		}
		armClient, err := cloudazure.NewRealARMJobsClient(subID, cred)
		if err != nil {
			return nil, fmt.Errorf("arm jobs client: %w", err)
		}
		return cloudazure.NewContainerAppsJobDispatcher(armClient, rg, jobName), nil
	}
	if rg != "" || jobName != "" || subID != "" {
		slog.Warn("serve: partial Azure dispatcher config — falling back to subprocess",
			"AZURE_CAE_RESOURCE_GROUP_set", rg != "",
			"AZURE_CAE_JOB_NAME_set", jobName != "",
			"AZURE_SUBSCRIPTION_ID_set", subID != "")
	}
	return cloud.NewSubprocessDispatcher(), nil
}
```

Add the imports at the top of `serve.go`:

```go
"github.com/gambtho/cronfoundry/internal/jobdispatch/flymachines"
"github.com/gambtho/cronfoundry/internal/jobdispatch/k8sjobs"
```

- [ ] **Step 3: Wire `RunnerAPIKey` into `Deps`**

In `runServe`, after building `apiDeps`, add:

```go
apiDeps.RunnerAPIKey = os.Getenv("CRONFOUNDRY_RUNNER_API_KEY")
```

- [ ] **Step 4: Verify it compiles**

```bash
go build ./cmd/cronfoundry/...
```
Expected: no errors.

- [ ] **Step 5: Run all tests**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok" | tail -25
```
Expected: all `ok`, no `FAIL`.

- [ ] **Step 6: Commit**

```bash
git add cmd/cronfoundry/serve.go go.mod go.sum
git commit -m "feat(serve): wire K8s and Fly.io dispatchers from env vars"
```

---

## Task 8: AKS Helm chart

Add the Helm chart for AKS deployment.

**Files:**
- Create: `deploy/aks/chart/Chart.yaml`
- Create: `deploy/aks/chart/values.yaml`
- Create: `deploy/aks/chart/templates/deployment-api.yaml`
- Create: `deploy/aks/chart/templates/deployment-scheduler.yaml`
- Create: `deploy/aks/chart/templates/serviceaccount.yaml`
- Create: `deploy/aks/chart/templates/configmap.yaml`
- Create: `deploy/aks/README.md`

- [ ] **Step 1: Create `Chart.yaml`**

Create `deploy/aks/chart/Chart.yaml`:

```yaml
apiVersion: v2
name: cronfoundry
description: CronFoundry — GitOps LLM skill scheduler
type: application
version: 0.1.0
appVersion: "latest"
```

- [ ] **Step 2: Create `values.yaml`**

Create `deploy/aks/chart/values.yaml`:

```yaml
image:
  api: ghcr.io/cronfoundry/cronfoundry
  tag: latest

runner:
  namespace: cronfoundry
  image: ghcr.io/cronfoundry/cronfoundry
  tag: latest
  serviceAccount: cf-runner

workloadIdentity:
  clientId: ""   # Azure AD managed identity client ID for cf-runner

env:
  DATABASE_URL: ""
  CRONFOUNDRY_MASTER_KEY: ""
  CRONFOUNDRY_GITHUB_APP_ID: ""
  CRONFOUNDRY_GITHUB_APP_PEM: ""
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID: ""
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET: ""
  CRONFOUNDRY_GITHUB_WEBHOOK_SECRET: ""
  CRONFOUNDRY_ADMIN_LOGINS: ""
  AZURE_KEYVAULT_URL: ""
  K8S_RUNNER_NAMESPACE: cronfoundry
  K8S_RUNNER_IMAGE: ""   # set to runner image:tag
  K8S_RUNNER_SERVICE_ACCOUNT: cf-runner
```

- [ ] **Step 3: Create `serviceaccount.yaml`**

Create `deploy/aks/chart/templates/serviceaccount.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cf-runner
  namespace: {{ .Release.Namespace }}
  annotations:
    azure.workload.identity/client-id: {{ .Values.workloadIdentity.clientId | quote }}
  labels:
    azure.workload.identity/use: "true"
```

- [ ] **Step 4: Create `configmap.yaml`**

Create `deploy/aks/chart/templates/configmap.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cronfoundry-env
  namespace: {{ .Release.Namespace }}
data:
  K8S_RUNNER_NAMESPACE: {{ .Values.runner.namespace | quote }}
  K8S_RUNNER_IMAGE: {{ printf "%s:%s" .Values.runner.image .Values.runner.tag | quote }}
  K8S_RUNNER_SERVICE_ACCOUNT: {{ .Values.runner.serviceAccount | quote }}
```

- [ ] **Step 5: Create `deployment-api.yaml`**

Create `deploy/aks/chart/templates/deployment-api.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cronfoundry-api
  namespace: {{ .Release.Namespace }}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cronfoundry-api
  template:
    metadata:
      labels:
        app: cronfoundry-api
    spec:
      containers:
        - name: api
          image: {{ printf "%s:%s" .Values.image.api .Values.image.tag | quote }}
          args: ["serve"]
          envFrom:
            - configMapRef:
                name: cronfoundry-env
          env:
            {{- range $k, $v := .Values.env }}
            - name: {{ $k }}
              value: {{ $v | quote }}
            {{- end }}
          ports:
            - containerPort: 8080
```

- [ ] **Step 6: Create `deployment-scheduler.yaml`**

Create `deploy/aks/chart/templates/deployment-scheduler.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cronfoundry-scheduler
  namespace: {{ .Release.Namespace }}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cronfoundry-scheduler
  template:
    metadata:
      labels:
        app: cronfoundry-scheduler
    spec:
      containers:
        - name: scheduler
          image: {{ printf "%s:%s" .Values.image.api .Values.image.tag | quote }}
          args: ["serve"]
          envFrom:
            - configMapRef:
                name: cronfoundry-env
          env:
            {{- range $k, $v := .Values.env }}
            - name: {{ $k }}
              value: {{ $v | quote }}
            {{- end }}
```

- [ ] **Step 7: Create `deploy/aks/README.md`**

Create `deploy/aks/README.md`:

```markdown
# CronFoundry — AKS Deployment

## Prerequisites

- AKS cluster with OIDC issuer enabled and workload identity addon installed
- Azure Key Vault with a managed identity `cf-runner` that has `Key Vault Secrets User` role
- Federated credential on `cf-runner` pointing to the AKS OIDC issuer + `cf-runner` service account
- `kubectl` and `helm` installed and pointed at your cluster

## Steps

1. Create namespace:
   ```bash
   kubectl create namespace cronfoundry
   ```

2. Copy and fill in values:
   ```bash
   cp deploy/aks/chart/values.yaml my-values.yaml
   # Edit my-values.yaml — fill in all empty strings
   ```

3. Set `workloadIdentity.clientId` to the client ID of the `cf-runner` managed identity.

4. Set `K8S_RUNNER_IMAGE` to the same image tag you are deploying (e.g. `ghcr.io/cronfoundry/cronfoundry:v1.2.0`).

5. Deploy:
   ```bash
   helm install cronfoundry deploy/aks/chart \
     -n cronfoundry \
     -f my-values.yaml
   ```

6. Verify pods are running:
   ```bash
   kubectl get pods -n cronfoundry
   ```

## Upgrade

```bash
helm upgrade cronfoundry deploy/aks/chart -n cronfoundry -f my-values.yaml
```

## Secrets

Secrets are stored in Azure Key Vault. Set `AZURE_KEYVAULT_URL` to your vault's URL. The `cf-runner` service account must have `Key Vault Secrets User` role on the vault.
```

- [ ] **Step 8: Commit**

```bash
git add deploy/aks/
git commit -m "feat(deploy/aks): add Helm chart for AKS deployment"
```

---

## Task 9: Fly.io deploy files

Add `fly.toml` files and setup guide for Fly.io.

**Files:**
- Create: `deploy/fly/fly.api.toml`
- Create: `deploy/fly/fly.runner.toml`
- Create: `deploy/fly/README.md`

- [ ] **Step 1: Create `fly.api.toml`**

Create `deploy/fly/fly.api.toml`:

```toml
app = "cronfoundry-api"
primary_region = "iad"

[build]
  image = "ghcr.io/cronfoundry/cronfoundry:latest"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "off"
  auto_start_machines = true
  min_machines_running = 1

[[vm]]
  memory = "512mb"
  cpu_kind = "shared"
  cpus = 1

# Required environment variables — set via: flyctl secrets set KEY=value
# DATABASE_URL
# CRONFOUNDRY_MASTER_KEY          (32-byte hex key for Postgres secret store)
# CRONFOUNDRY_RUNNER_API_KEY      (high-entropy random string shared with runner)
# CRONFOUNDRY_GITHUB_APP_ID
# CRONFOUNDRY_GITHUB_APP_PEM      (contents of the GitHub App private key PEM)
# CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID
# CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET
# CRONFOUNDRY_GITHUB_WEBHOOK_SECRET
# CRONFOUNDRY_ADMIN_LOGINS        (comma-separated GitHub logins)
# FLY_RUNNER_APP                  (= "cronfoundry-runner")
# FLY_RUNNER_IMAGE                (= "registry.fly.io/cronfoundry-runner:vX.Y.Z")
# FLY_API_TOKEN

[env]
  CF_SERVE_ADDR = "0.0.0.0:8080"
```

- [ ] **Step 2: Create `fly.runner.toml`**

Create `deploy/fly/fly.runner.toml`:

```toml
app = "cronfoundry-runner"
primary_region = "iad"

[build]
  image = "ghcr.io/cronfoundry/cronfoundry:latest"

# This app has no persistent processes — it exists only so Fly Machines
# can be spawned from the registered image. Do not deploy it as a service.
# Use: flyctl deploy --no-ha --strategy=immediate (to register the image)

[[vm]]
  memory = "1gb"
  cpu_kind = "shared"
  cpus = 1
```

- [ ] **Step 3: Create `deploy/fly/README.md`**

Create `deploy/fly/README.md`:

```markdown
# CronFoundry — Fly.io Deployment

## Prerequisites

- `flyctl` installed and authenticated (`flyctl auth login`)
- A Fly.io organization
- An external Postgres database OR `fly postgres create` (see below)

## Steps

### 1. Create apps

```bash
flyctl apps create cronfoundry-api
flyctl apps create cronfoundry-runner
```

### 2. Create Postgres

```bash
fly postgres create --name cronfoundry-db --region iad
fly postgres attach --app cronfoundry-api cronfoundry-db
# This sets DATABASE_URL automatically on cronfoundry-api
```

Or set `DATABASE_URL` manually if using an external DB.

### 3. Generate secrets

```bash
# 32-byte master key for the Postgres secret store
openssl rand -hex 32

# High-entropy runner API key
openssl rand -hex 32
```

### 4. Set secrets

```bash
flyctl secrets set --app cronfoundry-api \
  CRONFOUNDRY_MASTER_KEY=<hex-key-from-step-3> \
  CRONFOUNDRY_RUNNER_API_KEY=<runner-key-from-step-3> \
  CRONFOUNDRY_GITHUB_APP_ID=<app-id> \
  CRONFOUNDRY_GITHUB_APP_PEM="$(cat your-app.private-key.pem)" \
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID=<client-id> \
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET=<client-secret> \
  CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<webhook-secret> \
  CRONFOUNDRY_ADMIN_LOGINS=<your-github-login> \
  FLY_RUNNER_APP=cronfoundry-runner \
  FLY_RUNNER_IMAGE=registry.fly.io/cronfoundry-runner:latest \
  FLY_API_TOKEN=$(flyctl auth token)
```

The same `CRONFOUNDRY_RUNNER_API_KEY` must be set on the runner app:

```bash
flyctl secrets set --app cronfoundry-runner \
  CRONFOUNDRY_RUNNER_API_KEY=<same-runner-key>
```

### 5. Deploy API

```bash
flyctl deploy --config deploy/fly/fly.api.toml --app cronfoundry-api \
  --image ghcr.io/cronfoundry/cronfoundry:latest
```

### 6. Register runner image

```bash
flyctl deploy --config deploy/fly/fly.runner.toml --app cronfoundry-runner \
  --image ghcr.io/cronfoundry/cronfoundry:latest --no-ha
```

### 7. Verify

```bash
flyctl logs --app cronfoundry-api
# Open the API URL in a browser — you should see the CronFoundry login page
flyctl open --app cronfoundry-api
```

## Secrets Management

Secrets entered via the CronFoundry UI are stored **encrypted in Postgres** using AES-256-GCM with `CRONFOUNDRY_MASTER_KEY` as the encryption key. Back up this key — loss means loss of all stored secrets.

To rotate the master key, re-encrypt all secrets:
1. Export secrets via the CronFoundry admin CLI (future feature — for now, re-enter them manually after rotation).
2. Set the new `CRONFOUNDRY_MASTER_KEY`.
3. Re-enter secrets in the UI.
```

- [ ] **Step 4: Commit**

```bash
git add deploy/fly/
git commit -m "feat(deploy/fly): add Fly.io deploy files and setup guide"
```

---

## Task 10: Final verification

Run the full test suite and confirm everything passes.

- [ ] **Step 1: Run all tests**

```bash
go test ./... 2>&1 | grep -E "FAIL|ok|---"
```
Expected: all packages `ok`, no `FAIL`.

- [ ] **Step 2: Build all binaries**

```bash
go build ./cmd/cronfoundry/...
```
Expected: no errors.

- [ ] **Step 3: Verify new packages are covered**

```bash
go test ./internal/jobdispatch/... -v 2>&1 | grep -E "PASS|FAIL|RUN"
go test ./internal/api/... -run TestRequireBearerOrAPIKey -v
```
Expected: all PASS.

- [ ] **Step 4: Commit if any stray changes remain**

```bash
git status
# If clean, nothing to do.
```
