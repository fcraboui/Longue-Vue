package api

// Push-collector write handlers for NetworkPolicies (ADR-0038).
//
// The GET handlers live in network_policy_handlers.go (ADR-0034); these are
// the writes that close the gap left by "Read-only API surface" for clusters
// collected by cmd/longue-vue-collector through the DMZ ingest GW.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// errMissingBody is returned when the strict server delivers a nil request body.
// This should never happen in practice (oapi-codegen enforces the schema), but
// we guard defensively.
var errMissingBody = errors.New("missing request body")

// marshalJSONOrNull JSON-marshals v and returns the bytes; on error returns nil
// (which pgx writes as SQL NULL). Acceptable here because the inputs are
// guaranteed-valid map/array shapes from the codegen-validated request body.
//
//nolint:errchkjson // callers pass map/slice values that are always serialisable; nil result is safe sentinel
func marshalJSONOrNull(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// rowToAPINetworkPolicy converts a store NetworkPolicyRow into the codegen
// NetworkPolicy response type. PodSelector is re-decoded from JSONB so the
// response field is a plain JSON object, not a base64-encoded blob.
func rowToAPINetworkPolicy(row *NetworkPolicyRow) (NetworkPolicy, error) {
	var podSel map[string]interface{}
	if len(row.PodSelector) > 0 {
		if err := json.Unmarshal(row.PodSelector, &podSel); err != nil {
			return NetworkPolicy{}, fmt.Errorf("decode pod_selector: %w", err)
		}
	}
	if podSel == nil {
		podSel = map[string]interface{}{}
	}

	ptypes := make([]NetworkPolicyPolicyTypes, len(row.PolicyTypes))
	for i, pt := range row.PolicyTypes {
		ptypes[i] = NetworkPolicyPolicyTypes(pt)
	}

	return NetworkPolicy{
		Id:          row.ID,
		ClusterId:   row.ClusterID,
		NamespaceId: row.NamespaceID,
		Name:        row.Name,
		PodSelector: podSel,
		PolicyTypes: ptypes,
	}, nil
}

// CreateNetworkPolicy is the strict-server impl for POST /v1/network-policies.
//
// Scope: write. Returns 201 on first write, 200 on subsequent upsert.
// The policy and its rules are persisted atomically via UpsertNetworkPolicyAtomic.
//
//nolint:gocyclo // nil-guards for optional rule fields inflate the score; each branch is a single assignment
func (s *Server) CreateNetworkPolicy(
	ctx context.Context, req CreateNetworkPolicyRequestObject,
) (CreateNetworkPolicyResponseObject, error) {
	// Scope guard — the auth middleware attaches the Caller to r.Context()
	// before the strict server calls this handler, and InjectRequestMiddleware
	// carries r.Context() into ctx. So CallerFromContext(ctx) is reliable.
	caller := auth.CallerFromContext(ctx)
	if caller == nil || !caller.HasScope(auth.ScopeWrite) {
		return CreateNetworkPolicy403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse{},
		}, nil
	}

	body := req.Body
	if body == nil {
		return nil, errMissingBody
	}

	// Convert request body fields into store types.
	np := NetworkPolicyRow{
		ClusterID:   body.ClusterId,
		NamespaceID: body.NamespaceId,
		Name:        body.Name,
		PodSelector: marshalJSONOrNull(body.PodSelector),
		PolicyTypes: body.PolicyTypes,
		SpecRaw:     marshalJSONOrNull(body.SpecRaw),
	}

	rules := make([]NetworkPolicyRuleRow, 0, len(body.Rules))
	for _, in := range body.Rules {
		r := NetworkPolicyRuleRow{
			Direction: string(in.Direction),
			PeerKind:  string(in.PeerKind),
			Ports:     marshalJSONOrNull(in.Ports),
		}
		if in.PeerPodSelector != nil {
			r.PeerPodSelector = marshalJSONOrNull(*in.PeerPodSelector)
		}
		if in.PeerNamespaceSelector != nil {
			r.PeerNamespaceSelector = marshalJSONOrNull(*in.PeerNamespaceSelector)
		}
		if in.PeerIpBlockCidr != nil {
			r.PeerIPBlockCIDR = *in.PeerIpBlockCidr
		}
		if in.PeerIpBlockExcept != nil {
			r.PeerIPBlockExcept = marshalJSONOrNull(*in.PeerIpBlockExcept)
		}
		rules = append(rules, r)
	}

	// Existence pre-check to decide 201 vs 200. TOCTOU: two concurrent POSTs
	// for the same (cluster, namespace, name) can both see existed=false and
	// both return 201. The upsert is still safe (ON CONFLICT DO UPDATE is
	// idempotent) but the second caller gets a misleading 201. Acceptable
	// for the push-collector use case where ticks are serialised per cluster;
	// a strict guarantee would require SELECT FOR UPDATE.
	existed, err := s.store.NetworkPolicyExists(ctx, np.ClusterID, np.NamespaceID, np.Name)
	if err != nil {
		return nil, fmt.Errorf("exists check: %w", err)
	}

	id, err := s.store.UpsertNetworkPolicyAtomic(ctx, np, rules)
	if err != nil {
		return nil, fmt.Errorf("upsert: %w", err)
	}

	row, err := s.store.GetNetworkPolicy(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get after upsert: %w", err)
	}

	out, err := rowToAPINetworkPolicy(&row)
	if err != nil {
		return nil, fmt.Errorf("convert response: %w", err)
	}

	if existed {
		return CreateNetworkPolicy200JSONResponse(out), nil
	}
	return CreateNetworkPolicy201JSONResponse(out), nil
}

// ReconcileNetworkPolicies is the strict-server impl for POST
// /v1/network-policies/reconcile.
//
// Scope: delete. Deletes every policy in the given namespace whose name is
// NOT in keep_names, returning the count of deleted rows. The caller MUST
// only invoke after a successful list tick (CLAUDE.md reconcile contract).
func (s *Server) ReconcileNetworkPolicies(
	ctx context.Context, req ReconcileNetworkPoliciesRequestObject,
) (ReconcileNetworkPoliciesResponseObject, error) {
	caller := auth.CallerFromContext(ctx)
	if caller == nil || !caller.HasScope(auth.ScopeDelete) {
		return ReconcileNetworkPolicies403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse{},
		}, nil
	}

	body := req.Body
	if body == nil {
		return nil, errMissingBody
	}

	deleted, err := s.store.SweepNetworkPoliciesByNamespaceWithCount(ctx, body.NamespaceId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	return ReconcileNetworkPolicies200JSONResponse{Deleted: deleted}, nil
}
