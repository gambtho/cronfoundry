# Specs Index

One-line index of all design and PRD documents under
`docs/superpowers/specs/`. Status reflects whether the work has merged to
`main`; the SHA in parens is the merging commit.

| Date | Title | Status |
|---|---|---|
| 2026-04-19 | [CronFoundry — Technical Design](2026-04-19-cronfoundry-design.md) | Shipped (`fd91fd6`) |
| 2026-04-19 | [CronFoundry — Product Requirements](2026-04-19-cronfoundry-prd.md) | Shipped (`fd91fd6`) |
| 2026-04-19 | [P2 — Persistence + Always-On Scheduler](2026-04-19-cronfoundry-p2-design.md) | Shipped (`3b246ff`) |
| 2026-04-20 | [GitHub Pages landing site](2026-04-20-github-pages-design.md) | Shipped (`16c2af3`) |
| 2026-04-20 | [P3 — Context for Auth Layer](2026-04-20-p3-context.md) | Shipped (`e74e651`) |
| 2026-04-20 | [P3a — Auth Layer Design](2026-04-20-p3a-design.md) | Shipped (`e74e651`) |
| 2026-04-20 | [P4 — Azure Deployment Design](2026-04-20-p4-azure-deploy-design.md) | Shipped (`071966b`) |
| 2026-04-20 | [P4 — Context](2026-04-20-p4-context.md) | Shipped (`071966b`) |
| 2026-04-20 | [P5 — Operator Web UI](2026-04-20-p5-web-ui-design.md) | Shipped (`f6d1267`) |
| 2026-04-20 | [P7 — MVP Close-out](2026-04-20-p7-mvp-closeout-design.md) | Shipped (`f7f2f93`) |
| 2026-04-22 | [Auto-pause on Consecutive Failures](2026-04-22-auto-pause-design.md) | Shipped (`a007dfe`) |
| 2026-04-22 | [Copilot Enterprise LLM Provider](2026-04-22-copilot-enterprise-provider-design.md) | Shipped (`c86f48a`) |
| 2026-04-22 | [MCP Tool Support for Skills](2026-04-22-mcp-tool-support-design.md) | Shipped (`37fb496`) |
| 2026-04-22 | [Multicloud Deploy Targets](2026-04-22-multicloud-design.md) | Shipped (`4b6d7f2`) |
| 2026-04-22 | [MVPplus — Schedule Overrides + Routing + Formatting](2026-04-22-mvpplus-design.md) | Shipped (`fea9ec2`) |
| 2026-04-23 | [Docs Refresh](2026-04-23-docs-refresh-design.md) | Shipped (`7418fad`) |
| 2026-04-23 | [F24 — Runner GitHub Token Injection](2026-04-23-f24-runner-github-token-design.md) | Shipped (`563cf15`) |
| 2026-04-23 | [MVPplus-2 — Custom HTTP + SMTP Destinations](2026-04-23-mvpplus-2-design.md) | Shipped (`3ff3f2a`) |
| 2026-04-23 | [MVPplus-3 — Pluggable Secret Backends + LLM Redaction](2026-04-23-mvpplus-3-design.md) | Shipped (`3a5fde1`) |

## Conventions

Each spec begins with a metadata block:

```
**Status:** Proposed | Shipped (<sha>) | Deferred | Superseded by <doc>
**Date:** YYYY-MM-DD
**Author:** <handle>
```

When a spec ships, update the `Status:` line to `Shipped (<merge-sha>)` and
add a row to the table above. Deferred specs stay listed; superseded specs
keep a pointer to the document that replaced them.
