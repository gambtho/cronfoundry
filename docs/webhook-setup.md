# GitHub push-webhook setup

CronFoundry polls connected repos on a schedule, but a GitHub App push webhook
lets it resync immediately on every push to the default branch. This guide
wires that webhook up.

## Prerequisites

- CronFoundry deployed and reachable on a public HTTPS URL.
- A GitHub App registered for the org whose repos you want to connect.
- At least one `repo_connection` row for that org (created via
  `cronfoundry admin connect-repo` or the operator UI).

## Step 1: Configure the webhook in your GitHub App

In your GitHub App's settings page, fill in the **Webhook** section:

- **Webhook URL**: `https://<cronfoundry-host>/webhook/github`
- **Webhook secret**: a long random string. Generate one with:

  ```bash
  openssl rand -hex 32
  ```

- **Content type**: `application/json` (not `application/x-www-form-urlencoded`).
- Under **Subscribe to events**, enable `Push`.

Save the App. Keep the secret handy for the next step.

## Step 2: Plumb the shared secret into CronFoundry

Set `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET` in the deployment environment to the
same string you pasted into the GitHub App:

```bash
CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<same-string-from-step-1>
```

If the env var is unset, `POST /webhook/github` returns `503 Service
Unavailable`. A quick curl against the endpoint before and after setting the
var is an easy way to confirm the value is actually in the running process's
environment.

## Step 3: Verify with a ping

In your GitHub App's **Advanced > Recent Deliveries** page, find the `ping`
event GitHub sent when you saved the webhook config and click **Redeliver**.
The handler returns `200 OK`. A 200 response confirms the URL is reachable;
GitHub does sign ping deliveries, and when `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`
is set the handler short-circuits before signature verification, so a
successful ping alone does not prove the secret is correct. For end-to-end
verification (URL + secret + sync), trigger a real push (next step).

## Step 4: Trigger a real push

Push a commit to the default branch of a connected repo. CronFoundry runs a
sync pass against that repo's `repo_connection`, and the
`repo_connection.last_synced_at` column advances on success. If the push
targets a non-default branch, the handler returns `204 No Content` without
syncing — this is expected.

You can check the sync actually happened by querying:

```sql
SELECT owner, name, last_synced_at, last_synced_head_sha
FROM repo_connection
WHERE owner = '<owner>' AND name = '<name>';
```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `401 Unauthorized` | Signature mismatch | The env var doesn't match the GitHub App's webhook secret. Re-copy one into the other. |
| `503 Service Unavailable` | `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET` isn't set in the deployment env | Set it and restart the process. |
| `204 No Content` on a push | The push targeted a non-default branch | Expected. Only default-branch pushes trigger resync. |
| `200 OK` but no sync happens | Repo isn't in `repo_connection` for the current org | Add it: `cronfoundry admin connect-repo <owner>/<name> --installation-id <id>`. |

## Security notes

The shared secret should be at least 32 bytes of entropy (the `openssl rand
-hex 32` suggestion above produces 64 hex chars / 256 bits). Rotate by
updating the GitHub App and `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET` together —
during the brief window they disagree, deliveries fail with 401 and GitHub
will retry. Verification uses HMAC-SHA256 with constant-time comparison, and
the request body is capped at 5 MB to bound memory per request.
