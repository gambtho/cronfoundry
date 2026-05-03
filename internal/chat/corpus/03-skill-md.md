# SKILL.md format

Every skill directory contains a `SKILL.md` with YAML frontmatter and a
markdown prompt body:

```markdown
---
name: weekly-digest
description: Aggregates last week's activity
max_tokens: 4000
---
You are writing a weekly digest.

Context:
{{ include "context/template.md" }}

Respond with a short summary, then a <memory>...</memory> block with one
short learning.
```

Frontmatter fields:

- `name` (required) — used in destination templates and logs.
- `description` (optional) — surfaced in the operator UI.
- `max_tokens` (optional) — caps the LLM response size for this skill.

Body templating:

- Only `{{ include "relative/path.md" }}` is supported. No conditionals,
  no loops. Paths are relative to the skill directory and may not escape
  it (`..` is rejected).
- Includes are resolved before the prompt is sent to the LLM, so the
  rendered prompt is what the model sees.

`<memory>...</memory>` blocks in the model output are extracted before
publishing. If the schedule's `writeback` is enabled, the memory contents
are appended (or replacing) the writeback path and committed back to the
skill repo as `cronfoundry[bot]`.
