package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

//nolint:gocyclo // integration test exercises matched, updated, and no-op branches
func TestBackfillNodeImages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "bni-cluster"})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	providerID := "aws:///eu-west-2a/i-0abc123"
	if _, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId:  *cluster.Id,
		Name:       "node-1",
		ProviderId: &providerID,
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	// First backfill: matches by VmId substring, updates the row.
	matched, updated, err := pg.BackfillNodeImages(ctx, []api.NodeImage{
		{ProviderVMID: "i-0abc123", ImageID: "ami-1", ImageName: "master-k8s-1-32-2025.10.13"},
	})
	if err != nil {
		t.Fatalf("BackfillNodeImages: %v", err)
	}
	if matched != 1 || updated != 1 {
		t.Fatalf("first backfill matched=%d updated=%d; want 1,1", matched, updated)
	}

	// Idempotent: same values → matched 1, updated 0.
	matched, updated, err = pg.BackfillNodeImages(ctx, []api.NodeImage{
		{ProviderVMID: "i-0abc123", ImageID: "ami-1", ImageName: "master-k8s-1-32-2025.10.13"},
	})
	if err != nil {
		t.Fatalf("BackfillNodeImages idempotent: %v", err)
	}
	if matched != 1 || updated != 0 {
		t.Fatalf("idempotent backfill matched=%d updated=%d; want 1,0", matched, updated)
	}

	// No matching node → no-op.
	matched, updated, err = pg.BackfillNodeImages(ctx, []api.NodeImage{
		{ProviderVMID: "i-nomatch", ImageID: "ami-2", ImageName: "other"},
	})
	if err != nil {
		t.Fatalf("BackfillNodeImages nomatch: %v", err)
	}
	if matched != 0 || updated != 0 {
		t.Fatalf("nomatch backfill matched=%d updated=%d; want 0,0", matched, updated)
	}
}
