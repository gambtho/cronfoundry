package webapi

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// SQLC-generated row structs are emitted without JSON tags, so encoding them
// directly via writeJSON produces PascalCase keys ("Status", "StartedAt", ...)
// — which the React SPA, expecting snake_case, can't parse. Each handler that
// returns a list/object built from a sqlc row must therefore map to a
// hand-written DTO with explicit `json:"..."` tags. The helpers below
// encapsulate the parts that show up in every mapper: timestamp formatting,
// UUID-byte rendering, and JSON null-vs-empty-array discipline.

// toISOPtr formats a pgtype.Timestamptz as ISO 8601 (RFC3339Nano) when
// Valid; returns nil when NULL. Use for nullable timestamp columns.
func toISOPtr(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.Format(time.RFC3339Nano)
	return &s
}

// toISO formats a pgtype.Timestamptz as ISO 8601 (RFC3339Nano), assuming
// the column is NOT NULL. If the value is unexpectedly Invalid, returns "".
func toISO(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format(time.RFC3339Nano)
}

// uuidString renders a pgtype.UUID as a canonical string. The sqlc layer
// always Validates UUIDs that come back from queries, so we don't bother
// returning a nullable form here.
func uuidString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}
