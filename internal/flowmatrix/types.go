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

// Flow is one synthesized cell in the output.
type Flow struct {
	Layer       string      `json:"layer"`
	Direction   string      `json:"direction"`
	Src         Endpoint    `json:"src"`
	Dst         Endpoint    `json:"dst"`
	Ports       []PortRange `json:"ports"`
	State       State       `json:"state"`
	ReferenceID *string     `json:"reference_id,omitempty"`
	Sources     []Source    `json:"sources"`
}

// Source records a contributing rule for a flow.
type Source struct {
	Kind    string `json:"kind"` // sg_rule|netpol_rule|reference
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// Warning flags a non-fatal synthesis issue (e.g. a dangling reference ref).
type Warning struct {
	Kind        string `json:"kind"`
	ReferenceID string `json:"reference_id,omitempty"`
	Ref         string `json:"ref,omitempty"`
}

// Synthesis is the full per-cluster result.
type Synthesis struct {
	ClusterID string    `json:"cluster_id"`
	Perimeter []Flow    `json:"perimeter"`
	Internal  []Flow    `json:"internal"`
	Warnings  []Warning `json:"warnings"`
}

// Inputs is the already-loaded data the pure synthesizer works on.
type Inputs struct {
	ClusterID      string
	PerimeterRules []SGRule
	InternalRules  []NetPolRule
	References     []Reference
	Groups         []NamedCIDRGroup
}

// SGRule is a flattened perimeter security-group rule.
type SGRule struct {
	RuleID    string
	Direction string
	Port      PortRange
	PeerCIDR  string
	WideOpen  bool
	Summary   string
}

// NetPolRule is a flattened internal NetworkPolicy rule.
type NetPolRule struct {
	RuleID       string
	Direction    string
	Port         PortRange
	SrcWorkload  string
	DstWorkload  string
	PeerCIDR     string
	WideOpenSide string // "ingress"|"egress"|"" — K8s default-allow on this side
	Summary      string
}

// Reference is a flattened reference row for matching.
type Reference struct {
	ID        string
	Layer     string
	Direction string
	SrcKind   string
	SrcRef    string
	DstKind   string
	DstRef    string
	Port      PortRange
}
