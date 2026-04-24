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
