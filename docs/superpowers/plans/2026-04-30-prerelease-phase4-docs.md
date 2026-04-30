# Pre-release Polish — Phase 4: Docs Gap-Fill + Accuracy Pass

> **For agentic workers:** This plan is mostly procedural (audit + write docs). Use checkbox syntax to track progress. Where code is needed (generated reference docs), follow standard TDD.

**Goal:** Existing docs are accurate against the deployed product; new docs cover the gaps an internal-rollout operator hits in their first week (troubleshooting, manifest reference, env-var reference, operator runbook).

**Architecture:** Audit pass first (find drift, fix inline), then add new guides. Keep the docs in `docs/` as plain markdown — no docs-site reorganization. Generated references live under `docs/reference/` and are produced by small Go programs that walk the code.

**Tech Stack:** Markdown, `gomarkdoc` or hand-rolled Go AST walkers for generation, GitHub Pages (already configured per PR #33).

**Spec:** `docs/superpowers/specs/2026-04-30-prerelease-polish-design.md` §Phase 4.

**Prerequisite:** Phases 1, 2, 3 complete (so dogfood findings exist for the troubleshooting guide and screenshots exist for the README).

---

## Task 1: Audit existing docs against the codebase

**Output:** `docs/superpowers/specs/<date>-docs-audit.md` — a punch list of every drift found.

- [ ] **Step 1: Verify the README architecture tree is accurate**

```bash
ls internal/ | sort > /tmp/actual.txt
grep -A 50 "^## Architecture" README.md | grep -oE "^├── [a-z]+|^│   ├── [a-z]+" | sort > /tmp/documented.txt
diff /tmp/actual.txt /tmp/documented.txt
```

Record any difference in the audit punch list. Likely missing: `audit`, `bootstrap`, `githubapp`, `jobdispatch`, `mcp`, `metrics`.

- [ ] **Step 2: Verify the README "Quick start (local dev)" runs as written**

Walk the steps with a fresh clone and `make dev` (in a throwaway workspace). Record any drift: command flag changes, missing env vars, output mismatches.

- [ ] **Step 3: Verify the README "Quick start (standalone runner)" flags**

Run `./cronfoundry-runner run --help` and compare to the README flags table. Record drift.

- [ ] **Step 4: Verify every spec in `docs/superpowers/specs/` has an accurate status header**

```bash
grep -l "^**Status:**" docs/superpowers/specs/*.md | while read f; do
  echo "=== $f ==="
  head -5 "$f" | grep -i status
done
```

For each, judge: does the status match reality (Shipped vs In Progress vs Deferred)? Record drift.

- [ ] **Step 5: Cross-link check**

```bash
# Find every relative markdown link, check if the target exists.
grep -roE "\[[^]]+\]\(\.\.?/[^)]+\)" docs/ README.md | while IFS= read -r line; do
  file=$(echo "$line" | cut -d: -f1)
  link=$(echo "$line" | grep -oE "\([^)]+\)" | tr -d '()')
  target_dir=$(dirname "$file")
  target="$target_dir/$link"
  [[ ! -e "$target" ]] && echo "BROKEN: $file -> $link (resolved: $target)"
done
```

Record broken links.

- [ ] **Step 6: Commit the audit punch list**

```bash
git add docs/superpowers/specs/<date>-docs-audit.md
git commit -m "docs: audit punch list for pre-release polish"
```

---

## Task 2: Fix the audit findings

For each finding from Task 1, a focused commit. Examples:

- [ ] **README architecture tree** — add `audit`, `bootstrap`, `githubapp`, `jobdispatch`, `mcp`, `metrics` to the tree, with one-line descriptions matching the codebase.

- [ ] **README quick-start (local dev)** — fix any flag or env-var drift.

- [ ] **README quick-start (standalone runner)** — fix the flags table to match `--help` output verbatim.

- [ ] **Stale spec headers** — update each `**Status:**` line.

- [ ] **Broken cross-links** — fix or remove.

Each fix → one commit:

```bash
git commit -m "docs(readme): update architecture tree to match internal/ packages"
# etc.
```

---

## Task 3: Generate `docs/reference/env-vars.md` from code

**Files:**
- Create: `docs/reference/env-vars.md`
- Create: `cmd/dev-tools/gen-env-vars/main.go` — a small Go program that walks the codebase, finds every `os.Getenv("CRONFOUNDRY_*")` and `viper.Get*` call, builds a table.
- Modify: `Makefile` — add `make docs-env-vars` target.

- [ ] **Step 1: Write a small failing test for the generator**

```go
// cmd/dev-tools/gen-env-vars/main_test.go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractEnvVars_FindsBasicCalls(t *testing.T) {
	tmp := t.TempDir()
	src := `package x
import "os"
func init() {
  _ = os.Getenv("CRONFOUNDRY_FOO_BAR")
  _ = os.Getenv("CRONFOUNDRY_BAZ")
  _ = os.Getenv("UNRELATED")
}`
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := extractEnvVars(tmp, "CRONFOUNDRY_")
	want := []string{"CRONFOUNDRY_BAZ", "CRONFOUNDRY_FOO_BAR"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Implement**

```go
// cmd/dev-tools/gen-env-vars/main.go
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func extractEnvVars(root, prefix string) []string {
	seen := map[string]bool{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val := strings.Trim(lit.Value, `"`)
			if strings.HasPrefix(val, prefix) {
				seen[val] = true
			}
			return true
		})
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen { out = append(out, k) }
	sort.Strings(out)
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gen-env-vars <root>")
		os.Exit(2)
	}
	vars := extractEnvVars(os.Args[1], "CRONFOUNDRY_")
	fmt.Println("# Environment Variables")
	fmt.Println()
	fmt.Println("Generated from source. Do not edit by hand.")
	fmt.Println()
	fmt.Println("| Name | Used in |")
	fmt.Println("|------|---------|")
	for _, v := range vars {
		fmt.Printf("| `%s` | (TODO: annotate) |\n", v)
	}
}
```

- [ ] **Step 3: Run the test**

Run: `go test ./cmd/dev-tools/gen-env-vars/ -count=1`
Expected: PASS.

- [ ] **Step 4: Generate the doc**

```bash
go run ./cmd/dev-tools/gen-env-vars . > docs/reference/env-vars.md
```

- [ ] **Step 5: Hand-annotate the "Used in" column**

For each env var, add a one-line description. Skip ones that are clearly internal/test-only.

- [ ] **Step 6: Add Makefile target**

```make
docs-env-vars:
	go run ./cmd/dev-tools/gen-env-vars . > docs/reference/env-vars.md
	@echo 'Regenerated docs/reference/env-vars.md. Re-annotate any new entries.'
