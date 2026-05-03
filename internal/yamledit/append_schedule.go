// Package yamledit edits cronfoundry.yaml manifests with comment- and
// formatting-preserving precision via yaml.v3's Node API. Round-trips that
// go through gopkg.in/yaml.v3's *yaml.Node preserve comments, anchors,
// ordering, and source indentation; only the inserted lines change in a
// textual diff. config.ParseManifest still owns whole-manifest validation.
package yamledit

import (
	"errors"

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
	return nil, errors.New("yamledit: not implemented")
}
