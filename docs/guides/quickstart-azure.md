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
