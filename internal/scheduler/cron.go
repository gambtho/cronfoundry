// Package scheduler implements the tick loop that turns due schedules into
// run rows and dispatches them via the JobDispatcher.
package scheduler

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// NextFire returns the first time > base at which the given cron expression
// will fire in the given IANA timezone. The returned time is in UTC.
func NextFire(expr, tz string, base time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: NextFire: load timezone %q: %w", tz, err)
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: NextFire: parse %q: %w", expr, err)
	}
	return schedule.Next(base.In(loc)).UTC(), nil
}
