package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextFire_EveryMinute(t *testing.T) {
	base := time.Date(2026, 4, 20, 9, 0, 30, 0, time.UTC)
	next, err := NextFire("* * * * *", "UTC", base)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 9, 1, 0, 0, time.UTC), next)
}

func TestNextFire_HandlesTimezone(t *testing.T) {
	// 09:00 Pacific in April = 16:00 UTC (PDT, UTC-7). Start at 15:59 UTC
	// (= 08:59 PT); next fire should be 16:00 UTC (= 09:00 PT).
	base := time.Date(2026, 4, 20, 15, 59, 0, 0, time.UTC)
	next, err := NextFire("0 9 * * *", "America/Los_Angeles", base)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 16, 0, 0, 0, time.UTC), next)
}

func TestNextFire_BadExpr(t *testing.T) {
	_, err := NextFire("nonsense", "UTC", time.Now())
	require.Error(t, err)
}

func TestNextFire_BadTimezone(t *testing.T) {
	_, err := NextFire("* * * * *", "Not/A/Zone", time.Now())
	require.Error(t, err)
}

func TestNextFire_ReturnsUTC(t *testing.T) {
	// Result should always be in UTC regardless of the input timezone.
	next, err := NextFire("0 9 * * *", "America/Los_Angeles",
		time.Date(2026, 4, 20, 15, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, time.UTC, next.Location())
}
