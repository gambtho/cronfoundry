package chat

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// docsFS embeds a small, curated set of markdown files used as grounding for
// the assistant's system prompt. We keep the corpus small (a few KB) and
// inline it on every request rather than running a vector DB; the docs in
// this repo are small enough that a single context block is cheaper and
// more reliable than retrieval.
//
//go:embed corpus/*.md
var docsFS embed.FS

// docCorpus is loaded once at package init from the embedded FS. The
// concatenated form is what we splice into the system prompt.
var docCorpus = mustLoadCorpus()

func mustLoadCorpus() string {
	entries, err := fs.ReadDir(docsFS, "corpus")
	if err != nil {
		// An empty corpus is acceptable in tests / minimal builds. The
		// assistant just won't have grounding beyond its own training data.
		return ""
	}

	type docFile struct {
		name string
		body []byte
	}
	var files []docFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := fs.ReadFile(docsFS, "corpus/"+e.Name())
		if err != nil {
			continue
		}
		files = append(files, docFile{name: e.Name(), body: b})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("### %s\n\n", f.name))
		sb.Write(f.body)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// buildSystemPrompt assembles the assistant persona, the embedded docs
// corpus, and (optionally) the operator's current page so the model can
// answer "this run", "this job" without copy-paste.
func buildSystemPrompt(caller Caller, pageContext string) string {
	var sb strings.Builder
	sb.WriteString(`You are the CronFoundry in-app assistant. CronFoundry is a
self-hostable, GitOps-style scheduler for LLM skills. Your job is to help
operators:

  - understand the app and its concepts (manifests, skills, schedules,
    runs, destinations, secrets, writeback),
  - troubleshoot failed or unexpected runs by inspecting recent runs and
    their event timelines,
  - draft cronfoundry.yaml snippets when the operator wants to add a new
    skill or schedule.

GUIDELINES
  - Prefer concrete tool calls over guessing. If the user asks "why did my
    monday-morning job fail?", call get_recent_runs / get_run_events to
    look at the actual data before hypothesizing.
  - You CANNOT modify schedules, secrets, or repos. If the user asks for a
    mutation, explain that they need to make the change through the UI or
    by committing to their skill repo, and offer a YAML snippet.
  - When emitting YAML, wrap it in a fenced code block tagged 'yaml' so the
    UI can render it cleanly.
  - Keep responses short. Operators are usually mid-incident. If a long
    answer is unavoidable, lead with a one-sentence summary.
  - Never invent run IDs, schedule names, or commit SHAs. If the data isn't
    in the conversation or in a tool result, say you don't know.
  - Secret VALUES are never visible to you. Secret NAMES (as referenced
    from manifests) are fine to discuss.
`)

	if caller.Login != "" {
		sb.WriteString(fmt.Sprintf("\nThe current operator is %q (role: %s).\n", caller.Login, caller.Role))
	}
	if pageContext != "" {
		sb.WriteString(fmt.Sprintf("\nThe operator is currently viewing: %s\n", pageContext))
	}

	if docCorpus != "" {
		sb.WriteString("\n--- BEGIN PRODUCT DOCS ---\n\n")
		sb.WriteString(docCorpus)
		sb.WriteString("\n--- END PRODUCT DOCS ---\n")
	}

	return sb.String()
}
