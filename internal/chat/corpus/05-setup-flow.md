# Setting up a new job

The end-to-end flow when an operator wants to add a new scheduled skill:

1. **Connect the repo** (one-time): Settings → Source repos → Connect.
   Requires the GitHub App to be installed on the repo.
2. **Add secrets** referenced by the new schedule: Settings → Secrets.
   Names match the `{ secret: NAME }` references in the manifest.
3. **In the connected repo**, create the skill directory:
   - `skills/my-skill/SKILL.md` (frontmatter + prompt)
   - any `{{ include }}` files referenced by the prompt
4. **Edit `cronfoundry.yaml`** at the repo root and add the skill +
   schedule entries. See the manifest reference doc.
5. **Commit and push** to the default branch. The push webhook (or the
   sync poller) picks up the change within seconds and the new schedule
   appears in Jobs.
6. **Test** with "Run now" from the job detail page — this fires the
   schedule once immediately without waiting for the next cron tick.

Cron quick reference:

| Expression       | Meaning                              |
| ---------------- | ------------------------------------ |
| `0 9 * * MON`    | every Monday at 09:00                |
| `*/15 * * * *`   | every 15 minutes                     |
| `0 */4 * * *`    | every 4 hours, on the hour           |
| `0 0 1 * *`      | midnight on the 1st of every month   |
| `0 17 * * FRI`   | every Friday at 17:00                |

`timezone` is an IANA name (e.g. `America/Los_Angeles`, `UTC`,
`Europe/London`). The cron is interpreted in that timezone.
