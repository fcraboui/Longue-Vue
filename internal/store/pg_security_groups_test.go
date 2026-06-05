package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestUpsertSecurityGroup_InsertThenUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "sg-test-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	sg := SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   "sg-1",
		Name:           "pg",
		VPCID:          "vpc-aaa",
		Tags:           json.RawMessage(`{"env":"prod"}`),
	}
	id1, err := pg.UpsertSecurityGroup(ctx, sg)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sg.Name = "pg-renamed"
	id2, err := pg.UpsertSecurityGroup(ctx, sg)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert keyed on (account, provider_sg_id) should keep id stable")
	}
	got, err := pg.GetSecurityGroup(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "pg-renamed" {
		t.Fatalf("expected rename, got %q", got.Name)
	}
}

func TestGetSecurityGroup_NotFound(t *testing.T) {
	pg := newTestPG(t)
	_, err := pg.GetSecurityGroup(context.Background(), uuid.New())
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestReplaceSecurityGroupRules(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "sg-rules-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	sgID, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   "sg-rules-1",
		Name:           "test-sg",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert sg: %v", err)
	}

	port5432 := 5432
	// Replace with 2 rules: one tcp/5432 cidr, one any-protocol egress.
	rules2 := []SecurityGroupRule{
		{
			Direction: "ingress",
			Protocol:  "tcp",
			FromPort:  &port5432,
			ToPort:    &port5432,
			PeerKind:  "cidr",
			PeerCIDR:  "10.0.0.0/8",
		},
		{
			Direction: "egress",
			Protocol:  "-1",
			PeerKind:  "cidr",
			PeerCIDR:  "0.0.0.0/0",
		},
	}
	if err := pg.ReplaceSecurityGroupRules(ctx, sgID, rules2); err != nil {
		t.Fatalf("replace 2 rules: %v", err)
	}

	got, err := pg.ListSecurityGroupRules(ctx, sgID)
	if err != nil {
		t.Fatalf("list after 2: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rules, got %d", len(got))
	}

	// Shrink to 1 rule.
	if err := pg.ReplaceSecurityGroupRules(ctx, sgID, rules2[:1]); err != nil {
		t.Fatalf("replace 1 rule: %v", err)
	}

	got, err = pg.ListSecurityGroupRules(ctx, sgID)
	if err != nil {
		t.Fatalf("list after shrink: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 rule after shrink, got %d", len(got))
	}
}

func TestSweepSecurityGroupsByAccount_DeletesUnseen(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "sg-sweep-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	keepID, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   "sg-keep",
		Name:           "keep",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert sg-keep: %v", err)
	}
	_, err = pg.UpsertSecurityGroup(ctx, SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   "sg-gone",
		Name:           "gone",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert sg-gone: %v", err)
	}

	if err := pg.SweepSecurityGroupsByAccount(ctx, acc.ID, []string{"sg-keep"}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := pg.GetSecurityGroup(ctx, keepID); err != nil {
		t.Fatalf("kept sg should still exist: %v", err)
	}

	// Verify only 1 SG remains for the account.
	remaining, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ProviderSGID != "sg-keep" {
		t.Fatalf("expected only 'sg-keep', got %d items: %+v", len(remaining), remaining)
	}
}

//nolint:gocyclo // pagination test with multiple pages and duplicate checking
func TestListSecurityGroupsByAccount_PaginatesByLimit(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale", Name: "sg-paginate-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	for i := 0; i < 5; i++ {
		provID := fmt.Sprintf("sg-page-%d", i)
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   provID,
			Name:           provID,
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert %s: %v", provID, err)
		}
	}

	seen := make(map[uuid.UUID]bool)
	var cursor string
	totalFetched := 0

	// Page 1: expect 2 items + a cursor.
	page1, next1, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, 2, "")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d, want 2", len(page1))
	}
	if next1 == "" {
		t.Fatal("page1 next_cursor should be non-empty")
	}
	for _, sg := range page1 {
		if seen[sg.ID] {
			t.Fatalf("duplicate id on page1: %s", sg.ID)
		}
		seen[sg.ID] = true
	}
	totalFetched += len(page1)
	cursor = next1

	// Page 2: expect 2 items + a cursor.
	page2, next2, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, 2, cursor)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len=%d, want 2", len(page2))
	}
	if next2 == "" {
		t.Fatal("page2 next_cursor should be non-empty")
	}
	for _, sg := range page2 {
		if seen[sg.ID] {
			t.Fatalf("duplicate id on page2: %s", sg.ID)
		}
		seen[sg.ID] = true
	}
	totalFetched += len(page2)
	cursor = next2

	// Page 3: expect 1 item + empty cursor (last page).
	page3, next3, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, 2, cursor)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len=%d, want 1", len(page3))
	}
	if next3 != "" {
		t.Fatalf("page3 next_cursor should be empty, got %q", next3)
	}
	for _, sg := range page3 {
		if seen[sg.ID] {
			t.Fatalf("duplicate id on page3: %s", sg.ID)
		}
		seen[sg.ID] = true
	}
	totalFetched += len(page3)

	if totalFetched != 5 {
		t.Fatalf("total fetched=%d, want 5", totalFetched)
	}
}
