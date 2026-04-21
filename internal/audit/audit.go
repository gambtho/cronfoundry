// Package audit centralizes audit_log writes so mutating handlers can
// record their actions with a single call. Write failures are returned
// so the caller decides whether to fail the operation or carry on
// (typical webapi call sites swallow the error after a slog.Warn —
// audit is advisory, not load-bearing).
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// Entry is one audit event ready to persist.
//
// Zero-value fields map to NULL:
//   - Actor="" → NULL (use "system" when the caller is not a user)
//   - TargetKind="" → NULL
//   - TargetID=nil → NULL
//   - Detail=nil → JSON {}
type Entry struct {
	OrgID      pgtype.UUID
	Actor      string
	Action     string
	TargetKind string
	TargetID   *uuid.UUID
	Detail     map[string]any
}

// Log persists e. Returns the underlying DB error if any; does not log
// internally — the caller's logger already has request context.
func Log(ctx context.Context, q *dbgen.Queries, e Entry) error {
	detail := []byte(`{}`)
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("audit: marshal detail: %w", err)
		}
		detail = b
	}
	var target pgtype.UUID
	if e.TargetID != nil {
		target = pgtype.UUID{Bytes: *e.TargetID, Valid: true}
	}
	return q.InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		OrgID:      e.OrgID,
		Actor:      strPtr(e.Actor),
		Action:     e.Action,
		TargetKind: strPtr(e.TargetKind),
		TargetID:   target,
		DetailJson: detail,
	})
}

// strPtr returns nil for an empty string; callers want NULL (not "")
// for the nullable columns.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
