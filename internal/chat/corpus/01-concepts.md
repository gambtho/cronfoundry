# Core concepts

CronFoundry executes "skills" (LLM prompts) on a schedule and publishes the
output to one or more destinations. Each piece is small and orthogonal:

- **Repo**: a GitHub repository connected via the GitHub App. CronFoundry
  polls / receives webhooks from the repo and syncs its contents.
- **Skill**: a directory under the repo containing a `SKILL.md` file with
  YAML frontmatter and a markdown body. The body becomes the LLM prompt.
- **Manifest** (`cronfoundry.yaml`): top-level config in the repo that lists
  skills and the schedules attached to them.
- **Schedule**: a named `cron` expression on a skill. Has a provider/model,
  destinations, and (optionally) writeback config.
- **Run**: one execution of a schedule. Statuses: `pending`, `running`,
  `succeeded`, `partial_failure`, `failed`.
- **Destination**: where the run output is published. Built-ins:
  `github-issue`, `slack`, `discord`, `teams`.
- **Writeback**: a commit pushed back to the skill repo (typically a
  `memory.md` append) for skill self-improvement across runs.
- **Secret**: a named cleartext value stored under envelope encryption.
  Manifests reference secrets by NAME (e.g. `{ secret: slack_webhook }`),
  never by value.
- **Provider**: `openai`, `anthropic`, `azure-foundry`, `openrouter`, or
  `copilot-enterprise`. Configured per-schedule.

A run's lifecycle: scheduler tick → insert run row → dispatch runner →
runner clones repo at the recorded SHA → loads SKILL.md → invokes LLM →
strips `<memory>...</memory>` block → publishes to each destination
(failures isolated per destination) → optionally commits writeback.
