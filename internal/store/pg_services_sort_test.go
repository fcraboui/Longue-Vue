package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedServicesForSort creates a cluster → namespace → services fixture.
// Returns the namespace ID.
func seedServicesForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "svc-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertService(ctx, api.ServiceCreate{NamespaceId: *ns.Id, Name: n}); err != nil {
			t.Fatalf("service %s: %v", n, err)
		}
	}
	return *ns.Id
}

// seedIngressesForSort creates a cluster → namespace → ingresses fixture.
// Returns the namespace ID.
func seedIngressesForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "ing-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: "test-ns-" + uuid.New().String()[:8]})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertIngress(ctx, api.IngressCreate{NamespaceId: *ns.Id, Name: n}); err != nil {
			t.Fatalf("ingress %s: %v", n, err)
		}
	}
	return *ns.Id
}

// ── Services ──────────────────────────────────────────────────────────────────

func TestListServicesSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedServicesForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListServices(ctx, api.ServiceListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range items {
			got = append(got, s.Name)
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
	items, _, err := pg.ListServices(ctx, api.ServiceListFilter{NamespaceID: &nsID}, api.ListPage{Limit: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListServicesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedServicesForSort(t, pg, []string{"web-frontend", "web-backend", "db-service", "my_svc"})

	cases := []struct {
		term string
		want int
	}{
		{"web", 2},       // substring
		{"web-*", 2},     // prefix glob
		{"*-service", 1}, // suffix glob
		{"my_svc", 1},    // literal underscore (must not match e.g. "myxsvc")
		{"WEB", 2},       // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListServices(ctx, api.ServiceListFilter{NamespaceID: &nsID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListServicesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedServicesForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListServices(ctx, api.ServiceListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListServices(ctx, api.ServiceListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListServices(ctx, api.ServiceListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListServices(ctx, api.ServiceListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListServicesSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedServicesForSort(t, pg, []string{"svc1", "svc2", "svc3", "svc4", "svc5"})
	// Two type groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE services SET type = CASE WHEN name IN ('svc1','svc2') THEN 'ClusterIP' ELSE 'LoadBalancer' END`); err != nil {
		t.Fatalf("set types: %v", err)
	}

	seen := map[string]bool{}
	var types []string
	page := api.ListPage{Limit: 2, Sort: "type"}
	for {
		items, next, err := pg.ListServices(ctx, api.ServiceListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			s := &items[i]
			if seen[s.Id.String()] {
				t.Fatalf("service %s duplicated across pages (tiebreaker broken)", s.Id)
			}
			seen[s.Id.String()] = true
			if s.Type != nil {
				types = append(types, string(*s.Type))
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(types) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(types))
	}
	want := []string{"ClusterIP", "ClusterIP", "LoadBalancer", "LoadBalancer", "LoadBalancer"}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("type order = %v, want %v", types, want)
		}
	}
}

func TestListServicesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedServicesForSort(t, pg, []string{"svc1", "svc2", "svc3", "svc4", "svc5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListServices(ctx, api.ServiceListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, s := range items {
			if seen[s.Id.String()] {
				t.Fatalf("duplicate %s across pages", s.Id)
			}
			seen[s.Id.String()] = true
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

// ── Ingresses ─────────────────────────────────────────────────────────────────

func TestListIngressesSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedIngressesForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListIngresses(ctx, api.IngressListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, ing := range items {
			got = append(got, ing.Name)
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
	items, _, err := pg.ListIngresses(ctx, api.IngressListFilter{NamespaceID: &nsID}, api.ListPage{Limit: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListIngressesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedIngressesForSort(t, pg, []string{"prod-web", "prod-db", "dev-web", "my_ing"})

	cases := []struct {
		term string
		want int
	}{
		{"web", 2},    // substring
		{"prod-*", 2}, // prefix glob
		{"*-web", 2},  // suffix glob
		{"my_ing", 1}, // literal underscore (must not match e.g. "myxing")
		{"WEB", 2},    // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListIngresses(ctx, api.IngressListFilter{NamespaceID: &nsID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListIngressesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedIngressesForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListIngresses(ctx, api.IngressListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListIngresses(ctx, api.IngressListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListIngresses(ctx, api.IngressListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListIngresses(ctx, api.IngressListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks
func TestListIngressesSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedIngressesForSort(t, pg, []string{"ing1", "ing2", "ing3", "ing4", "ing5"})
	// Two ingress_class_name groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE ingresses SET ingress_class_name = CASE WHEN name IN ('ing1','ing2') THEN 'nginx' ELSE 'traefik' END`); err != nil {
		t.Fatalf("set ingress_class_name: %v", err)
	}

	seen := map[string]bool{}
	var classes []string
	page := api.ListPage{Limit: 2, Sort: "ingress_class_name"}
	for {
		items, next, err := pg.ListIngresses(ctx, api.IngressListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			ig := &items[i]
			if seen[ig.Id.String()] {
				t.Fatalf("ingress %s duplicated across pages (tiebreaker broken)", ig.Id)
			}
			seen[ig.Id.String()] = true
			if ig.IngressClassName != nil {
				classes = append(classes, *ig.IngressClassName)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(classes) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(classes))
	}
	want := []string{"nginx", "nginx", "traefik", "traefik", "traefik"}
	for i := range want {
		if classes[i] != want[i] {
			t.Fatalf("ingress_class_name order = %v, want %v", classes, want)
		}
	}
}

func TestListIngressesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	nsID := seedIngressesForSort(t, pg, []string{"ing1", "ing2", "ing3", "ing4", "ing5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListIngresses(ctx, api.IngressListFilter{NamespaceID: &nsID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, ig := range items {
			if seen[ig.Id.String()] {
				t.Fatalf("duplicate %s across pages", ig.Id)
			}
			seen[ig.Id.String()] = true
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
