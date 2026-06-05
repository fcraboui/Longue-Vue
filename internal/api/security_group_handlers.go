package api

// Hand-written HTTP handlers for the flow-matrix Phase 1 security-group
// read endpoints (Task 19, ADR-0031).
//
// Scope matrix:
//   GET /v1/security-groups      → read (cloud_account_id required)
//   GET /v1/security-groups/{id} → read (embeds rules)
//
// The list endpoint also accepts optional client-side filters:
//   vpc_id  — exact match on SecurityGroupRow.VPCID
//   name    — case-insensitive substring match on SecurityGroupRow.Name
//
// vpc_id / name filtering is performed after the store page fetch (option b)
// because ListSecurityGroupsByAccount only takes (accountID, limit, cursor).
// This is a known v1 limitation: results are filtered in-process and the
// declared limit is applied post-filter, so callers may receive fewer items
// than requested when filters are active. A future store-layer refactor
// (option a) would push predicates into SQL.

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// HandleListSecurityGroups — read scope. GET /v1/security-groups.
//
// Required query param: cloud_account_id (UUID).
// Optional: vpc_id (exact), name (substring), limit (default 50, max 500),
// cursor (opaque).
//
//nolint:gocognit,gocyclo // handler includes pagination + two optional client-side filters; complexity is inherent to the endpoint
func HandleListSecurityGroups(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		q := r.URL.Query()

		rawAccountID := q.Get("cloud_account_id")
		if rawAccountID == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cloud_account_id is required")
			return
		}
		accountID, err := uuid.Parse(rawAccountID)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cloud_account_id must be a valid UUID")
			return
		}

		vpcFilter := q.Get("vpc_id")
		nameFilter := strings.ToLower(q.Get("name"))

		limit := parseLimit(q.Get("limit"), 50)
		if limit > 500 {
			limit = 500
		}
		cursor := q.Get("cursor")

		// Fetch from store. vpc_id + name are post-filtered (v1 limitation —
		// see package doc above). We over-fetch with a high internal limit
		// only when client-side filters are active so we can apply them and
		// still return up to limit items.
		fetchLimit := limit
		if vpcFilter != "" || nameFilter != "" {
			fetchLimit = 500
		}
		raw, next, err := store.ListSecurityGroupsByAccount(r.Context(), accountID, fetchLimit, cursor)
		if err != nil {
			slog.Error("list security groups", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		// Apply optional client-side filters.
		if vpcFilter != "" || nameFilter != "" {
			filtered := raw[:0]
			for _, sg := range raw {
				if vpcFilter != "" && sg.VPCID != vpcFilter {
					continue
				}
				if nameFilter != "" && !strings.Contains(strings.ToLower(sg.Name), nameFilter) {
					continue
				}
				filtered = append(filtered, sg)
			}
			raw = filtered
			// Trim to requested limit; next_cursor is not meaningful after
			// client-side filtering, so clear it.
			next = ""
			if len(raw) > limit {
				raw = raw[:limit]
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      raw,
			respKeyNextCursor: next,
		})
	}
}

// sgDetailResponse is the GET /v1/security-groups/{id} response shape.
// Embeds the security group fields plus a rules array.
type sgDetailResponse struct {
	ID             uuid.UUID              `json:"id"`
	CloudAccountID uuid.UUID              `json:"cloud_account_id"`
	ProviderSGID   string                 `json:"provider_sg_id"`
	Name           string                 `json:"name"`
	VPCID          string                 `json:"vpc_id,omitempty"`
	Tags           any                    `json:"tags,omitempty"`
	Rules          []SecurityGroupRuleRow `json:"rules"`
}

// HandleGetSecurityGroup — read scope. GET /v1/security-groups/{id}.
//
// Fetches the security group by UUID and embeds all its rules in the response.
// 404 when the group does not exist.
func HandleGetSecurityGroup(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		sg, err := store.GetSecurityGroup(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get security group", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		rules, err := store.ListSecurityGroupRules(r.Context(), sg.ID)
		if err != nil {
			slog.Error("list security group rules", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		if rules == nil {
			rules = []SecurityGroupRuleRow{}
		}

		writeJSON(w, http.StatusOK, sgDetailResponse{
			ID:             sg.ID,
			CloudAccountID: sg.CloudAccountID,
			ProviderSGID:   sg.ProviderSGID,
			Name:           sg.Name,
			VPCID:          sg.VPCID,
			Tags:           sg.Tags,
			Rules:          rules,
		})
	}
}
