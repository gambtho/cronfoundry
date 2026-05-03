package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Loop runs Tick on a fixed cadence until ctx is canceled. Individual Tick
// errors are logged and the loop continues — only ctx cancellation or a
// panic stops the loop.
//
// The first tick runs immediately at Loop start, so operators (and tests)
// don't wait a full cadence before seeing any scheduler activity.
//
// Returns ctx.Err() when ctx expires.
func Loop(ctx context.Context, cadence time.Duration, deps Deps) error {
	// Initial tick.
	tickOnce(ctx, deps)

	ticker := time.NewTicker(cadence)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			tickOnce(ctx, deps)
		}
	}
}

// tickOnce runs one Tick and logs on success or failure. Never panics — any
// panic in Tick would take down the loop.
func tickOnce(ctx context.Context, deps Deps) {
	stats, err := Tick(ctx, deps)
	if err != nil {
		slog.Error("scheduler: Loop: tick failed", "err", err)
		return
	}
	if deps.Clock != nil {
		deps.Clock.Record(time.Now())
	}
	// Log only if something happened; avoid noise when idle.
	if stats.Dispatched > 0 || stats.Skipped > 0 || stats.Queued > 0 || stats.Errored > 0 {
		slog.Info("scheduler: Loop: tick",
			"dispatched", stats.Dispatched,
			"skipped", stats.Skipped,
			"queued", stats.Queued,
			"errored", stats.Errored)
	}
}
