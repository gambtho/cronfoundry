package yamledit

import (
	"errors"
	"testing"
)

func TestAppendScheduleToSkill_NotImplemented(t *testing.T) {
	_, err := AppendScheduleToSkill([]byte("version: 1\nskills: []\n"), "skills/x", nil)
	if err == nil {
		t.Fatalf("expected error from stub, got nil")
	}
	// Stubs must surface a typed sentinel once implemented; for now any error is fine.
	if errors.Is(err, ErrSkillNotFound) || errors.Is(err, ErrDuplicateScheduleName) {
		t.Fatalf("unexpected sentinel from stub: %v", err)
	}
}
