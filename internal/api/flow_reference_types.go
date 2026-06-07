package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

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

func (in FlowReferenceInput) Validate() error {
	if in.Justification == "" {
		return fmt.Errorf("justification is required: %w", ErrConflict)
	}
	egSides := 0
	if in.SrcKind == "endpoint_group" {
		egSides++
	}
	if in.DstKind == "endpoint_group" {
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
