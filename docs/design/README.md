# UI Mocks

Static-HTML reference for the UI redesign that landed in #PR1 / #PR2.
These are the **source of truth** for visual design — components in
`web/src/components/ui/` should match what you see here.

## Pages

| File              | Page         | Notes                                |
| ----------------- | ------------ | ------------------------------------ |
| `overview.html`   | Overview     | Triage: failures first, upcoming, 24h activity |
| `jobs.html`       | Jobs         | Primary index of all scheduled jobs  |
| `job-detail.html` | Job detail   | Schedule + recent runs + logs        |
| `run-detail.html` | Run detail   | Single-run forensics with timeline   |
| `index.html`      | (Index)      | Local navigation between the above   |

## Information architecture

```text
Operate
  ● Overview     ← triage home, alert badge when anything failing
  ● Jobs         ← primary object (Job ≡ Schedule in the API)

Settings ▾       ← collapsed; touched weekly at most
  Source repos
  Secrets
  Providers
  Users
  Audit log
```

Runs is intentionally NOT top-level — cross-job triage lives on
Overview, per-job runs live on Job detail, and the full filtered
runs feed is reachable as a deep link from Overview's activity card.

## Aesthetic

Dark terminal/ops, Geist + Geist Mono, near-black surfaces, single
green accent for healthy/success, red reserved for failure, amber
for warnings, cyan for code/identifiers. Faint scanlines on the
body for ambient texture.

The guiding principle: **absence of red is the win state.** Don't
paint everything green; reserve color for things the operator
should act on.

## Viewing locally

```sh
cd docs/design/mocks
python3 -m http.server 8765
# open http://localhost:8765/
```
