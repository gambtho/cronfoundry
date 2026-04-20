// Package template renders destination templates with a fixed, safe variable
// set — no logic, no loops.
package template

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Context carries the values exposed to destination templates. Fields map
// directly to the `{{ ... }}` directives understood by Render.
type Context struct {
	Output    string
	RunID     string
	RunDate   string
	StartedAt time.Time
	Schedule  Meta
	Skill     Meta
}

// Meta is the minimal identity (a name) of a schedule or skill surfaced to
// templates. Kept as a named type so templates can reach both `schedule.name`
// and `skill.name` via the same shape.
type Meta struct {
	Name string
}

var (
	directiveRe     = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)
	truncatedArgsRe = regexp.MustCompile(`^output\.truncated\s+(\d+)$`)
)

// Render returns the rendered string and warnings for unresolved variables.
// Unresolved variables are left as their literal `{{ name }}` form.
func Render(tmpl string, ctx Context) (string, []string) {
	var warnings []string
	out := directiveRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(directiveRe.FindStringSubmatch(match)[1])
		switch expr {
		case "output":
			return ctx.Output
		case "run.id":
			return ctx.RunID
		case "run.date":
			return ctx.RunDate
		case "run.started_at":
			return ctx.StartedAt.UTC().Format(time.RFC3339)
		case "schedule.name":
			return ctx.Schedule.Name
		case "skill.name":
			return ctx.Skill.Name
		}
		if m := truncatedArgsRe.FindStringSubmatch(expr); m != nil {
			n, _ := strconv.Atoi(m[1])
			return truncate(ctx.Output, n)
		}
		warnings = append(warnings, fmt.Sprintf("unresolved template variable: %s", expr))
		return match
	})
	return out, warnings
}

// truncate returns s limited to maxRunes runes. When truncated, a single
// horizontal ellipsis replaces the dropped tail.
func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= maxRunes-1 {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteRune('…')
	return b.String()
}
