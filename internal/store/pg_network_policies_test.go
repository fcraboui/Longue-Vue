package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)


func TestUpsertNetworkPolicy_InsertAndUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "np-test-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "prod"})
	if err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}

	np := NetworkPolicy{
		ClusterID:   *cluster.Id,
		NamespaceID: *ns.Id,
		Name:        "api-allow",
		PodSelector: json.RawMessage(`{"matchLabels":{"app":"api"}}`),
		PolicyTypes: []string{"Ingress"},
		SpecRaw:     json.RawMessage(`{"podSelector":{"matchLabels":{"app":"api"}},"policyTypes":["Ingress"]}`),
	}
	id1, err := pg.UpsertNetworkPolicy(ctx, np)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second upsert with same (cluster, ns, name) → same ID, updated columns.
	np.SpecRaw = json.RawMessage(`{"policyTypes":["Ingress","Egress"]}`)
	np.PolicyTypes = []string{"Ingress", "Egress"}
	id2, err := pg.UpsertNetworkPolicy(ctx, np)
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
