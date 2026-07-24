package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const cpListNameMaxLen = 100

var validScopes = map[string]bool{"cluster": true, "namespace": true}
var validResourceTypes = map[string]bool{"ClusterPolicy": true, "Policy": true}

func HandleCreateClusterPolicy(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
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
		var in ClusterPolicyCreate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if in.Name == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		if in.ClusterID == uuid.Nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id required")
			return
		}
		if in.ResourceType == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "resource_type required")
			return
		}
		if !validResourceTypes[in.ResourceType] {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"resource_type must be ClusterPolicy or Policy")
			return
		}
		if in.Scope == "" {
			in.Scope = "cluster"
		}
		if !validScopes[strings.ToLower(in.Scope)] {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"scope must be cluster or namespace")
			return
		}
		if len(in.SpecRaw) == 0 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "spec_raw required")
			return
		}
		if in.ResourceType == "Policy" && in.NamespaceID == nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"namespace_id required when resource_type is Policy")
			return
		}
		if in.ResourceType == "ClusterPolicy" && in.NamespaceID != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"namespace_id must be omitted when resource_type is ClusterPolicy")
			return
		}
		if in.Action != nil {
			a := strings.ToLower(*in.Action)
			in.Action = &a
		}
		if in.Severity != nil {
			s := strings.ToLower(*in.Severity)
			in.Severity = &s
		}
		if in.FailurePolicy != nil {
			fp := titleCaseFailurePolicy(*in.FailurePolicy)
			in.FailurePolicy = &fp
		}

		cp := ClusterPolicyRow{
			ClusterID:       in.ClusterID,
			NamespaceID:     in.NamespaceID,
			Name:            in.Name,
			ResourceType:    in.ResourceType,
			Scope:           in.Scope,
			Description:     in.Description,
			Category:        in.Category,
			Severity:        in.Severity,
			Action:          in.Action,
			FailurePolicy:   in.FailurePolicy,
			Background:      in.Background,
			RuleTypes:       in.RuleTypes,
			RulesCount:      in.RulesCount,
			TargetResources: in.TargetResources,
			KeyExclusions:   in.KeyExclusions,
			Ready:           in.Ready,
			Annotations:     in.Annotations,
			SpecRaw:         in.SpecRaw,
			Source:          SourceAPI,
		}

		id, err := store.UpsertClusterPolicy(r.Context(), cp)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity", err.Error())
				return
			}
			slog.Error("create cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		created, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			slog.Error("read back created cluster policy", slog.String("id", id.String()), slog.Any("error", err))
			writeProblem(w, http.StatusCreated, "Created", "cluster policy created but read-back failed")
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func titleCaseFailurePolicy(s string) string {
	switch strings.ToLower(s) {
	case "fail":
		return "Fail"
	case "ignore":
		return "Ignore"
	default:
		return s
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
