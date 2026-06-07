package store

import (
	"errors"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
)

//nolint:gocyclo // integration test exercises multiple CRUD + replace branches
func TestFlowReferences_CRUDAndReplace(t *testing.T) {
	pg := newTestPG(t)
	ctx := t.Context()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "flow-ref-test"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}

	p443 := 443
	created, err := pg.CreateFlowReference(ctx, *cluster.Id, api.FlowReferenceInput{
		Layer:         "perimeter",
		Direction:     "ingress",
		SrcKind:       "endpoint_group",
		SrcRef:        "corp-vpn",
		DstKind:       "service",
		DstRef:        "web",
		Protocol:      "tcp",
		FromPort:      &p443,
		ToPort:        &p443,
		Justification: "operators reach the web UI over the corporate VPN",
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Layer != "perimeter" || created.SrcKind != "endpoint_group" {
		t.Errorf("unexpected created: %+v", created)
	}
	if created.FromPort == nil || *created.FromPort != 443 {
		t.Errorf("from_port=%v", created.FromPort)
	}

	list, err := pg.ListFlowReferences(ctx, *cluster.Id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len=%d, want 1", len(list))
	}

	// ReplaceFlowReferences must wipe the existing set and install the new one.
	replaced, err := pg.ReplaceFlowReferences(ctx, *cluster.Id, []api.FlowReferenceInput{
		{
			Layer:         "internal",
			Direction:     "egress",
			SrcKind:       "workload",
			SrcRef:        "Deployment/web",
			DstKind:       "service",
			DstRef:        "db",
			Protocol:      "tcp",
			Justification: "web talks to the database",
		},
	}, nil)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(replaced) != 1 {
		t.Fatalf("replace returned %d rows, want 1", len(replaced))
	}
	if replaced[0].Layer != "internal" {
		t.Errorf("replaced layer=%q, want internal", replaced[0].Layer)
	}
	// The old perimeter row must be gone.
	if replaced[0].ID == created.ID {
		t.Errorf("replace reused the old id; expected the perimeter row to be deleted")
	}

	after, err := pg.ListFlowReferences(ctx, *cluster.Id)
	if err != nil {
		t.Fatalf("list after replace: %v", err)
	}
	if len(after) != 1 || after[0].Layer != "internal" {
		t.Errorf("after replace=%+v, want single internal row", after)
	}

	// Update + delete on the surviving row.
	updated, err := pg.UpdateFlowReference(ctx, after[0].ID, api.FlowReferenceInput{
		Layer:         "internal",
		Direction:     "egress",
		SrcKind:       "workload",
		SrcRef:        "Deployment/web",
		DstKind:       "service",
		DstRef:        "cache",
		Protocol:      "tcp",
		Justification: "web talks to the cache",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DstRef != "cache" {
		t.Errorf("dst_ref=%q, want cache", updated.DstRef)
	}

	if err := pg.DeleteFlowReference(ctx, after[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := pg.DeleteFlowReference(ctx, after[0].ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("second delete should be ErrNotFound, got %v", err)
	}

	final, err := pg.ListFlowReferences(ctx, *cluster.Id)
	if err != nil {
		t.Fatalf("final list: %v", err)
	}
	if len(final) != 0 {
		t.Errorf("final list len=%d, want 0", len(final))
	}
}

func TestRecordFlowDriftSeen_Throttles(t *testing.T) {
	pg := newTestPG(t)
	ctx := t.Context()

	cl, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "flow-drift-throttle"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	cluster := *cl.Id

	first, err := pg.RecordFlowDriftSeen(ctx, cluster, "ingress|a|b|tcp|443-443", 24*time.Hour)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first {
		t.Fatalf("first call = false, want true (should emit)")
	}

	second, err := pg.RecordFlowDriftSeen(ctx, cluster, "ingress|a|b|tcp|443-443", 24*time.Hour)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second {
		t.Fatalf("second call = true, want false (throttled within 24h)")
	}

	// With a zero window, the same key should emit again (window already elapsed).
	third, err := pg.RecordFlowDriftSeen(ctx, cluster, "ingress|a|b|tcp|443-443", 0)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if !third {
		t.Fatalf("third call (0 window) = false, want true (window elapsed)")
	}
}

func TestListClustersWithFlowReferences(t *testing.T) {
	pg := newTestPG(t)
	ctx := t.Context()

	withRefsCl, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "flow-with-refs"})
	if err != nil {
		t.Fatalf("seed with-refs: %v", err)
	}
	withRefs := *withRefsCl.Id
	if _, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "flow-no-refs"}); err != nil {
		t.Fatalf("seed no-refs: %v", err)
	}

	p := 443
	if _, err := pg.CreateFlowReference(ctx, withRefs, api.FlowReferenceInput{
		Layer: "internal", Direction: "ingress", SrcKind: "workload", SrcRef: "a",
		DstKind: "workload", DstRef: "b", Protocol: "tcp", FromPort: &p, ToPort: &p,
		Justification: "x",
	}, nil); err != nil {
		t.Fatalf("seed ref: %v", err)
	}

	got, err := pg.ListClustersWithFlowReferences(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0] != withRefs {
		t.Fatalf("got %v, want [%s]", got, withRefs)
	}
}
