# GitHub App Manifest Setup — Design

## Problem

`docs/install.sh` step 5/17 ("GitHub App setup") is the single roughest spot in the
quickstart. The user must:

1. Open a specific URL (and verify it's `/settings/apps/new`, not the OAuth App
   page that lives one click away).
2. Type a globally unique app name.
3. Paste three placeholder URLs (homepage, callback, webhook), knowing they must
   be re-edited in step 16/17 after deploy.
4. Run `openssl rand -hex 32` themselves and paste the secret in two places —
   except the install script never actually captures it (silent gap).
5. Toggle four permission scopes and one event subscription correctly.
6. Save, then visit two more pages to grab the App ID and generate a client
   secret, and download the `.pem`.
7. Install the app and copy the installation ID out of the post-install URL.

The script then asks for `App ID`, `Client ID`, `Client Secret`, `.pem path`,
and `Installation ID` — five sequential prompts where any error has to be
back-tracked through GitHub's UI.

## Goal

Collapse step 5 + the second prompt of step 6 into **two browser clicks** —
"Create GitHub App" and "Install" — using GitHub's
[App Manifest flow](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest).
Capture all credentials (App ID, Client ID, Client Secret, webhook secret, PEM,
installation ID) automatically. Keep a manual fallback for environments where
the local-callback trick can't work.

Step 16 ("update App URLs") stays — GitHub's API doesn't let an App rewrite its
own URLs — but it shrinks to a pre-filled paste prompt with the slug and FQDN
known.

## Approach

A new Go subcommand, `cronfoundry setup github-app`, replaces the manual
checklist. It:

1. Spins up a localhost HTTP server on a chosen port (default `8765`, fall
   back to next free port if taken).
2. Opens the user's browser to a small local page that auto-POSTs a manifest to
   `https://github.com/settings/apps/new?state=<csrf>` (or
   `https://github.com/organizations/<org>/settings/apps/new?state=<csrf>` if
   `--org` is passed).
3. The user sees GitHub's standard "Create App from manifest" confirmation
   page, clicks **Create**.
4. GitHub redirects to `http://localhost:<port>/callback?code=<temp>&state=<csrf>`.
5. Subcommand exchanges `code` via `POST /app-manifests/{code}/conversions` —
   GitHub returns App ID, slug, owner, client ID, client secret, webhook
   secret, and PEM in one JSON body.
6. Subcommand persists those into the install-state file (and writes the PEM
   to `~/.cronfoundry/<env>.pem` with `0600`).
7. Subcommand opens `https://github.com/apps/<slug>/installations/new` for the
   install flow. After the user picks repos, GitHub redirects to
   `http://localhost:<port>/installed?installation_id=<id>&setup_action=install`,
   which the same server captures.
8. Server exits; control returns to `install.sh`.

`install.sh` step 5 becomes:

```bash
if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  ./cronfoundry setup github-app \
      --state-file "$STATE_FILE" \
      --default-name "cronfoundry-$(whoami)" \
      || die "GitHub App setup failed; re-run, or pass --manual to use the legacy prompts."
  source "$STATE_FILE"  # pull in the new CF_* vars
fi
```

The legacy prompts are kept verbatim as a `--manual` mode in the same
subcommand for SSH/Codespaces/no-browser environments.

## Components

### 1. `cmd/cronfoundry/setup_githubapp.go` — new subcommand

Cobra subcommand under existing root. Entry point: `setup github-app`.

Flags:

| Flag | Default | Purpose |
|---|---|---|
| `--state-file` | `$HOME/.cronfoundry-quickstart-state` | Where to write `CF_*` vars |
| `--default-name` | `cronfoundry-<user>` | Pre-filled App name in manifest |
| `--org` | unset | Create under an org instead of personal account |
| `--port` | `8765` | Localhost port; auto-fallback if busy |
| `--pem-dir` | `$HOME/.cronfoundry` | Where to drop the `.pem` |
| `--manual` | `false` | Skip the browser flow; prompt as install.sh does today |
| `--timeout` | `10m` | Abort if user doesn't complete the flow |

