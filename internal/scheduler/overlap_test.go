package scheduler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecide_Skip_NoActive(t *testing.T) {
	assert.Equal(t, DecisionDispatch, Decide(PolicySkip, 0))
}

func TestDecide_Skip_WithActive(t *testing.T) {
	assert.Equal(t, DecisionSkip, Decide(PolicySkip, 1))
}

func TestDecide_Queue_NoActive(t *testing.T) {
	assert.Equal(t, DecisionDispatch, Decide(PolicyQueue, 0))
}

func TestDecide_Queue_WithActive(t *testing.T) {
	assert.Equal(t, DecisionQueue, Decide(PolicyQueue, 1))
}

func TestDecide_Concurrent_AlwaysDispatches(t *testing.T) {
	assert.Equal(t, DecisionDispatch, Decide(PolicyConcurrent, 0))
	assert.Equal(t, DecisionDispatch, Decide(PolicyConcurrent, 5))
}

func TestDecide_EmptyPolicyDefaultsToSkip(t *testing.T) {
	// Empty (unset in YAML) means "use default"; default is skip.
	assert.Equal(t, DecisionDispatch, Decide(Policy(""), 0))
	assert.Equal(t, DecisionSkip, Decide(Policy(""), 1))
}

func TestDecide_UnknownPolicyFailsClosed(t *testing.T) {
	assert.Equal(t, DecisionDispatch, Decide(Policy("weird"), 0))
	assert.Equal(t, DecisionSkip, Decide(Policy("weird"), 1))
}
