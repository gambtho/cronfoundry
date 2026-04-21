package webapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type auditHandler struct{ deps Deps }

// auditEntry is the DTO returned by GET /api/audit. It normalizes the
// sqlc row into a stable JSON shape (pgtype.UUID → "" for missing
// target_id, *string → "" for nullable text, raw jsonb → decoded value).
type auditEntry struct {
	ID         int64  `json:"id"`
	Ts         string `json:"ts"` // RFC3339 UTC
	Actor      string `json:"actor,omitempty"`
	Action     string `json:"action"`
	TargetKind string `json:"target_kind,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Detail     any    `json:"detail"`
}

const (
	auditDefaultLimit = 100
	auditMaxLimit     = 500
)

// list implements GET /api/audit?limit=N&offset=M.
//
// Admin-only (registered behind adminOnly middleware). Returns newest
// rows first. Pagination is offset-based — acceptable for operator
// review; if row counts grow large we can switch to keyset.
func (h *auditHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}

	// ParseInt with bitSize=32 so values above math.MaxInt32 fail cleanly
	// on 64-bit builds instead of wrapping into a negative int32.
	limit := int32(auditDefaultLimit)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			if n > auditMaxLimit {
				n = auditMaxLimit
			}
			limit = int32(n)
		}
	}
	offset := int32(0)
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	rows, err := h.deps.Queries.ListAuditLogByOrg(r.Context(), dbgen.ListAuditLogByOrgParams{
		OrgID:  org.ID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list audit log", "internal")
		return
	}

	out := make([]auditEntry, len(rows))
	for i, row := range rows {
		e := auditEntry{
			ID:     row.ID,
			Action: row.Action,
		}
		if row.Ts.Valid {
			e.Ts = row.Ts.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if row.Actor != nil {
			e.Actor = *row.Actor
		}
		if row.TargetKind != nil {
			e.TargetKind = *row.TargetKind
		}
		if row.TargetID.Valid {
			e.TargetID = uuid.UUID(row.TargetID.Bytes).String()
		}
		// Decode detail so the JSON is nested, not a string.
		if len(row.DetailJson) > 0 {
			_ = json.Unmarshal(row.DetailJson, &e.Detail)
		}
		out[i] = e
	}
	writeJSON(w, http.StatusOK, out)
}
