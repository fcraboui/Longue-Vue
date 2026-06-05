package api

// Hand-written HTTP handlers for the flow-matrix Phase 1 per-asset
// derived network-rules endpoints (Tasks 20 + 21, ADR-0031).
//
// Scope matrix:
//   GET /v1/workloads/{id}/network-rules        → read
//   GET /v1/virtual-machines/{id}/network-rules → read
//
// Both handlers are auth-guarded via requireScope(auth.ScopeRead),
// consistent with every other read-only endpoint in this package.
//
// P2 note: matchExpressions support and a full label-selector engine
// are explicitly out of scope here. The k8s_default_allow flag signals
// to the P2 consumer when no NetworkPolicy selects the workload.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// HandleWorkloadNetworkRules — read scope.
// GET /v1/workloads/{id}/network-rules
//
// Returns every NetworkPolicy in the workload's namespace whose
// pod_selector matchLabels is a subset of the workload's labels
// (Postgres @> operator). The empty selector (`{}`) matches everything.
// Rules for each matching policy are embedded in the response.
// When no policy matches, k8s_default_allow: true is returned to
// signal the Kubernetes "open by default" posture.
func HandleWorkloadNetworkRules(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}

		wl, err := store.GetWorkload(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "workload not found")
				return
			}
			slog.Error("get workload for network-rules", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		// Build the label JSON for the @> Postgres query.
		labels, err := workloadLabelsAsJSON(wl)
		if err != nil {
			slog.Error("encode workload labels", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		policies, err := store.ListNetworkPoliciesForWorkload(r.Context(), wl.NamespaceId, labels)
		if err != nil {
			slog.Error("list network policies for workload", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		type rule struct {
			ID                    uuid.UUID       `json:"id"`
			Direction             string          `json:"direction"`
			PeerKind              string          `json:"peer_kind"`
			PeerPodSelector       json.RawMessage `json:"peer_pod_selector,omitempty"`
			PeerNamespaceSelector json.RawMessage `json:"peer_namespace_selector,omitempty"`
			PeerIPBlockCIDR       string          `json:"peer_ip_block_cidr,omitempty"`
			PeerIPBlockExcept     json.RawMessage `json:"peer_ip_block_except,omitempty"`
			Ports                 json.RawMessage `json:"ports,omitempty"`
		}
		type matchingPolicy struct {
			ID          uuid.UUID `json:"id"`
			Name        string    `json:"name"`
			PolicyTypes []string  `json:"policy_types"`
			Rules       []rule    `json:"rules"`
		}

		out := make([]matchingPolicy, 0, len(policies))
		for _, p := range policies {
			rrs, err := store.ListNetworkPolicyRules(r.Context(), p.ID)
			if err != nil {
				slog.Error("list network policy rules", slog.String("policy_id", p.ID.String()), slog.Any("error", err))
				// Degrade gracefully: return the policy with empty rules rather than 500.
				rrs = nil
			}
			ruleItems := make([]rule, 0, len(rrs))
			for _, rr := range rrs {
				ruleItems = append(ruleItems, rule{
					ID:                    rr.ID,
					Direction:             rr.Direction,
					PeerKind:              rr.PeerKind,
					PeerPodSelector:       rr.PeerPodSelector,
					PeerNamespaceSelector: rr.PeerNamespaceSelector,
					PeerIPBlockCIDR:       rr.PeerIPBlockCIDR,
					PeerIPBlockExcept:     rr.PeerIPBlockExcept,
					Ports:                 rr.Ports,
				})
			}
			types := p.PolicyTypes
			if types == nil {
				types = []string{}
			}
			out = append(out, matchingPolicy{
				ID:          p.ID,
				Name:        p.Name,
				PolicyTypes: types,
				Rules:       ruleItems,
			})
		}

		wlID := uuid.Nil
		if wl.Id != nil {
			wlID = *wl.Id
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workload_id":       wlID,
			"policy_count":      len(out),
			"matching_policies": out,
			// Day-one wide-open signal — P2 engine consumes it.
			"k8s_default_allow": len(out) == 0,
		})
	}
}

// workloadLabelsAsJSON returns the workload's labels as a JSON object
// suitable for the Postgres @> containment operator. Returns `{}` when
// no labels are set so the query still matches open-selector policies.
func workloadLabelsAsJSON(wl Workload) (json.RawMessage, error) {
	if wl.Labels == nil || len(*wl.Labels) == 0 {
		return json.RawMessage(`{}`), nil
	}
	b, err := json.Marshal(*wl.Labels) //nolint:errchkjson // map[string]string is a safe type; defensive check kept for explicitness
	if err != nil {
		return nil, fmt.Errorf("marshal labels: %w", err)
	}
	return b, nil
}

// HandleVMNetworkRules — read scope.
// GET /v1/virtual-machines/{id}/network-rules
//
// Returns the security groups attached to the VM (via the canonical
// security_groups JSONB column) and their rules. When the VM's
// security_groups field predates the canonical schema, stale: true is
// returned and the caller should await the next collector tick.
func HandleVMNetworkRules(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}

		vm, err := store.GetVirtualMachine(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "virtual machine not found")
				return
			}
			slog.Error("get virtual machine for network-rules", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		if !IsCanonicalSGPayload(vm.SecurityGroups) {
			writeJSON(w, http.StatusOK, map[string]any{
				"vm_id":                    vm.ID,
				"stale":                    true,
				"attached_security_groups": []any{},
				"note":                     "VM record predates canonical SG schema; awaiting next collector tick.",
			})
			return
		}

		var payload struct {
			Groups []struct {
				ProviderSGID string `json:"provider_sg_id"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(vm.SecurityGroups, &payload); err != nil {
			slog.Error("unmarshal vm security_groups payload", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		type sgOut struct {
			ID    uuid.UUID              `json:"id"`
			Name  string                 `json:"name"`
			VPCID string                 `json:"vpc_id,omitempty"`
			Rules []SecurityGroupRuleRow `json:"rules"`
		}

		out := make([]sgOut, 0, len(payload.Groups))
		for _, g := range payload.Groups {
			sg, err := store.GetSecurityGroupByProviderID(r.Context(), vm.CloudAccountID, g.ProviderSGID)
			if err != nil {
				// Not found or transient: skip rather than 500 — the SG may
				// not have been collected yet (e.g. collector hasn't run for
				// this account).
				continue
			}
			rules, err := store.ListSecurityGroupRules(r.Context(), sg.ID)
			if err != nil {
				slog.Error("list security group rules", slog.String("sg_id", sg.ID.String()), slog.Any("error", err))
				rules = nil
			}
			if rules == nil {
				rules = []SecurityGroupRuleRow{}
			}
			out = append(out, sgOut{
				ID:    sg.ID,
				Name:  sg.Name,
				VPCID: sg.VPCID,
				Rules: rules,
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"vm_id":                    vm.ID,
			"stale":                    false,
			"attached_security_groups": out,
			"no_security_groups":       len(out) == 0,
		})
	}
}
