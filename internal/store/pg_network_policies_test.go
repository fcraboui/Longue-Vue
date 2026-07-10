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

// seedClusterAndNamespace creates a cluster and namespace for use in
// network policy tests, following the pattern used in pg_test.go.
func seedClusterAndNamespace(t *testing.T, pg *PG) (clusterID, namespaceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "np-atomic-" + uuid.New().String()})
	if err != nil {
		t.Fatalf("seedClusterAndNamespace: ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: testStoreNSDefault})
	if err != nil {
		t.Fatalf("seedClusterAndNamespace: upsert namespace: %v", err)
	}
	return *cluster.Id, *ns.Id
}

const (
	testStoreNSProd        = "prod"
	testStoreNSDefault     = "default"
	testStoreDirIngress    = "ingress"
	testStorePolicyIngress = "Ingress"
	testStoreNameKept      = "kept"
	testStoreSGProvider    = "outscale"
	testStoreSGIDKeep      = "sg-keep"
	netpolTestCIDRCorp     = "10.0.0.0/8"
	netpolTestCIDRInternet = "0.0.0.0/0"
	netpolTestPeerSelector = "selector"
	netpolTestDirEgress    = "egress"
)

func TestUpsertNetworkPolicy_InsertAndUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "np-test-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: testStoreNSProd})
	if err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	np := NetworkPolicy{
		ClusterID:   *cluster.Id,
		NamespaceID: *ns.Id,
		Name:        "api-allow",
		PodSelector: json.RawMessage(`{"matchLabels":{"app":"api"}}`),
		PolicyTypes: []string{testStorePolicyIngress},
		SpecRaw:     json.RawMessage(`{"podSelector":{"matchLabels":{"app":"api"}},"policyTypes":["Ingress"]}`),
	}
	id1, err := pg.UpsertNetworkPolicy(ctx, np, nil)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second upsert with same (cluster, ns, name) → same ID, updated columns.
	np.SpecRaw = json.RawMessage(`{"policyTypes":["Ingress","Egress"]}`)
	np.PolicyTypes = []string{testStorePolicyIngress, "Egress"}
	id2, err := pg.UpsertNetworkPolicy(ctx, np, nil)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("upsert should keep ID stable: %s vs %s", id1, id2)
	}
	got, err := pg.GetNetworkPolicy(ctx, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.PolicyTypes) != 2 {
		t.Fatalf("expected updated PolicyTypes len 2, got %v", got.PolicyTypes)
	}
}

func TestGetNetworkPolicy_NotFound(t *testing.T) {
	pg := newTestPG(t)
	_, err := pg.GetNetworkPolicy(context.Background(), uuid.New())
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSweepNetworkPoliciesByNamespace_DeletesUnseen(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "np-sweep-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: testStoreNSProd})
	if err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	keptID, err := pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
		ClusterID:   *cluster.Id,
		NamespaceID: *ns.Id,
		Name:        testStoreNameKept,
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{testStorePolicyIngress},
		SpecRaw:     json.RawMessage(`{}`),
	}, nil)
	if err != nil {
		t.Fatalf("upsert kept: %v", err)
	}
	_, err = pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
		ClusterID:   *cluster.Id,
		NamespaceID: *ns.Id,
		Name:        "gone",
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{"Egress"},
		SpecRaw:     json.RawMessage(`{}`),
	}, nil)
	if err != nil {
		t.Fatalf("upsert gone: %v", err)
	}

	if err := pg.SweepNetworkPoliciesByNamespace(ctx, *ns.Id, []string{testStoreNameKept}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := pg.GetNetworkPolicy(ctx, keptID); err != nil {
		t.Fatalf("kept policy should still exist: %v", err)
	}

	// List all policies to verify "gone" was deleted.
	all, _, err := pg.ListNetworkPoliciesByCluster(ctx, *cluster.Id, api.NetworkPolicyListFilter{NamespaceID: ns.Id}, api.ListPage{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].Name != testStoreNameKept {
		t.Fatalf("expected only 'kept', got %d items: %+v", len(all), all)
	}
}

//nolint:gocyclo // pagination test with multiple pages and duplicate checking
func TestListNetworkPoliciesByCluster_PaginatesByLimit(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "np-paginate-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: testStoreNSDefault})
	if err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	for i := range 5 {
		name := fmt.Sprintf("policy-%d", i)
		if _, err := pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
			ClusterID:   *cluster.Id,
			NamespaceID: *ns.Id,
			Name:        name,
			PodSelector: json.RawMessage(`{}`),
			PolicyTypes: []string{testStorePolicyIngress},
			SpecRaw:     json.RawMessage(`{}`),
		}, nil); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	seen := make(map[uuid.UUID]bool)
	var cursor string
	totalFetched := 0

	// Page 1: expect 2 items + a cursor.
	page1, next1, err := pg.ListNetworkPoliciesByCluster(ctx, *cluster.Id, api.NetworkPolicyListFilter{NamespaceID: ns.Id}, api.ListPage{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d, want 2", len(page1))
	}
	if next1 == "" {
		t.Fatal("page1 next_cursor should be non-empty")
	}
	for _, np := range page1 {
		if seen[np.ID] {
			t.Fatalf("duplicate id on page1: %s", np.ID)
		}
		seen[np.ID] = true
	}
	totalFetched += len(page1)
	cursor = next1

	// Page 2: expect 2 items + a cursor.
	page2, next2, err := pg.ListNetworkPoliciesByCluster(
		ctx, *cluster.Id, api.NetworkPolicyListFilter{NamespaceID: ns.Id}, api.ListPage{Limit: 2, Cursor: cursor},
	)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len=%d, want 2", len(page2))
	}
	if next2 == "" {
		t.Fatal("page2 next_cursor should be non-empty")
	}
	for _, np := range page2 {
		if seen[np.ID] {
			t.Fatalf("duplicate id on page2: %s", np.ID)
		}
		seen[np.ID] = true
	}
	totalFetched += len(page2)
	cursor = next2

	// Page 3: expect 1 item + empty cursor (last page).
	page3, next3, err := pg.ListNetworkPoliciesByCluster(
		ctx, *cluster.Id, api.NetworkPolicyListFilter{NamespaceID: ns.Id}, api.ListPage{Limit: 2, Cursor: cursor},
	)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len=%d, want 1", len(page3))
	}
	if next3 != "" {
		t.Fatalf("page3 next_cursor should be empty, got %q", next3)
	}
	for _, np := range page3 {
		if seen[np.ID] {
			t.Fatalf("duplicate id on page3: %s", np.ID)
		}
		seen[np.ID] = true
	}
	totalFetched += len(page3)

	if totalFetched != 5 {
		t.Fatalf("total fetched=%d, want 5", totalFetched)
	}
}

