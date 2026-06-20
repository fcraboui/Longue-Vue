package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

//nolint:gocyclo // integration test exercises multiple fixture branches
func TestListOSImages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "osi-cluster"})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	// A node carrying a backfilled OS image.
	providerID := "aws:///eu-west-2a/i-node1"
	if _, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: *cluster.Id, Name: "n1", ProviderId: &providerID,
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if _, _, err := pg.BackfillNodeImages(ctx, []api.NodeImage{
		{ProviderVMID: "i-node1", ImageID: "ami-node", ImageName: "shared-img-2025.10"},
	}); err != nil {
		t.Fatalf("BackfillNodeImages: %v", err)
	}
	// A non-terminated VM sharing the same image name (must dedup to one row).
	mustUpsertVMWithImage(t, ctx, pg, "i-vm1", "ami-vm", "shared-img-2025.10")
	// A distinct VM image.
	mustUpsertVMWithImage(t, ctx, pg, "i-vm2", "ami-other", "lonely-img-2025.09")

	// A soft-deleted node: its image must NOT appear (terminated_at IS NULL filter).
	ghostProvider := "aws:///eu-west-2a/i-ghost"
	if _, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: *cluster.Id, Name: "ghost", ProviderId: &ghostProvider,
	}); err != nil {
		t.Fatalf("UpsertNode ghost: %v", err)
	}
	if _, _, err := pg.BackfillNodeImages(ctx, []api.NodeImage{
		{ProviderVMID: "i-ghost", ImageID: "ami-ghost", ImageName: "ghost-img-2024.01"},
	}); err != nil {
		t.Fatalf("BackfillNodeImages ghost: %v", err)
	}
	if _, err := pg.DeleteNodesNotIn(ctx, *cluster.Id, []string{"n1"}); err != nil { // soft-delete "ghost"
		t.Fatalf("DeleteNodesNotIn: %v", err)
	}

	imgs, err := pg.ListOSImages(ctx)
	if err != nil {
		t.Fatalf("ListOSImages: %v", err)
	}
	byName := map[string]api.OSImage{}
	for _, im := range imgs {
		byName[im.ImageName] = im
	}
	shared, ok := byName["shared-img-2025.10"]
	if !ok {
		t.Fatalf("missing shared image; got %+v", imgs)
	}
	if shared.VMCount != 1 || shared.NodeCount != 1 {
		t.Fatalf("shared counts vm=%d node=%d; want 1,1", shared.VMCount, shared.NodeCount)
	}
	if len(shared.ImageIDs) != 2 { // ami-vm + ami-node
		t.Fatalf("shared image_ids=%v; want 2 distinct", shared.ImageIDs)
	}
	if _, ok := byName["lonely-img-2025.09"]; !ok {
		t.Fatalf("missing lonely image; got %+v", imgs)
	}
	if _, ok := byName["ghost-img-2024.01"]; ok {
		t.Fatalf("terminated node image must be excluded; got %+v", imgs)
	}
}

// mustUpsertVMWithImage creates a fresh cloud account and upserts a VM with
// the given provider VM id, image id, and image name. The cloud account is
// keyed by provider VM id so each call gets an isolated account.
func mustUpsertVMWithImage(t *testing.T, ctx context.Context, pg *PG, vmID, imageID, imageName string) {
	t.Helper()
	acct, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "osi-acct-" + vmID, Region: "eu-west-2",
	})
	if err != nil {
		t.Fatalf("UpsertCloudAccount: %v", err)
	}
	imgID, imgName := imageID, imageName
	if _, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   vmID,
		Name:           vmID,
		PowerState:     "running",
		ImageID:        &imgID,
		ImageName:      &imgName,
	}); err != nil {
		t.Fatalf("UpsertVirtualMachine: %v", err)
	}
}
