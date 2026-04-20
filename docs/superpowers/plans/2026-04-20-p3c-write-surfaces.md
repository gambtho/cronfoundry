# P3c — Write Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add all write surfaces to the CronFoundry dashboard: schedule pause/resume and run-now, secret create/rotate/delete, and repo connect/disconnect via GitHub App installation.

**Architecture:** New write handlers in `internal/webapi` extending the existing `RegisterRoutes`. GitHub App installation callback reuses the `/oauth/` route pattern. All mutations audit-logged. Frontend adds inline action buttons/forms to existing pages plus a new Secrets page.

**Tech Stack:** Go 1.22+, React 18, TypeScript, TanStack Query v5 mutations, shadcn/ui.

---

## File Map

**New Go files:**
- `internal/webapi/schedules_write.go` + `schedules_write_test.go` — pause/resume/run-now
- `internal/webapi/secrets_write.go` + `secrets_write_test.go` — list/create/rotate/delete
- `internal/webapi/repos_write.go` + `repos_write_test.go` — connect/disconnect
- `internal/webapi/audit.go` — shared `writeAudit(ctx, q, ...)` helper

**Modified Go files:**
- `internal/webapi/server.go` — add Secrets + Dispatcher + OrgName + GitHubAppSlug to Deps; register new routes
- `cmd/cronfoundry/serve.go` — pass new Deps fields

**New SQL queries:**
- `internal/db/queries/audit.sql` — InsertAuditLog
- Regenerate: `internal/db/gen/`

**New/modified frontend files:**
- `web/src/api/mutations.ts` — TanStack Query mutations for all write actions
- `web/src/pages/Secrets.tsx` — new Secrets page
- `web/src/pages/Schedules.tsx` — add pause/resume/run-now buttons (modify P3b file)
- `web/src/pages/Repos.tsx` — add connect button + disconnect per row (modify P3b file)
- `web/src/App.tsx` — add `/secrets` route (modify P3b file)
- `web/src/components/Layout.tsx` — add Secrets nav item (modify P3b file)
- `web/src/components/ConfirmDialog.tsx` — reusable confirmation dialog
- `web/src/components/InlineForm.tsx` — reusable inline form for secret create/rotate

---

## Task 1: Audit log SQL query

**Files:**
- Create: `internal/db/queries/audit.sql`
- Modify: `internal/db/gen/` (regenerated)

- [ ] **Step 1: Write the query**

Create `internal/db/queries/audit.sql`:

```sql
-- name: InsertAuditLog :exec
INSERT INTO audit_log (org_id, actor, action, target_kind, target_id, detail_json)
VALUES ($1, $2, $3, $4, $5, $6);
```

- [ ] **Step 2: Regenerate sqlc**

```bash
cd /home/tng/workspace/cronfoundry && make sqlc
```

Expected: `internal/db/gen/audit.sql.go` created with `InsertAuditLog` method.

- [ ] **Step 3: Commit**

```bash
git add internal/db/queries/audit.sql internal/db/gen/
git commit -m "feat(db): InsertAuditLog query"
```

---

## Task 2: Shared audit helper + extend Deps

**Files:**
- Create: `internal/webapi/audit.go`
- Modify: `internal/webapi/server.go`
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Write audit.go**

Create `internal/webapi/audit.go`:

```go
package webapi

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// writeAudit inserts an audit_log row. Errors are logged but not returned —
// audit failures must never block the primary action.
func writeAudit(ctx context.Context, q *dbgen.Queries, orgID pgtype.UUID, actor, action, targetKind string, targetID pgtype.UUID, detail any) {
	var detailJSON []byte
	if detail != nil {
		detailJSON, _ = json.Marshal(detail)
	}
	login := actor
	err := q.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		OrgID:      orgID,
		Actor:      &login,
		Action:     action,
		TargetKind: &targetKind,
		TargetID:   targetID,
		DetailJson: detailJSON,
	})
	if err != nil {
		slog.Warn("audit: insert failed", "action", action, "err", err)
	}
}
```

- [ ] **Step 2: Extend Deps in server.go**

Update `internal/webapi/server.go` Deps struct:

```go
type Deps struct {
	MasterKey         []byte
	OAuthClientID     string
	OAuthClientSecret string
	AdminLogins       []string
	ViewerLogins      []string
	Pool              *pgxpool.Pool
	OrgID             pgtype.UUID
	Secrets           secretstore.SecretStore
	Dispatcher        cloud.JobDispatcher
	RunnerBinary      string   // path to the runner binary (for subprocess dispatch)
	APIBaseURL        string   // e.g. "http://127.0.0.1:8080"
	GitHubAppSlug     string   // e.g. "cronfoundry-app"
	GitHubAPIBase     string   // empty = real GitHub API
}
```

Add imports:
```go
import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)
```

- [ ] **Step 3: Pass new fields in serve.go**

