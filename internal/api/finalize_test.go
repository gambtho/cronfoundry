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

	"github.com/gambtho/cronfoundry/internal/token"
)

func TestFinalize_PersistsSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID: runID, OrgID: uuid.UUID(orgID.Bytes), ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

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

func TestFinalize_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	actualRun, orgID := seedRun(t, pool)
	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"status": "succeeded"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+actualRun.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
