package store

// Integration tests for the uniform sort + name-glob surface of
// ListApplications (ADR-0042). Gated on PGX_TEST_DATABASE like the rest
// of internal/store.

import (
	"context"
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestListApplicationsSortByName seeds apps out of alphabetical order and
// verifies sort=name walks them back in order across cursor pages.
func TestListApplicationsSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	for _, name := range []string{"beta-app", "alpha-app", "delta-app"} {
		if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListApplications(ctx, api.ApplicationListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			got = append(got, items[i].Name)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	want := []string{"alpha-app", "beta-app", "delta-app"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

// TestListApplicationsGlobName proves the uniform name= glob semantics:
// `off*` anchors at the start and matches the two office-* seeds only.
func TestListApplicationsGlobName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	for _, name := range []string{"office-suite", "office-mobile", "data-platform"} {
		if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	glob := "off*"
	items, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{Name: &glob}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("glob off* got %d items want 2: %v", len(items), names(items))
	}

	// Anchored: `*form` matches the data-platform suffix only.
	suffix := "*form"
	items, _, err = pg.ListApplications(ctx, api.ApplicationListFilter{Name: &suffix}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list suffix glob: %v", err)
	}
	if len(items) != 1 || items[0].Name != "data-platform" {
		t.Fatalf("glob *form returned %v", names(items))
	}
}

// TestListApplicationsBadSort verifies an unknown sort key surfaces as
// api.ErrInvalidSort (handler translates to 400).
func TestListApplicationsBadSort(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	_, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{}, api.ListPage{Sort: "bogus"})
	if !errors.Is(err, api.ErrInvalidSort) {
		t.Fatalf("expected ErrInvalidSort, got %v", err)
	}
}

// TestListApplicationsMismatchedCursor verifies a cursor minted under
// sort=name is rejected when replayed under the default sort.
func TestListApplicationsMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	for _, name := range []string{"app-a", "app-b", "app-c"} {
		if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	_, next, err := pg.ListApplications(ctx, api.ApplicationListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil {
		t.Fatalf("mint cursor: %v", err)
	}
	if next == "" {
		t.Fatal("expected a continuation cursor")
	}

	_, _, err = pg.ListApplications(ctx, api.ApplicationListFilter{}, api.ListPage{Limit: 1, Cursor: next})
	if !errors.Is(err, api.ErrInvalidCursor) {
		t.Fatalf("expected ErrInvalidCursor on sort mismatch, got %v", err)
	}
}

// TestListApplicationsDefaultOrder verifies an unsorted request keeps the
// historical (created_at DESC, id DESC) order — newest first.
func TestListApplicationsDefaultOrder(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	for _, name := range []string{"first-app", "second-app", "third-app"} {
		if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	items, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(items))
	}
	if items[0].Name != "third-app" {
		t.Errorf("default order: first item = %q, want third-app (newest)", items[0].Name)
	}
	if items[2].Name != "first-app" {
		t.Errorf("default order: last item = %q, want first-app (oldest)", items[2].Name)
	}
}

// TestListApplicationsSortByOwnerNullsLast verifies nullable-column sort:
// rows without an owner sort into the NULLS LAST tail.
func TestListApplicationsSortByOwnerNullsLast(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	ownerB := "team-b"
	ownerA := "team-a"
	seeds := []api.ApplicationCreate{
		{Name: "owned-b", Owner: &ownerB},
		{Name: "unowned"},
		{Name: "owned-a", Owner: &ownerA},
	}
	for i := range seeds {
		if _, err := pg.CreateApplication(ctx, seeds[i]); err != nil {
			t.Fatalf("create %s: %v", seeds[i].Name, err)
		}
	}

	items, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{}, api.ListPage{Limit: 10, Sort: "owner"})
	if err != nil {
		t.Fatalf("list sort=owner: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(items))
	}
	if items[0].Name != "owned-a" || items[1].Name != "owned-b" || items[2].Name != "unowned" {
		t.Errorf("sort=owner order = %v, want [owned-a owned-b unowned]", names(items))
	}
}
