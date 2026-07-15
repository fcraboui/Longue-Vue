package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/flowmatrix"
)

// HandleClusterFlowMatrix — read scope. GET /v1/clusters/{id}/flow-matrix.
// Gated by flow_matrix_enabled; returns 409 problem+json when disabled. It
// loads the cluster's perimeter SG rules, internal NetworkPolicy rules,
// reference rows, and endpoint groups, flattens them into flowmatrix.Inputs,
// and serializes the read-time flowmatrix.Synthesis as JSON. The pure
// classification lives in internal/flowmatrix.
func HandleClusterFlowMatrix(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.GetSettings(r.Context())
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "settings unavailable", err.Error())
			return
		}
		if !settings.FlowMatrixEnabled {
			writeProblem(w, http.StatusConflict, "flow matrix disabled",
				"enable flow_matrix_enabled in admin settings to use this endpoint")
			return
		}
		clusterID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid cluster id", err.Error())
			return
		}
		inputs, warnings, err := loadFlowInputs(r.Context(), store, clusterID)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "load flow inputs", err.Error())
			return
		}
		out := flowmatrix.Synthesize(inputs)
		out.Warnings = append(out.Warnings, warnings...)
		// Best-effort: emit a throttled flow_drift audit event per
		// non_declare flow. Never affects the synthesis response.
		emitFlowDrift(r.Context(), store, clusterID, out)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// LoadFlowInputsForMetrics exposes loadFlowInputs to the metrics refresh loop
// (package main and the metricsrefresh package cannot reach the unexported
// helper). Returns the same inputs + warnings the synthesis handler uses.
func LoadFlowInputsForMetrics(ctx context.Context, store Store, clusterID uuid.UUID) (flowmatrix.Inputs, []flowmatrix.Warning, error) {
	return loadFlowInputs(ctx, store, clusterID)
}

// loadFlowInputs is the I/O bridge: it reads every persisted input the pure
// synthesizer needs and flattens it into flowmatrix.Inputs. The returned
// warnings slice is currently always empty (dangling-reference detection is
// deferred); it is kept so the handler can append future load-time warnings.
func loadFlowInputs(
	ctx context.Context,
	store Store,
	clusterID uuid.UUID,
) (flowmatrix.Inputs, []flowmatrix.Warning, error) {
	in := flowmatrix.Inputs{ClusterID: clusterID.String()}
	warnings := []flowmatrix.Warning{}

	groups, err := store.ListEndpointGroups(ctx)
	if err != nil {
		return flowmatrix.Inputs{}, nil, fmt.Errorf("list endpoint groups: %w", err)
	}
	for i := range groups {
		in.Groups = append(in.Groups, flowmatrix.NamedCIDRGroup{Name: groups[i].Name, CIDRs: groups[i].CIDRs})
	}

	if in.PerimeterRules, err = loadPerimeterRules(ctx, store, clusterID); err != nil {
		return flowmatrix.Inputs{}, nil, err
	}
	if in.InternalRules, err = loadInternalRules(ctx, store, clusterID); err != nil {
		return flowmatrix.Inputs{}, nil, err
	}

	refs, err := store.ListFlowReferences(ctx, clusterID)
	if err != nil {
		return flowmatrix.Inputs{}, nil, fmt.Errorf("list flow references: %w", err)
	}
	for i := range refs {
		ref := &refs[i]
		in.References = append(in.References, flowmatrix.Reference{
			ID:        ref.ID.String(),
			Layer:     ref.Layer,
			Direction: ref.Direction,
			SrcKind:   ref.SrcKind,
			SrcRef:    ref.SrcRef,
			DstKind:   ref.DstKind,
			DstRef:    ref.DstRef,
			Port:      flowmatrix.PortRange{Protocol: ref.Protocol, From: ref.FromPort, To: ref.ToPort},
		})
	}

	return in, warnings, nil
}

