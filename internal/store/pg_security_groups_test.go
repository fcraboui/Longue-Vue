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
		Provider: testStoreSGProvider, Name: "sg-test-acc",
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
		Provider: testStoreSGProvider, Name: "sg-rules-acc",
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
			Direction: testStoreDirIngress,
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
		Provider: testStoreSGProvider, Name: "sg-sweep-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	keepID, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
		CloudAccountID: acc.ID,
		ProviderSGID:   testStoreSGIDKeep,
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

	if err := pg.SweepSecurityGroupsByAccount(ctx, acc.ID, []string{testStoreSGIDKeep}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := pg.GetSecurityGroup(ctx, keepID); err != nil {
		t.Fatalf("kept sg should still exist: %v", err)
	}

	// Verify only 1 SG remains for the account.
	remaining, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ProviderSGID != testStoreSGIDKeep {
		t.Fatalf("expected only 'sg-keep', got %d items: %+v", len(remaining), remaining)
	}
}

//nolint:gocyclo // pagination test with multiple pages and duplicate checking
func TestListSecurityGroupsByAccount_PaginatesByLimit(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-paginate-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	for i := range 5 {
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
	page1, next1, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 2})
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
	page2, next2, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 2, Cursor: cursor})
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
	page3, next3, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 2, Cursor: cursor})
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

// TestListSecurityGroupsByAccount_SortByName verifies that sort=name asc returns
// rows in ascending lexicographic order.
func TestListSecurityGroupsByAccount_SortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-sort-name-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	names := []string{"zebra-sg", "alpha-sg", "mango-sg"}
	for i, n := range names {
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   fmt.Sprintf("sg-sort-%d", i),
			Name:           n,
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert %s: %v", n, err)
		}
	}

	items, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 10, Sort: "name", Order: "asc"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	// Expect alpha, mango, zebra
	if items[0].Name != "alpha-sg" || items[1].Name != "mango-sg" || items[2].Name != "zebra-sg" {
		t.Errorf("unexpected order: %q %q %q", items[0].Name, items[1].Name, items[2].Name)
	}
}

// TestListSecurityGroupsByAccount_NameFilter verifies the name= glob/substring filter.
func TestListSecurityGroupsByAccount_NameFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-name-filter-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	for i, n := range []string{"frontend-sg", "backend-sg", "cache-sg"} {
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   fmt.Sprintf("sg-nf-%d", i),
			Name:           n,
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Substring match: "end" matches frontend and backend.
	name := "end"
	items, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{Name: &name}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items for name=%q, got %d: %+v", name, len(items), items)
	}

	// Glob match: "front*" matches frontend-sg only.
	glob := "front*"
	items2, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{Name: &glob}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list glob: %v", err)
	}
	if len(items2) != 1 || items2[0].Name != "frontend-sg" {
		t.Fatalf("want 1 item 'frontend-sg', got %d: %+v", len(items2), items2)
	}
}

