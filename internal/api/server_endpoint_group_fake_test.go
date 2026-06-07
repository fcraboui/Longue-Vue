package api

// In-memory endpoint-group store methods for memStore (ADR-0036). Replaces the
// bare stubs that previously lived in server_cloud_fake_test.go so the handler
// tests in endpoint_group_handlers_test.go can drive the full CRUD surface
// without a Postgres dependency.
//
// Mirrors the flow-reference fake pattern in
// server_flow_reference_fake_test.go: a process-wide singleton with a reset
// helper called from each test's setup. Behaviour matches the canonical
// *store.PG impl: Create assigns a fresh id + timestamps and records
// created_by; List returns all rows; Get/Update/Delete key on row id
// (ErrNotFound on miss).

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memEndpointGroupState struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]EndpointGroup
	order []uuid.UUID // insertion-ordered ids for stable List output
}

var endpointGroupFake = &memEndpointGroupState{
	byID: make(map[uuid.UUID]EndpointGroup),
}

func resetEndpointGroupFake() {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	endpointGroupFake.byID = make(map[uuid.UUID]EndpointGroup)
	endpointGroupFake.order = nil
}

func (m *memStore) ListEndpointGroups(_ context.Context) ([]EndpointGroup, error) {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	out := make([]EndpointGroup, 0, len(endpointGroupFake.order))
	for _, id := range endpointGroupFake.order {
		out = append(out, endpointGroupFake.byID[id])
	}
	return out, nil
}

func (m *memStore) GetEndpointGroup(_ context.Context, id uuid.UUID) (EndpointGroup, error) {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	eg, ok := endpointGroupFake.byID[id]
	if !ok {
		return EndpointGroup{}, ErrNotFound
	}
	return eg, nil
}

func (m *memStore) CreateEndpointGroup(_ context.Context, in EndpointGroupInput, createdBy *uuid.UUID) (EndpointGroup, error) {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	for _, id := range endpointGroupFake.order {
		if endpointGroupFake.byID[id].Name == in.Name {
			return EndpointGroup{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	cidrs := append([]string(nil), in.CIDRs...)
	eg := EndpointGroup{
		ID:        uuid.New(),
		Name:      in.Name,
		Notes:     in.Notes,
		CIDRs:     cidrs,
		CreatedBy: createdBy,
		CreatedAt: now,
		UpdatedAt: now,
	}
	endpointGroupFake.byID[eg.ID] = eg
	endpointGroupFake.order = append(endpointGroupFake.order, eg.ID)
	return eg, nil
}

func (m *memStore) UpdateEndpointGroup(_ context.Context, id uuid.UUID, in EndpointGroupInput) (EndpointGroup, error) {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	eg, ok := endpointGroupFake.byID[id]
	if !ok {
		return EndpointGroup{}, ErrNotFound
	}
	for _, oid := range endpointGroupFake.order {
		if oid != id && endpointGroupFake.byID[oid].Name == in.Name {
			return EndpointGroup{}, ErrConflict
		}
	}
	eg.Name = in.Name
	eg.Notes = in.Notes
	eg.CIDRs = append([]string(nil), in.CIDRs...)
	eg.UpdatedAt = time.Now().UTC()
	endpointGroupFake.byID[id] = eg
	return eg, nil
}

func (m *memStore) DeleteEndpointGroup(_ context.Context, id uuid.UUID) error {
	endpointGroupFake.mu.Lock()
	defer endpointGroupFake.mu.Unlock()
	if _, ok := endpointGroupFake.byID[id]; !ok {
		return ErrNotFound
	}
	delete(endpointGroupFake.byID, id)
	kept := endpointGroupFake.order[:0]
	for _, oid := range endpointGroupFake.order {
		if oid != id {
			kept = append(kept, oid)
		}
	}
	endpointGroupFake.order = kept
	return nil
}
