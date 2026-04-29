# CronFoundry mvpplus-2 — Design

**Status:** Shipped (3ff3f2a)
**Date:** 2026-04-23  
**Author:** gambtho (brainstormed with Claude)

## Overview

mvpplus-2 widens destination coverage for CronFoundry by adding two new
destination types: SMTP email and custom HTTP webhooks. Together they close the
"fits my stack" gap for users whose workflows are email-driven or who integrate
with internal tools and automation platforms (n8n, Zapier, custom APIs).

PagerDuty is deferred to a later phase.

---

## Scope

| Destination | Notes |
|-------------|-------|
| **Email (SMTP)** | SMTP with STARTTLS; HTML and plain-text formats; digest and alert use cases |
| **Custom HTTP** | POST/GET to any URL; optional Bearer auth; optional body template |

Both destinations follow the existing publisher pattern: new config struct →
new `Publisher` implementation → registered in the `Dispatcher`. No database
schema changes — destinations live in `destinations_json` already.

---

## Implementation Order

Both destinations were shipped in a single PR (#28) with staged commits:
custom HTTP plumbing first (config struct, publisher, dispatcher wiring), followed
by Email / SMTP config, publisher, and tests. The staged-commit approach preserved
reviewability without requiring two separate PRs.

---

## Feature: Custom HTTP Destination

### Config Shape

```yaml
destinations:
  - http:
      url: https://hooks.example.com/notify
      method: POST                              # optional, default POST
      secret: my_auth_token                     # optional, sent as Bearer token
      headers:                                  # optional static headers
        X-Source: cronfoundry
      body_template: '{"text":"{{ .Output }}","channel":"#ops"}'  # optional
      output: summary                           # named output block; empty = full output
      when: on_failure
```

### Config Struct

New `HTTPDest` field added to `config.Destination`:

```go
type HTTPDest struct {
    URL          string            `json:"url"`
    Method       string            `json:"method,omitempty"`       // default "POST"
    Secret       string            `json:"secret,omitempty"`       // logical secret name
    Headers      map[string]string `json:"headers,omitempty"`
    BodyTemplate string            `json:"body_template,omitempty"`
    Output       string            `json:"output,omitempty"`
}
```

Default body (no `body_template`): `{"output": "<JSON-escaped output>"}`.

### Publisher Behavior

1. Resolve secret via `SecretGetter` if `Secret` is set; inject as `Authorization: Bearer <value>`.
2. Render `body_template` if set (Go template, same `template.Context` as elsewhere), otherwise use default JSON envelope.
3. Build `http.Request` with configured method and headers.
4. Fire request; treat 2xx as success.
5. Non-2xx: return `Result{OK: false}` with HTTP status in `Result.Detail`.
6. Timeout governed by the run's existing context — no separate timeout.

### Validation

- `url` required; must be a valid HTTP/HTTPS URL.
- `method` must be a valid HTTP method if set.

---

## Feature: Email Destination

### Config Shape

```yaml
destinations:
  - email:
      smtp_host: smtp.gmail.com
      smtp_port: 587                            # optional, default 587
      username_secret: smtp_username
      password_secret: smtp_password
      from: cronfoundry@example.com
      to:
        - team@example.com
        - oncall@example.com
      subject: "{{ .SkillName }} run {{ .RunOutcome }}"  # optional template
      format: html                              # "html" (default) or "text"
      output: summary                           # named output block; empty = full output
      when: always
```

### Config Struct

New `EmailDest` field added to `config.Destination`:

```go
type EmailDest struct {
    SMTPHost       string   `json:"smtp_host"`
    SMTPPort       int      `json:"smtp_port,omitempty"`        // default 587
    UsernameSecret string   `json:"username_secret"`
    PasswordSecret string   `json:"password_secret"`
    From           string   `json:"from"`
    To             []string `json:"to"`
    Subject        string   `json:"subject,omitempty"`          // Go template
    Format         string   `json:"format,omitempty"`           // "html" or "text"
    Output         string   `json:"output,omitempty"`
}
```

Default subject: `"{{ .SkillName }}: run {{ .RunOutcome }}"`.

### Publisher Behavior

1. Resolve username and password via `SecretGetter`.
2. Render subject template using `template.Context`.
3. Dial SMTP with STARTTLS (`smtp.SendMail` or equivalent).
4. Build multipart/alternative message:
   - `text/plain` part always included (output as-is).
   - `text/html` part included when `format: html`. Template: `<h2>` containing skill name and run date, followed by a `<pre>` block containing the output. No external CSS or images — inline styles only.
5. Send to all `to` addresses in a single SMTP transaction.
6. Any SMTP error (dial, auth, send) fails the whole destination and is surfaced in `Result.Err`.

### Validation

- `smtp_host`, `username_secret`, `password_secret`, `from`, `to` all required.
- `smtp_port` must be in range 1–65535 if set.
- `format` must be `"html"` or `"text"` if set.
- `to` must be non-empty.

---

## Shared Concerns

### Secret Resolution

Both destinations use the existing `SecretGetter` interface. No new secret
storage mechanism is needed.

### Named Output Blocks

Both destinations support the `output:` field identical to existing destinations
— the dispatcher's `outputFor` function already handles this.

### `when:` Condition

Both destinations support `when: on_success | on_failure | always` via the
existing `Destination.ShouldPublish` path. No changes needed.

### `destType()` in dispatcher

Two new cases added to `destType()`:

```go
case d.HTTP != nil:
    return "http", nil
case d.Email != nil:
    return "email", nil
```

---

## Testing

### Custom HTTP

- Unit tests with `httptest.NewServer` — no mocks needed.
- Cases: 2xx success, non-2xx failure, body template rendering, Bearer token
  header injection, default JSON envelope, named output selection.
- `when` condition skip covered by existing dispatcher tests.

### Email

- Unit tests with a fake SMTP server (net.Listener speaking minimal SMTP, or
  `github.com/emersion/go-smtp` test server).
- Cases: HTML vs text format, subject template rendering, multiple recipients,
  auth failure, named output selection.

### Config Validation

- Both types get validation cases in `manifest_test.go`: missing required
  fields, invalid port, invalid format value.

---

## Success Criteria

**Custom HTTP:**
- A destination configured with a URL receives a POST with the LLM output in
  the request body within the run's timeout.
- A `body_template` is rendered correctly; `.Output` receives the named or full
  output as configured.
- A non-2xx response is recorded as a destination failure in the run detail
  page.

**Email:**
- A schedule configured with an email destination delivers a message to all
  `to` addresses after each qualifying run.
- `format: html` produces a multipart message with both plain-text and HTML
  parts; `format: text` produces plain-text only.
- SMTP auth failure is surfaced in the run detail page as a destination error.

---

## Out of Scope for mvpplus-2

- PagerDuty — deferred.
- Transactional email APIs (SendGrid, Mailgun, Resend) — SMTP covers the use
  case; APIs can be added as separate destination types later.
- UI configuration for new destinations — YAML-only, consistent with existing
  destination types.
- Per-recipient SMTP error handling — whole destination fails on any error.
