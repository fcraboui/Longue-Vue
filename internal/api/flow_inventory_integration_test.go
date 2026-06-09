//go:build integration

package api_test

// End-to-end integration test for the flow-matrix Phase 1 inventory stack
// (Tasks 9-21, ADR-0031). Exercises NetPol collection + canonical SG ingest
// storage through the read endpoints against a real PostgreSQL instance.
//
// Seeding strategy: store CRUD directly (no ingest handler) — this avoids the
// vm-collector token auth entanglement and gives a cleaner seam that validates
// the store layer and handlers independently. See Task 27 for rationale.
//
// Run:
//
//	PGX_TEST_DATABASE='postgres://longue_vue:lv@127.0.0.1:55432/longue_vue?sslmode=disable' \
//	  go test -tags=integration -count=1 -timeout 120s ./internal/api/ \
//	  -run TestFlowInventoryEndToEnd -v

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/store"
)

// flowTestEnv holds the real-PG-backed server and a read-scope caller.
type flowTestEnv struct {
	srv *httptest.Server
	pg  *store.PG
}

// newFlowTestEnv opens a real PG, runs migrations, registers cleanup, builds
// a minimal HTTP mux that routes the six flow-matrix read endpoints, and
// returns the test environment.
//
// The test server does not use auth middleware — instead we inject a
// read-scope Caller via auth.WithCaller on each request so we can focus on
// the handler logic rather than the auth plumbing.
func newFlowTestEnv(t *testing.T) *flowTestEnv {
	t.Helper()

	dsn := os.Getenv("PGX_TEST_DATABASE")
	if dsn == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping integration test")
	}

	ctx := context.Background()
	pg, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		t.Fatalf("migrate: %v", err)
	}

	// Open a raw pool for targeted cleanup (only the tables this test touches)
	// so we don't stomp on rows that parallel tests might be reading.
	rawPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		pg.Close()
		t.Fatalf("open raw pool: %v", err)
	}

	t.Cleanup(func() {
		// Clean only the tables seeded by this test. CASCADE from
		// clusters/cloud_accounts covers namespaces/workloads/VMs/netpols/SGs.
		_, _ = rawPool.Exec(context.Background(),
			"TRUNCATE clusters, cloud_accounts CASCADE")
		rawPool.Close()
		pg.Close()
	})

	// Build the mux with only the flow-matrix routes. We bypass the auth
	// middleware by injecting the caller into each request's context.
	caller := &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "flow-integ",
		Role:     auth.RoleViewer,
		Scopes:   auth.ScopesForRole(auth.RoleViewer),
	}
	injectCaller := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(auth.WithCaller(r.Context(), caller))
			h.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.Handle("GET /v1/network-policies", injectCaller(api.HandleListNetworkPolicies(pg)))
	mux.Handle("GET /v1/network-policies/{id}", injectCaller(api.HandleGetNetworkPolicy(pg)))
	mux.Handle("GET /v1/security-groups", injectCaller(api.HandleListSecurityGroups(pg)))
	mux.Handle("GET /v1/security-groups/{id}", injectCaller(api.HandleGetSecurityGroup(pg)))
	mux.Handle("GET /v1/workloads/{id}/network-rules", injectCaller(api.HandleWorkloadNetworkRules(pg)))
	mux.Handle("GET /v1/virtual-machines/{id}/network-rules", injectCaller(api.HandleVMNetworkRules(pg)))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &flowTestEnv{srv: srv, pg: pg}
}

