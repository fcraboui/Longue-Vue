package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedPodsForSort creates cluster → namespace → pods. Returns nsID.
func seedPodsForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "pod-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: n}); err != nil {
			t.Fatalf("pod %s: %v", n, err)
		}
	}
	return *ns.Id
}

// seedWorkloadsForSort creates cluster → namespace → workloads. Returns nsID.
func seedWorkloadsForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "wl-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: n}); err != nil {
			t.Fatalf("workload %s: %v", n, err)
		}
	}
	return *ns.Id
}

// ── Pods ─────────────────────────────────────────────────────────────────────

func TestListPodsSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPodsForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListPods(ctx, api.PodListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pod := range items {
			got = append(got, pod.Name)
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
	items, _, err := pg.ListPods(
		ctx,
		api.PodListFilter{NamespaceID: &nsID},
		api.ListPage{Limit: 10, Sort: "name", Order: "desc"},
	)
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListPodsNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	// "my_unit" must not match "myxunit" (underscore escape) and does not
	// contain "web" so the case-insensitive check stays at exactly 2.
	nsID := seedPodsForSort(t, pg, []string{"web-data", "web-logs", "nfs-data", "my_unit"})

	cases := []struct {
		term string
		want int
	}{
		{"data", 2},    // substring
		{"web-*", 2},   // prefix glob
		{"*-data", 2},  // suffix glob
		{"my_unit", 1}, // literal underscore (must not match e.g. "myxunit")
		{"WEB", 2},     // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListPods(ctx, api.PodListFilter{NamespaceID: &nsID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListPodsRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedPodsForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListPods(ctx, api.PodListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListPods(ctx, api.PodListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListPods(ctx, api.PodListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListPods(ctx, api.PodListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListPodsSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPodsForSort(t, pg, []string{"pod1", "pod2", "pod3", "pod4", "pod5"})
	// Two phase groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE pods SET phase = CASE WHEN name IN ('pod1','pod2') THEN 'Running' ELSE 'Pending' END
		  WHERE namespace_id = $1`, nsID); err != nil {
		t.Fatalf("set phases: %v", err)
	}

	seen := map[string]bool{}
	var phases []string
	page := api.ListPage{Limit: 2, Sort: "phase"}
	for {
		items, next, err := pg.ListPods(ctx, api.PodListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			pod := &items[i]
			if seen[pod.Id.String()] {
				t.Fatalf("pod %s duplicated across pages (tiebreaker broken)", pod.Id)
			}
			seen[pod.Id.String()] = true
			if pod.Phase != nil {
				phases = append(phases, *pod.Phase)
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
	want := []string{"Pending", "Pending", "Pending", "Running", "Running"}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", phases, want)
		}
	}
}

func TestListPodsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedPodsForSort(t, pg, []string{"pod1", "pod2", "pod3", "pod4", "pod5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListPods(ctx, api.PodListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, pod := range items {
			if seen[pod.Id.String()] {
				t.Fatalf("duplicate %s across pages", pod.Id)
			}
			seen[pod.Id.String()] = true
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

// ── Workloads ─────────────────────────────────────────────────────────────────

func TestListWorkloadsSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedWorkloadsForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, w := range items {
			got = append(got, w.Name)
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
	items, _, err := pg.ListWorkloads(
		ctx,
		api.WorkloadListFilter{NamespaceID: &nsID},
		api.ListPage{Limit: 10, Sort: "name", Order: "desc"},
	)
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListWorkloadsNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	// "my_unit" must not match "myxunit" (underscore escape) and does not
	// contain "web" so the case-insensitive check stays at exactly 2.
	nsID := seedWorkloadsForSort(t, pg, []string{"web-data", "web-logs", "nfs-data", "my_unit"})

	cases := []struct {
		term string
		want int
	}{
		{"data", 2},    // substring
		{"web-*", 2},   // prefix glob
		{"*-data", 2},  // suffix glob
		{"my_unit", 1}, // literal underscore (must not match e.g. "myxunit")
		{"WEB", 2},     // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: &nsID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListWorkloadsRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedWorkloadsForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListWorkloadsSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedWorkloadsForSort(t, pg, []string{"wl1", "wl2", "wl3", "wl4", "wl5"})
	// Two kind groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE workloads SET kind = CASE WHEN name IN ('wl1','wl2') THEN 'Deployment' ELSE 'StatefulSet' END
		  WHERE namespace_id = $1`, nsID); err != nil {
		t.Fatalf("set kinds: %v", err)
	}

	seen := map[string]bool{}
	var kinds []string
	page := api.ListPage{Limit: 2, Sort: "kind"}
	for {
		items, next, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			w := &items[i]
			if seen[w.Id.String()] {
				t.Fatalf("workload %s duplicated across pages (tiebreaker broken)", w.Id)
			}
			seen[w.Id.String()] = true
			kinds = append(kinds, string(w.Kind))
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(kinds) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(kinds))
	}
	want := []string{"Deployment", "Deployment", "StatefulSet", "StatefulSet", "StatefulSet"}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kind order = %v, want %v", kinds, want)
		}
	}
}

func TestListWorkloadsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedWorkloadsForSort(t, pg, []string{"wl1", "wl2", "wl3", "wl4", "wl5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, w := range items {
			if seen[w.Id.String()] {
				t.Fatalf("duplicate %s across pages", w.Id)
			}
			seen[w.Id.String()] = true
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

// ── ImageSubstring escape regression ─────────────────────────────────────────

func TestImageSubstringEscapesMetacharactersWorkloads(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Seed two workloads: images "repo/my_app:1" and "repo/myxapp:1".
	// "my_app" with an unescaped _ would match both via ILIKE;
	// with ESCAPE it must match exactly one.
	clusterName := "escape-wl-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	img1 := api.ContainerList{{"name": "app", "image": "repo/my_app:1"}}
	img2 := api.ContainerList{{"name": "app", "image": "repo/myxapp:1"}}
	if _, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "wl-underscore", Containers: &img1,
	}); err != nil {
		t.Fatalf("upsert wl1: %v", err)
	}
	if _, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "wl-xapp", Containers: &img2,
	}); err != nil {
		t.Fatalf("upsert wl2: %v", err)
	}

	img := "my_app"
	items, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{ImageSubstring: &img}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("my_app must not match myxapp: got %d items, want 1", len(items))
	}
}

func TestImageSubstringEscapesMetacharactersPods(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Seed two pods with images "repo/my_app:1" and "repo/myxapp:1".
	clusterName := "escape-pod-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	img1 := api.ContainerList{{"name": "app", "image": "repo/my_app:1"}}
	img2 := api.ContainerList{{"name": "app", "image": "repo/myxapp:1"}}
	if _, _, err := pg.UpsertPod(ctx, api.PodCreate{
		NamespaceId: *ns.Id, Name: "pod-underscore", Containers: &img1,
	}); err != nil {
		t.Fatalf("upsert pod1: %v", err)
	}
	if _, _, err := pg.UpsertPod(ctx, api.PodCreate{
		NamespaceId: *ns.Id, Name: "pod-xapp", Containers: &img2,
	}); err != nil {
		t.Fatalf("upsert pod2: %v", err)
	}

	img := "my_app"
	items, _, err := pg.ListPods(ctx, api.PodListFilter{ImageSubstring: &img}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("my_app must not match myxapp: got %d items, want 1", len(items))
	}
}
