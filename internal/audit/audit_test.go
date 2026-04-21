package audit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

func TestLog_WritesRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	ctx := context.Background()
	q := dbgen.New(pool)

	org, err := q.CreateOrganization(ctx, "test")
	require.NoError(t, err)

	targetID := uuid.New()
	err = Log(ctx, q, Entry{
		OrgID:      org.ID,
		Actor:      "alice",
		Action:     "schedule.pause",
		TargetKind: "schedule",
		TargetID:   &targetID,
		Detail:     map[string]any{"schedule_name": "monday", "enabled": false},
	})
	require.NoError(t, err)

	rows, err := q.ListAuditLogByOrg(ctx, dbgen.ListAuditLogByOrgParams{
		OrgID:  org.ID,
		Limit:  10,
		Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	got := rows[0]
	require.NotNil(t, got.Actor)
	assert.Equal(t, "alice", *got.Actor)
	assert.Equal(t, "schedule.pause", got.Action)
	require.NotNil(t, got.TargetKind)
	assert.Equal(t, "schedule", *got.TargetKind)
	assert.True(t, got.TargetID.Valid)
	assert.Equal(t, targetID[:], got.TargetID.Bytes[:])

	var detail map[string]any
	require.NoError(t, json.Unmarshal(got.DetailJson, &detail))
	assert.Equal(t, "monday", detail["schedule_name"])
	assert.Equal(t, false, detail["enabled"])
}

func TestLog_OmitsNilTargetAndEmptyDetail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	ctx := context.Background()
	q := dbgen.New(pool)

	org, err := q.CreateOrganization(ctx, "test")
	require.NoError(t, err)

	err = Log(ctx, q, Entry{
		OrgID:  org.ID,
		Actor:  "bob",
		Action: "user.login",
		// TargetKind empty, TargetID nil, Detail nil
	})
	require.NoError(t, err)

	rows, err := q.ListAuditLogByOrg(ctx, dbgen.ListAuditLogByOrgParams{
		OrgID: org.ID, Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	got := rows[0]
	assert.Nil(t, got.TargetKind, "empty TargetKind should persist as NULL")
	assert.False(t, got.TargetID.Valid, "nil TargetID should persist as NULL uuid")
	assert.Equal(t, "{}", string(got.DetailJson))
}

func TestLog_AnonymousActor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	ctx := context.Background()
	q := dbgen.New(pool)

	org, err := q.CreateOrganization(ctx, "test")
	require.NoError(t, err)

	err = Log(ctx, q, Entry{
		OrgID: org.ID,
		// Actor empty → should persist as NULL
		Action: "system.bootstrap",
	})
	require.NoError(t, err)

	rows, err := q.ListAuditLogByOrg(ctx, dbgen.ListAuditLogByOrgParams{
		OrgID: org.ID, Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].Actor, "empty Actor should persist as NULL")
}
