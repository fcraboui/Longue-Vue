package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const prListNameMaxLen = 100

func HandleListPolicyReports(store Store) http.HandlerFunc {
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

		var filter PolicyReportListFilter
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
			if len(v) > prListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}
		if v := q.Get("scope_kind"); v != "" {
			filter.ScopeKind = &v
		}
		if v := q.Get("scope_name"); v != "" {
			filter.ScopeName = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListPolicyReports(r.Context(), filter, page)
		if err != nil {
			writeListError(w, "list policy reports", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
			respKeyNextCursor: next,
		})
	}
}

func HandleGetPolicyReport(store Store) http.HandlerFunc {
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
		pr, err := store.GetPolicyReport(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get policy report", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, pr)
	}
}