```

- [ ] **Step 7: Link from README**

Add to README, after the "Configuration format" section:

```markdown
For a full list of supported environment variables, see [`docs/reference/env-vars.md`](docs/reference/env-vars.md).
```

- [ ] **Step 8: Commit**

```bash
git add cmd/dev-tools/gen-env-vars/ docs/reference/env-vars.md Makefile README.md
git commit -m "feat(docs): generated env-var reference"
```

---

## Task 4: Generate `docs/reference/manifest.md` from `internal/config`

Same pattern as Task 3, but walks the `internal/config` package's struct tags to produce a YAML field reference.

- [ ] **Step 1: Read `internal/config/*.go`** to identify the manifest struct (likely `Manifest` or `Config`).

- [ ] **Step 2: Write a generator** that reflects over the struct and produces a markdown table:

```
| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `version` | int | required | Manifest schema version |
| `skills[].path` | string | required | Path to skill directory |
| ... |
```

Use `reflect` + tag parsing (`yaml:` and a new `doc:` tag if descriptions are absent).

- [ ] **Step 3: Add `doc:` tags to the config structs** for any field missing one.

- [ ] **Step 4: Generate, hand-edit, commit, link from README and from `schedule-authoring.md`.**

```bash
git commit -m "feat(docs): generated manifest reference"
```

---

## Task 5: Write `docs/guides/troubleshooting.md`

**Source material:**
- The "Troubleshooting" tables already in `docs/guides/quickstart-copilot.md` and `docs/guides/quickstart-azure.md`.
- The dogfood Round-1 punch list from Phase 2.
- The error messages emitted by the codebase: `grep -rn "fmt.Errorf\|return.*error\|die " --include="*.go" --include="*.sh" .` filtered to the user-facing layers.

- [ ] **Step 1: Outline structure**

```markdown
# Troubleshooting

## Setup-time problems

### `install.sh` aborts at step N
- Symptoms: …
- Likely cause: …
- Fix: …

### GitHub App manifest flow doesn't open browser
- …

### Bicep deploy: `LocationIsOfferRestricted`
- …

### Bicep deploy: `VaultAlreadyExists`
- …

## Run-time problems

### Run status: `failed`
…

### Run status: `partial_failure`
…

### Run never fires
…

### Webhook signature validation fails
…

### Copilot device-flow times out
…

### Container App is reachable but UI says "not connected"
…

## Operational problems

### How do I rotate the master key?
…

### How do I add a new operator?
…

### How do I upgrade the deployed image?
…
```

- [ ] **Step 2: Fill each section with the symptom-cause-fix table from existing docs and the dogfood punch list.**

- [ ] **Step 3: Cross-link from quickstart-copilot.md** ("If you hit a problem, see [troubleshooting](./troubleshooting.md)").

- [ ] **Step 4: Commit**

```bash
git commit -m "docs: add troubleshooting guide"
```

---

## Task 6: Write `docs/guides/schedule-authoring.md`

The full reference for `cronfoundry.yaml` and `SKILL.md`, with examples.

- [ ] **Step 1: Outline**

```markdown
# Authoring Schedules

## A minimal example
…

## The `cronfoundry.yaml` manifest
- `version`
- `skills[]`
  - `path`
  - `schedules[]`
    - `name`, `cron`, `timezone`
    - `overlap_policy`: skip | queue | concurrent
    - `timeout_sec`
    - `provider`, `model`, `copilot_prefix`
    - `auto_pause_after` (Phase 5a)
    - `destinations[]`: github-issue, slack, discord, teams, http, smtp
    - `writeback`: enabled, path, mode
    - `env`

## The `SKILL.md` per-skill prompt
- Frontmatter: name, description, max_tokens
- `{{ include "..." }}` directive
- `<memory>...</memory>` block

## Destination templates
- Variable table (`{{ output }}`, `{{ run.* }}`, `{{ schedule.* }}`, `{{ skill.* }}`)
- Per-destination fields

## Secret resolution
- `{ secret: name }` → `CRONFOUNDRY_SECRET_<UPPER(name)>`

## Examples
- Daily digest to Slack
- Weekly issue with writeback
- Multi-destination with email + GitHub
```

- [ ] **Step 2: Pull authoritative content from `internal/config/*.go` and `internal/template/*.go`** to ensure every field listed actually exists.

- [ ] **Step 3: Link from `docs/reference/manifest.md`** (the generated table) to this guide for prose.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs: schedule-authoring guide"
```

---

## Task 7: Write `docs/guides/operator-runbook.md`

The "first 24 hours" runbook for an operator who's just stood up CronFoundry.

- [ ] **Step 1: Outline**

```markdown
# Operator Runbook

## Day 0 — after install.sh

- Verify the dashboard is reachable
- Verify the first run completed
- Bookmark the audit log

## Reading the audit log

- What gets recorded
- How to filter by actor / action / target

## Identifying a failing schedule

- Runs page filtering
- Run-detail Sheet timeline
- Common failure causes

## Auto-pause and resume (Phase 5a)
…

## Manual run replay (Phase 5b)
…

## Token usage and cost (Phase 5c)
…

## Routine operations

### Rotate the master key
…

### Add a new operator
…

### Upgrade the deployed image
- Bump image tag in deploy/params*.json
- `az deployment sub create …` (idempotent)

### Back up Postgres
- Azure Postgres Flexible Server automated backups (default 7 days)
- Manual: `pg_dump`

### Restore Postgres
- From automated backup
- From manual dump
```

- [ ] **Step 2: Fill in each section.** Where a procedure isn't yet documented, capture it from code (e.g. master-key rotation: trace `internal/secrets/*.go`).

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: operator runbook"
```

---

## Task 8: Add screenshots to README and quickstarts

Use the screenshots saved in Task 12 of Phase 3.

- [ ] **Step 1: Copy screenshots to `docs/assets/`** (already done in Phase 3 if structured well).

- [ ] **Step 2: Embed in README** — after the opening paragraphs, before "Requirements":

```markdown
![CronFoundry Dashboard](docs/assets/dashboard.png)

A schedule fires, the runner streams the LLM completion, the result lands as
a GitHub issue and a Slack message, and a `<memory>` block commits back to
your skill repo.
```

- [ ] **Step 3: Embed in `quickstart-copilot.md`** — at the top of "Complete setup in the UI", show the polished onboarding card.

- [ ] **Step 4: Verify GitHub Pages renders the images** (relative paths must work in both raw GitHub view and the Pages site).

- [ ] **Step 5: Commit**

```bash
git commit -m "docs: add UI screenshots to README and quickstart"
```

---

## Self-review

- [ ] Every section in the spec §Phase 4 has a corresponding task above.
- [ ] All cross-links work: re-run the cross-link check from Task 1, Step 5; expect zero broken links.
- [ ] Generated reference files have a "Generated from source. Do not edit by hand." banner.
- [ ] Every new doc is reachable from the README.

---

## Handoff

Phase 4 deliverables are immediately useful and don't hand off into another phase. Once landed, the polish-pass spec is complete and the worktree branches can be merged into main.
