# Docs Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `scripts/quickstart-copilot.sh` one-liner deploy script, a companion guide `docs/guides/quickstart-copilot.md`, and refresh `docs/index.html` to reflect post-MVP features and surface the Copilot quick-start prominently.

**Architecture:** Three independent file changes — the shell script, the markdown guide, and the HTML landing page. No Go code changes. No tests required beyond manually verifying the script's prereq-check and param-building logic with `bash -n` (syntax) and a dry-run flag.

**Tech Stack:** Bash, GitHub Pages (static HTML/Tailwind CDN), Markdown.

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `scripts/quickstart-copilot.sh` | Create | Interactive 17-step Azure deploy script |
| `docs/install.sh` | Create (copy of script) | GitHub Pages-hosted URL for `curl \| bash` |
| `docs/guides/quickstart-copilot.md` | Create | Prose companion to the script |
| `docs/index.html` | Modify | Hero badge, features grid, quick-start CTA, roadmap pills |

---

### Task 1: Scaffold the script with prereq check and dry-run flag

**Files:**
- Create: `scripts/quickstart-copilot.sh`

- [ ] **Step 1: Create the script skeleton**

```bash
cat > scripts/quickstart-copilot.sh << 'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[info]${RESET}  $*"; }
ok()      { echo -e "${GREEN}[ok]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[warn]${RESET}  $*"; }
die()     { echo -e "${RED}[error]${RESET} $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}$*${RESET}"; }

DRY_RUN=false
for arg in "$@"; do [[ "$arg" == "--dry-run" ]] && DRY_RUN=true; done

STATE_FILE="${HOME}/.cronfoundry-quickstart-state"
[[ -f "$STATE_FILE" ]] && source "$STATE_FILE"

save() { echo "$1=\"$2\"" >> "$STATE_FILE"; }

GUIDE_URL="https://gambtho.github.io/cronfoundry/guides/quickstart-copilot.html"

# ── Step 1: prereq check ─────────────────────────────────────────────────────
header "[step 1/17] Checking prerequisites"

check_cmd() {
  local cmd=$1 hint=$2
  if ! command -v "$cmd" &>/dev/null; then
    die "'$cmd' not found. $hint\nSee §1 of $GUIDE_URL"
  fi
  ok "$cmd found"
}

check_cmd az      "Install: https://learn.microsoft.com/cli/azure/install-azure-cli"
check_cmd git     "Install: https://git-scm.com/downloads"
check_cmd python3 "Install: https://www.python.org/downloads/"
check_cmd openssl "Usually pre-installed. Install via your OS package manager."

# az version check (need ≥ 2.60)
AZ_VER=$(az version --query '"azure-cli"' -o tsv 2>/dev/null || echo "0.0.0")
AZ_MAJOR=$(echo "$AZ_VER" | cut -d. -f1)
AZ_MINOR=$(echo "$AZ_VER" | cut -d. -f2)
if [[ "$AZ_MAJOR" -lt 2 ]] || { [[ "$AZ_MAJOR" -eq 2 ]] && [[ "$AZ_MINOR" -lt 60 ]]; }; then
  die "az CLI $AZ_VER found; need ≥ 2.60. Run: az upgrade"
fi
ok "az $AZ_VER"

# bicep check (need ≥ 0.26)
if ! az bicep version &>/dev/null; then
  info "Bicep not found — installing via az bicep install..."
  az bicep install
fi
BICEP_VER=$(az bicep version 2>/dev/null | grep -oP '\d+\.\d+' | head -1 || echo "0.0")
BICEP_MINOR=$(echo "$BICEP_VER" | cut -d. -f2)
if [[ "${BICEP_MINOR:-0}" -lt 26 ]]; then
  warn "Bicep $BICEP_VER found; recommend ≥ 0.26. Run: az bicep upgrade"
fi
ok "bicep $BICEP_VER"

ok "All prerequisites satisfied"
SCRIPT
chmod +x scripts/quickstart-copilot.sh
```

- [ ] **Step 2: Verify script parses cleanly**

```bash
bash -n scripts/quickstart-copilot.sh && echo "syntax ok"
```