Responsibilities (does *not* know about Bicep, Azure, or `.env` — pure
GitHub-side concerns):

- Build manifest JSON (see schema below).
- Run the local server lifecycle.
- Hit `POST /app-manifests/{code}/conversions`.
- Write state-file entries: `CF_GITHUB_APP_ID`, `CF_GITHUB_APP_SLUG`,
  `CF_GITHUB_CLIENT_ID`, `CF_GITHUB_CLIENT_SECRET`,
  `CF_GITHUB_WEBHOOK_SECRET`, `CF_GITHUB_PEM_PATH`, `CF_INSTALLATION_ID`.
- Print the next instruction (post-deploy URL update) so step 16 already knows
  the slug.

### 2. `internal/githubapp/manifest.go` — manifest builder

Pure builder. Input: name, default-base-url placeholder (`http://localhost:8765`
during creation; URLs get patched in step 16). Output: `ManifestPayload` matching
GitHub's documented schema:

```json
{
  "name": "cronfoundry-tng",
  "url": "http://localhost:8765",
  "hook_attributes": { "url": "http://localhost:8765/webhook" },
  "redirect_url": "http://localhost:8765/callback",
  "callback_urls": ["http://localhost:8765/oauth/callback"],
  "setup_url": "http://localhost:8765/installed",
  "setup_on_update": true,
  "public": false,
  "default_permissions": {
    "contents": "write",
    "issues": "write",
    "metadata": "read",
    "email_addresses": "read"
  },
  "default_events": ["push"]
}
```

Note: every URL is a placeholder — GitHub requires *some* URL but they're all
overwritten in step 16 once the FQDN is known.

### 3. `internal/githubapp/server.go` — local callback server

Single-purpose `http.Server` with three handlers:

- `GET /` — auto-submits the manifest form (HTML + tiny JS that posts to
  `https://github.com/settings/apps/new?state=<csrf>`). Renders a "creating
  your GitHub App in a new tab — return here when done" message.
- `GET /callback` — receives `?code&state`, validates state, calls
  `POST /app-manifests/{code}/conversions`, writes credentials, renders a
  success page with a button linking to the installation URL.
- `GET /installed` — receives `?installation_id&setup_action`, persists ID,
  renders "all done — return to your terminal" page, signals the server to shut
  down via a context cancel.

Concurrency: a single `done` channel; whichever path completes successfully
(install) closes it. `/callback` only collects credentials and hands off.

CSRF: random 32-byte hex string in `state`, mandatory match on both callback
hits.

### 4. `internal/githubapp/conversion.go` — manifest exchange

Thin wrapper around `POST /app-manifests/{code}/conversions`. Returns a typed
`Conversion` struct. Unit-testable with an injected `httpClient`. Reuses the
existing `internal/httpx` helpers for retries and timeouts where they make
sense (single shot, 30 s timeout is fine).

### 5. `install.sh` step 5 + 6 + 16 edits

- Step 5 becomes the snippet shown in *Approach* above. The 9-line printed
  instruction list is removed.
- Step 6's installation-ID prompt is gated: only ask if `CF_INSTALLATION_ID` is
  still empty (manual fallback path).
- Step 16 prints the slug-aware URL: `https://github.com/settings/apps/${CF_GITHUB_APP_SLUG}`
  and the three URLs already substituted with `$CF_FQDN`. Same one-line
  `read -rp` confirmation.

### 6. Tests

- `internal/githubapp/manifest_test.go` — golden-file test of the JSON shape;
  ensures we don't drift from GitHub's expected schema.
- `internal/githubapp/conversion_test.go` — `httptest.Server` simulating the
  conversions endpoint; verify happy path, 422 (slug taken), 5xx retry
  behaviour, and malformed JSON.
