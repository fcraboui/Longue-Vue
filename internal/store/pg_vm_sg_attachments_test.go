package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

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
