package api

// Network-policy domain types used by the Store interface and the API handlers
// (flow-matrix Phase 1). Kept separate from cloud_types.go because network
// policies are a K8s-native entity with their own table and list endpoints
// (Tasks 17-18, ADR-0031).
//
// The store package's pg_network_policies.go uses its own internal NetworkPolicy
// / NetworkPolicyRule structs, and the PG methods that implement the Store
// interface accept/return these api-package types via adapters in the store
// implementation.

import (
	"encoding/json"

	"github.com/google/uuid"
)

// NetworkPolicyRow is the persisted view of one Kubernetes NetworkPolicy.
// PodSelector and PolicyTypes mirror the K8s API object. SpecRaw carries
// the full raw spec JSONB for future engine use (P2).
type NetworkPolicyRow struct {
	ID          uuid.UUID       `json:"id"`
	ClusterID   uuid.UUID       `json:"cluster_id"`
	NamespaceID uuid.UUID       `json:"namespace_id"`
	Name        string          `json:"name"`
	PodSelector json.RawMessage `json:"pod_selector"`
	PolicyTypes []string        `json:"policy_types"`
	SpecRaw     json.RawMessage `json:"spec_raw,omitempty"`
}

// NetworkPolicyRuleRow is the persisted view of one ingress or egress rule
// on a NetworkPolicy. Direction is "ingress" or "egress"; PeerKind is
// "selector" or "ip_block". Non-applicable fields are zero-valued.
type NetworkPolicyRuleRow struct {
	ID                    uuid.UUID       `json:"id"`
	NetworkPolicyID       uuid.UUID       `json:"network_policy_id"`
	Direction             string          `json:"direction"`
	PeerKind              string          `json:"peer_kind"`
	PeerPodSelector       json.RawMessage `json:"peer_pod_selector,omitempty"`
	PeerNamespaceSelector json.RawMessage `json:"peer_namespace_selector,omitempty"`
	PeerIPBlockCIDR       string          `json:"peer_ip_block_cidr,omitempty"`
	PeerIPBlockExcept     json.RawMessage `json:"peer_ip_block_except,omitempty"`
	Ports                 json.RawMessage `json:"ports,omitempty"`
}
