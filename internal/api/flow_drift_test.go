package api

// Tests for the throttled flow_drift audit emitter (ADR-0036, R3 Task 2).
// driftKeys is the pure selector (only non_declare flows count); emitFlowDrift
// is exercised against the memStore fake, whose RecordFlowDriftSeen always
// reports "emit" and whose InsertAuditEvent captures rows into
// authState.auditEvents.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/flowmatrix"
)

// nonDeclareSSH is the canonical drifting flow used across these tests:
// an actual ingress tcp/22 perimeter rule with no reference row.
func nonDeclareSSH() flowmatrix.Flow {
	return flowmatrix.Flow{
		Layer:     "perimeter",
		Direction: "ingress",
		Src:       flowmatrix.Endpoint{Kind: "cidr", Name: "Internet"},
		Dst:       flowmatrix.Endpoint{Name: "cluster"},
		State:     flowmatrix.StateNonDeclare,
		Ports:     []flowmatrix.PortRange{{Protocol: "tcp", From: intPtr(22), To: intPtr(22)}},
	}
}

func TestFlowDriftKeys_OnlyNonDeclare(t *testing.T) {
	syn := flowmatrix.Synthesis{
		ClusterID: "c1",
		Perimeter: []flowmatrix.Flow{
			nonDeclareSSH(),
			{
				Direction: "ingress",
				Src:       flowmatrix.Endpoint{Name: "Internet"},
				Dst:       flowmatrix.Endpoint{Name: "cluster"},
				State:     flowmatrix.StateConforme,
			},
		},
		Internal: []flowmatrix.Flow{
			{
				Direction: "egress",
				Src:       flowmatrix.Endpoint{Name: "api"},
				Dst:       flowmatrix.Endpoint{Name: "pg"},
				State:     flowmatrix.StateLargeOuvert,
			},
		},
	}
	keys := driftKeys(syn)
	if len(keys) != 1 {
		t.Fatalf("driftKeys = %v, want exactly the one non_declare flow", keys)
	}
	if keys[0] != "ingress|Internet|cluster|tcp|22-22" {
		t.Fatalf("key = %q", keys[0])
	}
}

func TestFlowDriftKey_AnyPorts(t *testing.T) {
	// No ports => proto+ports default to "any".
	f := flowmatrix.Flow{
		Direction: "egress",
		Src:       flowmatrix.Endpoint{Name: "api"},
		Dst:       flowmatrix.Endpoint{Name: "pg"},
		State:     flowmatrix.StateNonDeclare,
	}
	if got := flowDriftKey(f); got != "egress|api|pg|any|any" {
		t.Fatalf("flowDriftKey = %q", got)
	}
}

// assertFlowDriftEvent checks the audit row shape for the canonical
// nonDeclareSSH flow. Kept separate so the test bodies stay under the
// gocyclo threshold.
func assertFlowDriftEvent(t *testing.T, ev *AuditEvent, clusterID uuid.UUID) {
	t.Helper()
	if ev.Action != "flow_drift" {
		t.Errorf("action = %q, want flow_drift", ev.Action)
	}
	if string(ev.ActorKind) != "system" {
		t.Errorf("actor_kind = %q, want system", ev.ActorKind)
	}
	if ev.ResourceType == nil || *ev.ResourceType != "cluster" {
		t.Errorf("resource_type = %v, want cluster", ev.ResourceType)
	}
	if ev.ResourceId == nil || *ev.ResourceId != clusterID.String() {
		t.Errorf("resource_id = %v, want %s", ev.ResourceId, clusterID)
	}
	if ev.Details == nil {
		t.Fatal("details missing")
	}
	d := *ev.Details
	wantDetails := map[string]string{
		"flow_key":  "ingress|Internet|cluster|tcp|22-22",
		"state":     "non_declare",
		"direction": "ingress",
		"action":    "flow_drift",
	}
	for k, want := range wantDetails {
		if d[k] != want {
			t.Errorf("details.%s = %v, want %q", k, d[k], want)
		}
	}
}

func TestEmitFlowDrift_RecordsAuditEvent(t *testing.T) {
	store := newMemStore()
	clusterID := uuid.New()
	conforme := flowmatrix.Flow{
		Layer: "perimeter", Direction: "ingress",
		Src:   flowmatrix.Endpoint{Name: "Internet"},
		Dst:   flowmatrix.Endpoint{Name: "cluster"},
		State: flowmatrix.StateConforme,
	}
	syn := flowmatrix.Synthesis{
		ClusterID: clusterID.String(),
		Perimeter: []flowmatrix.Flow{nonDeclareSSH(), conforme},
		Internal: []flowmatrix.Flow{
			// large_ouvert + manquant must NOT produce a drift event.
			{
				Layer: "internal", Direction: "egress",
				Src:   flowmatrix.Endpoint{Name: "api"},
				Dst:   flowmatrix.Endpoint{Name: "pg"},
				State: flowmatrix.StateLargeOuvert,
			},
			{
				Layer: "internal", Direction: "ingress",
				Src:   flowmatrix.Endpoint{Name: "x"},
				Dst:   flowmatrix.Endpoint{Name: "y"},
				State: flowmatrix.StateManquant,
			},
		},
	}

	emitFlowDrift(t.Context(), store, clusterID, syn)

	store.mu.Lock()
	events := append([]AuditEvent(nil), store.authState.auditEvents...)
	store.mu.Unlock()

	if len(events) != 1 {
		t.Fatalf("audit events = %d, want exactly 1 (the single non_declare flow)", len(events))
	}
	assertFlowDriftEvent(t, &events[0], clusterID)
}

func TestEmitFlowDrift_NoNonDeclare_NoEvents(t *testing.T) {
	store := newMemStore()
	syn := flowmatrix.Synthesis{
		Perimeter: []flowmatrix.Flow{
			{
				Direction: "ingress",
				Src:       flowmatrix.Endpoint{Name: "a"},
				Dst:       flowmatrix.Endpoint{Name: "b"},
				State:     flowmatrix.StateConforme,
			},
		},
	}
	emitFlowDrift(t.Context(), store, uuid.New(), syn)

	store.mu.Lock()
	n := len(store.authState.auditEvents)
	store.mu.Unlock()
	if n != 0 {
		t.Fatalf("audit events = %d, want 0", n)
	}
}
