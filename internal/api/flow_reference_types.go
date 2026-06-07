package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// FlowReference is one operator-declared row of the reference flow matrix for a
// cluster: an expected perimeter or internal flow the synthesizer matches
// discovered rules against.
type FlowReference struct {
	ID            uuid.UUID  `json:"id"`
	ClusterID     uuid.UUID  `json:"cluster_id"`
	Layer         string     `json:"layer"`
	Direction     string     `json:"direction"`
	SrcKind       string     `json:"src_kind"`
	SrcRef        string     `json:"src_ref"`
	DstKind       string     `json:"dst_kind"`
	DstRef        string     `json:"dst_ref"`
	Protocol      string     `json:"protocol"`
	FromPort      *int       `json:"from_port,omitempty"`
	ToPort        *int       `json:"to_port,omitempty"`
	Justification string     `json:"justification"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// kindEndpointGroup is the src_kind/dst_kind value identifying a named CIDR
// endpoint group (the perimeter "outside" side of a flow).
const kindEndpointGroup = "endpoint_group"

// FlowReferenceInput is the create/update payload for a FlowReference.
type FlowReferenceInput struct {
	Layer         string `json:"layer" yaml:"layer"`
	Direction     string `json:"direction" yaml:"direction"`
	SrcKind       string `json:"src_kind" yaml:"src_kind"`
	SrcRef        string `json:"src_ref" yaml:"src_ref"`
	DstKind       string `json:"dst_kind" yaml:"dst_kind"`
	DstRef        string `json:"dst_ref" yaml:"dst_ref"`
	Protocol      string `json:"protocol" yaml:"protocol"`
	FromPort      *int   `json:"from_port,omitempty" yaml:"from_port,omitempty"`
	ToPort        *int   `json:"to_port,omitempty" yaml:"to_port,omitempty"`
	Justification string `json:"justification" yaml:"justification"`
}

// Validate enforces the layer/endpoint_group invariants and the required
// justification for a FlowReferenceInput. Errors wrap ErrConflict. The value
// receiver is intentional (read-only check on inline-constructed inputs).
//
//nolint:gocritic // hugeParam: value receiver is intentional, see doc above.
func (in FlowReferenceInput) Validate() error {
	if in.Justification == "" {
		return fmt.Errorf("justification is required: %w", ErrConflict)
	}
	egSides := 0
	if in.SrcKind == kindEndpointGroup {
		egSides++
	}
	if in.DstKind == kindEndpointGroup {
		egSides++
	}
	switch in.Layer {
	case "perimeter":
		if egSides != 1 {
			return fmt.Errorf("perimeter flow needs exactly one endpoint_group side: %w", ErrConflict)
		}
	case "internal":
		if egSides != 0 {
			return fmt.Errorf("internal flow must not reference an endpoint_group: %w", ErrConflict)
		}
	default:
		return fmt.Errorf("layer must be perimeter|internal: %w", ErrConflict)
	}
	return nil
}