In `cmd/cronfoundry/serve.go`, update the `webapi.RegisterRoutes` call:

```go
webapi.RegisterRoutes(mux, webapi.Deps{
    MasterKey:         master,
    OAuthClientID:     oauthClientID,
    OAuthClientSecret: oauthClientSecret,
    AdminLogins:       adminLogins,
    ViewerLogins:      viewerLogins,
    Pool:              pool,
    OrgID:             org.ID,
    Secrets:           store,
    Dispatcher:        dispatcher,
    RunnerBinary:      self,
    APIBaseURL:        "http://" + addr,
    GitHubAppSlug:     os.Getenv("CRONFOUNDRY_GITHUB_APP_SLUG"),
})
```

- [ ] **Step 4: Build to confirm it compiles**

```bash
cd /home/tng/workspace/cronfoundry && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/audit.go internal/webapi/server.go cmd/cronfoundry/serve.go
git commit -m "feat(webapi): audit helper, extend Deps with Secrets/Dispatcher/AppSlug"
```

---

## Task 3: POST /api/schedules/{id}/pause and /resume

**Files:**
- Create: `internal/webapi/schedules_write.go`
- Create: `internal/webapi/schedules_write_test.go`

- [ ] **Step 1: Add SetScheduleEnabled SQL query**

Append to `internal/db/queries/schedule.sql`:

```sql
-- name: SetScheduleEnabled :one
UPDATE schedule
SET enabled    = $2,
    updated_at = now()
WHERE id = $1 AND org_id = $3
RETURNING *;
```

Regenerate:
```bash
cd /home/tng/workspace/cronfoundry && make sqlc
```

- [ ] **Step 2: Write the failing test**

Create `internal/webapi/schedules_write_test.go`:

```go
package webapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestPauseSchedule(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org, _, _, sched := testdb.SeedSchedule(t, pool, ctx)

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey: key, Pool: pool, OrgID: org.ID,
	})

	req := httptest.NewRequest("POST", "/api/schedules/"+sched.ID.String()+"/pause", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	q := dbgen.New(pool)
	updated, err := q.GetSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if updated.Enabled {
		t.Error("expected schedule to be disabled")
	}
}

func TestResumeSchedule(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org, _, _, sched := testdb.SeedSchedule(t, pool, ctx)

	// Disable it first
	q := dbgen.New(pool)
	_, err := q.SetScheduleEnabled(ctx, dbgen.SetScheduleEnabledParams{
		ID: sched.ID, Enabled: false, OrgID: org.ID,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{MasterKey: key, Pool: pool, OrgID: org.ID})

	req := httptest.NewRequest("POST", "/api/schedules/"+sched.ID.String()+"/resume", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	updated, err := q.GetSchedule(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if !updated.Enabled {
		t.Error("expected schedule to be enabled")
	}
}
```

- [ ] **Step 3: Run to confirm they fail**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestPauseSchedule|TestResumeSchedule" -v
```

Expected: FAIL

- [ ] **Step 4: Implement schedules_write.go**

Create `internal/webapi/schedules_write.go`:

```go
package webapi

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type schedulePauseHandler struct {
	pool  *pgxpool.Pool
	orgID pgtype.UUID
}

func (h schedulePauseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setScheduleEnabled(w, r, h.pool, h.orgID, false)
}

type scheduleResumeHandler struct {
	pool  *pgxpool.Pool
	orgID pgtype.UUID
}

func (h scheduleResumeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setScheduleEnabled(w, r, h.pool, h.orgID, true)
}

