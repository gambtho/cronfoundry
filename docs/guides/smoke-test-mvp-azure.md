# Smoke Test — MVP on Azure

End-to-end verification that a fresh CronFoundry deployment on Azure can fire
a scheduled skill, publish to Slack + GitHub, commit a memory update back to
the skill repo, and write audit rows for every mutating action.

Executed once by the author on a fresh subscription. Any step that fails
becomes a fix (code or doc), and the runbook is re-run. The committed version
is the one that worked end to end.

**Depends on:** P6 merged (`docs/superpowers/plans/2026-04-20-p6-mvp-gaps.md`)
for the audit-verification and push-webhook steps.

---

## 1. Prerequisites

- An Azure subscription with Contributor rights.
- `az` CLI ≥ 2.60 and Bicep CLI ≥ 0.26. Verify: `az --version`.
- A GitHub account that can register a new GitHub App.
- **One** LLM key from OpenAI, Anthropic, or Azure AI Foundry.
- A Slack **Incoming Webhook URL** for any channel. Create one at
  https://api.slack.com/apps.
- Two GitHub repos under the same owner:
  1. A **skill repo** containing `cronfoundry.yaml` + one `SKILL.md`. You can
     copy `testdata/` from this repo into a new private repo to get going.
  2. A **reports repo** where the `github-issue` destination will file.
- Local clone of this repo for the admin CLI.

## 2. Register the GitHub App

1. GitHub → Settings → Developer settings → GitHub Apps → **New GitHub App**.
2. **Homepage URL:** anything (e.g., `https://<your-api-hostname>`). You'll fill
   the real hostname after the Bicep deploy in step 3.
3. **Callback URL:** `https://<your-api-hostname>/oauth/callback`
4. **Webhook URL:** `https://<your-api-hostname>/webhook/github`  
   **Webhook secret:** generate a long random string; save for step 4.
5. **Permissions:**
   - Repository: Contents (R+W), Issues (W), Metadata (R).
   - Account: Email (R).
6. **Subscribe to events:** Push.
7. Save. Generate + download the **private key** (`.pem`). Note the **App ID**
   and **Client ID / Client Secret**.
8. **Install the App** on your two repos (skill + reports).

You'll come back after step 3 to update the three URLs with the real hostname.

## 3. Deploy via Bicep

```bash
cp deploy/params.example.json deploy/params.p7smoke.json
# Edit deploy/params.p7smoke.json — set:
#   envName:           "p7smoke"
#   location:          "eastus" (or your region)
#   adminLogins:       ["<your-github-login>"]
#   containerImageTag: "latest" (or a specific release tag)

az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters @deploy/params.p7smoke.json
```

After ~10 minutes, the deployment completes. Grab the API hostname:

```bash
az containerapp show \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --query properties.configuration.ingress.fqdn -o tsv
```

Go back to the GitHub App settings and replace `<your-api-hostname>` in
Homepage URL, Callback URL, and Webhook URL with this FQDN. Save.

## 4. First-boot config (admin CLI)

All of the following run from a local shell with the master key exported
(see README § Quick start; the Bicep deploy prints it once at the end).

```bash
export CRONFOUNDRY_MASTER_KEY='<from deploy output>'
export CRONFOUNDRY_DATABASE_URL='<from deploy output>'

# Connect the skill repo. Replace with your install ID and coords.
./cronfoundry admin connect-repo <owner>/<skill-repo> \
  --installation-id <from GitHub App installation page>

# Set three secrets.
echo -n '<openai/anthropic/azure key>'  | ./cronfoundry admin set-secret llm_key
echo -n '<slack webhook URL>'           | ./cronfoundry admin set-secret slack_webhook
echo -n '<github webhook secret>'       | ./cronfoundry admin set-secret github_webhook_secret
```

Also set the webhook secret as an env var on the API Container App (the
webhook endpoint reads it from `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`):

```bash
az containerapp update \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --set-env-vars CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<same value>
```

## 5. Land a skill

In your skill repo's `cronfoundry.yaml`, define one schedule firing every 5
minutes:

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
        provider: openai        # or anthropic / azure-foundry
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

And `skills/smoke/SKILL.md`:

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

Commit and push. The push webhook re-syncs the schedule within seconds.

## 6. Observe the first fire

1. Open `https://<fqdn>/`. Log in via GitHub. (You should already be
   allowlisted from the bootstrap admin list.)
2. Dashboard shows the new `every-5` schedule.
3. Wait up to 5 minutes for the first natural fire — or click **Run now**.
4. Go to **Runs**, click the newest row.
5. Confirm the **log panel streams** with row levels `info/warn/error` and
   event types (`llm.start`, `llm.chunk.batched`, `publish.slack.ok`,
   `publish.github-issue.ok`, `writeback.commit.ok`). Status transitions to
   `succeeded`.

## 7. Verify the three side effects

- **Slack:** message lands in the configured channel with the skill output.
- **GitHub issue:** a new issue exists in the reports repo, titled
  `smoke — YYYY-MM-DD`, labeled `smoke`.
- **Writeback commit:** the skill repo's `memory.md` has a new line; commit
  author is `cronfoundry[bot]` with message
  `chore(cronfoundry): update memory.md from run <uuid>`.

## 8. Verify the audit log

Navigate to **Audit** in the sidebar (shipped in P6c). Confirm rows are
present for the session you just walked through:

| Action | Target |
|---|---|
| `auth.login` | your github login |
| `repo.connect` | `<owner>/<skill-repo>` |
| `secret.create` | `llm_key` |
| `secret.create` | `slack_webhook` |
| `secret.create` | `github_webhook_secret` |
| `schedule.run_now` | `every-5` (only if you clicked **Run now**) |

If any row is missing, file it as a P6c fix and do not mark the smoke passed.

## 9. Teardown

```bash
az group delete --name rg-cronfoundry-p7smoke --yes --no-wait
```

Revoke the GitHub App installation and delete the App registration once the
resource group is gone.

---

## Pass/fail checklist

- [ ] One `succeeded` run visible in the dashboard.
- [ ] Slack message present.
- [ ] GitHub issue filed in the reports repo.
- [ ] `memory.md` commit authored by `cronfoundry[bot]`.
- [ ] Audit log contains login + repo-connect + secret-create rows.

If every box is checked, MVP is shipped. Otherwise, every unchecked box
becomes a fix in code or docs and the runbook re-runs.
