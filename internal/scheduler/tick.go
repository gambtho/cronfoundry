package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/cloud"
	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/token"
)

// Deps bundles the scheduler's collaborators.
type Deps struct {
	Pool         *pgxpool.Pool
	Signer       *token.Signer
	Dispatcher   cloud.JobDispatcher
	APIBaseURL   string // e.g. "http://127.0.0.1:8080"
	RunnerBinary string // absolute path; typically os.Executable()
}

// Stats summarizes one Tick's effects.
type Stats struct {
	Dispatched int
	Skipped    int
	Queued     int
	Errored    int
}

// Tick runs a single pass of the scheduler:
//  1. List due schedules.
//  2. For each: compute next_fire_at, idempotently insert a pending run,
//     update schedule.next_fire_at.
//  3. Apply overlap policy using the count of existing active runs.
//  4. Dispatch via cloud.JobDispatcher when decided.
//  5. Dispatch any already-pending rows not tied to a due schedule — manual
//     run-now triggers and queued scheduled runs whose prior run finished.
//  6. Run OrphanSweep to reclaim stalled runs.
//
// Stats fields are informational for logging; errors from individual
// schedules are logged but don't abort the whole tick.
func Tick(ctx context.Context, deps Deps) (Stats, error) {
	q := dbgen.New(deps.Pool)

	due, err := q.ListDueSchedulesWithSha(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("scheduler: Tick: list due: %w", err)
	}

	var stats Stats
	for _, sched := range due {
		if err := processOne(ctx, deps, sched, &stats); err != nil {
			stats.Errored++
			slog.Error("scheduler: Tick: schedule error",
				"schedule_id", uuid.UUID(sched.ID.Bytes).String(),
				"err", err)
		}
	}

	// Dispatch pending manual triggers + queued scheduled runs whose prior
	// run has terminated. Due-schedule processing above doesn't touch these:
	// manual rows aren't tied to next_fire_at, and queued rows were left
	// pending by a previous tick's overlap-policy decision.
	if err := dispatchPending(ctx, deps, &stats); err != nil {
		slog.Error("scheduler: Tick: dispatch pending failed", "err", err)
	}

	if _, err := q.OrphanSweep(ctx); err != nil {
		slog.Warn("scheduler: Tick: orphan sweep failed", "err", err)
	}

	return stats, nil
}

// processOne handles a single due schedule: advance next_fire_at, insert
// the pending run, apply overlap policy, dispatch.
func processOne(
	ctx context.Context,
	deps Deps,
	sched dbgen.ListDueSchedulesWithShaRow,
	stats *Stats,
) error {
	q := dbgen.New(deps.Pool)

	// Compute the next fire time relative to this one.
	thisFire := sched.NextFireAt.Time
	nextFire, err := NextFire(sched.Cron, sched.Timezone, thisFire)
	if err != nil {
		return fmt.Errorf("compute next fire: %w", err)
	}

	// Mint a placeholder token hash to satisfy the NOT NULL constraint on
	// runner_token_hash. The real hash gets set below once we sign the JWT.
	placeholderHash, err := randomHex(32)
	if err != nil {
		return fmt.Errorf("placeholder hash: %w", err)
	}

	// Insert the run. ON CONFLICT DO NOTHING means duplicate ticks (or
	// double-scheduling) collapse to one row.
	run, err := q.InsertRun(ctx, dbgen.InsertRunParams{
		OrgID:      sched.OrgID,
		ScheduleID: sched.ID,
		SkillSha:   sched.SkillSha,
		FireTime: pgtype.Timestamptz{
			Time:  thisFire,
			Valid: true,
		},
		FireReason:      "schedule",
		Actor:           nil,
		RunnerTokenHash: "pending:" + placeholderHash,
	})
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	// Advance the schedule's next_fire_at regardless of whether the insert
	// landed a new row (idempotency: a duplicate tick still advances once).
	if err := q.UpdateScheduleNextFireAt(ctx, dbgen.UpdateScheduleNextFireAtParams{
		ID:         sched.ID,
		NextFireAt: pgtype.Timestamptz{Time: nextFire, Valid: true},
	}); err != nil {
		return fmt.Errorf("update next_fire_at: %w", err)
	}

	// If ON CONFLICT fired, our query returned the pre-existing row with
	// Inserted=false. That means another tick won the race — we have no
	// dispatch work for this fire_time.
	if !run.Inserted {
		return nil
	}

	// Apply overlap policy: count currently-active runs for this schedule,
	// excluding the one we just inserted.
	active, err := q.ListActiveRunsForSchedule(ctx, sched.ID)
	if err != nil {
		return fmt.Errorf("list active: %w", err)
	}
	activeCount := 0
	for _, r := range active {
		if r.ID != run.ID {
			activeCount++
		}
	}

	decision := Decide(Policy(sched.OverlapPolicy), activeCount)
	switch decision {
	case DecisionSkip:
		if err := q.DeleteRun(ctx, run.ID); err != nil {
			return fmt.Errorf("delete skipped: %w", err)
		}
		stats.Skipped++
		return nil
	case DecisionQueue:
		// Leave the pending row in place. dispatchPending (invoked at the
		// end of Tick, and on every subsequent tick) will pick it up once
		// the prior run terminates. See TODO(P2d) in overlap.go.
		stats.Queued++
		return nil
	case DecisionDispatch:
		// fall through
	}

	if err := dispatchRun(ctx, deps, dispatchArgs{
		RunID:      run.ID,
		OrgID:      sched.OrgID,
		TimeoutSec: sched.TimeoutSec,
		SecretRefs: config.CollectSecretRefs(sched.DestinationsJson, sched.EnvJson, sched.LlmSecretRef),
	}); err != nil {
		return err
	}

	stats.Dispatched++
	return nil
}