func setScheduleEnabled(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, orgID pgtype.UUID, enabled bool) {
	idStr := r.PathValue("id")
	var schedID pgtype.UUID
	if err := schedID.Scan(idStr); err != nil {
		http.Error(w, "invalid schedule id", http.StatusBadRequest)
		return
	}

	q := dbgen.New(pool)
	updated, err := q.SetScheduleEnabled(r.Context(), dbgen.SetScheduleEnabledParams{
		ID:      schedID,
		Enabled: enabled,
		OrgID:   orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	claims := SessionClaimsFromContext(r.Context())
	action := "schedule.pause"
	if enabled {
		action = "schedule.resume"
	}
	writeAudit(r.Context(), q, orgID, claims.Login, action, "schedule", updated.ID, nil)

	w.WriteHeader(http.StatusNoContent)
}
```

Register in `server.go`:
```go
mux.Handle("POST /api/schedules/{id}/pause", session(schedulePauseHandler{pool: deps.Pool, orgID: deps.OrgID}))
mux.Handle("POST /api/schedules/{id}/resume", session(scheduleResumeHandler{pool: deps.Pool, orgID: deps.OrgID}))
```

- [ ] **Step 5: Run tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestPauseSchedule|TestResumeSchedule" -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/schedules_write.go internal/webapi/schedules_write_test.go internal/db/
git commit -m "feat(webapi): POST /api/schedules/{id}/pause and /resume"
```

---

## Task 4: POST /api/schedules/{id}/run-now

**Files:**
- Modify: `internal/webapi/schedules_write.go` + `schedules_write_test.go`

- [ ] **Step 1: Add GetSchedule SQL query (if not already present)**

Append to `internal/db/queries/schedule.sql` if missing:

```sql
-- name: GetSchedule :one
SELECT * FROM schedule WHERE id = $1;
```

Regenerate: `make sqlc`

- [ ] **Step 2: Write failing test**

Append to `internal/webapi/schedules_write_test.go`:

```go
func TestRunNow_CreatesRun(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org, _, _, sched := testdb.SeedSchedule(t, pool, ctx)

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	// Use a no-op dispatcher
	dispatcher := &noopDispatcher{}

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:  key,
		Pool:       pool,
		OrgID:      org.ID,
		Dispatcher: dispatcher,
		APIBaseURL: "http://127.0.0.1:8080",
	})

	req := httptest.NewRequest("POST", "/api/schedules/"+sched.ID.String()+"/run-now", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RunID == "" {
		t.Error("expected run_id in response")
	}
}

type noopDispatcher struct{}

func (d *noopDispatcher) Dispatch(_ context.Context, _ cloud.DispatchSpec) (cloud.Handle, error) {
	return &noopHandle{}, nil
}

type noopHandle struct{}

func (h *noopHandle) Wait(_ context.Context) error { return nil }
```

- [ ] **Step 3: Run to confirm it fails**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run TestRunNow -v
```

Expected: FAIL

- [ ] **Step 4: Implement run-now handler**

Append to `internal/webapi/schedules_write.go`:

```go
import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/token"
)

type scheduleRunNowHandler struct {
	pool         *pgxpool.Pool
	orgID        pgtype.UUID
	dispatcher   cloud.JobDispatcher
	runnerBinary string
	apiBaseURL   string
}

func (h scheduleRunNowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	var schedID pgtype.UUID
	if err := schedID.Scan(idStr); err != nil {
		http.Error(w, "invalid schedule id", http.StatusBadRequest)
		return
	}

	claims := SessionClaimsFromContext(r.Context())

	q := dbgen.New(h.pool)

	// Verify schedule exists and belongs to org
	sched, err := q.GetSchedule(r.Context(), schedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sched.OrgID != h.orgID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Generate runner token
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	rawToken := hex.EncodeToString(tokenBytes)
	tokenHash := token.Hash(rawToken)

	// Insert manual run
	run, _, err := q.InsertRun(r.Context(), dbgen.InsertRunParams{
		OrgID:           h.orgID,
		ScheduleID:      schedID,
		SkillSha:        sched.CurrentSha, // will be resolved by runner; use schedule's cached sha
		FireReason:      "manual",
		Actor:           &claims.Login,
		RunnerTokenHash: tokenHash,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Dispatch runner
	_, err = h.dispatcher.Dispatch(r.Context(), cloud.DispatchSpec{
		BinaryPath: h.runnerBinary,
		Args:       []string{"runner"},
		Env: []string{
			"RUN_ID=" + run.ID.String(),
			"CF_API_BASE_URL=" + h.apiBaseURL,
			"CF_RUNNER_TOKEN=" + rawToken,
		},
	})
	if err != nil {
		// Mark run failed if dispatch fails
		http.Error(w, "dispatch failed", http.StatusInternalServerError)
		return
	}

	writeAudit(r.Context(), q, h.orgID, claims.Login, "schedule.run_now", "run", run.ID, nil)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"run_id": run.ID.String()})
}
```

Also add `token.Hash` if it doesn't exist. Check `internal/token/jwt.go`:

```bash
grep -n "Hash" /home/tng/workspace/cronfoundry/internal/token/jwt.go
```

If missing, add to `internal/token/jwt.go`:
```go
import "crypto/sha256"

// Hash returns the hex-encoded SHA-256 of a raw token string.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
```

Register in `server.go`:
```go
mux.Handle("POST /api/schedules/{id}/run-now", session(scheduleRunNowHandler{
    pool:         deps.Pool,
    orgID:        deps.OrgID,
    dispatcher:   deps.Dispatcher,
    runnerBinary: deps.RunnerBinary,
    apiBaseURL:   deps.APIBaseURL,
}))
```

Note: `InsertRunParams` in the existing `internal/db/gen/run.sql.go` uses `SkillSha` not `CurrentSha` from schedule — the runner will re-resolve the sha at runtime via the API context endpoint. Pass an empty string or the schedule's cached skill sha as a placeholder; the runner overwrites it.

- [ ] **Step 5: Run tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run TestRunNow -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/schedules_write.go internal/webapi/schedules_write_test.go internal/db/ internal/token/
git commit -m "feat(webapi): POST /api/schedules/{id}/run-now"
```

---

## Task 5: Secret CRUD endpoints

**Files:**
- Create: `internal/webapi/secrets_write.go`
- Create: `internal/webapi/secrets_write_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/webapi/secrets_write_test.go`:

```go
package webapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestSecrets_CreateAndList(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.SeedOrg(t, pool, ctx)
	store := secretstore.NewEnvelopePostgresStore(pool, org.ID, make([]byte, 32))

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{MasterKey: key, Pool: pool, OrgID: org.ID, Secrets: store})

	// Create
	body, _ := json.Marshal(map[string]string{"name": "MY_KEY", "value": "secret123"})
	req := httptest.NewRequest("POST", "/api/secrets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List
	req2 := httptest.NewRequest("GET", "/api/secrets", nil)
	req2.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var secrets []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&secrets); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(secrets) != 1 || secrets[0].Name != "MY_KEY" {
		t.Errorf("unexpected secrets: %+v", secrets)
	}
}

func TestSecrets_Delete(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.SeedOrg(t, pool, ctx)
	store := secretstore.NewEnvelopePostgresStore(pool, org.ID, make([]byte, 32))
	if err := store.Put(ctx, "TO_DELETE", "val"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{MasterKey: key, Pool: pool, OrgID: org.ID, Secrets: store})

	req := httptest.NewRequest("DELETE", "/api/secrets/TO_DELETE", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	names, _ := store.List(ctx)
	if len(names) != 0 {
		t.Errorf("expected secret deleted, still got %v", names)
	}
}
```

- [ ] **Step 2: Run to confirm they fail**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestSecrets_" -v
```

Expected: FAIL

- [ ] **Step 3: Implement secrets_write.go**

Create `internal/webapi/secrets_write.go`:

```go
package webapi

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/secretstore"
)

type secretsHandler struct {
	pool    *pgxpool.Pool
	orgID   pgtype.UUID
	secrets secretstore.SecretStore
}

type secretListItem struct {
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	UpdatedAt  int64  `json:"updatedAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
}

// GET /api/secrets
func (h secretsHandler) list(w http.ResponseWriter, r *http.Request) {
	q := dbgen.New(h.pool)
	rows, err := q.ListSecretNames(r.Context(), h.orgID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := make([]secretListItem, len(rows))
	for i, row := range rows {
		item := secretListItem{
			Name:      row.Name,
			Version:   row.Version,
			UpdatedAt: row.UpdatedAt.Time.Unix(),
		}
		if row.LastUsedAt.Valid {
			t := row.LastUsedAt.Time.Unix()
			item.LastUsedAt = &t
		}
		resp[i] = item
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type secretWriteBody struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// POST /api/secrets
func (h secretsHandler) create(w http.ResponseWriter, r *http.Request) {
	var body secretWriteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Value == "" {
		http.Error(w, "name and value required", http.StatusBadRequest)
		return
	}

	// Check for conflict
	q := dbgen.New(h.pool)
	existing, _ := q.ListSecretNames(r.Context(), h.orgID)
	for _, s := range existing {
		if s.Name == body.Name {
			http.Error(w, "secret already exists", http.StatusConflict)
			return
		}
	}

	if err := h.secrets.Put(r.Context(), body.Name, body.Value); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	claims := SessionClaimsFromContext(r.Context())
	var dummyID pgtype.UUID
	writeAudit(r.Context(), q, h.orgID, claims.Login, "secret.create", "secret", dummyID, map[string]string{"name": body.Name})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"name": body.Name})
}

// PUT /api/secrets/{name}
func (h secretsHandler) rotate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct{ Value string `json:"value"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Value == "" {
		http.Error(w, "value required", http.StatusBadRequest)
		return
	}

	if err := h.secrets.Put(r.Context(), name, body.Value); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	q := dbgen.New(h.pool)
	claims := SessionClaimsFromContext(r.Context())
	var dummyID pgtype.UUID
	writeAudit(r.Context(), q, h.orgID, claims.Login, "secret.rotate", "secret", dummyID, map[string]string{"name": name})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"name": name})
}

