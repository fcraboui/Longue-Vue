package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestListClustersNameCoversDisplayName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	seed := []struct{ name, display string }{
		{"prod-eu", "Production Europe"},
		{"dev-eu", "Dev Europe"},
		{"lab", ""},
	}
	for _, s := range seed {
		c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: s.name})
		if err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
		if s.display != "" {
			d := s.display
			if _, err := pg.UpdateCluster(ctx, *c.Id, api.ClusterUpdate{DisplayName: &d}); err != nil {
				t.Fatalf("display %s: %v", s.name, err)
			}
		}
	}

	cases := []struct {
		term string
		want int
	}{
		{"europe", 2}, // display_name match, ci
		{"prod", 1},   // name match
		{"*-eu", 2},   // glob on name
		{"zzz", 0},
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListClusters(ctx, api.ClusterListFilter{Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListClustersSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		if _, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: n}); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	items, _, err := pg.ListClusters(ctx, api.ClusterListFilter{}, api.ListPage{Limit: 10, Sort: "name"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if items[0].Name != "alpha" || items[2].Name != "charlie" {
		t.Errorf("order: %s..%s, want alpha..charlie", items[0].Name, items[2].Name)
	}
}
