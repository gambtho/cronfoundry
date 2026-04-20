// Package config parses CronFoundry manifest and skill files.
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
	Name          string              `json:"name"`
	Cron          string              `json:"cron"`
	Timezone      string              `json:"timezone"`
	OverlapPolicy string              `json:"overlap_policy"`
	TimeoutSec    int                 `json:"timeout_sec"`
	Provider      string              `json:"provider"`
	Model         string              `json:"model"`
	Destinations  []Destination       `json:"destinations"`
	Writeback     *WritebackConfig    `json:"writeback,omitempty"`
	Env           map[string]EnvValue `json:"env"`
}

type Destination struct {
	GitHubIssue *GitHubIssueDest `json:"github-issue,omitempty"`
	Slack       *WebhookDest     `json:"slack,omitempty"`
	Discord     *WebhookDest     `json:"discord,omitempty"`
	Teams       *WebhookDest     `json:"teams,omitempty"`
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
			if !validOverlap[sch.OverlapPolicy] {
				return fmt.Errorf("skill %q schedule %q: overlap_policy %q invalid (want: skip|queue|concurrent)", s.Path, sch.Name, sch.OverlapPolicy)
			}
		}
	}
	return nil
}

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