// DELETE /api/secrets/{name}
func (h secretsHandler) delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if err := h.secrets.Delete(r.Context(), name); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	q := dbgen.New(h.pool)
	claims := SessionClaimsFromContext(r.Context())
	var dummyID pgtype.UUID
	writeAudit(r.Context(), q, h.orgID, claims.Login, "secret.delete", "secret", dummyID, map[string]string{"name": name})

	w.WriteHeader(http.StatusNoContent)
}
```

Register in `server.go`:
```go
sh := secretsHandler{pool: deps.Pool, orgID: deps.OrgID, secrets: deps.Secrets}
mux.Handle("GET /api/secrets", session(http.HandlerFunc(sh.list)))
mux.Handle("POST /api/secrets", session(http.HandlerFunc(sh.create)))
mux.Handle("PUT /api/secrets/{name}", session(http.HandlerFunc(sh.rotate)))
mux.Handle("DELETE /api/secrets/{name}", session(http.HandlerFunc(sh.delete)))
```

- [ ] **Step 4: Run tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestSecrets_" -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/secrets_write.go internal/webapi/secrets_write_test.go
git commit -m "feat(webapi): secret list/create/rotate/delete endpoints"
```

---

## Task 6: Repo connect (GitHub App installation callback) and disconnect

**Files:**
- Create: `internal/webapi/repos_write.go`
- Create: `internal/webapi/repos_write_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/webapi/repos_write_test.go`:

