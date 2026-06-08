//go:build integration

package api_test

// End-to-end integration test for the netpol push surface (ADR-0038). Spins
// up a real *store.PG, mounts the api.Server on httptest via the codegen
// strict-server machinery, points an apiclient.Store at it, runs the
// collector against a fake KubeSource, verifies netpols land in the DB.
// Then drops one policy and verifies the sweep deletes it.
//
// Run:
//
//	PGX_TEST_DATABASE='postgres://longue_vue:lv@127.0.0.1:55432/longue_vue?sslmode=disable' \
//	  go test -tags=integration -count=1 -timeout 120s ./internal/api/ \
//	  -run TestPushCollector_NetPol_EndToEnd -v

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/collector"
	"github.com/sthalbert/longue-vue/internal/collector/apiclient"
	"github.com/sthalbert/longue-vue/internal/store"
)

// netpolPushTestEnv holds the test server backed by a real PG instance and
// an apiclient.Store wired to it.
type netpolPushTestEnv struct {
	srv   *httptest.Server
	pg    *store.PG
	apcl  *apiclient.Store
}

// newNetpolPushTestEnv opens a real PG, runs migrations, registers cleanup,
// builds a strict-server HTTP mux exposing the two netpol push routes plus
// the read list route (for assertions), injects an admin-scope caller, and
// wires an apiclient.Store at the test server URL.
//
// The admin role is used because:
//   - CreateNetworkPolicy requires ScopeWrite
//   - ReconcileNetworkPolicies requires ScopeDelete
//   - Admin implies both (see auth.ScopesForRole).
func newNetpolPushTestEnv(t *testing.T) *netpolPushTestEnv {
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

	// Raw pool for targeted cleanup only — we never query through it.
	rawPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		pg.Close()
		t.Fatalf("open raw pool: %v", err)
	}

	t.Cleanup(func() {
		// CASCADE from clusters covers namespaces, network_policies, etc.
		_, _ = rawPool.Exec(context.Background(), "TRUNCATE clusters CASCADE")
		rawPool.Close()
		pg.Close()
	})

	// Build the strict-server handler. The handler exposes the full API mux
	// (Handler(strict) routes everything), but the apiclient only calls the
	// two push routes. The test server is plain HTTP — no TLS needed.
	caller := &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "netpol-push-integ",
		Role:     auth.RoleAdmin,
		Scopes:   auth.ScopesForRole(auth.RoleAdmin),
	}

	strict := api.NewStrictHandlerWithOptions(
		api.NewServer("test", pg, auth.SecureNever, nil, api.NewLoginRateLimiter(), api.NewVerifyRateLimiter()),
		[]api.StrictMiddlewareFunc{api.InjectRequestMiddleware},
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	base := api.Handler(strict)

	// Wrap every request with the admin caller injected into the context.
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithCaller(r.Context(), caller))
		base.ServeHTTP(w, r)
	})

	srv := httptest.NewServer(authed)
	t.Cleanup(srv.Close)

	// Build the apiclient.Store pointing at the test server. No TLS, no mTLS —
	// the test server is plain HTTP.
	apcl, err := apiclient.NewStore(apiclient.Config{
		ServerURL: srv.URL,
		Token:     "fake-integration-pat",
	})
	if err != nil {
		t.Fatalf("new apiclient store: %v", err)
	}

	return &netpolPushTestEnv{srv: srv, pg: pg, apcl: apcl}
}

// fakeNetpolSource implements collector.KubeSource with only
// ListNetworkPolicies populated — every other method returns empty results.
// This is sufficient for CollectNetworkPolicies, which calls only
// ListNetworkPolicies.
type fakeNetpolSource struct {
	byNamespace map[string][]collector.NetworkPolicyInfo
}

func (f *fakeNetpolSource) ListNetworkPolicies(_ context.Context, ns string) ([]collector.NetworkPolicyInfo, error) {
	return f.byNamespace[ns], nil
}

// Stub all other KubeSource methods to satisfy the interface.

func (f *fakeNetpolSource) ServerVersion(_ context.Context) (string, error) {
	return "v1.30.0", nil
}

