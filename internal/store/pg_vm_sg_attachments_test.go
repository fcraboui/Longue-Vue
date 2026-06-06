package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestPerimeterSecurityGroupsForCluster proves the nodes.provider_id ->
// attachment provider_vm_id substring join: a cluster node whose provider_id
// embeds a cloud VM id resolves exactly the SG attached to that node-VM, and
// does not pick up an SG attached to a different (non-node) VM.
func TestPerimeterSecurityGroupsForCluster(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acc := vmTestAccount(t, pg, "perim-acct").ID

	// Cluster with one node whose provider_id embeds the cloud VM id i-node1.
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "perim-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name, provider_id, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1, 'node-1', 'outscale:///eu-west-2a/i-node1', NOW(), NOW())`,
		*cluster.Id); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	// Two SGs in the account: one attached to the node VM, one to a non-node VM.
	perimeterSGID, err := pg.UpsertSecurityGroup(ctx, api.SecurityGroupRow{
		CloudAccountID: acc, ProviderSGID: "sg-perimeter", Name: "nodes",
		Tags: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert perimeter sg: %v", err)
	}
	if _, err := pg.UpsertSecurityGroup(ctx, api.SecurityGroupRow{
		CloudAccountID: acc, ProviderSGID: "sg-other", Name: "app",
		Tags: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("upsert other sg: %v", err)
	}

	// Attachments: node VM -> sg-perimeter ; non-node VM -> sg-other.
	if err := pg.UpsertVMSecurityGroupAttachment(ctx, api.VMSecurityGroupAttachment{
		CloudAccountID: acc, ProviderVMID: "i-node1", ProviderSGID: "sg-perimeter",
	}); err != nil {
		t.Fatalf("attach node: %v", err)
	}
	if err := pg.UpsertVMSecurityGroupAttachment(ctx, api.VMSecurityGroupAttachment{
		CloudAccountID: acc, ProviderVMID: "i-app1", ProviderSGID: "sg-other",
	}); err != nil {
		t.Fatalf("attach app: %v", err)
	}

	got, err := pg.PerimeterSecurityGroupsForCluster(ctx, *cluster.Id)
	if err != nil {
		t.Fatalf("PerimeterSecurityGroupsForCluster: %v", err)
	}
	if len(got) != 1 || got[0].ID != perimeterSGID {
		t.Fatalf("perimeter SGs = %+v, want exactly sg-perimeter (%v)", got, perimeterSGID)
	}
}

func TestVMSGAttachments_UpsertAndSweep(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acc := vmTestAccount(t, pg, "vm-sg-attach").ID

	mk := func(vm, sg string) api.VMSecurityGroupAttachment {
		return api.VMSecurityGroupAttachment{CloudAccountID: acc, ProviderVMID: vm, ProviderSGID: sg}
	}

	for _, a := range []api.VMSecurityGroupAttachment{mk("i-1", "sg-a"), mk("i-1", "sg-b"), mk("i-2", "sg-a")} {
		if err := pg.UpsertVMSecurityGroupAttachment(ctx, a); err != nil {
			t.Fatalf("upsert %v: %v", a, err)
		}
	}
	if err := pg.UpsertVMSecurityGroupAttachment(ctx, mk("i-1", "sg-a")); err != nil {
		t.Fatalf("idempotent upsert: %v", err)
	}

	if err := pg.SweepVMSecurityGroupAttachments(ctx, acc, []api.VMSecurityGroupAttachment{mk("i-1", "sg-a")}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var count int
	if err := pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM vm_security_group_attachments WHERE cloud_account_id=$1`, acc).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("after sweep count = %d, want 1", count)
	}
}