// TestUpsertNetworkPolicy_PersistsPolicyAndRules verifies the happy-path
// persistence: after a successful atomic upsert, the policy row is
// retrievable and all rules are stored. This is a regression test, not a
// true atomicity test — verifying rollback on mid-tx failure would
// require error injection (deferred).
func TestUpsertNetworkPolicy_PersistsPolicyAndRules(t *testing.T) {
	ctx := t.Context()
	pg := newTestPG(t)
	clusterID, namespaceID := seedClusterAndNamespace(t, pg)

	np := NetworkPolicy{
		ClusterID: clusterID, NamespaceID: namespaceID, Name: "deny-all-ingress",
		PodSelector: []byte(`{}`), PolicyTypes: []string{testStorePolicyIngress}, SpecRaw: []byte(`{}`),
	}
	rules := []NetworkPolicyRule{
		{Direction: testStoreDirIngress, PeerKind: netpolTestPeerSelector, PeerPodSelector: []byte(`{}`), Ports: []byte(`[]`)},
		{
			Direction:         testStoreDirIngress,
			PeerKind:          "ip_block",
			PeerIPBlockCIDR:   netpolTestCIDRCorp,
			PeerIPBlockExcept: []byte(`[]`),
			Ports:             []byte(`[]`),
		},
	}

	id, err := pg.UpsertNetworkPolicy(ctx, np, rules)
	if err != nil {
		t.Fatalf("UpsertNetworkPolicy: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("expected non-nil id")
	}

	got, err := pg.GetNetworkPolicy(ctx, id)
	if err != nil {
		t.Fatalf("GetNetworkPolicy: %v", err)
	}
	if got.Name != np.Name {
		t.Errorf("name: got %q, want %q", got.Name, np.Name)
	}

	gotRules, err := pg.ListNetworkPolicyRules(ctx, id)
	if err != nil {
		t.Fatalf("ListNetworkPolicyRules: %v", err)
	}
	if len(gotRules) != 2 {
		t.Errorf("rule count: got %d, want 2", len(gotRules))
	}
}

// TestUpsertNetworkPolicy_ReplacesRulesIdempotently re-runs the atomic
// upsert with a different rule set and asserts the old rules are gone.
func TestUpsertNetworkPolicy_ReplacesRulesIdempotently(t *testing.T) {
	ctx := t.Context()
	pg := newTestPG(t)
	clusterID, namespaceID := seedClusterAndNamespace(t, pg)

	np := NetworkPolicy{
		ClusterID: clusterID, NamespaceID: namespaceID, Name: "p1",
		PodSelector: []byte(`{}`), PolicyTypes: []string{testStorePolicyIngress}, SpecRaw: []byte(`{}`),
	}
	_, err := pg.UpsertNetworkPolicy(ctx, np, []NetworkPolicyRule{
		{Direction: testStoreDirIngress, PeerKind: netpolTestPeerSelector, PeerPodSelector: []byte(`{"a":1}`), Ports: []byte(`[]`)},
		{Direction: testStoreDirIngress, PeerKind: netpolTestPeerSelector, PeerPodSelector: []byte(`{"a":2}`), Ports: []byte(`[]`)},
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	id, err := pg.UpsertNetworkPolicy(ctx, np, []NetworkPolicyRule{
		{
			Direction:         netpolTestDirEgress,
			PeerKind:          "ip_block",
			PeerIPBlockCIDR:   netpolTestCIDRInternet,
			PeerIPBlockExcept: []byte(`[]`),
			Ports:             []byte(`[]`),
		},
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	rules, err := pg.ListNetworkPolicyRules(ctx, id)
	if err != nil {
		t.Fatalf("ListNetworkPolicyRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rule count after 2nd upsert: got %d, want 1; rules=%+v", len(rules), rules)
	}
	if rules[0].Direction != netpolTestDirEgress {
		t.Errorf("rule direction: got %q, want egress", rules[0].Direction)
	}
}

// TestListNetworkPoliciesByCluster_SortByName verifies that sort=name asc
// returns rows in ascending lexicographic order.
func TestListNetworkPoliciesByCluster_SortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	clusterID, namespaceID := seedClusterAndNamespace(t, pg)

	names := []string{"zebra-pol", "alpha-pol", "mango-pol"}
	for i, n := range names {
		if _, err := pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
			ClusterID:   clusterID,
			NamespaceID: namespaceID,
			Name:        n,
			PodSelector: json.RawMessage(`{}`),
			PolicyTypes: []string{testStorePolicyIngress},
			SpecRaw:     json.RawMessage(`{}`),
		}, nil); err != nil {
			t.Fatalf("upsert %s (%d): %v", n, i, err)
		}
	}

	items, _, err := pg.ListNetworkPoliciesByCluster(
		ctx, clusterID, api.NetworkPolicyListFilter{}, api.ListPage{Limit: 10, Sort: "name", Order: "asc"},
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0].Name != "alpha-pol" || items[1].Name != "mango-pol" || items[2].Name != "zebra-pol" {
		t.Errorf("unexpected order: %q %q %q", items[0].Name, items[1].Name, items[2].Name)
	}
}

// TestListNetworkPoliciesByCluster_NameFilter verifies the name= glob/substring filter.
func TestListNetworkPoliciesByCluster_NameFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	clusterID, namespaceID := seedClusterAndNamespace(t, pg)

	for i, n := range []string{"allow-ingress", "deny-egress", "allow-egress"} {
		if _, err := pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
			ClusterID:   clusterID,
			NamespaceID: namespaceID,
			Name:        n,
			PodSelector: json.RawMessage(`{}`),
			PolicyTypes: []string{testStorePolicyIngress},
			SpecRaw:     json.RawMessage(`{}`),
		}, nil); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	// Substring: "allow" matches allow-ingress and allow-egress.
	name := "allow"
	items, _, err := pg.ListNetworkPoliciesByCluster(ctx, clusterID, api.NetworkPolicyListFilter{Name: &name}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items for name=%q, got %d: %+v", name, len(items), items)
	}

	// Glob: "deny*" matches deny-egress only.
	glob := "deny*"
	items2, _, err := pg.ListNetworkPoliciesByCluster(ctx, clusterID, api.NetworkPolicyListFilter{Name: &glob}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list glob: %v", err)
	}
	if len(items2) != 1 || items2[0].Name != "deny-egress" {
		t.Fatalf("want 1 item 'deny-egress', got %d: %+v", len(items2), items2)
	}
}

