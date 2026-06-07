package api

// In-memory flow-reference store methods for memStore (ADR-0036, R2 Task 8).
// Replaces the bare stubs that previously lived in
// server_cloud_fake_test.go so the handler tests in
// flow_reference_handlers_test.go can drive the full CRUD + replace-all
// surface without a Postgres dependency.
//
// Mirrors the ApplicationBlock fake pattern in
// server_application_block_fake_test.go: a process-wide singleton with a
// reset helper called from each test's setup. Behaviour matches the
// canonical *store.PG impl in pg_flow_references.go: Create assigns a fresh
// id + timestamps and records created_by; List returns the cluster's rows;
// Update/Delete key on row id (ErrNotFound on miss); Replace clears the
// cluster's rows then re-inserts the posted set (OQ-1 replace-all).

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memFlowRefState struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]FlowReference
	byClus map[uuid.UUID][]uuid.UUID // cluster id → ordered row ids
}

var flowRefFake = &memFlowRefState{
	byID:   make(map[uuid.UUID]FlowReference),
	byClus: make(map[uuid.UUID][]uuid.UUID),
}

func resetFlowRefFake() {
	flowRefFake.mu.Lock()
	defer flowRefFake.mu.Unlock()
	flowRefFake.byID = make(map[uuid.UUID]FlowReference)
	flowRefFake.byClus = make(map[uuid.UUID][]uuid.UUID)
}

func flowRefFromInput(clusterID uuid.UUID, in *FlowReferenceInput, createdBy *uuid.UUID) FlowReference {
	now := time.Now().UTC()
	return FlowReference{
		ID:            uuid.New(),
		ClusterID:     clusterID,
		Layer:         in.Layer,
		Direction:     in.Direction,
		SrcKind:       in.SrcKind,
		SrcRef:        in.SrcRef,
		DstKind:       in.DstKind,
		DstRef:        in.DstRef,
		Protocol:      in.Protocol,
		FromPort:      in.FromPort,
		ToPort:        in.ToPort,
		Justification: in.Justification,
		CreatedBy:     createdBy,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func (m *memStore) ListFlowReferences(_ context.Context, clusterID uuid.UUID) ([]FlowReference, error) {
	flowRefFake.mu.Lock()
	defer flowRefFake.mu.Unlock()
	ids := flowRefFake.byClus[clusterID]
	out := make([]FlowReference, 0, len(ids))
	for _, id := range ids {
		out = append(out, flowRefFake.byID[id])
	}
	return out, nil
}

//nolint:gocritic // hugeParam: fake mirrors the Store interface signature.
func (m *memStore) CreateFlowReference(_ context.Context, clusterID uuid.UUID, in FlowReferenceInput, createdBy *uuid.UUID) (FlowReference, error) {
	flowRefFake.mu.Lock()
	defer flowRefFake.mu.Unlock()
	ref := flowRefFromInput(clusterID, &in, createdBy)
	flowRefFake.byID[ref.ID] = ref
	flowRefFake.byClus[clusterID] = append(flowRefFake.byClus[clusterID], ref.ID)
	return ref, nil
}

//nolint:gocritic // hugeParam: fake mirrors the Store interface signature.
func (m *memStore) UpdateFlowReference(_ context.Context, id uuid.UUID, in FlowReferenceInput) (FlowReference, error) {
	flowRefFake.mu.Lock()
	defer flowRefFake.mu.Unlock()
	ref, ok := flowRefFake.byID[id]
	if !ok {
		return FlowReference{}, ErrNotFound
	}
	ref.Layer = in.Layer
	ref.Direction = in.Direction
	ref.SrcKind = in.SrcKind
	ref.SrcRef = in.SrcRef
	ref.DstKind = in.DstKind
	ref.DstRef = in.DstRef
	ref.Protocol = in.Protocol
	ref.FromPort = in.FromPort
	ref.ToPort = in.ToPort
	ref.Justification = in.Justification
	ref.UpdatedAt = time.Now().UTC()
	flowRefFake.byID[id] = ref
	return ref, nil
}

func (m *memStore) DeleteFlowReference(_ context.Context, id uuid.UUID) error {
	flowRefFake.mu.Lock()
	defer flowRefFake.mu.Unlock()
	ref, ok := flowRefFake.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(flowRefFake.byID, id)
	ids := flowRefFake.byClus[ref.ClusterID]
	kept := ids[:0]
	for _, rid := range ids {
		if rid != id {
			kept = append(kept, rid)
		}
	}
	flowRefFake.byClus[ref.ClusterID] = kept
	return nil
}

func (m *memStore) ReplaceFlowReferences(
	ctx context.Context,
	clusterID uuid.UUID,
	ins []FlowReferenceInput,
	createdBy *uuid.UUID,
) ([]FlowReference, error) {
	flowRefFake.mu.Lock()
	for _, id := range flowRefFake.byClus[clusterID] {
		delete(flowRefFake.byID, id)
	}
	flowRefFake.byClus[clusterID] = nil
	for i := range ins {
		ref := flowRefFromInput(clusterID, &ins[i], createdBy)
		flowRefFake.byID[ref.ID] = ref
		flowRefFake.byClus[clusterID] = append(flowRefFake.byClus[clusterID], ref.ID)
	}
	flowRefFake.mu.Unlock()
	return m.ListFlowReferences(ctx, clusterID)
}

// RecordFlowDriftSeen is a minimal fake: it always reports "emit" (never
// throttled). The drift-emit task (R3-B2) refines this if a test needs the
// throttle behaviour.
func (m *memStore) RecordFlowDriftSeen(_ context.Context, _ uuid.UUID, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

// ListClustersWithFlowReferences is a minimal fake returning no clusters.
func (m *memStore) ListClustersWithFlowReferences(_ context.Context) ([]uuid.UUID, error) {
	return nil, nil
}
