package scheduler

import (
	"context"
	"fmt"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// SweepOrphans marks any non-terminal runs older than `timeout_sec + 300s`
// as failed with error_kind='shutdown'. Returns the number of rows updated.
//
// Called at serve startup (to recover runs stuck when the previous process
// died mid-execution) and periodically from the scheduler Loop.
func SweepOrphans(ctx context.Context, deps Deps) (int64, error) {
	q := dbgen.New(deps.Pool)
	n, err := q.OrphanSweep(ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler: SweepOrphans: %w", err)
	}
	return n, nil
}
