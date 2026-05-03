package scheduler

import (
	"testing"
	"time"
)

func TestTickClock_RecordAndRead(t *testing.T) {
	var c TickClock
	if !c.LastTickAt().IsZero() {
		t.Fatal("zero value should be zero time")
	}
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	c.Record(now)
	if got := c.LastTickAt(); !got.Equal(now) {
		t.Fatalf("got %v, want %v", got, now)
	}
}
