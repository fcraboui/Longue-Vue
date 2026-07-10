package api

// Hand-written HTTP handlers for the flow-matrix Phase 1 security-group
// read endpoints (Task 19, ADR-0031).
//
// Scope matrix:
//   GET /v1/security-groups      → read (cloud_account_id required)
//   GET /v1/security-groups/{id} → read (embeds rules)

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const sgListNameMaxLen = 100

// HandleListSecurityGroups — read scope. GET /v1/security-groups.
//
// Required query param: cloud_account_id (UUID).
// Optional: name (substring/glob, max 100), sort, order, limit, cursor.
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

		var filter SecurityGroupListFilter
		if v := q.Get("name"); v != "" {
			if len(v) > sgListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListSecurityGroupsByAccount(r.Context(), accountID, filter, page)
		if err != nil {
			writeListError(w, "list security groups", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
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