```go
package webapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestRepoConnect_RedirectsToGitHub(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.SeedOrg(t, pool, ctx)

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:     key,
		Pool:          pool,
		OrgID:         org.ID,
		GitHubAppSlug: "test-app",
	})

	req := httptest.NewRequest("GET", "/api/repos/connect", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "https://github.com/apps/test-app/installations/new" {
		t.Errorf("unexpected redirect: %s", loc)
	}
}

func TestRepoAppCallback_UpsertsMissingInstallationID(t *testing.T) {
	// When installation_id is absent, redirect to /repos?error=installation_cancelled
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.SeedOrg(t, pool, ctx)

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:     key,
		Pool:          pool,
		OrgID:         org.ID,
		GitHubAppSlug: "test-app",
	})

	req := httptest.NewRequest("GET", "/oauth/app-callback", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/repos?error=installation_cancelled" {
		t.Errorf("unexpected redirect: %s", w.Header().Get("Location"))
	}
}

func TestRepoDisconnect_RemovesRepo(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	org := testdb.SeedOrg(t, pool, ctx)

	// Seed a repo
	q := dbgen.New(pool)
	repo, err := q.UpsertRepoConnection(ctx, dbgen.UpsertRepoConnectionParams{
		OrgID:              org.ID,
		GithubAppInstallID: 42,
		Owner:              "myorg",
		Name:               "myrepo",
		DefaultBranch:      "main",
	})
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	key := make([]byte, 32)
	cookie := mustSignSession(t, key, "alice", "admin")

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{MasterKey: key, Pool: pool, OrgID: org.ID})

	req := httptest.NewRequest("DELETE", "/api/repos/"+repo.ID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	repos, _ := q.ListReposForOrg(ctx, org.ID)
	if len(repos) != 0 {
		t.Errorf("expected repo deleted, still have %d", len(repos))
	}
}
```

- [ ] **Step 2: Add DeleteRepoConnection SQL query**

Append to `internal/db/queries/repo_connection.sql`:

```sql
-- name: DeleteRepoConnection :execrows
DELETE FROM repo_connection WHERE id = $1 AND org_id = $2;
```

Regenerate: `make sqlc`

- [ ] **Step 3: Run to confirm they fail**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestRepo" -v
```

Expected: FAIL

- [ ] **Step 4: Implement repos_write.go**

Create `internal/webapi/repos_write.go`:

```go
package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
)

type repoConnectHandler struct {
	appSlug string
}