// dispatchArgs bundles the inputs shared between processOne's happy path and
// dispatchPending's catch-up path. Keeps the two call sites behaviorally
// identical (sign JWT, persist hash, dispatch, mark running).
type dispatchArgs struct {
	RunID      pgtype.UUID
	OrgID      pgtype.UUID
	TimeoutSec int32
	SecretRefs []string
}

// dispatchRun signs a JWT for the run, persists its hash, dispatches the
// runner subprocess, and flips the row to 'running'. Returns an error only
// on failures that should abort the caller's loop iteration — the
// SetRunRunning step is best-effort and logged on failure (a runner that
// already finalized by the time we get here is benign).
func dispatchRun(ctx context.Context, deps Deps, args dispatchArgs) error {
	timeout := time.Duration(args.TimeoutSec) * time.Second
	tok, hash, err := deps.Signer.Sign(token.RunClaims{
		RunID:      uuid.UUID(args.RunID.Bytes),
		OrgID:      uuid.UUID(args.OrgID.Bytes),
		SecretRefs: args.SecretRefs,
		ExpiresAt:  time.Now().Add(timeout + 120*time.Second),
	})
	if err != nil {
		return fmt.Errorf("sign token: %w", err)
	}

	// Persist the hash so /internal/* can bind the bearer to this run.
	if _, err := deps.Pool.Exec(ctx,
		`UPDATE run SET runner_token_hash = $1 WHERE id = $2`,
		hash, args.RunID); err != nil {
		return fmt.Errorf("update token hash: %w", err)
	}

	spec := cloud.DispatchSpec{
		BinaryPath: deps.RunnerBinary,
		Args:       []string{"runner", "--run-id", uuid.UUID(args.RunID.Bytes).String()},
		Env: []string{
			"CRONFOUNDRY_API_URL=" + deps.APIBaseURL,
			"CRONFOUNDRY_RUN_ID=" + uuid.UUID(args.RunID.Bytes).String(),
			"CRONFOUNDRY_RUN_TOKEN=" + tok,
		},
	}
	h, err := deps.Dispatcher.Dispatch(ctx, spec)
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}

	// Flip to 'running' and record the pid. The WHERE clause in SetRunRunning
	// only matches status='pending' — if the runner already finalized by the
	// time we get here, this is a no-op and we swallow the pgx.ErrNoRows.
	q := dbgen.New(deps.Pool)
	pid := int32(h.PID())
	if _, err := q.SetRunRunning(ctx, dbgen.SetRunRunningParams{
		ID:        args.RunID,
		RunnerPid: &pid,
	}); err != nil {
		slog.Warn("scheduler: SetRunRunning failed (non-fatal)",
			"run_id", uuid.UUID(args.RunID.Bytes).String(),
			"err", err)
	}
	return nil
}

// dispatchPending scans for pending runs that are ready for dispatch but
// which the due-schedule loop wouldn't reach:
//
//   - fire_reason='manual' rows inserted by POST /internal/schedules/:id/run-now
//   - fire_reason='schedule' rows left pending by a prior tick's
//     Queue decision, whose prior run has since terminated
//
// Runs are processed oldest-first. Errors on individual runs are logged but
// don't abort the sweep.
func dispatchPending(ctx context.Context, deps Deps, stats *Stats) error {
	rows, err := deps.Pool.Query(ctx, `
		SELECT r.id, r.org_id,
		       s.timeout_sec, s.destinations_json, s.env_json, s.llm_secret_ref
		FROM run r
		JOIN schedule s ON s.id = r.schedule_id
		WHERE r.status = 'pending'
		  AND s.enabled = true
		  AND (
		      r.fire_reason = 'manual'
		      OR (
		          r.fire_reason = 'schedule'
		          AND s.overlap_policy = 'queue'
		          AND NOT EXISTS (
		              SELECT 1 FROM run r2
		              WHERE r2.schedule_id = r.schedule_id
		                AND r2.id != r.id
		                AND r2.status IN ('pending','running')
		                AND r2.created_at < r.created_at
		          )
		      )
		  )
		ORDER BY r.created_at ASC
	`)
	if err != nil {
		return fmt.Errorf("query pending: %w", err)
	}
	defer rows.Close()

	type pendingRow struct {
		ID         pgtype.UUID
		OrgID      pgtype.UUID
		TimeoutSec int32
		SecretRefs []string
	}
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		var destsJSON, envJSON []byte
		var llmRef *string
		if err := rows.Scan(&r.ID, &r.OrgID, &r.TimeoutSec, &destsJSON, &envJSON, &llmRef); err != nil {
			slog.Error("scheduler: dispatchPending: scan failed", "err", err)
			continue
		}
		r.SecretRefs = config.CollectSecretRefs(destsJSON, envJSON, llmRef)
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iter pending: %w", err)
	}

	for _, r := range pending {
		if err := dispatchRun(ctx, deps, dispatchArgs{
			RunID:      r.ID,
			OrgID:      r.OrgID,
			TimeoutSec: r.TimeoutSec,
			SecretRefs: r.SecretRefs,
		}); err != nil {
			slog.Error("scheduler: dispatchPending: dispatch failed",
				"run_id", uuid.UUID(r.ID.Bytes).String(),
				"err", err)
			stats.Errored++
			continue
		}
		stats.Dispatched++
	}
	return nil
}


func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