// flowGet issues a GET and decodes the JSON response body into dst. Returns
// the HTTP status code. dst may be nil to discard the body.
func flowGet(t *testing.T, env *flowTestEnv, path string, dst any) int {
	t.Helper()
	resp, err := http.Get(env.srv.URL + path) //nolint:noctx // test helper; context not needed
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			t.Fatalf("decode GET %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

// TestFlowInventoryEndToEnd seeds the full flow-matrix Phase 1 inventory
// (cluster → namespace → workload + NetPol; cloud account → VM + SG) and
// exercises all six read endpoints.
//
//nolint:gocyclo // straight-line integration test; factoring would obscure the scenario
func TestFlowInventoryEndToEnd(t *testing.T) {
	env := newFlowTestEnv(t)
	ctx := context.Background()
	pg := env.pg

	// --- 1. Seed K8s hierarchy ------------------------------------------

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "integ-flow-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	clusterID := *cluster.Id

	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{
		ClusterId: clusterID,
		Name:      "integ-ns",
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	nsID := *ns.Id

	appLabel := map[string]string{"app": "api"}
	wl, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: nsID,
		Kind:        api.Deployment,
		Name:        "api-server",
		Labels:      &appLabel,
	})
	if err != nil {
		t.Fatalf("create workload: %v", err)
	}
	wlID := *wl.Id

	// --- 2. Seed a NetworkPolicy -----------------------------------------

	if _, err := pg.UpsertNetworkPolicy(ctx, api.NetworkPolicyRow{
		ClusterID:   clusterID,
		NamespaceID: nsID,
		Name:        "api-allow",
		PodSelector: json.RawMessage(`{"matchLabels":{"app":"api"}}`),
		PolicyTypes: []string{"Ingress"},
		SpecRaw:     json.RawMessage(`{}`),
	}, []api.NetworkPolicyRuleRow{
		{
			Direction:       "ingress",
			PeerKind:        "selector",
			PeerPodSelector: json.RawMessage(`{"matchLabels":{"app":"web"}}`),
			Ports:           json.RawMessage(`[{"protocol":"TCP","port":8080}]`),
		},
	}); err != nil {
		t.Fatalf("upsert network policy: %v", err)
	}

	// --- 3. Seed cloud account + VM + canonical SGs ----------------------

	acct, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale",
		Name:     "integ-flow-account",
		Region:   "eu-west-2",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}
	acctID := acct.ID

	// Seed a security group with a PostgreSQL rule.
	sgID, err := pg.UpsertSecurityGroup(ctx, api.SecurityGroupRow{
		CloudAccountID: acctID,
		ProviderSGID:   "sg-integ-001",
		Name:           "postgres-sg",
		VPCID:          "vpc-integ",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert security group: %v", err)
	}
	fromPort := 5432
	toPort := 5432
	if err := pg.ReplaceSecurityGroupRules(ctx, sgID, []api.SecurityGroupRuleRow{
		{
			Direction: "ingress",
			Protocol:  "tcp",
			FromPort:  &fromPort,
			ToPort:    &toPort,
			PeerKind:  "cidr",
			PeerCIDR:  "10.0.0.0/8",
		},
		{
			Direction: "egress",
			Protocol:  "any",
			PeerKind:  "cidr",
			PeerCIDR:  "0.0.0.0/0",
		},
	}); err != nil {
		t.Fatalf("replace security group rules: %v", err)
	}

	// Canonical SG payload referencing our seeded provider_sg_id.
	canonicalSGs := json.RawMessage(`{"schema_version":1,"groups":[{"provider_sg_id":"sg-integ-001"}]}`)
	vm, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acctID,
		ProviderVMID:   "vm-integ-001",
		Name:           "integ-vm",
		PowerState:     "running",
		Ready:          true,
		SecurityGroups: canonicalSGs,
		Tags:           map[string]string{},
		Labels:         map[string]string{},
	})
	if err != nil {
		t.Fatalf("upsert virtual machine: %v", err)
	}
	vmID := vm.ID

	// --- 4. GET /v1/workloads/{id}/network-rules -------------------------

	t.Run("workload network-rules", func(t *testing.T) {
		var resp struct {
			WorkloadID       uuid.UUID `json:"workload_id"`
			PolicyCount      int       `json:"policy_count"`
			K8sDefaultAllow  bool      `json:"k8s_default_allow"`
			MatchingPolicies []struct {
				Name  string `json:"name"`
				Rules []struct {
					Direction string `json:"direction"`
				} `json:"rules"`
			} `json:"matching_policies"`
		}
		status := flowGet(t, env, fmt.Sprintf("/v1/workloads/%s/network-rules", wlID), &resp)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d", status)
		}
		if resp.PolicyCount != 1 {
			t.Errorf("policy_count: want 1, got %d", resp.PolicyCount)
		}
		if resp.K8sDefaultAllow {
			t.Error("k8s_default_allow should be false when a policy matches")
		}
		if len(resp.MatchingPolicies) < 1 {
			t.Fatalf("matching_policies is empty")
		}
		if resp.MatchingPolicies[0].Name != "api-allow" {
			t.Errorf("policy name: want api-allow, got %q", resp.MatchingPolicies[0].Name)
		}
		if len(resp.MatchingPolicies[0].Rules) != 1 {
			t.Errorf("rules count: want 1, got %d", len(resp.MatchingPolicies[0].Rules))
		}
		if len(resp.MatchingPolicies[0].Rules) > 0 &&
			resp.MatchingPolicies[0].Rules[0].Direction != "ingress" {
			t.Errorf("rule direction: want ingress, got %q",
				resp.MatchingPolicies[0].Rules[0].Direction)
		}
	})

	// --- 5. GET /v1/virtual-machines/{id}/network-rules ------------------

	t.Run("vm network-rules", func(t *testing.T) {
		var resp struct {
			VMID                   uuid.UUID `json:"vm_id"`
			Stale                  bool      `json:"stale"`
			NoSecurityGroups       bool      `json:"no_security_groups"`
			AttachedSecurityGroups []struct {
				Name  string `json:"name"`
				Rules []struct {
					Direction string `json:"direction"`
					Protocol  string `json:"protocol"`
					FromPort  *int   `json:"from_port"`
				} `json:"rules"`
			} `json:"attached_security_groups"`
		}
		status := flowGet(t, env, fmt.Sprintf("/v1/virtual-machines/%s/network-rules", vmID), &resp)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d", status)
		}
		if resp.Stale {
			t.Error("stale should be false for canonical SG payload")
		}
		if resp.NoSecurityGroups {
			t.Error("no_security_groups should be false")
		}
		if len(resp.AttachedSecurityGroups) != 1 {
			t.Fatalf("attached_security_groups: want 1, got %d", len(resp.AttachedSecurityGroups))
		}
		sg := resp.AttachedSecurityGroups[0]
		if sg.Name != "postgres-sg" {
			t.Errorf("sg name: want postgres-sg, got %q", sg.Name)
		}
		// Verify at least one ingress tcp rule with from_port 5432.
		foundPG := false
		for _, rule := range sg.Rules {
			if rule.Protocol == "tcp" && rule.FromPort != nil && *rule.FromPort == 5432 {
				foundPG = true
				break
			}
		}
		if !foundPG {
			t.Errorf("expected tcp/5432 ingress rule, rules=%+v", sg.Rules)
		}
	})

	// --- 6. GET /v1/network-policies?cluster_id=... ----------------------

	t.Run("list network-policies by cluster", func(t *testing.T) {
		var resp struct {
			Items []api.NetworkPolicyRow `json:"items"`
		}
		status := flowGet(t, env, fmt.Sprintf("/v1/network-policies?cluster_id=%s", clusterID), &resp)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d", status)
		}
		if len(resp.Items) < 1 {
			t.Errorf("network-policies list: want at least 1 item, got %d", len(resp.Items))
		}
	})

	// --- 7. GET /v1/security-groups?cloud_account_id=... -----------------

	t.Run("list security-groups by account", func(t *testing.T) {
		var resp struct {
			Items []api.SecurityGroupRow `json:"items"`
		}
		status := flowGet(t, env, fmt.Sprintf("/v1/security-groups?cloud_account_id=%s", acctID), &resp)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d", status)
		}
		if len(resp.Items) < 1 {
			t.Errorf("security-groups list: want at least 1 item, got %d", len(resp.Items))
		}
	})
}
