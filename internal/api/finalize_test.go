package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestFinalize_PersistsSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{
		"status":               "succeeded",
		"duration_ms":          1234,
		"tokens_in":            400,
		"tokens_out":           120,
		"cost_cents":           1,
		"writeback_commit_sha": "abc1234",
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify DB state.
	var status string
	var durationMs *int32
	var tokensIn *int32
	var wbSha *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, duration_ms, tokens_in, writeback_commit_sha FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status, &durationMs, &tokensIn, &wbSha))
	assert.Equal(t, "succeeded", status)
	require.NotNil(t, durationMs)
	assert.EqualValues(t, 1234, *durationMs)
	require.NotNil(t, tokensIn)
	assert.EqualValues(t, 400, *tokensIn)
	require.NotNil(t, wbSha)
	assert.Equal(t, "abc1234", *wbSha)

	// finished_at should be set.
	var finishedAt *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT finished_at FROM run WHERE id = $1`, pgtype.UUID{Bytes: runID, Valid: true}).Scan(&finishedAt))
	assert.NotNil(t, finishedAt)
}

func TestFinalize_PersistsFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{
		"status":     "failed",
		"error_kind": "llm",
		"error_msg":  "429 rate limit",
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var status string
	var errKind, errMsg *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, error_kind, error_msg FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status, &errKind, &errMsg))
	assert.Equal(t, "failed", status)
	require.NotNil(t, errKind)
	assert.Equal(t, "llm", *errKind)
	require.NotNil(t, errMsg)
	assert.Equal(t, "429 rate limit", *errMsg)
}

func TestFinalize_RejectsInvalidStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"status": "weird"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestFinalize_RejectsNegativeAccounting pins the MAJ-6 fix: runners must
// not be able to log negative token counts, durations, or costs.
func TestFinalize_RejectsNegativeAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	fields := []string{"duration_ms", "tokens_in", "tokens_out", "cost_cents"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			body := map[string]any{"status": "succeeded", field: -1}
			buf, _ := json.Marshal(body)
			req, _ := http.NewRequest("POST",
				ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := ts.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
				"negative %s must be rejected", field)
		})
	}
}

// TestFinalize_RejectsAlreadyTerminal pins the MAJ-7 fix: a crashed-then-
// restarted runner can't clobber the original terminal state.
func TestFinalize_RejectsAlreadyTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	// Force the run into a terminal state directly.
	_, err := pool.Exec(context.Background(),
		`UPDATE run SET status='succeeded', finished_at=now() WHERE id=$1`,
		pgtype.UUID{Bytes: runID, Valid: true})
	require.NoError(t, err)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{"status": "failed", "error_msg": "restart"}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST",
		ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// The original status must be unchanged.
	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM run WHERE id = $1`,
		pgtype.UUID{Bytes: runID, Valid: true}).Scan(&status))
	assert.Equal(t, "succeeded", status)
}

func TestFinalize_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Sign for runA (seeded, hash bound); POST to runB's finalize URL so
	// the middleware accepts the token and the handler's URL-vs-claim
	// guard fires with 403.
	runA, orgID := seedRun(t, pool)
	runB, _ := seedRunInOrg(t, pool, orgID)
	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runA,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runA, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"status": "succeeded"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runB.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
