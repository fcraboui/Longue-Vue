package api

// Hand-written HTTP handlers for the flow-matrix Phase 1 network-policy
// read endpoints (Tasks 17 + 18, ADR-0031).
//
// Scope matrix:
//   GET /v1/network-policies      → read (cluster_id required, namespace_id optional)
//   GET /v1/network-policies/{id} → read (embeds rules)
//
// Both handlers are auth-guarded via requireScope(auth.ScopeRead), consistent
// with every other read-only collection endpoint in this package.

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const (
	respKeyItems      = "items"
	respKeyNextCursor = "next_cursor"

	npListNameMaxLen = 100
)

// HandleListNetworkPolicies — read scope. GET /v1/network-policies.
//
// Required query param: cluster_id (UUID). Optional: namespace_id (UUID),
// name (substring/glob, max 100), sort, order, limit, cursor.
//
// Errors: 400 on missing/invalid params or invalid sort/cursor; 500 on store failure.
func HandleListNetworkPolicies(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		q := r.URL.Query()

		rawClusterID := q.Get("cluster_id")
		if rawClusterID == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id is required")
			return
		}
		clusterID, err := uuid.Parse(rawClusterID)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id must be a valid UUID")
			return
		}

		var filter NetworkPolicyListFilter
		if raw := q.Get("namespace_id"); raw != "" {
			id, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "namespace_id must be a valid UUID")
				return
			}
			filter.NamespaceID = &id
		}
		if v := q.Get("name"); v != "" {
			if len(v) > npListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListNetworkPoliciesByCluster(r.Context(), clusterID, filter, page)
		if err != nil {
			writeListError(w, "list network policies", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
			respKeyNextCursor: next,
		})
	}
}

// netpolDetailResponse is the GET /v1/network-policies/{id} response shape.
// Embeds the policy fields plus a rules array.
type netpolDetailResponse struct {
	ID          uuid.UUID              `json:"id"`
	ClusterID   uuid.UUID              `json:"cluster_id"`
	NamespaceID uuid.UUID              `json:"namespace_id"`
	Name        string                 `json:"name"`
	PodSelector any                    `json:"pod_selector"`
	PolicyTypes []string               `json:"policy_types"`
	Rules       []NetworkPolicyRuleRow `json:"rules"`
}

// HandleGetNetworkPolicy — read scope. GET /v1/network-policies/{id}.
//
// Fetches the policy by UUID and embeds all its rules in the response.
// 404 when the policy does not exist.
func HandleGetNetworkPolicy(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		np, err := store.GetNetworkPolicy(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get network policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		rules, err := store.ListNetworkPolicyRules(r.Context(), np.ID)
		if err != nil {
			slog.Error("list network policy rules", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		if rules == nil {
			rules = []NetworkPolicyRuleRow{}
		}

		writeJSON(w, http.StatusOK, netpolDetailResponse{
			ID:          np.ID,
			ClusterID:   np.ClusterID,
			NamespaceID: np.NamespaceID,
			Name:        np.Name,
			PodSelector: np.PodSelector,
			PolicyTypes: np.PolicyTypes,
			Rules:       rules,
		})
	}
}
