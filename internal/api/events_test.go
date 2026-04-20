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

func TestEvents_PersistsBatch(t *testing.T) {
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
		"events": []map[string]any{
			{"type": "llm.start", "level": "info", "payload": map[string]string{"model": "gpt-4o-mini"}},
			{"type": "publish.slack.ok", "level": "info", "payload": map[string]string{"http": "200"}},
		},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/events", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM run_event WHERE run_id = $1`, pgtype.UUID{Bytes: runID, Valid: true}).Scan(&count))
	assert.Equal(t, 2, count)
}

func TestEvents_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	actualRun, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(), // different from actualRun
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	buf, _ := json.Marshal(map[string]any{"events": []map[string]any{{"type": "x", "level": "info"}}})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+actualRun.String()+"/events", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestEvents_RejectsBadLevel(t *testing.T) {
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

	buf, _ := json.Marshal(map[string]any{"events": []map[string]any{{"type": "x", "level": "critical"}}})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/events", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEvents_DefaultsLevelToInfo(t *testing.T) {
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

	// Event with no level field.
	buf, _ := json.Marshal(map[string]any{"events": []map[string]any{{"type": "x"}}})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/events", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	var level string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT level FROM run_event WHERE run_id = $1`, pgtype.UUID{Bytes: runID, Valid: true}).Scan(&level))
	assert.Equal(t, "info", level)
}
