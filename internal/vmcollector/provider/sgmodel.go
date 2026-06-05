// Package provider defines the cloud-provider abstraction the vm-collector
// uses to fetch VMs and their security groups. The canonical SG schema
// below is the wire contract between every provider impl and the server-
// side store. New providers (AWS, OVH, Scaleway) map their native SG
// format into these types; the server is provider-agnostic.
package provider

// SGSchemaVersion identifies the wire-payload shape of security_groups
// JSONB carried on each virtual_machines row. Bumped only on breaking
// changes to SecurityGroup / SecurityGroupRule / SGPeer. The server
// detects a missing or older value and renders the row as stale.
const SGSchemaVersion = 1

// SecurityGroup is the canonical cloud security-group record. Provider
// impls fill this in; the server reads it.
type SecurityGroup struct {
	ProviderSGID string              `json:"provider_sg_id"`
	Name         string              `json:"name"`
	VPCID        string              `json:"vpc_id,omitempty"`
	Ingress      []SecurityGroupRule `json:"ingress"`
	Egress       []SecurityGroupRule `json:"egress"`
	Tags         map[string]string   `json:"tags,omitempty"`
}

// SecurityGroupRule is one canonical rule. Direction is always either
// "ingress" or "egress". Protocol is one of "tcp", "udp", "icmp", "any".
// FromPort/ToPort are nil for "any port".
type SecurityGroupRule struct {
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	FromPort    *int   `json:"from_port,omitempty"`
	ToPort      *int   `json:"to_port,omitempty"`
	Peer        SGPeer `json:"peer"`
	Description string `json:"description,omitempty"`
}

// SGPeer expresses the peer side of a rule. Exactly one Kind-specific
// field is set per row; others are zero-valued.
type SGPeer struct {
	Kind             string `json:"kind"` // "cidr" | "sg_ref" | "prefix_list_ref" | "self"
	CIDR             string `json:"cidr,omitempty"`
	ProviderSGID     string `json:"provider_sg_id,omitempty"`
	ProviderPrefixID string `json:"provider_prefix_id,omitempty"`
}

// VMSecurityGroupsPayload is the JSONB shape stored in
// virtual_machines.security_groups. The Version field carries
// SGSchemaVersion at write time; the server uses it to detect pre-deploy
// rows (Version=0) and render them as stale.
type VMSecurityGroupsPayload struct {
	Version int             `json:"schema_version"`
	Groups  []SecurityGroup `json:"groups"`
}