func (f *fakeNetpolSource) ListNodes(_ context.Context) ([]collector.NodeInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListNamespaces(_ context.Context) ([]collector.NamespaceInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListPods(_ context.Context) ([]collector.PodInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListWorkloads(_ context.Context) ([]collector.WorkloadInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListServices(_ context.Context) ([]collector.ServiceInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListIngresses(_ context.Context) ([]collector.IngressInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListReplicaSetOwners(_ context.Context) ([]collector.ReplicaSetOwner, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListPersistentVolumes(_ context.Context) ([]collector.PVInfo, error) {
	return nil, nil
}

func (f *fakeNetpolSource) ListPersistentVolumeClaims(_ context.Context) ([]collector.PVCInfo, error) {
	return nil, nil
}

// countNetpolsForNS returns the count of NetworkPolicies in the DB for the
// given namespace. Uses pg.ListNetworkPoliciesByCluster with the namespace
// filter — no raw-pool access required.
func countNetpolsForNS(t *testing.T, pg *store.PG, clusterID, nsID uuid.UUID) int {
	t.Helper()
	items, _, err := pg.ListNetworkPoliciesByCluster(context.Background(), clusterID, &nsID, 500, "")
	if err != nil {
		t.Fatalf("list network policies: %v", err)
	}
	return len(items)
}

// TestPushCollector_NetPol_EndToEnd is the end-to-end proof that:
//
//  1. CollectNetworkPolicies → apiclient.Store → HTTP POST /v1/network-policies
//     → *store.PG → DB lands two rows on the first tick.
//
//  2. Dropping one policy from the KubeSource and re-running
//     CollectNetworkPolicies → apiclient.Store → POST /v1/network-policies/reconcile
//     → *store.PG → DB removes the absent row, leaving one.
//
//nolint:gocyclo // straight-line integration test; factoring would obscure the scenario
func TestPushCollector_NetPol_EndToEnd(t *testing.T) {
	env := newNetpolPushTestEnv(t)
	ctx := context.Background()
	pg := env.pg

	// --- 1. Seed cluster + namespace directly via the store ---------------
	// We seed via the store (not via HTTP) to keep the test focused on the
	// push-path verification rather than the cluster/namespace upsert flows.

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "netpol-push-integ-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	clusterID := *cluster.Id

	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{
		ClusterId: clusterID,
		Name:      "netpol-push-integ-ns",
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	nsID := *ns.Id
	nsName := "netpol-push-integ-ns"

	// --- 2. Build fake KubeSource with two policies -----------------------

	src := &fakeNetpolSource{
		byNamespace: map[string][]collector.NetworkPolicyInfo{
			nsName: {
				{
					Name:        "p1",
					PodSelector: []byte(`{}`),
					PolicyTypes: []string{"Ingress"},
					SpecRaw:     []byte(`{}`),
				},
				{
					Name:        "p2",
					PodSelector: []byte(`{}`),
					PolicyTypes: []string{"Ingress"},
					SpecRaw:     []byte(`{}`),
				},
			},
		},
	}

	// --- 3. Tick 1: both p1 + p2 must arrive in the DB -------------------

	if err := collector.CollectNetworkPolicies(ctx, src, env.apcl, clusterID, nsID, nsName); err != nil {
		t.Fatalf("tick 1: CollectNetworkPolicies: %v", err)
	}

	if got := countNetpolsForNS(t, pg, clusterID, nsID); got != 2 {
		t.Errorf("after tick 1: want 2 netpols in ns, got %d", got)
	}

	// --- 4. Tick 2: drop p2; sweep must delete it -------------------------

	src.byNamespace[nsName] = src.byNamespace[nsName][:1] // keep only p1

	if err := collector.CollectNetworkPolicies(ctx, src, env.apcl, clusterID, nsID, nsName); err != nil {
		t.Fatalf("tick 2: CollectNetworkPolicies: %v", err)
	}

	if got := countNetpolsForNS(t, pg, clusterID, nsID); got != 1 {
		t.Errorf("after tick 2: want 1 netpol in ns, got %d", got)
	}
}