Expected output: `syntax ok`

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): scaffold script — prereq check and dry-run flag"
```

---

### Task 2: Add az login, subscription selection, and clone check (steps 2–4)

**Files:**
- Modify: `scripts/quickstart-copilot.sh`

- [ ] **Step 1: Append steps 2–4 to the script**

Add the following block at the end of `scripts/quickstart-copilot.sh` (before the final newline):

```bash
# ── Step 2: az login ──────────────────────────────────────────────────────────
header "[step 2/17] Azure login"
if az account show &>/dev/null; then
  CURRENT_ACCOUNT=$(az account show --query '[name, id]' -o tsv | tr '\t' ' / ')
  ok "Already logged in: $CURRENT_ACCOUNT"
else
  info "Running az login..."
  az login
fi

# ── Step 3: subscription ──────────────────────────────────────────────────────
header "[step 3/17] Select subscription"
if [[ -z "${CF_SUBSCRIPTION_ID:-}" ]]; then
  az account list --query '[].{Name:name, ID:id}' -o table
  read -rp "Enter subscription ID or name: " CF_SUBSCRIPTION_ID
  az account set --subscription "$CF_SUBSCRIPTION_ID"
  save CF_SUBSCRIPTION_ID "$CF_SUBSCRIPTION_ID"
fi
ok "Subscription: $CF_SUBSCRIPTION_ID"

# ── Step 4: clone check ───────────────────────────────────────────────────────
header "[step 4/17] Verify cronfoundry clone"
if [[ ! -f "deploy/main.bicep" ]]; then
  die "Run this script from inside a cronfoundry clone.\n  git clone https://github.com/gambtho/cronfoundry && cd cronfoundry\nSee §4 of $GUIDE_URL"
fi
REPO_ROOT=$(git rev-parse --show-toplevel)
ok "Repo root: $REPO_ROOT"
```

- [ ] **Step 2: Verify syntax**

```bash
bash -n scripts/quickstart-copilot.sh && echo "syntax ok"
```

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): add az login, subscription, clone-check steps"
```

---

### Task 3: GitHub App prompts (step 5)

**Files:**
- Modify: `scripts/quickstart-copilot.sh`

- [ ] **Step 1: Append step 5**

```bash
# ── Step 5: GitHub App ────────────────────────────────────────────────────────
header "[step 5/17] GitHub App setup"
echo ""
echo "  CronFoundry uses a GitHub App (not an OAuth App) for repo access."
echo "  You need to create one if you haven't already."
echo ""
echo "  1. Open: https://github.com/settings/apps/new"
echo "     (Check the URL ends in /settings/apps/new — not /applications/new)"
echo "  2. Name: anything globally unique, e.g. cronfoundry-$(whoami)"
echo "  3. Homepage URL: https://example.com  (placeholder — you'll update after deploy)"
echo "  4. Callback URL: https://example.com/oauth/callback"
echo "  5. Webhook URL: https://example.com/webhook/github"
echo "     Webhook secret: generate with: openssl rand -hex 32"
echo "  6. Permissions → Repository: Contents (R+W), Issues (W), Metadata (R)"
echo "     Account: Email (R)"
echo "  7. Subscribe to events: Push"
echo "  8. Save, then note the App ID, generate a Client Secret, download the .pem"
echo "  9. Install App on your skill repo and reports repo"
echo ""

if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  read -rp "GitHub App ID (numeric): " CF_GITHUB_APP_ID
  save CF_GITHUB_APP_ID "$CF_GITHUB_APP_ID"
fi
if [[ -z "${CF_GITHUB_CLIENT_ID:-}" ]]; then
  read -rp "GitHub App Client ID (starts with Iv23li): " CF_GITHUB_CLIENT_ID
  save CF_GITHUB_CLIENT_ID "$CF_GITHUB_CLIENT_ID"
fi
if [[ -z "${CF_GITHUB_CLIENT_SECRET:-}" ]]; then
  read -rsp "GitHub App Client Secret: " CF_GITHUB_CLIENT_SECRET; echo
  save CF_GITHUB_CLIENT_SECRET "$CF_GITHUB_CLIENT_SECRET"
fi
if [[ -z "${CF_GITHUB_PEM_PATH:-}" ]]; then
  read -rp "Path to GitHub App .pem file: " CF_GITHUB_PEM_PATH
  [[ -f "$CF_GITHUB_PEM_PATH" ]] || die "File not found: $CF_GITHUB_PEM_PATH"
  save CF_GITHUB_PEM_PATH "$CF_GITHUB_PEM_PATH"
fi
ok "GitHub App credentials collected"
```

- [ ] **Step 2: Verify syntax**

