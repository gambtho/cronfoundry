package scheduler

// Policy is the overlap policy declared on a schedule.
type Policy string

const (
	PolicySkip       Policy = "skip"
	PolicyQueue      Policy = "queue"
	PolicyConcurrent Policy = "concurrent"
)

// Decision tells the scheduler what to do with a freshly-inserted pending run.
type Decision int

const (
	DecisionDispatch Decision = iota // proceed to dispatch
	DecisionSkip                     // delete the pending row; don't dispatch
	DecisionQueue                    // leave the pending row for a later tick
)

// Decide applies the overlap policy given the count of non-terminal runs for
// the same schedule (not including the row we just inserted).
//
// "skip" (default) → dispatch only if no active runs; otherwise skip
// "queue" → dispatch only if no active runs; otherwise queue for next tick
// "concurrent" → always dispatch
//
// Empty or unknown policies fail closed to the skip semantics.
//
// Queued runs are left pending by processOne and picked up by
// tick.dispatchPending on a subsequent tick once prior runs finish. The
// split is deliberate: Tick's primary loop only walks schedules whose
// next_fire_at is due, so a run that was queued (and has no further
// scheduled fire until its cron boundary) would otherwise never get picked
// up. The trade-off is a one-tick latency between prior-run-finish and
// queued-run-dispatch; a dedicated queue-drain loop would close that gap
// but is not worth the complexity at MVP scale.
func Decide(policy Policy, activeCount int) Decision {
	switch policy {
	case PolicyConcurrent:
		return DecisionDispatch
	case PolicyQueue:
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionQueue
	case PolicySkip, Policy(""):
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionSkip
	default:
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionSkip
	}
}
