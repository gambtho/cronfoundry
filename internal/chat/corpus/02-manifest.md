# cronfoundry.yaml manifest reference

A complete schedule example with every common option:

```yaml
version: 1
skills:
  - path: skills/weekly-digest
    schedules:
      - name: monday-morning
        cron: "0 9 * * MON"
        timezone: America/Los_Angeles
        overlap_policy: skip       # skip | queue | concurrent
        timeout_sec: 600
        provider: openai           # openai | anthropic | azure-foundry | openrouter | copilot-enterprise
        model: gpt-4o-mini
        destinations:
          - github-issue:
              repo: myorg/reports
              title: "Digest — {{ run.date }}"
              labels: [digest]
          - slack:
              secret: slack_webhook
              text: "{{ output.truncated 35000 }}"
        writeback:
          enabled: true
          path: memory.md
          mode: append             # append | replace
        env:
          LOOKBACK_DAYS: "7"
          TEAM_NAME:
            secret: team_name      # resolved from CRONFOUNDRY_SECRET_TEAM_NAME
```

Key rules:

- `cron` follows standard 5-field syntax (`min hour dom month dow`).
- `overlap_policy: skip` is the safe default — if a run is still going
  when the next tick fires, the new tick is dropped.
- `timeout_sec` is enforced by the runner; a hung LLM call is killed and
  the run is marked `failed` with kind `timeout`.
- `secret: NAME` resolves from the secret store; `CRONFOUNDRY_SECRET_NAME`
  env vars are runner-side fallbacks for the standalone runner only.

Template variables available in destination text fields: `{{ output }}`,
`{{ output.truncated N }}`, `{{ run.id }}`, `{{ run.date }}`,
`{{ run.started_at }}`, `{{ schedule.name }}`, `{{ skill.name }}`.
