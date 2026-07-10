package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

const powerStateStopped = "stopped"

// seedVMsForSort creates a cloud account and upserts VMs with the given
// names into it. Returns the account and the seeded VMs in insertion order.
func seedVMsForSort(t *testing.T, pg *PG, suffix string, names []string) api.CloudAccount {
	t.Helper()
	ctx := context.Background()
	acct := vmTestAccount(t, pg, "vm-sort-"+suffix+"-"+uuid.New().String()[:8])
	for i, n := range names {
		provID := "i-sort-" + uuid.New().String()[:8]
		ps := "running"
		if i%2 == 1 {
			ps = powerStateStopped
		}
		_, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
			CloudAccountID: acct.ID,
			ProviderVMID:   provID,
			Name:           n,
			PowerState:     ps,
		})
		if err != nil {
			t.Fatalf("upsert vm %s: %v", n, err)
		}
	}
	return acct
}

func TestListVirtualMachinesSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acct := seedVMsForSort(t, pg, "name", []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{CloudAccountID: &acct.ID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, vm := range items {
			got = append(got, vm.Name)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	want := []string{"alpha", "beta", "delta", "epsilon", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("asc order: got %v, want %v", got, want)
		}
	}

	// Descending flips the order.
	items, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{CloudAccountID: &acct.ID},
		api.ListPage{Limit: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListVirtualMachinesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acct := seedVMsForSort(t, pg, "glob", []string{"prod-web-1", "prod-db-1", "dev-web-1", "my_vm"})

	cases := []struct {
		term string
		want int
	}{
		{"web", 2},      // substring
		{"prod-*", 2},   // prefix glob
		{"*-1", 3},      // suffix glob
		{"prod-*-1", 2}, // anchored both ends
		{"my_vm", 1},    // literal underscore (must not match e.g. "myxvm")
		{"WEB", 2},      // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
			CloudAccountID: &acct.ID,
			Name:           &name,
		}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListVirtualMachinesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedVMsForSort(t, pg, "badcursor", []string{"a", "b", "c"})

	if _, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under default (created_at) → mismatch.
	_, _, mismatchErr := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, api.ListPage{Limit: 1, Cursor: next})
	if !errors.Is(mismatchErr, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", mismatchErr)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListVirtualMachinesSortByLastSeenAtDesc(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acct := seedVMsForSort(t, pg, "lastseen", []string{"z-vm", "a-vm", "m-vm"})

	// Default last_seen_at is set to the upsert time; seeding sequentially
	// means z-vm < a-vm < m-vm by last_seen_at. Desc should give m-vm first.
	items, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{CloudAccountID: &acct.ID},
		api.ListPage{Limit: 10, Sort: "last_seen_at", Order: "desc"})
	if err != nil {
		t.Fatalf("sort by last_seen_at desc: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// Most recently seen first.
	if items[0].Name != "m-vm" {
		t.Errorf("first item = %s, want m-vm (most recently seeded)", items[0].Name)
	}
}

func TestListVirtualMachinesTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	// Seed VMs, then set power_state to force ties.
	acct := seedVMsForSort(t, pg, "tiebreak", []string{"v1", "v2", "v3", "v4", "v5"})
	if _, err := pg.pool.Exec(ctx,
		`UPDATE virtual_machines SET power_state = CASE WHEN name IN ('v1','v2') THEN 'running' ELSE 'stopped' END
		 WHERE cloud_account_id = $1`, acct.ID); err != nil {
		t.Fatalf("set power_state: %v", err)
	}

	seen := map[string]bool{}
	var states []string
	page := api.ListPage{Limit: 2, Sort: "power_state"}
	for {
		items, next, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{CloudAccountID: &acct.ID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			v := &items[i]
			if seen[v.ID.String()] {
				t.Fatalf("vm %s duplicated across pages (tiebreaker broken)", v.ID)
			}
			seen[v.ID.String()] = true
			states = append(states, v.PowerState)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(states) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(states))
	}
	// asc: running < stopped (lexicographic).
	r, s := "running", powerStateStopped
	want := []string{r, r, s, s, s}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("power_state order = %v, want %v", states, want)
		}
	}
}

func TestListVirtualMachinesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	acct := seedVMsForSort(t, pg, "default", []string{"v1", "v2", "v3", "v4", "v5"})

	// No sort params → newest-first, 2/2/1 pages, no duplicates.
	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{CloudAccountID: &acct.ID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, vm := range items {
			if seen[vm.ID.String()] {
				t.Fatalf("duplicate %s across pages", vm.ID)
			}
			seen[vm.ID.String()] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total != 5 {
		t.Fatalf("total=%d want 5", total)
	}
}

// ─── Cloud account sort tests ─────────────────────────────────────────────────

func seedCloudAccountsForSort(t *testing.T, pg *PG, accounts []api.CloudAccountUpsert) []api.CloudAccount {
	t.Helper()
	ctx := context.Background()
	out := make([]api.CloudAccount, 0, len(accounts))
	for _, a := range accounts {
		acct, err := pg.UpsertCloudAccount(ctx, a)
		if err != nil {
			t.Fatalf("upsert cloud account %s: %v", a.Name, err)
		}
		out = append(out, acct)
	}
	return out
}

func TestListCloudAccountsNameFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	seedCloudAccountsForSort(t, pg, []api.CloudAccountUpsert{
		{Provider: "outscale", Name: "prod-acct-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "dev-acct-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "prod-backup-" + suffix, Region: "eu-west-2"},
	})

	cases := []struct {
		term string
		want int
	}{
		{"prod-" + suffix, 0}, // exact prefix — use glob
		{"prod-*", 2},         // glob prefix
		{"acct-" + suffix, 2}, // substring
		{suffix, 3},           // all match
		{"DEV", 1},            // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListCloudAccounts(ctx, api.CloudAccountListFilter{Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

//nolint:gocyclo // paginated test with suffix-filter loop
func TestListCloudAccountsSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	seedCloudAccountsForSort(t, pg, []api.CloudAccountUpsert{
		{Provider: "outscale", Name: "gamma-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "alpha-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "beta-" + suffix, Region: "eu-west-2"},
	})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListCloudAccounts(ctx, api.CloudAccountListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, a := range items {
			// Only include the ones we seeded here (filter by suffix).
			if len(a.Name) > len(suffix) && a.Name[len(a.Name)-len(suffix):] == suffix {
				got = append(got, a.Name)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	// We should get our 3 accounts; check they appear in alpha order.
	want := []string{"alpha-" + suffix, "beta-" + suffix, "gamma-" + suffix}
	found := make([]string, 0, 3)
	for _, n := range got {
		for _, w := range want {
			if n == w {
				found = append(found, n)
				break
			}
		}
	}
	if len(found) != 3 {
		t.Fatalf("got sorted=%v, want 3 of %v present in alpha order", got, want)
	}
	for i := range want {
		if found[i] != want[i] {
			t.Fatalf("asc order: found=%v, want=%v", found, want)
		}
	}
}

func TestListCloudAccountsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	seeded := seedCloudAccountsForSort(t, pg, []api.CloudAccountUpsert{
		{Provider: "outscale", Name: "ca1-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "ca2-" + suffix, Region: "eu-west-2"},
		{Provider: "outscale", Name: "ca3-" + suffix, Region: "eu-west-2"},
	})
	seededIDs := make(map[uuid.UUID]bool, len(seeded))
	for _, a := range seeded {
		seededIDs[a.ID] = true
	}

	// No sort params → newest-first, no duplicates.
	seen := map[uuid.UUID]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListCloudAccounts(ctx, api.CloudAccountListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, a := range items {
			if !seededIDs[a.ID] {
				continue // ignore rows seeded by other tests
			}
			if seen[a.ID] {
				t.Fatalf("duplicate %s across pages", a.ID)
			}
			seen[a.ID] = true
			total++
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total != 3 {
		t.Fatalf("total=%d want 3", total)
	}
}
