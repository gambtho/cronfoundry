# GitHub Copilot Enterprise Provider — Design

**Status:** Proposed
**Date:** 2026-04-22
**Author:** gambtho (brainstormed with Claude)

## Overview

Add GitHub Copilot Enterprise as a fourth LLM provider in CronFoundry. Copilot Enterprise uses a short-lived OAuth access token obtained via GitHub's OAuth device flow. Because the token expires, CronFoundry handles silent refresh transparently — the user authorizes once via the web UI and scheduled runs proceed without re-authorization.

This is the first deferred item from the MVP backlog (`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`, deferred item #1).

## Scope

- OAuth device flow UI (`/providers` page, admin-only)
- Token storage in Key Vault (access + refresh + expiry)
- API-mediated token refresh (runner never holds the refresh token)
- `copilot-enterprise` provider adapter in `internal/llm/`
- `cronfoundry.yaml` `provider:` field accepting `copilot-enterprise`
- Tests for all new code paths

**Out of scope:** Configurable Copilot API endpoints (GitHub Enterprise Server). Standard `api.githubcopilot.com` only.

## Token Storage

Two Key Vault secrets are created when the user completes the device flow, plus one metadata secret:

| Secret name | Contents |
|---|---|
| `{prefix}_access_token` | GitHub OAuth access token |
| `{prefix}_refresh_token` | GitHub OAuth refresh token |
| `{prefix}_expiry` | Access token expiry as Unix timestamp string |

`prefix` is chosen by the user during the connect flow (e.g. `copilot`).

The `schedule` row in Postgres gains an optional `copilot_token_refs_json` column:

```json
{ "access": "vault/{prefix}_access_token", "refresh": "vault/{prefix}_refresh_token", "expiry": "vault/{prefix}_expiry" }
```

This field is only populated when `provider = copilot-enterprise`. All other providers continue to use `keyvault_ref_llm_key`. No schema changes are required for the non-Copilot path.

## Device Flow UI

A new **Providers** section in the web UI (sidebar link alongside Secrets and Repos). Admin-only; viewer-role users do not see this section.

### Connect flow

1. User navigates to **Providers → GitHub Copilot Enterprise → Connect**.
2. User enters a secret prefix `P` (e.g. `copilot`). UI calls `POST /api/copilot/connect` with `{ "prefix": "P" }`.
3. API calls `POST https://github.com/login/device/code` requesting scope `copilot`. Returns `device_code`, `user_code`, `verification_uri`, `interval`, `expires_in` to the UI.
4. UI displays `user_code` prominently and an **Open GitHub to authorize** button (opens `verification_uri` in a new tab). UI begins polling `GET /api/copilot/connect/{device_code}/poll`.
5. API polls GitHub's token endpoint on the server side, respecting the `interval` field. On success, API writes `{P}_access_token`, `{P}_refresh_token`, `{P}_expiry` to Key Vault.
6. Polling endpoint returns a success event. UI shows confirmation: "Copilot Enterprise connected. Use `provider: copilot-enterprise` in your `cronfoundry.yaml`."

### Error states

| Condition | UI message |
|---|---|
| User declines authorization | "Authorization was declined. Try again." |
| Device code expires (>5 min) | "Code expired. Try again." |
| Network error during poll | "Connection error. Try again." |
| Prefix already exists in KV | "A Copilot connection with that prefix already exists. Choose a different prefix or delete the existing one first." |

Each error shows a **Try again** button that restarts the flow from step 2.

### API endpoints

`POST /api/copilot/connect` — admin only; starts device flow, returns `user_code` / `verification_uri` / `device_code` / `expires_in`.

`GET /api/copilot/connect/{device_code}/poll` — long-polls GitHub token endpoint (server side); returns `{ "status": "pending" | "success" | "error", "error": "..." }`. Closes on success or terminal error.

`GET /api/copilot/connections` — admin only; lists connected prefixes (names only, no token values).

`DELETE /api/copilot/connections/{prefix}` — admin only; soft-deletes all three KV secrets for the prefix, audit-logged.

## Internal Token Endpoint

`GET /internal/runs/{id}/copilot-token`

Called by the runner after `GET /internal/runs/{id}/context`, but only when `provider = copilot-enterprise`. The runner's context response includes `copilot_token_refs_json` so the runner knows to make this call.

**Behavior:**

1. API reads `{prefix}_expiry` from KV.
2. If the access token has more than 60 seconds of remaining life, API reads `{prefix}_access_token` from KV and returns it.
3. If expiry is within 60 seconds (or the expiry secret is missing), API refreshes: calls GitHub token endpoint with the refresh token, writes new values for all three secrets to KV, returns the new access token.
4. Refresh failure → API returns `503 { "error": "copilot_token_refresh_failed" }`.

**Response (success):**
```json
{ "access_token": "...", "expires_at": "2026-04-22T15:30:00Z" }
```

The runner caches the token for the duration of the run — no re-fetch mid-run.

**Permissions:** No change to `cf-runner` managed identity. The runner never reads or writes KV for Copilot tokens; all KV interaction is in the API (`cf-api` managed identity).

## Provider Adapter

**File:** `internal/llm/copilot.go`

GitHub Copilot's API is OpenAI-compatible (same chat completions request shape). The adapter reuses `openAIProvider` with `baseURL = "https://api.githubcopilot.com"` and two additional required headers:

- `Editor-Version: cronfoundry/1.0`
- `Copilot-Integration-Id: cronfoundry`

Injected via `option.WithHeader(...)` on the OpenAI client. The `APIKey` from `CallOptions` is passed as a Bearer token by the OpenAI SDK automatically.

`NewCopilotEnterprise()` returns a `ToolCapableProvider` (same interface as OpenAI, since the API is compatible).

**factory.go** gains a `"copilot-enterprise"` case:
```go
case "copilot-enterprise":
    return NewCopilotEnterprise(), nil
```

**config/manifest.go** `provider:` field accepts `copilot-enterprise` as a valid value (validated in JSON schema).

**pricing.go** gains a `copilot-enterprise` entry with zero cost-per-token. Token counts are recorded from the API usage field as normal; `cost_cents` is 0. The UI run detail page shows "Included in Copilot subscription" instead of a cost figure when provider is `copilot-enterprise`.

## Runner Changes

In the runner's context-fetch path, after calling `GET /internal/runs/{id}/context`:

```
if context.Provider == "copilot-enterprise" {
    resp := GET /internal/runs/{id}/copilot-token
    opts.APIKey = resp.access_token
}
```

No other runner changes. `CallOptions.APIKey` carries the access token exactly as it carries API keys for other providers.

## Error Handling

| Condition | Run status | `error_kind` | UI message |
|---|---|---|---|
| Token refresh fails (API returns 503) | `failed` | `copilot_token_refresh` | "Copilot token could not be refreshed — reconnect via Providers." |
| Copilot API 401 mid-run | `failed` | `copilot_unauthorized` | "Copilot request unauthorized — token may have been revoked. Reconnect via Providers." |
| Copilot API 429/5xx | retried (3× exp backoff), then `failed` | `llm_error` | Same as other providers |

401 responses are not retried (token is invalid, retry would not help).

## Testing

| File | Coverage |
|---|---|
| `internal/llm/copilot_test.go` | Normal streaming completion, 401, 429 with retry, missing usage field — mock HTTP server, same pattern as `openai_test.go` |
| `internal/webapi/copilot_connect_test.go` | Device flow happy path, expired device code, declined authorization, concurrent poll requests |
| `internal/webapi/copilot_token_test.go` | Fresh token (no refresh needed), expired token (refresh triggered), refresh failure → 503 |

No integration test against the real GitHub Copilot API (consistent with other provider tests).

## Database Migration

One new nullable column on `schedule`:

```sql
ALTER TABLE schedule
  ADD COLUMN copilot_token_refs_json JSONB;
```

No other schema changes.

## `cronfoundry.yaml` Example

```yaml
- path: skills/daily-standup
  schedules:
    - name: morning
      cron: "0 9 * * MON-FRI"
      timezone: America/Los_Angeles
      provider: copilot-enterprise
      model: gpt-4o
      destinations:
        - slack: { secret: standup_webhook }
```

No additional fields needed in the YAML — the token is resolved automatically at run time from the stored KV refs.
