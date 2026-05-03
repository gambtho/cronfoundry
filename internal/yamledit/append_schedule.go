// Package yamledit edits cronfoundry.yaml manifests with comment- and
// formatting-preserving precision via yaml.v3's Node API. Round-trips that
// go through gopkg.in/yaml.v3's *yaml.Node preserve comments, anchors,
// ordering, and source indentation; only the inserted lines change in a
// textual diff. config.ParseManifest still owns whole-manifest validation.
package yamledit

import (
	"bytes"
	"errors"
	"fmt"

	sigsyaml "sigs.k8s.io/yaml"
	"gopkg.in/yaml.v3"

	"github.com/gambtho/cronfoundry/internal/config"
)

// ErrSkillNotFound is returned when the requested skill_path is not present
// in the manifest's skills: list. Callers map this to HTTP 409.
var ErrSkillNotFound = errors.New("yamledit: skill_path not found in manifest")

// ErrDuplicateScheduleName is returned when a schedule with sched.Name
// already exists under the target SkillEntry. Callers map this to HTTP 409.
var ErrDuplicateScheduleName = errors.New("yamledit: schedule with this name already exists under skill")

// AppendScheduleToSkill appends sched to the schedules: sequence under the
// SkillEntry whose path matches skillPath in the manifest YAML.
//
// Preserves comments, ordering, indentation, and quoting style of the
// surrounding document — only the inserted lines change in a textual diff.
//
// The marshaled schedule omits zero-valued optional fields so the diff
// stays minimal: empty maps, nil pointers, and zero ints are not emitted.
//
// If the SkillEntry has no schedules: key, one is created.
func AppendScheduleToSkill(yamlBytes []byte, skillPath string, sched *config.Schedule) ([]byte, error) {
	if sched == nil {
		return nil, fmt.Errorf("yamledit: nil schedule")
	}
	if skillPath == "" {
		return nil, fmt.Errorf("yamledit: empty skill_path")
	}

	// Parse the document while preserving comments / ordering / quoting.
	var doc yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil, fmt.Errorf("yamledit: parse: %w", err)
	}

	skillsSeq, err := findSkillsSequence(&doc)
	if err != nil {
		return nil, err
	}

	target, schedulesSeq, err := findSkillEntry(skillsSeq, skillPath)
	if err != nil {
		return nil, err
	}

	if hasScheduleNamed(schedulesSeq, sched.Name) {
		return nil, ErrDuplicateScheduleName
	}

	schedNode, err := scheduleToNode(sched)
	if err != nil {
		return nil, fmt.Errorf("yamledit: marshal schedule: %w", err)
	}

	if schedulesSeq == nil {
		// No schedules: key on this entry — create one.
		// target is the *yaml.Node for the SkillEntry (mapping).
		target.Content = append(target.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "schedules"},
			&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{schedNode}},
		)
	} else {
		schedulesSeq.Content = append(schedulesSeq.Content, schedNode)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("yamledit: marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("yamledit: close encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// findSkillsSequence returns the *yaml.Node for the top-level skills: list,
// or an error if the document shape is wrong.
func findSkillsSequence(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("yamledit: empty manifest")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yamledit: manifest root is not a mapping")
	}
	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Value == "skills" {
			if val.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("yamledit: skills: is not a sequence")
			}
			return val, nil
		}
	}
	return nil, fmt.Errorf("yamledit: manifest has no skills: key")
}

// findSkillEntry returns (skillEntryMappingNode, schedulesSeqNode_or_nil).
// schedulesSeq is nil when the SkillEntry has no schedules: key — caller
// is expected to add one.
func findSkillEntry(skillsSeq *yaml.Node, skillPath string) (*yaml.Node, *yaml.Node, error) {
	for _, entry := range skillsSeq.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		var (
			matchedPath  = false
			schedulesSeq *yaml.Node
		)
		for i := 0; i < len(entry.Content)-1; i += 2 {
			k := entry.Content[i]
			v := entry.Content[i+1]
			switch k.Value {
			case "path":
				if v.Value == skillPath {
					matchedPath = true
				}
			case "schedules":
				if v.Kind == yaml.SequenceNode {
					schedulesSeq = v
				}
			}
		}
		if matchedPath {
			return entry, schedulesSeq, nil
		}
	}
	return nil, nil, ErrSkillNotFound
}

func hasScheduleNamed(seq *yaml.Node, name string) bool {
	if seq == nil {
		return false
	}
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i < len(item.Content)-1; i += 2 {
			k := item.Content[i]
			v := item.Content[i+1]
			if k.Value == "name" && v.Value == name {
				return true
			}
		}
	}
	return false
}

// scheduleToNode marshals a *config.Schedule to a yaml.Node by routing
// through sigs.k8s.io/yaml (which honors the existing json tags), then
// re-parsing into a yaml.Node so the caller can splice into a tree.
//
// We can't go straight through yaml.v3 because config.Schedule uses
// json tags exclusively; yaml.v3 doesn't read json tags.
func scheduleToNode(s *config.Schedule) (*yaml.Node, error) {
	yamlBytes, err := sigsyaml.Marshal(s)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &n); err != nil {
		return nil, err
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) == 0 {
		return nil, fmt.Errorf("yamledit: marshaled schedule has no content")
	}
	return n.Content[0], nil
}