func (h repoConnectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.appSlug == "" {
		http.Error(w, "GitHub App not configured", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "https://github.com/apps/"+h.appSlug+"/installations/new", http.StatusFound)
}

type appCallbackHandler struct {
	pool          *pgxpool.Pool
	orgID         pgtype.UUID
	installations *github.InstallationCache
	apiBase       string
}

func (h appCallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	installIDStr := r.URL.Query().Get("installation_id")
	if installIDStr == "" {
		http.Redirect(w, r, "/repos?error=installation_cancelled", http.StatusFound)
		return
	}

	installID, err := strconv.ParseInt(installIDStr, 10, 64)
	if err != nil {
		http.Redirect(w, r, "/repos?error=installation_cancelled", http.StatusFound)
		return
	}

	// Fetch repos granted for this installation via GitHub App API
	repos, err := h.installations.ListInstallationRepos(r.Context(), installID)
	if err != nil {
		http.Redirect(w, r, "/repos?error=github_api_error", http.StatusFound)
		return
	}

	q := dbgen.New(h.pool)
	claims := SessionClaimsFromContext(r.Context())

	for _, repo := range repos {
		conn, err := q.UpsertRepoConnection(r.Context(), dbgen.UpsertRepoConnectionParams{
			OrgID:              h.orgID,
			GithubAppInstallID: installID,
			Owner:              repo.Owner,
			Name:               repo.Name,
			DefaultBranch:      repo.DefaultBranch,
		})
		if err != nil {
			continue
		}
		writeAudit(r.Context(), q, h.orgID, claims.Login, "repo.connect", "repo_connection", conn.ID, map[string]string{
			"owner": repo.Owner, "name": repo.Name,
		})
	}

	http.Redirect(w, r, "/repos", http.StatusFound)
}

type repoDisconnectHandler struct {
	pool  *pgxpool.Pool
	orgID pgtype.UUID
}

func (h repoDisconnectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	var repoID pgtype.UUID
	if err := repoID.Scan(idStr); err != nil {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.pool)
	n, err := q.DeleteRepoConnection(r.Context(), dbgen.DeleteRepoConnectionParams{
		ID:    repoID,
		OrgID: h.orgID,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	claims := SessionClaimsFromContext(r.Context())
	writeAudit(r.Context(), q, h.orgID, claims.Login, "repo.disconnect", "repo_connection", repoID, nil)

	w.WriteHeader(http.StatusNoContent)
}
```

Note: `github.InstallationCache` needs a `ListInstallationRepos` method. Check if it exists:

```bash
grep -n "ListInstallationRepos\|listInstallationRepos" /home/tng/workspace/cronfoundry/internal/github/installation.go
```

If missing, add to `internal/github/installation.go`:

```go
type InstallationRepo struct {
	Owner         string
	Name          string
	DefaultBranch string
}

// ListInstallationRepos returns the repos accessible under a given installation ID.
func (c *InstallationCache) ListInstallationRepos(ctx context.Context, installID int64) ([]InstallationRepo, error) {
	token, err := c.Token(ctx, installID)
	if err != nil {
		return nil, err
	}

	base := c.baseURL()
	url := base + fmt.Sprintf("/installation/repositories?per_page=100")

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Repositories []struct {
			Name          string `json:"name"`
			DefaultBranch string `json:"default_branch"`
			Owner         struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	repos := make([]InstallationRepo, len(body.Repositories))
	for i, r := range body.Repositories {
		repos[i] = InstallationRepo{
			Owner:         r.Owner.Login,
			Name:          r.Name,
			DefaultBranch: r.DefaultBranch,
		}
	}
	return repos, nil
}
```

Add `Installations` to Deps and register routes in `server.go`:

```go
type Deps struct {
	// ... existing fields ...
	Installations *github.InstallationCache
}

// In RegisterRoutes:
mux.Handle("GET /api/repos/connect", session(repoConnectHandler{appSlug: deps.GitHubAppSlug}))
mux.Handle("GET /oauth/app-callback", session(appCallbackHandler{
    pool:          deps.Pool,
    orgID:         deps.OrgID,
    installations: deps.Installations,
    apiBase:       deps.APIBaseURL,
}))
mux.Handle("DELETE /api/repos/{id}", session(repoDisconnectHandler{pool: deps.Pool, orgID: deps.OrgID}))
```

Pass `Installations` in `serve.go`:
```go
webapi.RegisterRoutes(mux, webapi.Deps{
    // ...existing...
    Installations: installs,
})
```

- [ ] **Step 5: Run tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./internal/webapi/... -run "TestRepo" -v
```

Expected: PASS

- [ ] **Step 6: Run all tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./...
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/webapi/repos_write.go internal/webapi/repos_write_test.go internal/github/ internal/db/
git commit -m "feat(webapi): repo connect/disconnect and app-callback"
```

---

## Task 7: Frontend mutations

**Files:**
- Create: `web/src/api/mutations.ts`

- [ ] **Step 1: Write mutations.ts**

Create `web/src/api/mutations.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import apiFetch from './client'

export function usePauseSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/api/schedules/${id}/pause`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })
}

export function useResumeSchedule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/api/schedules/${id}/resume`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })
}

export function useRunNow() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<{ run_id: string }>(`/api/schedules/${id}/run-now`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })
}

export function useCreateSecret() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      apiFetch('/api/secrets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, value }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['secrets'] }),
  })
}

export function useRotateSecret() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      apiFetch(`/api/secrets/${name}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ value }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['secrets'] }),
  })
}

export function useDeleteSecret() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => apiFetch(`/api/secrets/${name}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['secrets'] }),
  })
}

export function useDisconnectRepo() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/api/repos/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['repos'] }),
  })
}
```

Add `useSecrets` query to `queries.ts`:
```ts
export interface SecretMeta {
  name: string
  version: number
  updatedAt: number
  lastUsedAt: number | null
}

export function useSecrets() {
  return useQuery({ queryKey: ['secrets'], queryFn: () => apiFetch<SecretMeta[]>('/api/secrets') })
}
```

Also add `SecretMeta` to `types.ts`.

- [ ] **Step 2: Verify TypeScript compiles**

```bash
cd /home/tng/workspace/cronfoundry/web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/api/mutations.ts web/src/api/queries.ts web/src/api/types.ts
git commit -m "feat(web): TanStack Query mutations for all write actions"
```

---

## Task 8: Secrets page + write UI on Schedules/Repos pages

**Files:**
- Create: `web/src/pages/Secrets.tsx`
- Create: `web/src/components/ConfirmDialog.tsx`
- Create: `web/src/components/InlineForm.tsx`
- Modify: `web/src/pages/Schedules.tsx`
- Modify: `web/src/pages/Repos.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/Layout.tsx`

- [ ] **Step 1: Write ConfirmDialog.tsx**

Create `web/src/components/ConfirmDialog.tsx`:

```tsx
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

