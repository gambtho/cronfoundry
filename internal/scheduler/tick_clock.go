package scheduler

import (
	"sync/atomic"
	"time"
)

// TickClock records the wall-clock time of the most recent successful
// scheduler tick. Read-side is lock-free; safe for concurrent readers.
type TickClock struct {
	unixNano atomic.Int64
}

func (c *TickClock) Record(t time.Time) { c.unixNano.Store(t.UnixNano()) }

func (c *TickClock) LastTickAt() time.Time {
	n := c.unixNano.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}
