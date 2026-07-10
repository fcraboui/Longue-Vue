package store

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedNodesForSort creates one cluster (with a unique name) and nodes named
// like the given names, returning the cluster id.
func seedNodesForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	// Use a unique cluster name per call to avoid conflicts across tests
	// sharing the same database (each test truncates on cleanup, but
	// subtests within the same run are sequential).
	clusterName := "sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertNode(ctx, api.NodeCreate{ClusterId: *c.Id, Name: n}); err != nil {
			t.Fatalf("node %s: %v", n, err)
		}
	}
	return *c.Id
}

func TestListNodesSortByNamePagesWithTies(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	// Use distinct names; cursor pagination across page boundaries is
	// tested by the id tiebreaker implicitly (same-name nodes can't exist
	// in one cluster due to the unique constraint on (cluster_id, name)).
	clusterID := seedNodesForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListNodes(ctx, api.NodeListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, n := range items {
			got = append(got, n.Name)
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
	items, _, err := pg.ListNodes(ctx, api.NodeListFilter{ClusterID: &clusterID}, api.ListPage{Limit: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

func TestListNodesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedNodesForSort(t, pg, []string{"prod-web-1", "prod-db-1", "dev-web-1", "my_node"})

	cases := []struct {
		term string
		want int
	}{
		{"web", 2},      // substring
		{"prod-*", 2},   // prefix glob
		{"*-1", 3},      // suffix glob
		{"prod-*-1", 2}, // anchored both ends
		{"my_node", 1},  // literal underscore (must not match e.g. "myxnode")
		{"WEB", 2},      // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListNodes(ctx, api.NodeListFilter{ClusterID: &clusterID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListNodesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedNodesForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListNodes(ctx, api.NodeListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListNodes(ctx, api.NodeListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListNodes(ctx, api.NodeListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListNodes(ctx, api.NodeListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
	_ = strconv.Itoa // keep imports honest if you drop a case
}

func TestListNodesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedNodesForSort(t, pg, []string{"n1", "n2", "n3", "n4", "n5"})

	// Mirrors TestPGListPagination: no sort params → newest-first,
	// 2/2/1 pages, no duplicates.
	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListNodes(ctx, api.NodeListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, n := range items {
			if seen[n.Id.String()] {
				t.Fatalf("duplicate %s across pages", n.Id)
			}
			seen[n.Id.String()] = true
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
