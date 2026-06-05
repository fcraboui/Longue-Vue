package api

// Handler tests for GET /v1/network-policies and GET /v1/network-policies/{id}
// (flow-matrix Phase 1, Tasks 17 + 18). Uses the in-memory npFake / sgFake
// fakes from server_cloud_fake_test.go; no Postgres dependency.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildNetworkPolicyMux wires the two read-only network-policy routes.
func buildNetworkPolicyMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller != nil {
				r = r.WithContext(auth.WithCaller(r.Context(), caller))
			}
			h.ServeHTTP(w, r)
		})
	}
	mux.Handle("GET /v1/network-policies", wrap(HandleListNetworkPolicies(store)))
	mux.Handle("GET /v1/network-policies/{id}", wrap(HandleGetNetworkPolicy(store)))
	return mux
}

// readCaller returns a minimal *auth.Caller with ScopeRead so the scope guard passes.
func readCaller() *auth.Caller {
	return &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: testViewerRole,
		Role:     auth.RoleViewer,
		Scopes:   auth.ScopesForRole(auth.RoleViewer),
	}
}

func TestListNetworkPolicies_FilterByCluster_AndNamespace(t *testing.T) {
	resetNPFake()
	store := newMemStore()

	clusterA := uuid.New()
	clusterB := uuid.New()
	nsX := uuid.New()
	nsY := uuid.New()

	// Three policies: two in clusterA (different namespaces), one in clusterB.
	npA1 := upsertNetworkPolicyFake(NetworkPolicyRow{
		ClusterID: clusterA, NamespaceID: nsX, Name: "allow-ingress",
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{testPolicyIngress},
	})
	_ = npA1
	npA2 := upsertNetworkPolicyFake(NetworkPolicyRow{
		ClusterID: clusterA, NamespaceID: nsY, Name: "deny-egress",
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{"Egress"},
	})
	_ = npA2
	upsertNetworkPolicyFake(NetworkPolicyRow{
		ClusterID: clusterB, NamespaceID: nsX, Name: "other",
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{testPolicyIngress},
	})

	h := buildNetworkPolicyMux(t, store, readCaller())

	// List clusterA + nsX → expect exactly 1 result.
	rr := doReq(t, h, http.MethodGet,
		fmt.Sprintf("/v1/network-policies?cluster_id=%s&namespace_id=%s", clusterA, nsX),
		nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items      []NetworkPolicyRow `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("want 1 item, got %d: %+v", len(resp.Items), resp.Items)
	}
	if resp.Items[0].Name != "allow-ingress" {
		t.Errorf("want 'allow-ingress', got %q", resp.Items[0].Name)
	}

	// List clusterA (no namespace filter) → expect 2.
	rr2 := doReq(t, h, http.MethodGet,
		fmt.Sprintf("/v1/network-policies?cluster_id=%s", clusterA),
		nil)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr2.Code, rr2.Body.String())
	}
	var resp2 struct {
		Items []NetworkPolicyRow `json:"items"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if len(resp2.Items) != 2 {
		t.Fatalf("want 2 items for clusterA, got %d", len(resp2.Items))
	}
}

func TestListNetworkPolicies_MissingClusterID_Returns400(t *testing.T) {
	resetNPFake()
	store := newMemStore()
	h := buildNetworkPolicyMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/network-policies", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestGetNetworkPolicy_EmbedsRules(t *testing.T) {
	resetNPFake()
	store := newMemStore()

	clusterID := uuid.New()
	nsID := uuid.New()
	policyID := upsertNetworkPolicyFake(NetworkPolicyRow{
		ClusterID: clusterID, NamespaceID: nsID, Name: "test-policy",
		PodSelector: json.RawMessage(`{}`),
		PolicyTypes: []string{testPolicyIngress},
	})
	replaceNetworkPolicyRulesFake(policyID, []NetworkPolicyRuleRow{
		{Direction: testDirIngress, PeerKind: "selector", PeerPodSelector: json.RawMessage(`{"matchLabels":{"app":"web"}}`)},
		{Direction: testDirIngress, PeerKind: "ip_block", PeerIPBlockCIDR: testCIDR10},
	})

	h := buildNetworkPolicyMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/network-policies/%s", policyID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var resp netpolDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != policyID {
		t.Errorf("id mismatch: got %s want %s", resp.ID, policyID)
	}
	if resp.Name != "test-policy" {
		t.Errorf("name: got %q want test-policy", resp.Name)
	}
	if len(resp.Rules) != 2 {
		t.Fatalf("want 2 rules, got %d", len(resp.Rules))
	}
}

func TestGetNetworkPolicy_NotFound(t *testing.T) {
	resetNPFake()
	store := newMemStore()
	h := buildNetworkPolicyMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/network-policies/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}
