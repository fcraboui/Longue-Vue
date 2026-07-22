package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const cpListNameMaxLen = 100

func HandleCreateClusterPolicy(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		var cp ClusterPolicyRow
		if err := json.NewDecoder(r.Body).Decode(&cp); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if cp.Name == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		if cp.ClusterID == uuid.Nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id required")
			return
		}
		id, err := store.UpsertClusterPolicy(r.Context(), cp)
		if err != nil {
			slog.Error("create cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		created, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusCreated, map[string]any{"id": id})
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func HandleListClusterPolicies(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		settings, err := store.GetSettings(r.Context())
		if err != nil {
			slog.Error("settings unavailable", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "settings unavailable", "")
			return
		}
		if !settings.PoliciesEnabled {
			writeProblem(w, http.StatusConflict, "policies disabled",
				"enable policies_enabled in admin settings to use this endpoint")
			return
		}
		q := r.URL.Query()

		var filter ClusterPolicyListFilter
		if raw := q.Get("cluster_id"); raw != "" {
			id, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id must be a valid UUID")
				return
			}
			filter.ClusterID = &id
		}
		if raw := q.Get("namespace_id"); raw != "" {
			id, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "namespace_id must be a valid UUID")
				return
			}
			filter.NamespaceID = &id
		}
		if v := q.Get("name"); v != "" {
			if len(v) > cpListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}
		if v := q.Get("resource_type"); v != "" {
			filter.ResourceType = &v
		}
		if v := q.Get("action"); v != "" {
			filter.Action = &v
		}
		if v := q.Get("severity"); v != "" {
			filter.Severity = &v
		}
		if v := q.Get("failure_policy"); v != "" {
			filter.FailurePolicy = &v
		}
		if v := q.Get("category"); v != "" {
			filter.Category = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListClusterPolicies(r.Context(), filter, page)
		if err != nil {
			writeListError(w, "list cluster policies", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
			respKeyNextCursor: next,
		})
	}
}

func HandleGetClusterPolicy(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		settings, err := store.GetSettings(r.Context())
		if err != nil {
			slog.Error("settings unavailable", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "settings unavailable", "")
			return
		}
		if !settings.PoliciesEnabled {
			writeProblem(w, http.StatusConflict, "policies disabled",
				"enable policies_enabled in admin settings to use this endpoint")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		cp, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, cp)
	}
}
