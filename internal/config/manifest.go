// Package config parses CronFoundry manifest and skill files and resolves
// single-level `{{ include "..." }}` directives inside skill bodies.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"

	"sigs.k8s.io/yaml"
)

type Manifest struct {
	Version int          `json:"version"`
	Skills  []SkillEntry `json:"skills"`
}

type SkillEntry struct {
	Path      string     `json:"path"`
	Schedules []Schedule `json:"schedules"`
}

type Schedule struct {
	Name          string                         `json:"name"`
	Cron          string                         `json:"cron"`
	Timezone      string                         `json:"timezone"`
	OverlapPolicy string                         `json:"overlap_policy"`
	TimeoutSec    int                            `json:"timeout_sec"`
	Provider      string                         `json:"provider"`
	Model         string                         `json:"model"`
	MaxTurns      int                            `json:"max_turns,omitempty"`
	Destinations  []Destination                  `json:"destinations"`
	Writeback     *WritebackConfig               `json:"writeback,omitempty"`
	Env           map[string]EnvValue            `json:"env"`
	MCPEnv        map[string]map[string]EnvValue `json:"mcp_env,omitempty"`
	AutoPause     *AutoPauseConfig               `json:"auto_pause,omitempty"`
}

type Destination struct {
	GitHubIssue *GitHubIssueDest `json:"github-issue,omitempty"`
	Slack       *WebhookDest     `json:"slack,omitempty"`
	Discord     *WebhookDest     `json:"discord,omitempty"`
	Teams       *WebhookDest     `json:"teams,omitempty"`
	// When controls which run outcomes trigger this destination.
	// Valid values: "always" (default), "on_success", "on_failure".
	When string `json:"when,omitempty"`
}

// ShouldPublish returns true when the destination should fire given runSucceeded.
func (d Destination) ShouldPublish(runSucceeded bool) bool {
	switch d.When {
	case "on_success":
		return runSucceeded
	case "on_failure":
		return !runSucceeded
	default: // "always" or empty
		return true
	}
}

type GitHubIssueDest struct {
	Repo      string   `json:"repo"`
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
}

type WebhookDest struct {
	Secret   string `json:"secret"`
	Text     string `json:"text,omitempty"`
	Content  string `json:"content,omitempty"`
	Title    string `json:"title,omitempty"`
	Username string `json:"username,omitempty"`
}

type WritebackConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
	Mode    string `json:"mode"`
}

// AutoPauseConfig controls the auto-pause-on-consecutive-failures behavior.
// If nil, the schedule uses the global default (DefaultAutoPauseAfter).
type AutoPauseConfig struct {
	After int `json:"after"`
}

// EnvValue is either a literal string or a `{ secret: name }` reference.
type EnvValue struct {
	Literal string
	Secret  string
}

func (e *EnvValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("env value must not be empty")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("env value must not be null")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("env string value: %w", err)
		}
		e.Literal = s
		return nil
	case '{':
		var ref struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(data, &ref); err != nil {
			return fmt.Errorf("env object value: %w", err)
		}
		if ref.Secret == "" {
			return fmt.Errorf("env object value must set 'secret'")
		}
		e.Secret = ref.Secret
		return nil
	default:
		return fmt.Errorf("env value must be a string or { secret: name } object, got %s", string(trimmed))
	}
}

func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m, yaml.DisallowUnknownFields); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

var validOverlap = map[string]bool{
	"":           true, // default to skip
	"skip":       true,
	"queue":      true,
	"concurrent": true,
}

// EffectiveOverlapPolicy returns the overlap policy with the default resolved.
// An empty OverlapPolicy in the YAML means "skip" — this method centralizes
// that default so callers don't need to handle both forms.
func (s *Schedule) EffectiveOverlapPolicy() string {
	if s.OverlapPolicy == "" {
		return "skip"
	}
	return s.OverlapPolicy
}

// Validate returns the first validation error found, or nil.
func (m *Manifest) Validate() error {
	if m.Version == 0 {
		return fmt.Errorf("version: required")
	}
	if m.Version != 1 {
		return fmt.Errorf("version %d not supported (supported: 1)", m.Version)
	}
	seenSkills := map[string]bool{}
	for _, s := range m.Skills {
		if s.Path == "" {
			return fmt.Errorf("skill: path required")
		}
		if seenSkills[s.Path] {
			return fmt.Errorf("duplicate skill path %q", s.Path)
		}
		seenSkills[s.Path] = true

		seenSched := map[string]bool{}
		for _, sch := range s.Schedules {
			if sch.Name == "" {
				return fmt.Errorf("skill %q: schedule name required", s.Path)
			}
			if seenSched[sch.Name] {
				return fmt.Errorf("skill %q: duplicate schedule name %q", s.Path, sch.Name)
			}
			seenSched[sch.Name] = true
			if sch.Cron == "" {
				return fmt.Errorf("skill %q schedule %q: cron required", s.Path, sch.Name)
			}
			if sch.Provider == "" {
				return fmt.Errorf("skill %q schedule %q: provider required", s.Path, sch.Name)
			}
			if sch.Model == "" {
				return fmt.Errorf("skill %q schedule %q: model required", s.Path, sch.Name)
			}
			if sch.MaxTurns < 0 || sch.MaxTurns > 2147483647 {
				return fmt.Errorf("skill %q schedule %q: max_turns must be between 0 and 2147483647 (got %d)", s.Path, sch.Name, sch.MaxTurns)
			}
			if !validOverlap[sch.OverlapPolicy] {
				return fmt.Errorf("skill %q schedule %q: overlap_policy %q invalid (want: skip|queue|concurrent)", s.Path, sch.Name, sch.OverlapPolicy)
			}
			if sch.AutoPause != nil && sch.AutoPause.After < 1 {
				return fmt.Errorf("skill %q schedule %q: auto_pause.after must be >= 1 (got %d)",
					s.Path, sch.Name, sch.AutoPause.After)
			}
		}
	}
	return nil
}

// FindSchedule returns pointers to the skill and schedule matching the given
// coordinates, or an error if either is not found.
func (m *Manifest) FindSchedule(skillPath, scheduleName string) (*SkillEntry, *Schedule, error) {
	for i := range m.Skills {
		if m.Skills[i].Path == skillPath {
			for j := range m.Skills[i].Schedules {
				if m.Skills[i].Schedules[j].Name == scheduleName {
					return &m.Skills[i], &m.Skills[i].Schedules[j], nil
				}
			}
			return nil, nil, fmt.Errorf("schedule %q not found under skill %q", scheduleName, skillPath)
		}
	}
	return nil, nil, fmt.Errorf("skill %q not found in manifest", skillPath)
}
