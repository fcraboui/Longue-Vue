package api

import (
	"time"

	"github.com/google/uuid"
)

type EndpointGroup struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Notes     string     `json:"notes,omitempty"`
	CIDRs     []string   `json:"cidrs"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type EndpointGroupInput struct {
	Name  string   `json:"name"`
	Notes string   `json:"notes"`
	CIDRs []string `json:"cidrs"`
}