```bash
bash -n scripts/quickstart-copilot.sh && echo "syntax ok"
```

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): add GitHub App prompt step"
```

---

### Task 4: Repo prompts, key generation, region/suffix/tag (steps 6–12)

**Files:**
- Modify: `scripts/quickstart-copilot.sh`

- [ ] **Step 1: Append steps 6–12**

```bash
# ── Step 6: skill repo ────────────────────────────────────────────────────────
header "[step 6/17] Skill repo"
if [[ -z "${CF_SKILL_REPO:-}" ]]; then
  read -rp "Skill repo (owner/repo, e.g. acme/cronfoundry-skills): " CF_SKILL_REPO
  save CF_SKILL_REPO "$CF_SKILL_REPO"
fi
if [[ -z "${CF_INSTALLATION_ID:-}" ]]; then
  read -rp "GitHub App Installation ID (number from the install URL): " CF_INSTALLATION_ID
  save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
fi
ok "Skill repo: $CF_SKILL_REPO (installation $CF_INSTALLATION_ID)"

# ── Step 7: reports repo ──────────────────────────────────────────────────────
header "[step 7/17] Reports repo"
if [[ -z "${CF_REPORTS_REPO:-}" ]]; then
  read -rp "Reports repo (owner/repo, e.g. acme/cronfoundry-reports): " CF_REPORTS_REPO
  save CF_REPORTS_REPO "$CF_REPORTS_REPO"
fi
ok "Reports repo: $CF_REPORTS_REPO"

# ── Step 8: master key ────────────────────────────────────────────────────────
header "[step 8/17] Generate master key"
if [[ -z "${CF_MASTER_KEY:-}" ]]; then
  CF_MASTER_KEY=$(openssl rand -base64 32)
  save CF_MASTER_KEY "$CF_MASTER_KEY"
  warn "SAVE THIS KEY — if lost, encrypted secrets are unrecoverable."
  echo "  Master key: $CF_MASTER_KEY"
fi
ok "Master key ready"

# ── Step 9: env suffix ────────────────────────────────────────────────────────
header "[step 9/17] Environment suffix"
if [[ -z "${CF_ENV:-}" ]]; then
  read -rp "Env suffix (≤10 chars, default: copilot1): " CF_ENV
  CF_ENV="${CF_ENV:-copilot1}"
  save CF_ENV "$CF_ENV"
fi
warn "Key Vault soft-delete retains the name 'cf-kv-${CF_ENV}' for 7 days after teardown."
warn "Re-runs after teardown need a new suffix (e.g. copilot2)."
ok "Env: $CF_ENV"

# ── Step 10: region ───────────────────────────────────────────────────────────
header "[step 10/17] Region"
if [[ -z "${CF_REGION:-}" ]]; then
  read -rp "Azure region (default: swedencentral): " CF_REGION
  CF_REGION="${CF_REGION:-swedencentral}"
  save CF_REGION "$CF_REGION"
fi
info "Note: Postgres Flexible Server offer restrictions vary by subscription."
info "swedencentral is known-good for Microsoft-internal subs. See §10 of $GUIDE_URL"
ok "Region: $CF_REGION"

