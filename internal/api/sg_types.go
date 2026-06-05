package api

// Security-group domain types used by the Store interface and the VM ingest
// handler (flow-matrix Phase 1). Kept separate from cloud_types.go because
// security groups are a distinct entity with their own table and list
// endpoints (Tasks 11-12, ADR-0031).
//
// The "Row" suffix distinguishes these api-package types from the identically-
// named types in the store package. The store package uses api.SecurityGroupRow
// / api.SecurityGroupRuleRow as its public surface so the api.Store interface
// can reference them without an import cycle.

import (
	"encoding/json"

	"github.com/google/uuid"
)

// SecurityGroupRow is the persisted view of one cloud-provider security group.
// Tags is a JSONB map<string,string>. VPCID is empty-string when the provider
// does not report a VPC.
type SecurityGroupRow struct {
	ID             uuid.UUID
	CloudAccountID uuid.UUID
	ProviderSGID   string
	Name           string
	VPCID          string
	Tags           json.RawMessage
}

// SecurityGroupRuleRow is the persisted view of one ingress or egress rule.
// Direction is "ingress" or "egress". Protocol is one of "tcp", "udp",
// "icmp", "any", or "-1" (some providers use "-1" for "all traffic").
// FromPort / ToPort are nil when the protocol has no port concept.
// PeerKind is "cidr", "sg_ref", "prefix_list_ref", or "self".
type SecurityGroupRuleRow struct {
	ID               uuid.UUID
	SecurityGroupID  uuid.UUID
	Direction        string
	Protocol         string
	FromPort         *int
	ToPort           *int
	PeerKind         string
	PeerCIDR         string
	PeerSGProviderID string
	PeerPrefixID     string
	Description      string
}

// IsCanonicalSGPayload returns true when the JSONB carries a schema_version
// >= 1. Pre-deploy rows (missing or zero version) return false; the VM ingest
// handler skips SG persistence for those rows and they are rendered with
// stale=true by the read path.
func IsCanonicalSGPayload(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var probe struct {
		V int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.V >= 1
}