interface Props {
  open: boolean
  title: string
  description: string
  onConfirm: () => void
  onCancel: () => void
}

export default function ConfirmDialog({ open, title, description, onConfirm, onCancel }: Props) {
  return (
    <AlertDialog open={open}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onCancel}>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>Confirm</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
```

Install the alert-dialog component:
```bash
cd /home/tng/workspace/cronfoundry/web && npx shadcn@latest add alert-dialog input
```

- [ ] **Step 2: Write Secrets.tsx**

Create `web/src/pages/Secrets.tsx`:

```tsx
import { useState } from 'react'
import { useSecrets } from '../api/queries'
import { useCreateSecret, useDeleteSecret, useRotateSecret } from '../api/mutations'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import ConfirmDialog from '../components/ConfirmDialog'

export default function Secrets() {
  const { data: secrets, isLoading, error } = useSecrets()
  const createMutation = useCreateSecret()
  const rotateMutation = useRotateSecret()
  const deleteMutation = useDeleteSecret()

  const [showCreate, setShowCreate] = useState(false)
  const [createName, setCreateName] = useState('')
  const [createValue, setCreateValue] = useState('')
  const [createError, setCreateError] = useState('')

  const [rotatingName, setRotatingName] = useState<string | null>(null)
  const [rotateValue, setRotateValue] = useState('')

  const [deletingName, setDeletingName] = useState<string | null>(null)

  if (isLoading) return <div className="text-muted-foreground">Loading...</div>
  if (error || !secrets) return <div className="text-destructive">Failed to load secrets.</div>

  function handleCreate() {
    setCreateError('')
    createMutation.mutate(
      { name: createName, value: createValue },
      {
        onSuccess: () => { setShowCreate(false); setCreateName(''); setCreateValue('') },
        onError: (e: Error) => {
          if (e.message.startsWith('409')) {
            setCreateError('A secret with this name already exists.')
          } else {
            setCreateError('Failed to create secret.')
          }
        },
      },
    )
  }

  function handleRotate(name: string) {
    rotateMutation.mutate(
      { name, value: rotateValue },
      { onSuccess: () => { setRotatingName(null); setRotateValue('') } },
    )
  }

  function handleDelete(name: string) {
    deleteMutation.mutate(name, { onSuccess: () => setDeletingName(null) })
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Secrets</h1>
        {!showCreate && (
          <Button size="sm" onClick={() => setShowCreate(true)}>Add secret</Button>
        )}
      </div>

      {showCreate && (
        <div className="border rounded p-4 space-y-2">
          <div className="flex gap-2">
            <Input placeholder="Name" value={createName} onChange={(e) => setCreateName(e.target.value)} />
            <Input type="password" placeholder="Value" value={createValue} onChange={(e) => setCreateValue(e.target.value)} />
            <Button size="sm" onClick={handleCreate} disabled={!createName || !createValue}>Save</Button>
            <Button size="sm" variant="ghost" onClick={() => { setShowCreate(false); setCreateError('') }}>Cancel</Button>
          </div>
          {createError && <p className="text-sm text-destructive">{createError}</p>}
        </div>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Updated</TableHead>
            <TableHead>Last Used</TableHead>
            <TableHead></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {secrets.map((s) => (
            <>
              <TableRow key={s.name}>
                <TableCell className="font-mono">{s.name}</TableCell>
                <TableCell>{s.version}</TableCell>
                <TableCell className="text-sm">{new Date(s.updatedAt * 1000).toLocaleString()}</TableCell>
                <TableCell className="text-sm">{s.lastUsedAt ? new Date(s.lastUsedAt * 1000).toLocaleString() : '—'}</TableCell>
                <TableCell className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={() => setRotatingName(s.name)}>Rotate</Button>
                  <Button size="sm" variant="destructive" onClick={() => setDeletingName(s.name)}>Delete</Button>
                </TableCell>
              </TableRow>
              {rotatingName === s.name && (
                <TableRow key={s.name + '-rotate'}>
                  <TableCell colSpan={5}>
                    <div className="flex gap-2">
                      <Input type="password" placeholder="New value" value={rotateValue} onChange={(e) => setRotateValue(e.target.value)} />
                      <Button size="sm" onClick={() => handleRotate(s.name)} disabled={!rotateValue}>Save</Button>
                      <Button size="sm" variant="ghost" onClick={() => setRotatingName(null)}>Cancel</Button>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </>
          ))}
          {secrets.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="text-center text-muted-foreground">No secrets yet.</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      <ConfirmDialog
        open={deletingName !== null}
        title="Delete secret"
        description={`Delete "${deletingName}"? This cannot be undone.`}
        onConfirm={() => deletingName && handleDelete(deletingName)}
        onCancel={() => setDeletingName(null)}
      />
    </div>
  )
}
```

- [ ] **Step 3: Update Schedules.tsx with pause/resume/run-now**

In `web/src/pages/Schedules.tsx`, add action columns:

```tsx
import { useNavigate } from 'react-router-dom'
import { usePauseSchedule, useResumeSchedule, useRunNow } from '../api/mutations'
import { Button } from '@/components/ui/button'

// Inside the component:
const navigate = useNavigate()
const pauseMutation = usePauseSchedule()
const resumeMutation = useResumeSchedule()
const runNowMutation = useRunNow()

// Add to TableHeader:
<TableHead>Actions</TableHead>

// Add to TableRow:
<TableCell>
  <div className="flex gap-1">
    {s.enabled ? (
      <Button size="sm" variant="outline" onClick={() => pauseMutation.mutate(s.id)}>Pause</Button>
    ) : (
      <Button size="sm" variant="outline" onClick={() => resumeMutation.mutate(s.id)}>Resume</Button>
    )}
    <Button
      size="sm"
      onClick={() =>
        runNowMutation.mutate(s.id, {
          onSuccess: (data) => navigate(`/runs/${data.run_id}`),
        })
      }
    >
      Run now
    </Button>
  </div>
</TableCell>
```

- [ ] **Step 4: Update Repos.tsx with connect + disconnect**

In `web/src/pages/Repos.tsx`, add connect button and disconnect per row:

```tsx
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useDisconnectRepo } from '../api/mutations'
import { Button } from '@/components/ui/button'
import ConfirmDialog from '../components/ConfirmDialog'

// Inside component:
const [searchParams] = useSearchParams()
const installError = searchParams.get('error')
const disconnectMutation = useDisconnectRepo()
const [disconnectingId, setDisconnectingId] = useState<string | null>(null)
const [disconnectingName, setDisconnectingName] = useState('')

// Add after h1:
{installError === 'installation_cancelled' && (
  <div className="p-3 bg-destructive/10 rounded text-sm text-destructive">
    GitHub App installation was cancelled.
  </div>
)}
<div className="flex justify-end">
  <Button size="sm" onClick={() => window.location.href = '/api/repos/connect'}>Connect repo</Button>
</div>

// Add action column to each row:
<TableCell>
  <Button
    size="sm"
    variant="destructive"
    onClick={() => { setDisconnectingId(repo.id); setDisconnectingName(`${repo.owner}/${repo.name}`) }}
  >
    Disconnect
  </Button>
</TableCell>

// After table:
<ConfirmDialog
  open={disconnectingId !== null}
  title="Disconnect repo"
  description={`This will remove all skills and schedules for ${disconnectingName}. Are you sure?`}
  onConfirm={() => {
    if (disconnectingId) disconnectMutation.mutate(disconnectingId, { onSuccess: () => setDisconnectingId(null) })
  }}
  onCancel={() => setDisconnectingId(null)}
/>
```

- [ ] **Step 5: Add Secrets to routing and nav**

In `web/src/App.tsx`, add:
```tsx
import Secrets from './pages/Secrets'
// Inside Routes:
<Route path="/secrets" element={<Secrets />} />
```

In `web/src/components/Layout.tsx`, add to `navItems`:
```tsx
{ to: '/secrets', label: 'Secrets' },
```
(Insert between Repos and Schedules.)

- [ ] **Step 6: Build and verify**

```bash
cd /home/tng/workspace/cronfoundry/web && npm run build
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/
git commit -m "feat(web): Secrets page, pause/resume/run-now, repo connect/disconnect"
```

---

## Task 9: Full test suite + integration smoke test

- [ ] **Step 1: Run all Go tests**

```bash
cd /home/tng/workspace/cronfoundry && go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Build the binary with embedded UI**

```bash
cd /home/tng/workspace/cronfoundry && make build
```

Expected: binary built successfully.

- [ ] **Step 3: Start stack and smoke test**

```bash
docker compose -f deploy/docker-compose.yml up -d
sleep 3
./cronfoundry admin init --org default
./cronfoundry serve &
sleep 2

# Secrets endpoint (without session should 401)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/secrets
# Expected: 401

# Connect redirect (without session should 401)
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/api/repos/connect
# Expected: 401

kill %1
docker compose -f deploy/docker-compose.yml down
```

- [ ] **Step 4: Final commit**

```bash
git add .
git commit -m "test(p3c): integration smoke test passing"
```