// TestListNetworkPoliciesByCluster_MismatchedCursor verifies that a cursor
// minted under a different sort key is rejected with ErrInvalidCursor.
func TestListNetworkPoliciesByCluster_MismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	clusterID, namespaceID := seedClusterAndNamespace(t, pg)

	for i := range 3 {
		if _, err := pg.UpsertNetworkPolicy(ctx, NetworkPolicy{
			ClusterID:   clusterID,
			NamespaceID: namespaceID,
			Name:        fmt.Sprintf("pol-%d", i),
			PodSelector: json.RawMessage(`{}`),
			PolicyTypes: []string{testStorePolicyIngress},
			SpecRaw:     json.RawMessage(`{}`),
		}, nil); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Mint a cursor with sort=name.
	_, cursor, err := pg.ListNetworkPoliciesByCluster(
		ctx, clusterID,
		api.NetworkPolicyListFilter{NamespaceID: &namespaceID},
		api.ListPage{Limit: 1, Sort: "name", Order: "asc"},
	)
	if err != nil {
		t.Fatalf("page1 (sort=name): %v", err)
	}
	if cursor == "" {
		t.Fatal("expected a cursor from page1")
	}

	// Use that cursor with sort=reconcile_seen_at → should fail.
	_, _, err = pg.ListNetworkPoliciesByCluster(
		ctx, clusterID,
		api.NetworkPolicyListFilter{NamespaceID: &namespaceID},
		api.ListPage{Limit: 1, Cursor: cursor},
	)
	if !errors.Is(err, api.ErrInvalidCursor) {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
}
