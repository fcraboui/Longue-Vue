package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/store"
)

const (
	peerKindSelector = "selector"
	peerKindIPBlock  = "ip_block"
)

// NetPolStore is the slice of the store interface the netpol collector
// uses. Both *store.PG (direct, in-process) and apiclient.Store (HTTP
// push via the ingest GW, ADR-0038) satisfy this interface. Defined here
// so a test fake can stub without dragging the full store.
type NetPolStore interface {
	UpsertNetworkPolicy(ctx context.Context, np store.NetworkPolicy, rules []store.NetworkPolicyRule) (uuid.UUID, error)
	SweepNetworkPoliciesByNamespace(ctx context.Context, nsID uuid.UUID, seen []string) error
}

// CollectNetworkPolicies runs one tick of NetworkPolicy reconciliation for
// the local cluster. Issues ONE cluster-wide list call (replaces the
// per-namespace fan-out that exhausted client-go's rate limiter on
// large clusters — bugfix 2026-06-10). Groups results by namespace,
// upserts each, and sweeps every known namespace (per the reconcile
// contract — even empty ones, to clean up stale rows). Netpols in unknown
// namespaces (created between ingestNamespaces and this call) are skipped
// and picked up on the next tick — same pattern as ingestServices.
//
// MUST only be called after the kube list call succeeds — transient API
// errors must never wipe the store (CLAUDE.md reconcile contract).
func CollectNetworkPolicies(
	ctx context.Context,
	src KubeSource,
	st NetPolStore,
	clusterID uuid.UUID,
	namespaceIDsByName map[string]uuid.UUID,
) error {
	infos, err := src.ListAllNetworkPolicies(ctx)
	if err != nil {
		return fmt.Errorf("list netpols cluster-wide: %w", err)
	}

	// Pre-seed seen map for every known namespace so the sweep below runs
	// even on namespaces with zero netpols (cleans up stale rows after a
	// NetworkPolicy is deleted from K8s).
	seenByNS := make(map[uuid.UUID][]string, len(namespaceIDsByName))
	for _, nsID := range namespaceIDsByName {
		seenByNS[nsID] = make([]string, 0)
	}

	for _, info := range infos { //nolint:gocritic // rangeValCopy: NetworkPolicyInfo has slices; shallow copy is safe here
		nsID, ok := namespaceIDsByName[info.Namespace]
		if !ok {
			slog.Warn("collector: netpol in unknown namespace; skipping",
				slog.String("netpol", info.Name),
				slog.String("namespace", info.Namespace))
			continue
		}
		rules := flattenNetPolRules(info)
		if _, err := st.UpsertNetworkPolicy(ctx, store.NetworkPolicy{
			ClusterID:   clusterID,
			NamespaceID: nsID,
			Name:        info.Name,
			PodSelector: info.PodSelector,
			PolicyTypes: info.PolicyTypes,
			SpecRaw:     info.SpecRaw,
		}, rules); err != nil {
			return fmt.Errorf("upsert netpol %s/%s: %w", info.Namespace, info.Name, err)
		}
		seenByNS[nsID] = append(seenByNS[nsID], info.Name)
	}

	for _, nsID := range namespaceIDsByName {
		if err := st.SweepNetworkPoliciesByNamespace(ctx, nsID, seenByNS[nsID]); err != nil {
			return fmt.Errorf("sweep netpols in ns %s: %w", nsID, err)
		}
	}
	return nil
}

// flattenNetPolRules turns each ingress/egress entry into one
// store.NetworkPolicyRule per peer. Empty-peers rules are kept as a
// single row with peer_kind='selector' and null selectors — that
// represents "from anywhere in the cluster" in K8s semantics. The
// engine (P2) interprets this correctly.
//
//nolint:gocritic // hugeParam: NetworkPolicyInfo contains slices; passing by pointer would require a larger refactor
func flattenNetPolRules(info NetworkPolicyInfo) []store.NetworkPolicyRule {
	var out []store.NetworkPolicyRule
	flatten := func(direction string, rules []NetworkPolicyRuleInfo) {
		for _, r := range rules {
			if len(r.Peers) == 0 {
				out = append(out, store.NetworkPolicyRule{
					Direction: direction,
					PeerKind:  peerKindSelector,
					Ports:     json.RawMessage(r.Ports),
				})
				continue
			}
			for _, p := range r.Peers {
				row := store.NetworkPolicyRule{
					Direction: direction,
					PeerKind:  p.Kind,
					Ports:     json.RawMessage(r.Ports),
				}
				switch p.Kind {
				case peerKindSelector:
					row.PeerPodSelector = json.RawMessage(p.PodSelector)
					row.PeerNamespaceSelector = json.RawMessage(p.NamespaceSelector)
				case peerKindIPBlock:
					row.PeerIPBlockCIDR = p.IPBlockCIDR
					row.PeerIPBlockExcept = json.RawMessage(p.IPBlockExcept)
				}
				out = append(out, row)
			}
		}
	}
	flatten("ingress", info.Ingress)
	flatten("egress", info.Egress)
	return out
}
