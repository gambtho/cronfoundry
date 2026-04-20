package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/cloud"
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
//  5. Run OrphanSweep to reclaim stalled runs.
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

	// If ON CONFLICT skipped the insert, `run.ID` will be the zero value.
	if !run.ID.Valid {
		// Another tick already inserted; no dispatch work for us.
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
		stats.Queued++
		return nil
	case DecisionDispatch:
		// fall through
	}

	// Sign a per-run JWT.
	timeout := time.Duration(sched.TimeoutSec) * time.Second
	tok, hash, err := deps.Signer.Sign(token.RunClaims{
		RunID:      uuid.UUID(run.ID.Bytes),
		OrgID:      uuid.UUID(sched.OrgID.Bytes),
		SecretRefs: secretRefsFor(sched),
		ExpiresAt:  time.Now().Add(timeout + 120*time.Second),
	})
	if err != nil {
		return fmt.Errorf("sign token: %w", err)
	}

	// Update runner_token_hash on the run so /internal/* can validate.
	if _, err := deps.Pool.Exec(ctx,
		`UPDATE run SET runner_token_hash = $1 WHERE id = $2`,
		hash, run.ID); err != nil {
		return fmt.Errorf("update token hash: %w", err)
	}

	// Dispatch.
	spec := cloud.DispatchSpec{
		BinaryPath: deps.RunnerBinary,
		Args:       []string{"runner", "--run-id", uuid.UUID(run.ID.Bytes).String()},
		Env: []string{
			"CRONFOUNDRY_API_URL=" + deps.APIBaseURL,
			"CRONFOUNDRY_RUN_ID=" + uuid.UUID(run.ID.Bytes).String(),
			"CRONFOUNDRY_RUN_TOKEN=" + tok,
		},
	}
	if _, err := deps.Dispatcher.Dispatch(ctx, spec); err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}

	stats.Dispatched++
	return nil
}

// secretRefsFor returns the list of secret names a run may fetch via
// /internal/secrets. Parses the destinations_json + env_json to collect
// every { "secret": "name" } reference. For MVP we use a conservative
// string scan — the JSON is small and the secret-ref shape is well-known.
//
// TODO(P2d): extract to internal/config/secretrefs.go once other sites need it.
func secretRefsFor(sched dbgen.ListDueSchedulesWithShaRow) []string {
	seen := map[string]struct{}{}
	scanJSON := func(b []byte) {
		// Naive but safe: walk JSON looking for "secret":"<name>" entries.
		// A full config-parser round-trip would be cleaner but requires
		// JSON → Destination parsing we haven't exposed at the DB layer.
		s := string(b)
		i := 0
		for {
			idx := strings.Index(s[i:], `"secret"`)
			if idx < 0 {
				break
			}
			idx += i
			j := idx + len(`"secret"`)
			// Skip whitespace + colon.
			for j < len(s) && (s[j] == ' ' || s[j] == ':' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j >= len(s) || s[j] != '"' {
				break
			}
			j++ // past opening quote
			end := strings.IndexByte(s[j:], '"')
			if end < 0 {
				break
			}
			end += j
			name := s[j:end]
			if name != "" {
				seen[name] = struct{}{}
			}
			i = end + 1
		}
	}
	scanJSON(sched.DestinationsJson)
	scanJSON(sched.EnvJson)
	if sched.LlmSecretRef != nil && *sched.LlmSecretRef != "" {
		seen[*sched.LlmSecretRef] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
