// internal/webapi/runnow_test.go
package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSchedules_RunNow_ForwardsRunID(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "abc-123"})
	}))
	defer internalSrv.Close()

	h := &schedulesHandler{deps: Deps{APIBaseURL: internalSrv.URL}}
	r := httptest.NewRequest("POST", "/api/schedules/00000000-0000-0000-0000-000000000001/run-now", nil)
	r.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	h.runNow(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["run_id"] != "abc-123" {
		t.Errorf("run_id: %q", body["run_id"])
	}
}

func TestSchedules_RunNow_BadGateway_OnEmptyRunID(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer internalSrv.Close()
	h := &schedulesHandler{deps: Deps{APIBaseURL: internalSrv.URL}}
	r := httptest.NewRequest("POST", "/api/schedules/00000000-0000-0000-0000-000000000001/run-now", nil)
	r.SetPathValue("id", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	h.runNow(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}
}