# ── Step 11: image tag ────────────────────────────────────────────────────────
header "[step 11/17] Image tag"
if [[ -z "${CF_IMAGE_TAG:-}" ]]; then
  CF_IMAGE_TAG=$(curl -fsSL "https://api.github.com/repos/gambtho/cronfoundry/releases/latest" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'].lstrip('v'))" 2>/dev/null || echo "latest")
  save CF_IMAGE_TAG "$CF_IMAGE_TAG"
fi
ok "Image tag: $CF_IMAGE_TAG"

# ── Step 12: postgres password ────────────────────────────────────────────────
header "[step 12/17] Generate Postgres password"
if [[ -z "${CF_PG_PASSWORD:-}" ]]; then
  CF_PG_PASSWORD=$(openssl rand -base64 18 | tr -dc 'A-Za-z0-9' | head -c24)
  save CF_PG_PASSWORD "$CF_PG_PASSWORD"
fi
ok "Postgres password generated (saved to state file)"
```

- [ ] **Step 2: Verify syntax**

```bash
bash -n scripts/quickstart-copilot.sh && echo "syntax ok"
```

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): add repo prompts and key/tag/region generation steps"
```

---

### Task 5: Params file, deploy, admin init, FQDN, UI checklist (steps 13–17)

**Files:**
- Modify: `scripts/quickstart-copilot.sh`

- [ ] **Step 1: Append steps 13–17**

```bash
# ── Step 13: build params file ────────────────────────────────────────────────
header "[step 13/17] Build params file"
PARAMS_FILE="deploy/params.quickstart-${CF_ENV}.json"
if [[ ! -f "$PARAMS_FILE" ]]; then
  python3 - <<PYEOF
import json, sys

params = {
  "\$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env":                        {"value": "${CF_ENV}"},
    "location":                   {"value": "${CF_REGION}"},
    "imageTag":                   {"value": "${CF_IMAGE_TAG}"},
    "githubAppId":                {"value": "${CF_GITHUB_APP_ID}"},
    "githubAppOAuthClientId":     {"value": "${CF_GITHUB_CLIENT_ID}"},
    "githubAppOAuthClientSecret": {"value": "${CF_GITHUB_CLIENT_SECRET}"},
    "postgresAdminPassword":      {"value": "${CF_PG_PASSWORD}"},
    "masterKey":                  {"value": "${CF_MASTER_KEY}"},
    "adminLogins":                {"value": "$(git config user.name || echo admin)"},
    "viewerLogins":               {"value": ""},
    "ingressExternal":            {"value": True},
    "githubAppPem":               {"value": ""},
  }
}
with open("${CF_GITHUB_PEM_PATH}") as pf:
    params["parameters"]["githubAppPem"]["value"] = pf.read()
with open("${PARAMS_FILE}", "w") as f:
    json.dump(params, f, indent=2)
print("Wrote ${PARAMS_FILE}")
PYEOF
fi
ok "Params file: $PARAMS_FILE"

# ── Step 14: deploy ───────────────────────────────────────────────────────────
header "[step 14/17] Deploy to Azure (~10 min)"
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping az deployment sub create"
else
  az deployment sub create \
    --location "$CF_REGION" \
    --template-file deploy/main.bicep \
    --parameters "@$PARAMS_FILE"
fi

CF_FQDN=$(az containerapp show \
  --resource-group "rg-cronfoundry-${CF_ENV}" \
  --name "cf-serve-${CF_ENV}" \
  --query properties.configuration.ingress.fqdn -o tsv 2>/dev/null || echo "")
save CF_FQDN "$CF_FQDN"
ok "Deployed. FQDN: $CF_FQDN"

# ── Step 15: admin init ───────────────────────────────────────────────────────
header "[step 15/17] Initialize database"
if [[ "$DRY_RUN" != "true" ]]; then
  # WSL2-safe: use broad rule (0.0.0.0–255.255.255.255) — WSL2 NAT may present
  # a different source IP to Azure than what ifconfig.me reports.
  az postgres flexible-server firewall-rule create \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-pg-${CF_ENV}" \
    --rule-name AllowOperator \
    --start-ip-address "0.0.0.0" \
    --end-ip-address "255.255.255.255" 2>/dev/null || true

  CF_DB_URL="postgres://cfadmin:${CF_PG_PASSWORD}@cf-pg-${CF_ENV}.postgres.database.azure.com:5432/cronfoundry?sslmode=require"

  make build 2>/dev/null || go build -o cronfoundry ./cmd/cronfoundry

  CRONFOUNDRY_DATABASE_URL="$CF_DB_URL" \
  CRONFOUNDRY_MASTER_KEY="$CF_MASTER_KEY" \
  ./cronfoundry admin init

  az containerapp update \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-serve-${CF_ENV}" \
    --set-env-vars "RESTART_TRIGGER=$(date +%s)" >/dev/null

  info "Waiting for Container App to become healthy..."
  for i in $(seq 1 12); do
    HEALTH=$(az containerapp revision list \
      --resource-group "rg-cronfoundry-${CF_ENV}" \
      --name "cf-serve-${CF_ENV}" \
      --query '[?properties.trafficWeight>`0`].properties.healthState' \
      -o tsv 2>/dev/null | head -1 || echo "unknown")
    [[ "$HEALTH" == "Healthy" ]] && break
    sleep 10
  done
  ok "Container App health: $HEALTH"
fi

# ── Step 16: update GitHub App URLs ──────────────────────────────────────────
header "[step 16/17] Update GitHub App URLs"
echo ""
echo "  Go to your GitHub App settings and update these URLs to:"
echo "  https://${CF_FQDN}"
echo ""
echo "  Homepage URL:  https://${CF_FQDN}"
echo "  Callback URL:  https://${CF_FQDN}/oauth/callback"
echo "  Webhook URL:   https://${CF_FQDN}/webhook/github"
echo ""
read -rp "Press Enter once you've updated the GitHub App URLs..."

# ── Step 17: UI checklist ─────────────────────────────────────────────────────
header "[step 17/17] Complete setup in the web UI"
echo ""
echo "  Open: https://${CF_FQDN}/"
echo ""
echo "  a) Log in via GitHub"
echo "  b) Providers → GitHub Copilot Enterprise → Connect"
echo "     Enter a prefix (e.g. 'copilot'), open the verification URL,"
echo "     enter the code shown, and authorize in your browser."
echo "  c) Repos → Connect repo → paste '${CF_SKILL_REPO}' and installation ID '${CF_INSTALLATION_ID}'"
echo "  d) Secrets → Add 'github_webhook_secret' (the value from your GitHub App webhook config)"
echo "  e) Push a cronfoundry.yaml to your skill repo using:"
echo "       provider: copilot-enterprise"
echo "       copilot_prefix: <prefix from step b>"
echo ""
echo "  Full guide: $GUIDE_URL"
echo ""

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  CronFoundry deployed successfully!"
echo "  URL:         https://${CF_FQDN}/"
echo "  State file:  $STATE_FILE"
echo "  Guide:       $GUIDE_URL"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
```

- [ ] **Step 2: Verify syntax**

```bash
bash -n scripts/quickstart-copilot.sh && echo "syntax ok"
```

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): add params build, deploy, admin init, and UI checklist steps"
```

---

### Task 6: Copy script to docs/install.sh

**Files:**
- Create: `docs/install.sh`

- [ ] **Step 1: Copy script**

```bash
cp scripts/quickstart-copilot.sh docs/install.sh
```

- [ ] **Step 2: Verify the file exists and parses**

```bash
bash -n docs/install.sh && echo "syntax ok"
```

- [ ] **Step 3: Commit**

```bash
git add docs/install.sh
git commit -m "feat(quickstart): publish script to docs/install.sh for GitHub Pages"
```

---

### Task 7: Write the companion guide

**Files:**
- Create: `docs/guides/quickstart-copilot.md`

- [ ] **Step 1: Create the guide**

```bash
cat > docs/guides/quickstart-copilot.md << 'EOF'
# Quick Start — CronFoundry with GitHub Copilot Enterprise