- `internal/githubapp/server_test.go` — drive the three handlers with a fake
  `http.Client` for the upstream conversion call; verify state-file is
  written with `0600` perms and PEM lands in pem-dir.

No e2e test against real GitHub — the conversion endpoint requires a live
manifest exchange; covered manually during `--dry-run` smoke runs.

## Data Flow

```
install.sh                cronfoundry setup           browser           github.com
    |                            |                       |                  |
    |-- exec subcommand -------->|                       |                  |
    |                            |-- start :8765 ------->|                  |
    |                            |-- open browser ------>|                  |
    |                            |                       |-- POST /apps/new |
    |                            |                       |<-- consent UI ---|
    |                            |                       |-- click Create   |
    |                            |                       |<-- 302 ----------|
    |                            |<-- GET /callback?code |                  |
    |                            |-- POST /conversions ------------------->|
    |                            |<-- {id,secrets,pem} ---------------------|
    |                            |-- write state-file --|                  |
    |                            |-- redirect to /installations/new ------>|
    |                            |                       |-- pick repos    |
    |                            |                       |<-- 302 ----------|
    |                            |<-- GET /installed?installation_id       |
    |                            |-- write CF_INSTALLATION_ID              |
    |<-- exit 0 -----------------|                                          |
    |-- source state-file        |                                          |
    |-- continue to step 6       |                                          |
```

## Error Handling

| Failure | Behaviour |
|---|---|
| Port `8765` busy | Try `8766..8775`; abort if all busy with hint to pass `--port` |
| Browser doesn't open (`xdg-open`/`open` missing) | Print the URL; user pastes it manually |
| User closes browser without finishing | `--timeout` (default 10 min) cancels the server, prints a clear "no manifest received" error, suggests `--manual` |
| `state` mismatch on callback | Reject with 400, log to terminal, keep server alive for retry |
| Conversion returns 422 (name taken) | Print the GitHub error, prompt user to re-run with a different `--default-name` (state-file unchanged) |
| Conversion returns 5xx | One retry after 2 s, then surface error |
| User installs app on wrong repos | Out of scope — the install flow already lets them re-pick; we just record whichever installation ID GitHub redirects with |
| State-file write fails | Abort before any redirect; user retries cleanly |
| `--manual` flag | Print the legacy 9-line checklist verbatim and run the same five `read -rp` prompts that exist today |

## Security Notes

- State-file already has `0600`; we keep that. PEM lands in `$HOME/.cronfoundry/`
  with `0600`, parent dir `0700`.
- `state` parameter is 32 bytes from `crypto/rand`. Hex-encoded.
- Server binds to `127.0.0.1` only — never `0.0.0.0`.
- All credentials are written directly to disk; the subcommand never logs
  them. Webhook secret is captured in `CF_GITHUB_WEBHOOK_SECRET`; threading
  it into Bicep params is a small follow-up to step 13 (out of scope of this
  spec — flagged as `TODO` in the implementation plan).

## What This Doesn't Do

- **Doesn't eliminate step 16.** GitHub's API doesn't allow an App to rewrite
  its own URLs; the user must edit those in the GitHub UI after deploy. This
  spec leaves step 16 in place but pre-fills the slug-aware settings URL and
  the three exact URLs to paste.
- **Doesn't reorder deploy.** A two-pass Bicep that creates the Container Apps
  Environment first to derive a stable FQDN is plausible but invasive; logged
  as a future improvement.
- **Doesn't wire the captured webhook secret into Bicep params.** Today's
  `main.bicep` doesn't take a webhook secret param; adding it is a tracked
  follow-up but not gating this UX win.
- **Doesn't auto-generate a Bicep deploy.** Same boundary as today.

## Open Questions

None at design time. The two judgement calls — (a) keep step 16, (b) defer
webhook-secret threading — are explicit non-goals above.
