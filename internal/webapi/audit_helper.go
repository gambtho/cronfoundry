package webapi

import (
	"context"
	"log/slog"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// auditLog is a package-local convenience for every mutating handler.
// It merges the actor into the entry before calling audit.Log, and
// emits a slog.Warn (not an error) if the audit write fails — we never
// fail a user action because auditing broke.
func auditLog(ctx context.Context, q *dbgen.Queries, actor string, e audit.Entry) {
	if e.Actor == "" {
		e.Actor = actor
	}
	if err := audit.Log(ctx, q, e); err != nil {
		slog.Warn("audit: log failed",
			"action", e.Action, "actor", actor, "err", err)
	}
}
