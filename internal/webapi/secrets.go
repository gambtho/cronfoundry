package webapi

import (
	"encoding/json"
	"net/http"
)

type secretsHandler struct{ deps Deps }

type secretMeta struct {
	Name        string  `json:"name"`
	Version     int32   `json:"version"`
	LastUpdated string  `json:"last_updated"`
	LastUsed    *string `json:"last_used,omitempty"`
}

func (h *secretsHandler) list(w http.ResponseWriter, r *http.Request) {
	org, err := h.deps.Queries.GetFirstOrganization(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load org", "internal")
		return
	}
	rows, err := h.deps.Queries.ListSecretNames(r.Context(), org.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list secrets", "internal")
		return
	}
	out := make([]secretMeta, len(rows))
	for i, row := range rows {
		m := secretMeta{Name: row.Name, Version: row.Version}
		if row.UpdatedAt.Valid {
			s := row.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			m.LastUpdated = s
		}
		if row.LastUsedAt.Valid {
			s := row.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			m.LastUsed = &s
		}
		out[i] = m
	}
	writeJSON(w, http.StatusOK, out)
}

type secretWriteRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (h *secretsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req secretWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Value == "" {
		writeErr(w, http.StatusBadRequest, "name and value are required", "bad_request")
		return
	}
	if err := h.deps.Secrets.Put(r.Context(), req.Name, req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create secret", "internal")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

func (h *secretsHandler) rotate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Value == "" {
		writeErr(w, http.StatusBadRequest, "value is required", "bad_request")
		return
	}
	if err := h.deps.Secrets.Put(r.Context(), name, req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to rotate secret", "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (h *secretsHandler) delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.deps.Secrets.Delete(r.Context(), name); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete secret", "internal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