// loadPerimeterRules flattens every perimeter security group's CIDR rules.
// sg_ref / prefix_list_ref / self peers are skipped (deferred to a later R).
func loadPerimeterRules(ctx context.Context, store Store, clusterID uuid.UUID) ([]flowmatrix.SGRule, error) {
	sgs, err := store.PerimeterSecurityGroupsForCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("perimeter security groups: %w", err)
	}
	var out []flowmatrix.SGRule
	for i := range sgs {
		sg := &sgs[i]
		rules, rerr := store.ListSecurityGroupRules(ctx, sg.ID)
		if rerr != nil {
			return nil, fmt.Errorf("list security group rules: %w", rerr)
		}
		for j := range rules {
			ru := &rules[j]
			if ru.PeerKind != "cidr" {
				continue
			}
			out = append(out, flowmatrix.SGRule{
				RuleID:    ru.ID.String(),
				Direction: ru.Direction,
				Port:      flowmatrix.PortRange{Protocol: ru.Protocol, From: ru.FromPort, To: ru.ToPort},
				PeerCIDR:  ru.PeerCIDR,
				WideOpen:  ru.PeerCIDR == "0.0.0.0/0" || ru.PeerCIDR == "::/0",
				Summary:   sg.Name + " " + ru.Direction + " " + ru.Protocol + " from " + ru.PeerCIDR,
			})
		}
	}
	return out, nil
}

// loadInternalRules flattens every NetworkPolicy rule for the cluster.
func loadInternalRules(ctx context.Context, store Store, clusterID uuid.UUID) ([]flowmatrix.NetPolRule, error) {
	pols, _, err := store.ListNetworkPoliciesByCluster(ctx, clusterID, NetworkPolicyListFilter{}, ListPage{Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("list network policies: %w", err)
	}
	var out []flowmatrix.NetPolRule
	for i := range pols {
		rules, rerr := store.ListNetworkPolicyRules(ctx, pols[i].ID)
		if rerr != nil {
			return nil, fmt.Errorf("list network policy rules: %w", rerr)
		}
		for j := range rules {
			out = append(out, flattenNetPolRule(&pols[i], &rules[j]))
		}
	}
	return out, nil
}

// flattenNetPolRule coarsely flattens one NetworkPolicy rule into the pure
// synthesizer's NetPolRule shape. R2 is intentionally coarse: the rule's port
// is taken from the first element of the JSONB ports array, and peer selectors
// are summarized verbatim rather than resolved to concrete workloads.
// Selector→workload resolution is a later refinement.
func flattenNetPolRule(pol *NetworkPolicyRow, ru *NetworkPolicyRuleRow) flowmatrix.NetPolRule {
	port := flowmatrix.PortRange{Protocol: "any"}
	if len(ru.Ports) > 0 {
		var ports []struct {
			Protocol string `json:"protocol"`
			Port     *int   `json:"port"`
		}
		if err := json.Unmarshal(ru.Ports, &ports); err == nil && len(ports) > 0 {
			p := ports[0]
			proto := p.Protocol
			if proto == "" {
				proto = "any"
			}
			port = flowmatrix.PortRange{Protocol: proto, From: p.Port, To: p.Port}
		}
	}

	out := flowmatrix.NetPolRule{
		RuleID:    ru.ID.String(),
		Direction: ru.Direction,
		Port:      port,
		PeerCIDR:  ru.PeerIPBlockCIDR,
		Summary:   pol.Name + " " + ru.Direction,
	}
	peer := selectorSummary(ru.PeerPodSelector)
	if ru.Direction == "ingress" {
		out.DstWorkload = pol.Name
		out.SrcWorkload = peer
	} else {
		out.SrcWorkload = pol.Name
		out.DstWorkload = peer
	}
	return out
}

// selectorSummary renders a peer pod selector for the coarse R2 flattening:
// "all" for an empty selector, else the raw JSON verbatim.
func selectorSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "all"
	}
	return string(raw)
}