Deploy CronFoundry to Azure in one session using GitHub Copilot Enterprise as
the LLM provider. No external API key required — just a GitHub Copilot
Enterprise seat.

```bash
bash <(curl -fsSL https://gambtho.github.io/cronfoundry/install.sh)
```

The script automates steps 1–16. This guide documents every step so you can
understand what it does, run steps manually if the script fails, or adapt the
process for a different environment.

---

## Prerequisites (§1)

The script checks these automatically and exits with a hint if any are missing.

| Tool | Min version | Install |
|------|-------------|---------|
| `az` CLI | 2.60 | https://learn.microsoft.com/cli/azure/install-azure-cli |
| Bicep | 0.26 | `az bicep install` |
| `git` | any | https://git-scm.com/downloads |
| `python3` | 3.8+ | pre-installed on most systems |
| `openssl` | any | pre-installed on most systems |

You also need:
- An Azure subscription with Contributor rights
- A GitHub account that can register a GitHub App
- A GitHub Copilot Enterprise seat
- Two GitHub repos under the same owner: one **skill repo** (will hold `cronfoundry.yaml`) and one **reports repo** (where `github-issue` destinations will file issues)
- A local clone of this repo: `git clone https://github.com/gambtho/cronfoundry && cd cronfoundry`

### WSL2 note

If you run this from WSL2, the operator IP reported by `curl ifconfig.me` may
differ from the source IP Azure sees due to NAT. The script uses a broad
Postgres firewall rule (`0.0.0.0–255.255.255.255`) to work around this. This
is safe for a fresh deployment where the database isn't yet in production use;
tighten the rule after setup if needed.

---

## §2 — Azure login

```bash
az login
az account set --subscription <subscription-id>
```

The script skips this if you're already logged in.

---

## §3 — Subscription

```bash
az account list --query '[].{Name:name, ID:id}' -o table
```

Pick the subscription ID you want to deploy into.

---

## §4 — Clone check

The script must be run from inside the cronfoundry repo root (it references
`deploy/main.bicep`). Clone and `cd` if needed:

```bash
git clone https://github.com/gambtho/cronfoundry
cd cronfoundry
```

---

## §5 — Register a GitHub App

