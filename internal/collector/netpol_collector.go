package collector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/store"
)

const (
	peerKindSelector = "selector"
	peerKindIPBlock  = "ip_block"
)

// NetPolStore is the slice of the store interface the netpol resource
// handler uses. Defined here so a test fake can stub without dragging
// the full *store.PG. The in-process *store.PG satisfies this interface;
// the push-mode apiclient.Store does not (netpol writes go via the
// in-process path on the server side).
type NetPolStore interface {
	UpsertNetworkPolicy(ctx context.Context, np store.NetworkPolicy) (uuid.UUID, error)
	ReplaceNetworkPolicyRules(ctx context.Context, policyID uuid.UUID, rules []store.NetworkPolicyRule) error
	SweepNetworkPoliciesByNamespace(ctx context.Context, nsID uuid.UUID, seen []string) error
}

// CollectNetworkPolicies runs one tick of NetworkPolicy reconciliation
// for a single (cluster, namespace). MUST only be called after the kube
// list call succeeds — transient API errors must never wipe the store
// (CLAUDE.md reconcile contract).
func CollectNetworkPolicies(ctx context.Context, src KubeSource, st NetPolStore, clusterID, nsID uuid.UUID, nsName string) error {
	infos, err := src.ListNetworkPolicies(ctx, nsName)
	if err != nil {
		return fmt.Errorf("list netpols in %s: %w", nsName, err)
	}
	seen := make([]string, 0, len(infos))
	for _, info := range infos {
		id, err := st.UpsertNetworkPolicy(ctx, store.NetworkPolicy{
			ClusterID:   clusterID,
			NamespaceID: nsID,
			Name:        info.Name,
			PodSelector: info.PodSelector,
			PolicyTypes: info.PolicyTypes,
			SpecRaw:     info.SpecRaw,
		})
		if err != nil {
			return fmt.Errorf("upsert netpol %q: %w", info.Name, err)
		}
		rules := flattenNetPolRules(info)
		if err := st.ReplaceNetworkPolicyRules(ctx, id, rules); err != nil {
			return fmt.Errorf("replace rules for %q: %w", info.Name, err)
		}
		seen = append(seen, info.Name)
	}
	if err := st.SweepNetworkPoliciesByNamespace(ctx, nsID, seen); err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	return nil
}

// flattenNetPolRules turns each ingress/egress entry into one
// store.NetworkPolicyRule per peer. Empty-peers rules are kept as a
// single row with peer_kind='selector' and null selectors — that
// represents "from anywhere in the cluster" in K8s semantics. The
// engine (P2) interprets this correctly.
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
