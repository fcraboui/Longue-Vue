package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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
			if in.ResourceType == "Policy" {
				in.Scope = "namespace"
			} else {
				in.Scope = "cluster"
			}
		}
		in.Scope = strings.ToLower(in.Scope)
		if !validScopes[in.Scope] {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"scope must be cluster or namespace")
			return
		}
		if in.ResourceType == "Policy" && in.Scope != "namespace" {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"scope must be namespace when resource_type is Policy")
			return
		}
		if in.ResourceType == "ClusterPolicy" && in.Scope != "cluster" {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"scope must be cluster when resource_type is ClusterPolicy")
			return
		}
		if len(in.SpecRaw) == 0 || string(in.SpecRaw) == "null" {
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
		if in.NamespaceID != nil {
			ns, nsErr := store.GetNamespace(r.Context(), *in.NamespaceID)
			if nsErr != nil {
				if errors.Is(nsErr, ErrNotFound) {
					writeProblem(w, http.StatusBadRequest, "Bad Request",
						"namespace_id does not exist")
					return
				}
				slog.Error("lookup namespace for cluster-policy", slog.Any("error", nsErr))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
			if ns.ClusterId != in.ClusterID {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"namespace_id does not belong to the specified cluster_id")
				return
			}
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
			if fp != "Fail" && fp != "Ignore" {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"failure_policy must be Fail or Ignore")
				return
			}
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
			if errors.Is(err, ErrConflict) {
				writeProblem(w, http.StatusConflict, "Conflict",
					"a collector-managed policy already exists at this key; delete it first or wait for the next collector tick")
				return
			}
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity",
					"referenced cluster or namespace does not exist")
				return
			}
			slog.Error("create cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		created, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			slog.Error("read back created cluster policy", slog.String("id", id.String()), slog.Any("error", err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"%s","warning":"cluster policy created but read-back failed"}`, id)
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

func HandleDeleteClusterPolicy(store Store) http.HandlerFunc {
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
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeleteClusterPolicy(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found",
					"cluster policy not found or is collector-managed (cannot delete)")
				return
			}
			slog.Error("delete cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
