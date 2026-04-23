# mvpplus-3: LLM Prompt Secret Redaction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent secret values from appearing in LLM prompts by emitting `KEY=[secret]` placeholders in the `<env>` block instead of resolved values, while keeping late resolution (MCP env injection, destination dispatch) intact.

**Architecture:** The `buildEnvBanner` function in `internal/runner/runner.go` currently resolves secret values and injects them into the LLM system prompt. We change it to emit `KEY=[secret]` for secret-backed entries. The two late-resolution points (`resolveServerEnv` and the publish dispatcher) already call `s.Get()` independently and are unaffected. The `Resolver`'s `AllValues()` / redactor pipeline already scrubs values from run output — no changes needed there.

**Context — what's already done:** The pluggable secret backend infrastructure (`secretstore.SecretStore`, Postgres + Azure Key Vault implementations, management API, internal runner fetch endpoint) is fully implemented on this branch. This plan closes only the remaining gap: LLM prompt exposure.

**Tech Stack:** Go, `internal/runner/runner.go`, `internal/secrets/resolver.go`, `internal/runner/runner_test.go`, `internal/secrets/resolver_test.go`

---

## File Map

| File | Change |
|------|--------|
| `internal/runner/runner.go` | Modify `buildEnvBanner` — emit `[secret]` placeholder instead of resolved value |
| `internal/runner/runner_test.go` | Add test: secret value absent from env banner; existing env banner tests stay |
| `internal/secrets/resolver_test.go` | Verify `AllValues()` still returns values (redactor needs them) |

No new files. No schema changes.

---

## Task 1: Modify `buildEnvBanner` to withhold secret values from LLM prompt

**Files:**
- Modify: `internal/runner/runner.go:412-437`
- Test: `internal/runner/runner_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/runner/runner_test.go` (inside `package runner`, after the existing tests):

```go
func TestBuildEnvBanner_SecretRedacted(t *testing.T) {
	env := map[string]config.EnvValue{
		"GITHUB_TOKEN": {Secret: "github_pat"},
		"BASE_URL":     {Literal: "https://api.example.com"},
	}
	r := secrets.New(map[string]string{
		"CRONFOUNDRY_SECRET_GITHUB_PAT": "ghp_supersecret",
	})

	banner, err := buildEnvBanner(env, r)
	require.NoError(t, err)

	assert.Contains(t, banner, "GITHUB_TOKEN=[secret]")
	assert.Contains(t, banner, "BASE_URL=https://api.example.com")
	assert.NotContains(t, banner, "ghp_supersecret")
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd /home/tng/workspace/cronfoundry/.worktrees/mvpplus-3
go test ./internal/runner/... -run TestBuildEnvBanner_SecretRedacted -v
```

Expected: FAIL — `banner` contains `GITHUB_TOKEN=ghp_supersecret` and does not contain `GITHUB_TOKEN=[secret]`.

- [ ] **Step 3: Modify `buildEnvBanner`**

In `internal/runner/runner.go`, replace lines 424–432:

```go
// Before:
		val := v.Literal
		if v.Secret != "" {
			resolved, err := s.Get(v.Secret)
			if err != nil {
				return "", fmt.Errorf("env %s: %w", k, err)
			}
			val = resolved
		}
		fmt.Fprintf(&b, "%s=%s\n", k, val)
```

with:

```go
		if v.Secret != "" {
			fmt.Fprintf(&b, "%s=[secret]\n", k)
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v.Literal)
```

- [ ] **Step 4: Run the new test to verify it passes**

```bash
go test ./internal/runner/... -run TestBuildEnvBanner_SecretRedacted -v
```

Expected: PASS.

- [ ] **Step 5: Run the full runner test suite**

```bash
go test ./internal/runner/... -v
```

Expected: all tests pass. The existing integration test (`TestRun_WithSlack`) passes because it uses `secrets.New(...)` but the skill's `env:` block in that test doesn't reference a secret — only the destination does, which resolves via `resolveServerEnv` / dispatcher (unaffected).

- [ ] **Step 6: Commit**

```bash
cd /home/tng/workspace/cronfoundry/.worktrees/mvpplus-3
git add internal/runner/runner.go internal/runner/runner_test.go
git commit -m "feat(mvpplus-3): redact secret values from LLM env banner"
```

---

## Task 2: Verify redactor still receives secret values via `AllValues()`

The `Resolver.AllValues()` method feeds the redactor that scrubs run output. Since `buildEnvBanner` no longer calls `s.Get()` for secret entries, we need to confirm `AllValues()` still returns the values (it reads directly from the env map, so it should be unaffected — this task verifies that invariant explicitly).

**Files:**
- Test: `internal/secrets/resolver_test.go`

- [ ] **Step 1: Add explicit regression test to `resolver_test.go`**

Add after the existing `TestResolver_AllValues_ForRedaction` test:

```go
func TestResolver_AllValues_IndependentOfGet(t *testing.T) {
	// AllValues must return all secret values even if Get was never called.
	// The redactor calls AllValues() before any secret resolution happens.
	r := New(map[string]string{
		"CRONFOUNDRY_SECRET_TOKEN": "tok_abc",
		"CRONFOUNDRY_SECRET_KEY":   "key_xyz",
		"UNRELATED":                "not-a-secret",
	})

	// Do NOT call r.Get() first — verify AllValues() is unconditional.
	vals := r.AllValues()
	assert.ElementsMatch(t, []string{"tok_abc", "key_xyz"}, vals)
}
```

- [ ] **Step 2: Run the test**

```bash
go test ./internal/secrets/... -run TestResolver_AllValues_IndependentOfGet -v
```

Expected: PASS (no code change needed — this confirms the invariant holds).

- [ ] **Step 3: Run the full secrets test suite**

```bash
go test ./internal/secrets/... -v
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/secrets/resolver_test.go
git commit -m "test(secrets): assert AllValues independent of Get call order"
```

---

## Task 3: Run full test suite and verify no regressions

- [ ] **Step 1: Run all tests**

```bash
cd /home/tng/workspace/cronfoundry/.worktrees/mvpplus-3
go test ./... 2>&1
```

Expected: all packages pass. Key packages to check: `internal/runner`, `internal/secrets`, `internal/secretstore`, `internal/webapi`, `internal/api`.

- [ ] **Step 2: Verify secret value absent from a run with an env secret**

Add a short integration-style test to `internal/runner/runner_test.go` that sets an `env:` entry backed by a secret, runs the skill, and asserts the raw secret value does not appear in the messages the LLM received:

```go
func TestRun_SecretNotInLLMMessages(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := git.PlainInit(repoRoot, false)
	require.NoError(t, err)

	manifest := `
version: 1
skills:
  - path: skills/check
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: fake
        model: fake-model
        destinations:
          - slack:
              secret: slack_url
        env:
          API_KEY:
            secret: my_api_key
`
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "cronfoundry.yaml"), []byte(manifest), 0o644))
	skillDir := filepath.Join(repoRoot, "skills/check")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Check things."), 0o644))

	// Seed commit so the runner has a valid git repo.
	repo, _ := git.PlainOpen(repoRoot)
	w, _ := repo.Worktree()
	_ = w.AddGlob(".")
	_, err = w.Commit("seed", &git.CommitOptions{Author: sig()})
	require.NoError(t, err)

	slackCalled := false
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer slackSrv.Close()

	fake := &fakeProvider{response: "all good"}
	r := New(Deps{
		ProviderFactory: func(_ string) (llm.Provider, error) { return fake, nil },
		Publishers:      map[string]publish.Publisher{"slack": publish.NewSlackPublisher()},
	})

	result, err := r.Run(context.Background(), RunInput{
		RepoRoot:     repoRoot,
		ManifestPath: "cronfoundry.yaml",
		SkillPath:    "skills/check",
		ScheduleName: "daily",
		Secrets: secrets.New(map[string]string{
			"CRONFOUNDRY_SECRET_MY_API_KEY": "super-secret-value",
			"CRONFOUNDRY_SECRET_SLACK_URL":  slackSrv.URL,
		}),
		LLMAPIKey: "sk-test",
		DryRun:    false,
		SkipPush:  true,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.True(t, slackCalled)

	for _, msg := range fake.received {
		assert.NotContains(t, msg.Content, "super-secret-value",
			"secret value must not appear in LLM messages")
		assert.Contains(t, msg.Content, "API_KEY=[secret]")
	}
}
```

- [ ] **Step 3: Run the integration test**

```bash
go test ./internal/runner/... -run TestRun_SecretNotInLLMMessages -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/runner/runner_test.go
git commit -m "test(runner): assert secret values absent from LLM message context"
```

---

## Task 4: Update the mvpplus design doc to reflect implemented state

The design doc (`docs/superpowers/specs/2026-04-22-mvpplus-design.md`) lists mvpplus-3 as "KV-proxy sidecar (F9), image signing / SBOM (F13)". The spec for this work (`docs/superpowers/specs/2026-04-23-mvpplus-3-design.md`) accurately describes what was built. Update the phase table in the mvpplus design doc to reflect the completed backend work and the remaining image signing item.

**Files:**
- Modify: `docs/superpowers/specs/2026-04-22-mvpplus-design.md:25`

- [ ] **Step 1: Update the phase table row for mvpplus-3**

Change line 25 from:
```
| **mvpplus-3** | KV-proxy sidecar (F9), image signing / SBOM (F13) | Production hardening |
```
to:
```
| **mvpplus-3** | Pluggable secret backends + LLM prompt redaction (F9), image signing / SBOM (F13) | Production hardening |
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-04-22-mvpplus-design.md
git commit -m "docs(mvpplus): update mvpplus-3 phase description to match implementation"
```
