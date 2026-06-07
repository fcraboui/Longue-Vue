package api

// Hand-written admin CRUD handlers for endpoint groups (ADR-0036). Endpoint
// groups are operator-curated named CIDR sets referenced by flow references
// and the flow-matrix synthesizer. Routes are wired in a later task; this file
// only defines the http.HandlerFunc factories. Store-error mapping reuses the
// package's per-handler errors.Is style (ErrNotFound→404, else 500).

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// HandleListEndpointGroups — read scope. GET /v1/admin/endpoint-groups.
func HandleListEndpointGroups(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		egs, err := store.ListEndpointGroups(r.Context())
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "list endpoint groups", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": egs})
	}
}

// HandleGetEndpointGroup — read scope. GET /v1/admin/endpoint-groups/{id}.
func HandleGetEndpointGroup(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid id", err.Error())
			return
		}
		eg, err := store.GetEndpointGroup(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "endpoint group not found", err.Error())
				return
			}
			writeProblem(w, http.StatusInternalServerError, "get endpoint group", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, eg)
	}
}

// HandleCreateEndpointGroup — admin scope. POST /v1/admin/endpoint-groups.
func HandleCreateEndpointGroup(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in EndpointGroupInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid body", err.Error())
			return
		}
		eg, err := store.CreateEndpointGroup(r.Context(), in, callerUserID(r))
		if err != nil {
			if errors.Is(err, ErrConflict) {
				writeProblem(w, http.StatusConflict, "endpoint group name already exists", err.Error())
				return
			}
			writeProblem(w, http.StatusInternalServerError, "create endpoint group", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, eg)
	}
}

// HandleUpdateEndpointGroup — admin scope. PATCH /v1/admin/endpoint-groups/{id}.
func HandleUpdateEndpointGroup(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid id", err.Error())
			return
		}
		var in EndpointGroupInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid body", err.Error())
			return
		}
		eg, err := store.UpdateEndpointGroup(r.Context(), id, in)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "endpoint group not found", err.Error())
				return
			}
			if errors.Is(err, ErrConflict) {
				writeProblem(w, http.StatusConflict, "endpoint group name already exists", err.Error())
				return
			}
			writeProblem(w, http.StatusInternalServerError, "update endpoint group", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, eg)
	}
}

// HandleDeleteEndpointGroup — admin scope. DELETE /v1/admin/endpoint-groups/{id}.
func HandleDeleteEndpointGroup(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid id", err.Error())
			return
		}
		if err := store.DeleteEndpointGroup(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "endpoint group not found", err.Error())
				return
			}
			writeProblem(w, http.StatusInternalServerError, "delete endpoint group", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