// TestListSecurityGroupsByAccount_VpcIDNullBoundary verifies that sorting by
// vpc_id paginates correctly across the NULL boundary.  Two SGs have a real
// vpc_id and two have NULL; walking with Limit=1 must return all four rows
// exactly once.
//
//nolint:gocyclo // multi-page boundary walk; complexity is inherent to the test
func TestListSecurityGroupsByAccount_VpcIDNullBoundary(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-vpc-null-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	// Two SGs with a real vpc_id, two with NULL (empty string → NULLIF → NULL).
	seeds := []struct {
		provID string
		name   string
		vpcID  string
	}{
		{"sg-vn-1", "alpha", "vpc-a"},
		{"sg-vn-2", "beta", "vpc-b"},
		{"sg-vn-3", "gamma", ""},
		{"sg-vn-4", "delta", ""},
	}
	for _, s := range seeds {
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   s.provID,
			Name:           s.name,
			VPCID:          s.vpcID,
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert %s: %v", s.provID, err)
		}
	}

	// Null the vpc_id for the two seeds that had an empty string but may have
	// been stored as '' rather than NULL by UpsertSecurityGroup (which uses
	// NULLIF($4,'')).  In practice they are already NULL; this is a safety net.
	_, err = pg.pool.Exec(ctx,
		`UPDATE security_groups SET vpc_id = NULL WHERE name IN ('gamma','delta') AND cloud_account_id = $1`,
		acc.ID,
	)
	if err != nil {
		t.Fatalf("null vpc_id: %v", err)
	}

	seen := make(map[uuid.UUID]bool)
	var cursor string
	pages := 0

	for {
		page := api.ListPage{Limit: 1, Sort: "vpc_id", Order: "asc", Cursor: cursor}
		items, next, listErr := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, page)
		if listErr != nil {
			t.Fatalf("list page %d (cursor=%q): %v", pages+1, cursor, listErr)
		}
		if len(items) == 0 {
			// Exhausted: the last real page should have set cursor to "" already.
			break
		}
		pages++
		if pages > 4 {
			t.Fatalf("more than 4 pages returned for 4 rows; pagination loop did not terminate")
		}
		if len(items) != 1 {
			t.Fatalf("want 1 item per page, got %d (cursor=%q)", len(items), cursor)
		}
		id := items[0].ID
		if seen[id] {
			t.Fatalf("duplicate row %s on page %d (cursor=%q)", id, pages, cursor)
		}
		seen[id] = true
		cursor = next
		if next == "" {
			break
		}
	}

	if len(seen) != 4 {
		t.Fatalf("want 4 distinct rows, got %d after %d pages", len(seen), pages)
	}
}

// TestListSecurityGroupsByAccount_VpcIDFilter verifies that the vpc_id= exact
// filter returns only matching rows.
func TestListSecurityGroupsByAccount_VpcIDFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-vpc-filter-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	for i, s := range []struct {
		name  string
		vpcID string
	}{
		{"sg-vf-1", "vpc-target"},
		{"sg-vf-2", "vpc-target"},
		{"sg-vf-3", "vpc-other"},
	} {
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   fmt.Sprintf("sg-vf-%d", i),
			Name:           s.name,
			VPCID:          s.vpcID,
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert %s: %v", s.name, err)
		}
	}

	vpcID := "vpc-target"
	items, _, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{VpcID: &vpcID}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list with vpc_id filter: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items for vpc_id=%q, got %d: %+v", vpcID, len(items), items)
	}
	for _, sg := range items {
		if sg.VPCID != "vpc-target" {
			t.Errorf("unexpected vpc_id %q", sg.VPCID)
		}
	}
}

// TestListSecurityGroupsByAccount_MismatchedCursor verifies that a cursor
// minted under a different sort key is rejected with ErrInvalidCursor.
func TestListSecurityGroupsByAccount_MismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acc, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: testStoreSGProvider, Name: "sg-cursor-mismatch-acc",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}
	for i := range 3 {
		if _, err := pg.UpsertSecurityGroup(ctx, SecurityGroup{
			CloudAccountID: acc.ID,
			ProviderSGID:   fmt.Sprintf("sg-cm-%d", i),
			Name:           fmt.Sprintf("sg-%d", i),
			Tags:           json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Mint a cursor with sort=name.
	_, cursor, err := pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 1, Sort: "name", Order: "asc"})
	if err != nil {
		t.Fatalf("page1 (sort=name): %v", err)
	}
	if cursor == "" {
		t.Fatal("expected a cursor from page1")
	}

	// Use that cursor with sort=reconcile_seen_at → should fail.
	_, _, err = pg.ListSecurityGroupsByAccount(ctx, acc.ID, api.SecurityGroupListFilter{}, api.ListPage{Limit: 1, Cursor: cursor})
	if !errors.Is(err, api.ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
}