> Register a **GitHub App**, not an **OAuth App**. Both live under
> *Settings → Developer settings*. GitHub Apps have an App ID and a private
> key; OAuth Apps don't. The URL must end in `/settings/apps/new`.

1. Open: https://github.com/settings/apps/new
2. **Name:** globally unique, e.g. `cronfoundry-yourname`
3. **Homepage URL:** `https://example.com` (placeholder — update after deploy)
4. **Callback URL:** `https://example.com/oauth/callback`
5. **Webhook URL:** `https://example.com/webhook/github`
   **Webhook secret:** `openssl rand -hex 32` — save the value
6. **Permissions → Repository:** Contents (R+W), Issues (W), Metadata (R)
   **Account:** Email (R)
7. **Subscribe to events:** Push
8. Save. Note the **App ID**. Generate a **Client Secret** (shown once). Download the **.pem**.
9. **Install App** on your skill repo and reports repo.

The script pauses here and prompts for App ID, Client ID, Client Secret, and PEM path.

---

## §6 — Skill repo

Paste `owner/repo` for your skill repo and the installation ID (the number
at the end of the install URL: `github.com/settings/installations/<id>`).

---

## §7 — Reports repo

Paste `owner/repo` for your reports repo. GitHub issues will be filed here.

---

## §8 — Master key

The script generates a master key with:

```bash
openssl rand -base64 32
```

This key envelope-encrypts all secrets in the database. **Save it.** If lost,
encrypted secrets are unrecoverable.

---

## §9 — Environment suffix

Pick a short suffix (≤ 10 chars, e.g. `copilot1`). Every Azure resource name
includes this suffix. Azure Key Vault uses soft-delete with a 7-day retention,
so if you tear down and re-deploy, use a different suffix (e.g. `copilot2`) to
avoid a name collision on the vault.

---

## §10 — Region

Default: `swedencentral`. This region is known-good for Microsoft-internal
subscriptions.

Postgres Flexible Server offer restrictions vary by subscription and region.
The only reliable probe is a synchronous `az postgres flexible-server create`
— `--no-wait` returns exit 0 even when provisioning later fails with
`LocationIsOfferRestricted`. If your preferred region fails, try
`swedencentral`.

---

## §11 — Image tag

The script queries the GitHub API for the latest release tag and strips the
`v` prefix (e.g. `v0.7.6` → `0.7.6`). Falls back to `latest` if the API is
unreachable.

---

## §12 — Postgres password

Generated as a 24-character alphanumeric string. Avoid special characters —
the password ends up in a connection string URL.

---

## §13 — Build params file

The script writes `deploy/params.quickstart-<env>.json` using Python to
correctly embed the multi-line PEM file. Equivalent manual command:

```bash
python3 -c "
import json
with open('deploy/params.quickstart-copilot1.json') as f: d = json.load(f)
with open('/path/to/app.private-key.pem') as p: d['parameters']['githubAppPem'] = {'value': p.read()}
with open('deploy/params.quickstart-copilot1.json','w') as f: json.dump(d, f, indent=2)
"
```

---

## §14 — Deploy (~10 min)

```bash
az deployment sub create \
  --location swedencentral \
  --template-file deploy/main.bicep \
  --parameters @deploy/params.quickstart-copilot1.json
```

Creates: Key Vault, Postgres Flexible Server, Container Apps Environment, serve
Container App, runner Container Apps Job, managed identities, RBAC assignments.

The serve Container App will crash-loop until `admin init` runs in §15.

---

## §15 — Initialize database

```bash
# Add operator IP to Postgres firewall
az postgres flexible-server firewall-rule create \
  --resource-group rg-cronfoundry-copilot1 \
  --name cf-pg-copilot1 \
  --rule-name AllowOperator \
  --start-ip-address 0.0.0.0 --end-ip-address 255.255.255.255

# Run migrations and seed the default org
CRONFOUNDRY_DATABASE_URL="postgres://cfadmin:<password>@cf-pg-copilot1.postgres.database.azure.com:5432/cronfoundry?sslmode=require" \
CRONFOUNDRY_MASTER_KEY="<master-key>" \
./cronfoundry admin init

# Force a new revision to pick up the migrated schema
az containerapp update \
  --resource-group rg-cronfoundry-copilot1 \
  --name cf-serve-copilot1 \
  --set-env-vars "RESTART_TRIGGER=$(date +%s)"
```

---

## §16 — Update GitHub App URLs

Get the FQDN:

```bash
az containerapp show \
  --resource-group rg-cronfoundry-copilot1 \
  --name cf-serve-copilot1 \
  --query properties.configuration.ingress.fqdn -o tsv
```

