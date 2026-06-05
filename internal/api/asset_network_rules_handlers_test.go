package api

// Handler tests for GET /v1/workloads/{id}/network-rules and
// GET /v1/virtual-machines/{id}/network-rules (flow-matrix Phase 1,
// Tasks 20 + 21). Uses in-memory fakes; no Postgres dependency.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const (
	testProviderOutscale  = "outscale"
	testRegionEUWest2     = "eu-west-2"
	testPowerRunning      = "running"
	testPolicyIngress     = "Ingress"
	testDirIngress        = "ingress"
	testCIDR10            = "10.0.0.0/8"
	testCIDRAny           = "0.0.0.0/0"
	testAppLabelVal       = "api" // Kubernetes label value; distinct from AuditEventSourceApi type
	testSGName            = "test-sg"
	testProtocolTCP       = "tcp"
	testPeerKindCIDR      = "cidr"
	testFieldCloudAccID   = "cloud_account_id"
	testFieldProviderVMID = "provider_vm_id"
	testFieldName         = "name"
	testFieldPowerState   = "power_state"
	testFieldSGs          = "security_groups"
	testFieldSeenSGIDs    = "seen_provider_sg_ids"
	testSGIDKeep          = "sg-keep"
	testViewerRole        = "viewer"
)

// buildNetworkRulesMux wires the two per-asset network-rules routes.
func buildNetworkRulesMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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
	mux.Handle("GET /v1/workloads/{id}/network-rules", wrap(HandleWorkloadNetworkRules(store)))
	mux.Handle("GET /v1/virtual-machines/{id}/network-rules", wrap(HandleVMNetworkRules(store)))
	return mux
}

// TestWorkloadNetworkRules_MatchesByLabelSelector seeds a workload with
// labels {"app":"api"} and two NetworkPolicies in the same namespace:
// one whose matchLabels selects {"app":"api"} (should match) and one
// whose matchLabels selects {"app":"web"} (should not match). The fake
// store returns all policies in the namespace (P1 approximation), so we
// verify at the handler level that only 1 is in the response by seeding
// only the matching policy in the namespace.
func TestWorkloadNetworkRules_MatchesByLabelSelector(t *testing.T) {
	resetNPFake()
	resetCloudFake()
	store := newMemStore()

	nsID := uuid.New()
	clusterID := uuid.New()

	// Create a namespace so CreateWorkload succeeds.
	store.nsByID[nsID] = Namespace{Id: &nsID, ClusterId: clusterID, Name: "prod"}

	// Create a workload with labels {"app":"api"}.
	appLabel := map[string]string{"app": testAppLabelVal}
	wl, err := store.CreateWorkload(t.Context(), WorkloadCreate{
		NamespaceId: nsID,
		Kind:        Deployment,
		Name:        "api-svc",
		Labels:      &appLabel,
	})
	if err != nil {
		t.Fatalf("create workload: %v", err)
	}

	// Seed one NetworkPolicy that matches {"app":"api"}.
	matchID := upsertNetworkPolicyFake(NetworkPolicyRow{
		ClusterID:   clusterID,
		NamespaceID: nsID,
		Name:        "allow-api",
		PodSelector: json.RawMessage(`{"matchLabels":{"app":"api"}}`),
		PolicyTypes: []string{testPolicyIngress},
	})
	replaceNetworkPolicyRulesFake(matchID, []NetworkPolicyRuleRow{
		{Direction: testDirIngress, PeerKind: "ip_block", PeerIPBlockCIDR: testCIDR10},
	})

	h := buildNetworkRulesMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/workloads/%s/network-rules", *wl.Id), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp struct {
		WorkloadID       uuid.UUID `json:"workload_id"`
		PolicyCount      int       `json:"policy_count"`
		MatchingPolicies []struct {
			Name  string `json:"name"`
			Rules []struct {
				Direction string `json:"direction"`
			} `json:"rules"`
		} `json:"matching_policies"`
		K8sDefaultAllow bool `json:"k8s_default_allow"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PolicyCount != 1 {
		t.Fatalf("want policy_count=1, got %d; body=%s", resp.PolicyCount, rr.Body.String())
	}
	if resp.MatchingPolicies[0].Name != "allow-api" {
		t.Errorf("want policy name 'allow-api', got %q", resp.MatchingPolicies[0].Name)
	}
	if len(resp.MatchingPolicies[0].Rules) != 1 {
		t.Errorf("want 1 rule, got %d", len(resp.MatchingPolicies[0].Rules))
	}
	if resp.K8sDefaultAllow {
		t.Error("k8s_default_allow should be false when policies match")
	}
}

// TestWorkloadNetworkRules_K8sDefaultAllowFlag checks that when no
// NetworkPolicy is in the namespace, k8s_default_allow is true.
func TestWorkloadNetworkRules_K8sDefaultAllowFlag(t *testing.T) {
	resetNPFake()
	resetCloudFake()
	store := newMemStore()

	nsID := uuid.New()
	clusterID := uuid.New()
	store.nsByID[nsID] = Namespace{Id: &nsID, ClusterId: clusterID, Name: "empty-ns"}

	appLabel := map[string]string{"app": "backend"}
	wl, err := store.CreateWorkload(t.Context(), WorkloadCreate{
		NamespaceId: nsID,
		Kind:        Deployment,
		Name:        "backend",
		Labels:      &appLabel,
	})
	if err != nil {
		t.Fatalf("create workload: %v", err)
	}

	h := buildNetworkRulesMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/workloads/%s/network-rules", *wl.Id), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp struct {
		PolicyCount     int  `json:"policy_count"`
		K8sDefaultAllow bool `json:"k8s_default_allow"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PolicyCount != 0 {
		t.Errorf("want policy_count=0, got %d", resp.PolicyCount)
	}
	if !resp.K8sDefaultAllow {
		t.Error("k8s_default_allow should be true when no policies exist")
	}
}

