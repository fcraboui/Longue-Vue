// Package flowmatrix computes, at read time, a per-cluster comparison of the
// discovered network posture (perimeter security-group rules + internal
// NetworkPolicy rules) against an operator-declared reference matrix. It is the
// server-side counterpart to ui/src/pages/Flows.tsx and follows the read-time,
// no-derived-state pattern of internal/eolagg.
package flowmatrix

// State is the classification of one synthesized flow.
type State string

const (
	StateConforme    State = "conforme"     // actual rule fully covered by a reference row
	StateNonDeclare  State = "non_declare"  // actual flow with no matching reference — drift
	StateManquant    State = "manquant"     // reference declared, no actual rule implements it
	StateLargeOuvert State = "large_ouvert" // K8s default-allow OR 0.0.0.0/0 SG rule — hardening
)

// PortRange is an inclusive port span. From==nil means "any port".
type PortRange struct {
	Protocol string `json:"protocol"` // tcp|udp|icmp|any
	From     *int   `json:"from,omitempty"`
	To       *int   `json:"to,omitempty"`
}

// Endpoint identifies one side of a flow in the synthesis output.
type Endpoint struct {
	Kind string `json:"kind"` // workload|service|namespace|endpoint_group|cidr
	Name string `json:"name"`
	CIDR string `json:"cidr,omitempty"`
}

// NamedCIDRGroup is an endpoint group flattened for matching.
type NamedCIDRGroup struct {
	Name  string
	CIDRs []string
}
