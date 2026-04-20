// Package sync implements the repo sync poller (Loop 1 in the P2 design):
// periodically HEAD-check each connected repo, shallow-clone on SHA change,
// parse cronfoundry.yaml + referenced SKILL.md files, upsert skill +
// schedule rows.
package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gambtho/cronfoundry/internal/config"
)

// LoadManifest reads and validates a checked-out repo's cronfoundry.yaml,
// then parses each SKILL.md it references. Returns (manifest, skillsByPath)
// or an error on any step.
func LoadManifest(repoRoot string) (*config.Manifest, map[string]*config.Skill, error) {
	manifestPath := filepath.Join(repoRoot, "cronfoundry.yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: read cronfoundry.yaml: %w", err)
	}
	m, err := config.ParseManifest(manifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: parse cronfoundry.yaml: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, nil, fmt.Errorf("sync: validate cronfoundry.yaml: %w", err)
	}

	skills := make(map[string]*config.Skill, len(m.Skills))
	for _, entry := range m.Skills {
		skillMD := filepath.Join(repoRoot, entry.Path, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			return nil, nil, fmt.Errorf("sync: read SKILL.md for %q: %w", entry.Path, err)
		}
		sk, err := config.ParseSkillFile(data)
		if err != nil {
			return nil, nil, fmt.Errorf("sync: parse SKILL.md for %q: %w", entry.Path, err)
		}
		skills[entry.Path] = sk
	}
	return m, skills, nil
}