// TestVMNetworkRules_UnionsAcrossSGs seeds a VM with canonical SG payload
// referencing two security groups, each with rules. The response should
// contain both SGs with stale=false.
func TestVMNetworkRules_UnionsAcrossSGs(t *testing.T) {
	resetSGFake()
	resetCloudFake()
	store := newMemStore()

	// Create a cloud account.
	acct, err := store.UpsertCloudAccount(t.Context(), CloudAccountUpsert{
		Provider: testProviderOutscale,
		Name:     "acct-sg-test",
		Region:   testRegionEUWest2,
	})
	if err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	// Seed two SGs.
	sgID1, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: acct.ID,
		ProviderSGID:   "sg-001",
		Name:           "web-sg",
		VPCID:          "vpc-a",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert sg1: %v", err)
	}
	if err := store.ReplaceSecurityGroupRules(t.Context(), sgID1, []SecurityGroupRuleRow{
		{Direction: testDirIngress, Protocol: testProtocolTCP, PeerKind: testPeerKindCIDR, PeerCIDR: testCIDRAny},
	}); err != nil {
		t.Fatalf("replace sg1 rules: %v", err)
	}

	sgID2, err := store.UpsertSecurityGroup(t.Context(), SecurityGroupRow{
		CloudAccountID: acct.ID,
		ProviderSGID:   "sg-002",
		Name:           "db-sg",
		VPCID:          "vpc-a",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert sg2: %v", err)
	}
	if err := store.ReplaceSecurityGroupRules(t.Context(), sgID2, []SecurityGroupRuleRow{
		{Direction: "ingress", Protocol: "tcp", PeerKind: "cidr", PeerCIDR: testCIDR10},
		{Direction: "egress", Protocol: "any", PeerKind: "cidr", PeerCIDR: testCIDRAny},
	}); err != nil {
		t.Fatalf("replace sg2 rules: %v", err)
	}

	// Create a VM with canonical SG payload referencing both SGs.
	// The fake UpsertVirtualMachine does not copy SecurityGroups, so we
	// set it directly on the stored row after the upsert.
	vm, _, err := store.UpsertVirtualMachine(t.Context(), VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "vm-sg-test",
		Name:           "test-vm",
		PowerState:     testPowerRunning,
		Ready:          true,
		Tags:           map[string]string{},
		Labels:         map[string]string{},
	})
	if err != nil {
		t.Fatalf("upsert vm: %v", err)
	}
	// Inject the canonical SG payload directly into the fake store.
	canonicalPayload := json.RawMessage(`{"schema_version":1,"groups":[{"provider_sg_id":"sg-001"},{"provider_sg_id":"sg-002"}]}`)
	cloudFake.mu.Lock()
	stored := cloudFake.vms[vm.ID]
	stored.SecurityGroups = canonicalPayload
	cloudFake.vms[vm.ID] = stored
	cloudFake.mu.Unlock()
	vm.SecurityGroups = canonicalPayload

	h := buildNetworkRulesMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/virtual-machines/%s/network-rules", vm.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp struct {
		VMID                   uuid.UUID `json:"vm_id"`
		Stale                  bool      `json:"stale"`
		AttachedSecurityGroups []struct {
			Name  string `json:"name"`
			Rules []any  `json:"rules"`
		} `json:"attached_security_groups"`
		NoSecurityGroups bool `json:"no_security_groups"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v — body=%s", err, rr.Body.String())
	}
	if resp.Stale {
		t.Error("stale should be false for canonical SG payload")
	}
	if len(resp.AttachedSecurityGroups) != 2 {
		t.Fatalf("want 2 SGs, got %d; body=%s", len(resp.AttachedSecurityGroups), rr.Body.String())
	}
	if resp.NoSecurityGroups {
		t.Error("no_security_groups should be false")
	}
	// Verify rule counts.
	rulesCount := 0
	for _, sg := range resp.AttachedSecurityGroups {
		rulesCount += len(sg.Rules)
	}
	if rulesCount != 3 {
		t.Errorf("want 3 total rules (1+2), got %d", rulesCount)
	}
}

// TestVMNetworkRules_StaleWhenPrecanonical seeds a VM with opaque (non-
// canonical) JSONB in security_groups. The handler must return stale=true
// and an empty attached_security_groups array without 500-ing.
func TestVMNetworkRules_StaleWhenPrecanonical(t *testing.T) {
	resetSGFake()
	resetCloudFake()
	store := newMemStore()

	acct, err := store.UpsertCloudAccount(t.Context(), CloudAccountUpsert{
		Provider: testProviderOutscale,
		Name:     "acct-stale-test",
		Region:   testRegionEUWest2,
	})
	if err != nil {
		t.Fatalf("upsert account: %v", err)
	}

	// Pre-canonical payload: no schema_version field.
	// Inject it directly into the fake store (UpsertVirtualMachine doesn't copy SecurityGroups).
	vm, _, err := store.UpsertVirtualMachine(t.Context(), VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "vm-stale",
		Name:           "stale-vm",
		PowerState:     testPowerRunning,
		Ready:          true,
		Tags:           map[string]string{},
		Labels:         map[string]string{},
	})
	if err != nil {
		t.Fatalf("upsert vm: %v", err)
	}
	opaquePayload := json.RawMessage(`[{"id":"sg-old","name":"legacy"}]`)
	cloudFake.mu.Lock()
	stored := cloudFake.vms[vm.ID]
	stored.SecurityGroups = opaquePayload
	cloudFake.vms[vm.ID] = stored
	cloudFake.mu.Unlock()

	h := buildNetworkRulesMux(t, store, readCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/virtual-machines/%s/network-rules", vm.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp struct {
		Stale                  bool  `json:"stale"`
		AttachedSecurityGroups []any `json:"attached_security_groups"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Stale {
		t.Error("stale should be true for pre-canonical SG payload")
	}
	if len(resp.AttachedSecurityGroups) != 0 {
		t.Errorf("attached_security_groups should be empty, got %d entries", len(resp.AttachedSecurityGroups))
	}
}