In the GitHub App settings, update:
- **Homepage URL:** `https://<fqdn>`
- **Callback URL:** `https://<fqdn>/oauth/callback`
- **Webhook URL:** `https://<fqdn>/webhook/github`

---

## §17 — Complete setup in the UI

1. Open `https://<fqdn>/` and log in via GitHub.
2. **Providers → GitHub Copilot Enterprise → Connect.** Enter a prefix (e.g. `copilot`), open the verification URL, enter the user code, and authorize in your browser.
3. **Repos → Connect repo.** Paste `owner/skill-repo` and your installation ID.
4. **Secrets → Add** `github_webhook_secret` (the value from your GitHub App webhook config).
5. Push a `cronfoundry.yaml` to your skill repo:

```yaml
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: every-5
        cron: "*/5 * * * *"
        timezone: UTC
        provider: copilot-enterprise
        copilot_prefix: copilot       # matches the prefix from step 2 above
        model: gpt-4o
        destinations:
          - github-issue:
              repo: owner/reports-repo
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

And `skills/smoke/SKILL.md`:

```markdown
---
name: smoke
description: Proves the pipeline end to end
max_tokens: 200
---
Write one short sentence confirming this pipeline works.
End with:
<memory>
run at {{ run.started_at }}
</memory>
```

Push and watch the **Dashboard** — the schedule appears, fires within 5 minutes,
and a GitHub issue is filed in your reports repo.

---

## Teardown

```bash
az group delete --name rg-cronfoundry-copilot1 --yes --no-wait
```

Revoke the GitHub App installation and delete the App registration once the
resource group is deleted. The state file at `~/.cronfoundry-quickstart-state`
can be removed with `rm ~/.cronfoundry-quickstart-state`.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `LocationIsOfferRestricted` on Postgres | Region offer restriction | Change `CF_REGION` in state file and re-run |
| `VaultAlreadyExists` | KV soft-delete name collision | Change `CF_ENV` in state file (e.g. `copilot2`) |
| Container App crash-loops after deploy | `admin init` not run yet | Normal — complete §15 |
| `dial error: timeout` connecting to Postgres | Firewall rule missing | Re-run §15 firewall step |
| Runs show `partial_failure` on github-issue | GitHub token not set up | Complete §17 steps b–d |
| Model 404 error | Provider-prefixed model name | Use bare model ID (e.g. `gpt-4o` not `copilot-enterprise/gpt-4o`) |
EOF
```

- [ ] **Step 2: Verify file was created**

```bash
wc -l docs/guides/quickstart-copilot.md
```

Expected: > 150 lines

- [ ] **Step 3: Commit**

```bash
git add docs/guides/quickstart-copilot.md
git commit -m "docs: add Copilot Enterprise quick-start guide"
```

---

### Task 8: Refresh docs/index.html

**Files:**
- Modify: `docs/index.html`

Four targeted edits — no layout changes.

- [ ] **Step 1: Update hero badge**

Find:
```html
        MVP shipped &#x2713; &mdash; deployable to Azure today
```
Replace with:
```html
        v0.7.6 &middot; 4 LLM providers &middot; deployable to Azure today
```

- [ ] **Step 2: Replace the quick-start CTA block**

Find (the entire CTA div + paragraph below it):
```html
      <div class="flex flex-wrap items-center justify-center gap-4">
        <a href="#quickstart" class="rounded-lg bg-brand px-6 py-3 text-sm font-semibold text-white hover:bg-indigo-500 transition-colors">
          Try it in 5 minutes &rarr;
        </a>
        <a href="https://github.com/gambtho/cronfoundry" class="rounded-lg border border-white/20 px-6 py-3 text-sm font-semibold text-gray-300 hover:border-white/40 hover:text-white transition-colors">
          View on GitHub
        </a>
      </div>
      <p class="mt-4 mb-16 text-sm text-gray-400">
        Self-host on Azure in one afternoon &mdash;
        <a href="https://github.com/gambtho/cronfoundry/blob/main/docs/guides/smoke-test-mvp-azure.md"
           class="text-brand hover:underline">follow the runbook &rarr;</a>
      </p>
```

