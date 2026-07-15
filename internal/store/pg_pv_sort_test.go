package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedPVsForSort creates a cluster → persistent volumes fixture.
// Returns the cluster ID.
func seedPVsForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "pv-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertPersistentVolume(ctx, api.PersistentVolumeCreate{ClusterId: *c.Id, Name: n}); err != nil {
			t.Fatalf("pv %s: %v", n, err)
		}
	}
	return *c.Id
}

// seedPVCsForSort creates a cluster → namespace → PVCs fixture.
// Returns the namespace ID.
func seedPVCsForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "pvc-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertPersistentVolumeClaim(ctx, api.PersistentVolumeClaimCreate{NamespaceId: *ns.Id, Name: n}); err != nil {
			t.Fatalf("pvc %s: %v", n, err)
		}
	}
	return *ns.Id
}

// ── Persistent Volumes ────────────────────────────────────────────────────────

func TestListPersistentVolumesSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedPVsForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pv := range items {
			got = append(got, pv.Name)
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
	items, _, err := pg.ListPersistentVolumes(
		ctx,
		api.PersistentVolumeListFilter{ClusterID: &clusterID},
		api.ListPage{Limit: 10, Sort: "name", Order: "desc"},
	)
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListPersistentVolumesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedPVsForSort(t, pg, []string{"pv-data", "pv-logs", "nfs-data", "my_vol"})

	cases := []struct {
		term string
		want int
	}{
		{"data", 2},   // substring
		{"pv-*", 2},   // prefix glob
		{"*-data", 2}, // suffix glob
		{"my_vol", 1}, // literal underscore (must not match e.g. "myxvol")
		{"PV", 2},     // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{ClusterID: &clusterID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListPersistentVolumesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedPVsForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(
		err,
		api.ErrInvalidSort,
	) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(
		err,
		api.ErrInvalidCursor,
	) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(
		err,
		api.ErrInvalidCursor,
	) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListPersistentVolumesSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedPVsForSort(t, pg, []string{"pv1", "pv2", "pv3", "pv4", "pv5"})
	// Two phase groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE persistent_volumes SET phase = CASE WHEN name IN ('pv1','pv2') THEN 'Bound' ELSE 'Released' END
		  WHERE cluster_id = $1`, clusterID); err != nil {
		t.Fatalf("set phases: %v", err)
	}

	seen := map[string]bool{}
	var phases []string
	page := api.ListPage{Limit: 2, Sort: "phase"}
	for {
		items, next, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			pv := &items[i]
			if seen[pv.Id.String()] {
				t.Fatalf("pv %s duplicated across pages (tiebreaker broken)", pv.Id)
			}
			seen[pv.Id.String()] = true
			if pv.Phase != nil {
				phases = append(phases, *pv.Phase)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(phases) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(phases))
	}
	want := []string{"Bound", "Bound", "Released", "Released", "Released"}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", phases, want)
		}
	}
}

func TestListPersistentVolumesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedPVsForSort(t, pg, []string{"pv1", "pv2", "pv3", "pv4", "pv5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListPersistentVolumes(ctx, api.PersistentVolumeListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pv := range items {
			if seen[pv.Id.String()] {
				t.Fatalf("duplicate %s across pages", pv.Id)
			}
			seen[pv.Id.String()] = true
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

// ── Persistent Volume Claims ──────────────────────────────────────────────────

func TestListPersistentVolumeClaimsSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPVCsForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pvc := range items {
			got = append(got, pvc.Name)
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
	items, _, err := pg.ListPersistentVolumeClaims(
		ctx,
		api.PersistentVolumeClaimListFilter{NamespaceID: &nsID},
		api.ListPage{Limit: 10, Sort: "name", Order: "desc"},
	)
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListPersistentVolumeClaimsNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPVCsForSort(t, pg, []string{"pvc-data", "pvc-logs", "nfs-data", "my_claim"})

	cases := []struct {
		term string
		want int
	}{
		{"data", 2},     // substring
		{"pvc-*", 2},    // prefix glob
		{"*-data", 2},   // suffix glob
		{"my_claim", 1}, // literal underscore (must not match e.g. "myxclaim")
		{"PVC", 2},      // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListPersistentVolumeClaims(
			ctx,
			api.PersistentVolumeClaimListFilter{NamespaceID: &nsID, Name: &name},
			api.ListPage{Limit: 50},
		)
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListPersistentVolumeClaimsRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedPVCsForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(
		err,
		api.ErrInvalidSort,
	) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(
		err,
		api.ErrInvalidCursor,
	) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(
		err,
		api.ErrInvalidCursor,
	) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListPersistentVolumeClaimsSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPVCsForSort(t, pg, []string{"pvc1", "pvc2", "pvc3", "pvc4", "pvc5"})
	// Two phase groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE persistent_volume_claims SET phase = CASE WHEN name IN ('pvc1','pvc2') THEN 'Bound' ELSE 'Pending' END
		  WHERE namespace_id = $1`, nsID); err != nil {
		t.Fatalf("set phases: %v", err)
	}

	seen := map[string]bool{}
	var phases []string
	page := api.ListPage{Limit: 2, Sort: "phase"}
	for {
		items, next, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			pvc := &items[i]
			if seen[pvc.Id.String()] {
				t.Fatalf("pvc %s duplicated across pages (tiebreaker broken)", pvc.Id)
			}
			seen[pvc.Id.String()] = true
			if pvc.Phase != nil {
				phases = append(phases, *pvc.Phase)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(phases) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(phases))
	}
	want := []string{"Bound", "Bound", "Pending", "Pending", "Pending"}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", phases, want)
		}
	}
}

func TestListPersistentVolumeClaimsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPVCsForSort(t, pg, []string{"pvc1", "pvc2", "pvc3", "pvc4", "pvc5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListPersistentVolumeClaims(ctx, api.PersistentVolumeClaimListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pvc := range items {
			if seen[pvc.Id.String()] {
				t.Fatalf("duplicate %s across pages", pvc.Id)
			}
			seen[pvc.Id.String()] = true
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