Replace with:
```html
      <div class="mx-auto max-w-xl rounded-xl border border-white/10 bg-gray-900 p-4 font-mono text-sm text-gray-300 text-left mb-4">
        bash &lt;(curl -fsSL https://gambtho.github.io/cronfoundry/install.sh)
      </div>
      <p class="text-sm text-gray-400 mb-6">
        No API key needed &mdash; uses GitHub Copilot Enterprise. &nbsp;
        <a href="https://github.com/gambtho/cronfoundry/blob/main/docs/guides/quickstart-copilot.md"
           class="text-brand hover:underline">Full guide &rarr;</a>
      </p>
      <div class="flex flex-wrap items-center justify-center gap-4 mb-16">
        <a href="https://github.com/gambtho/cronfoundry/blob/main/docs/guides/quickstart-copilot.md" class="rounded-lg bg-brand px-6 py-3 text-sm font-semibold text-white hover:bg-indigo-500 transition-colors">
          Deploy in one afternoon &rarr;
        </a>
        <a href="https://github.com/gambtho/cronfoundry" class="rounded-lg border border-white/20 px-6 py-3 text-sm font-semibold text-gray-300 hover:border-white/40 hover:text-white transition-colors">
          View on GitHub
        </a>
      </div>
```

- [ ] **Step 3: Update LLM providers cell in comparison table**

Find:
```html
              <td class="px-4 py-3 text-white">OpenAI, Anthropic, Azure AI</td>
```
Replace with:
```html
              <td class="px-4 py-3 text-white">OpenAI, Anthropic, Azure AI, Copilot Enterprise</td>
```

- [ ] **Step 4: Update quick-start section (replace the old manual 3-step block)**

Find:
```html
        <h2 class="text-center text-3xl font-bold text-white mb-4">Up and running in one afternoon</h2>
        <p class="text-center text-gray-400 mb-16 max-w-xl mx-auto">No cluster upkeep. No certificate rotation. One binary, one command.</p>
```
Replace with:
```html
        <h2 class="text-center text-3xl font-bold text-white mb-4">Up and running in one afternoon</h2>
        <p class="text-center text-gray-400 mb-16 max-w-xl mx-auto">One script deploys to Azure. No external API key needed &mdash; uses your GitHub Copilot Enterprise seat.</p>
```

- [ ] **Step 5: Replace roadmap pills section — remove "coming soon" items that have shipped**

Find:
```html
        <div class="flex flex-wrap justify-center gap-2">
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">MCP tool support</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">GitHub Copilot Enterprise</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Auto-pause on failure</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Helm / AKS</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Multi-cloud</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Hosted SaaS</span>
        </div>
```
Replace with:
```html
        <div class="flex flex-wrap justify-center gap-2 mb-4">
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">MCP tool support &#x2713;</span>
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">Copilot Enterprise &#x2713;</span>
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">Auto-pause &#x2713;</span>
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">Conditional routing &#x2713;</span>
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">AKS + Fly.io &#x2713;</span>
          <span class="rounded-full border border-accent/40 bg-accent/5 px-3 py-1 text-xs text-accent">Custom HTTP / SMTP &#x2713;</span>
        </div>
        <div class="flex flex-wrap justify-center gap-2">
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Hosted SaaS</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">SSO / Entra</span>
          <span class="rounded-full border border-white/10 bg-gray-900 px-3 py-1 text-xs text-gray-400">Image signing / SBOM</span>
        </div>
```

- [ ] **Step 6: Verify the HTML parses (no obvious syntax error)**

```bash
python3 -c "
from html.parser import HTMLParser
class Check(HTMLParser):
    pass
with open('docs/index.html') as f:
    Check().feed(f.read())
print('html ok')
"
```

Expected: `html ok`

- [ ] **Step 7: Commit**

```bash
git add docs/index.html
git commit -m "docs(site): refresh hero badge, Copilot quick-start CTA, features, and roadmap pills"
```

---

### Task 9: Final check and push

- [ ] **Step 1: Verify all three deliverables exist**

```bash
test -f scripts/quickstart-copilot.sh && echo "script ok"
test -f docs/install.sh && echo "install.sh ok"
test -f docs/guides/quickstart-copilot.md && echo "guide ok"
bash -n docs/install.sh && echo "script syntax ok"
python3 -c "
from html.parser import HTMLParser
class C(HTMLParser): pass
with open('docs/index.html') as f: C().feed(f.read())
print('html ok')
"
```

Expected: four lines all ending in `ok`.

- [ ] **Step 2: Confirm git log looks clean**

```bash
git log --oneline -8
```

Expected: 8 commits, all from this branch.

- [ ] **Step 3: Push branch**

```bash
git push -u origin docs/docs-refresh
```
