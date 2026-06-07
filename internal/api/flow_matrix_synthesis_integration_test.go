//go:build integration

package api_test

// End-to-end integration test for the flow-matrix read-time synthesis
// classification path (ADR-0036). Where flow_inventory_integration_test.go
// covers the Phase-1 inventory read endpoints, this test seeds a coherent
// perimeter fixture and drives GET /v1/clusters/{id}/flow-matrix through the
// real handler so the store→handler→flowmatrix.Synthesize seam is locked
// against a real PostgreSQL instance.
//
// Run:
//
//	PGX_TEST_DATABASE='postgres://longue_vue:lv@127.0.0.1:55432/longue_vue?sslmode=disable' \
//	  go test -tags=integration -count=1 -timeout 120s ./internal/api/ \
//	  -run TestFlowMatrixSynthesisEndToEnd -v

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

// newSynthTestEnv mirrors newFlowTestEnv but wires only the flow-matrix
// synthesis endpoint, injecting a read-scope caller and cleaning up the
// flow-matrix tables this test touches.
func newSynthTestEnv(t *testing.T) (*httptest.Server, *store.PG) {
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

	rawPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		pg.Close()
		t.Fatalf("open raw pool: %v", err)
	}

	t.Cleanup(func() {
		// CASCADE from clusters covers nodes; CASCADE from cloud_accounts
		// covers security_groups + vm_security_group_attachments. The
		// endpoint_groups, cluster_flow_references and settings rows are
		// independent, so clean them explicitly (settings reset to default-off).
		_, _ = rawPool.Exec(context.Background(),
			"TRUNCATE clusters, cloud_accounts, endpoint_groups, cluster_flow_references CASCADE")
		_, _ = rawPool.Exec(context.Background(),
			"UPDATE settings SET flow_matrix_enabled = false WHERE id = 1")
		rawPool.Close()
		pg.Close()
	})

	caller := &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "flow-synth-integ",
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
	mux.Handle("GET /v1/clusters/{id}/flow-matrix", injectCaller(api.HandleClusterFlowMatrix(pg)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, pg
}

// synthFlow mirrors the JSON shape of flowmatrix.Flow for assertions.
type synthFlow struct {
	Layer     string `json:"layer"`
	Direction string `json:"direction"`
	Src       struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		CIDR string `json:"cidr"`
	} `json:"src"`
	Ports []struct {
		Protocol string `json:"protocol"`
		From     *int   `json:"from"`
		To       *int   `json:"to"`
	} `json:"ports"`
	State string `json:"state"`
}

// TestFlowMatrixSynthesisEndToEnd seeds a perimeter fixture (cluster + node
// whose provider_id embeds the cloud VM id; cloud account + perimeter SG with
// two ingress CIDR rules attached to that node-VM; two endpoint groups; one
// perimeter reference) and asserts the exact classified states the synthesizer
// produces through the real HTTP handler.
//
//nolint:gocyclo // straight-line integration test; factoring would obscure the scenario
func TestFlowMatrixSynthesisEndToEnd(t *testing.T) {
	srv, pg := newSynthTestEnv(t)
	ctx := context.Background()

	// --- 1. Cluster + node whose provider_id embeds the cloud VM id -------
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "synth-flow-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	clusterID := *cluster.Id

	providerID := "outscale:///eu-west-2a/i-synth001"
	if _, err := pg.CreateNode(ctx, api.NodeCreate{
		ClusterId:  clusterID,
		Name:       "synth-node-1",
		ProviderId: &providerID,
	}); err != nil {
		t.Fatalf("create node: %v", err)
	}

	// --- 2. Cloud account + perimeter SG + two ingress CIDR rules ---------
	acct, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale",
		Name:     "synth-flow-account",
		Region:   "eu-west-2",
	})
	if err != nil {
		t.Fatalf("upsert cloud account: %v", err)
	}

	sgID, err := pg.UpsertSecurityGroup(ctx, api.SecurityGroupRow{
		CloudAccountID: acct.ID,
		ProviderSGID:   "sg-synth-001",
		Name:           "edge-sg",
		VPCID:          "vpc-synth",
		Tags:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("upsert security group: %v", err)
	}

	p443 := 443
	p22 := 22
	if err := pg.ReplaceSecurityGroupRules(ctx, sgID, []api.SecurityGroupRuleRow{
		{
			Direction: "ingress",
			Protocol:  "tcp",
			FromPort:  &p443,
			ToPort:    &p443,
			PeerKind:  "cidr",
			PeerCIDR:  "0.0.0.0/0",
		},
		{
			Direction: "ingress",
			Protocol:  "tcp",
			FromPort:  &p22,
			ToPort:    &p22,
			PeerKind:  "cidr",
			PeerCIDR:  "10.0.0.0/8",
		},
	}); err != nil {
		t.Fatalf("replace security group rules: %v", err)
	}

	// Link the node-VM (raw provider VM id embedded in provider_id) → SG so
	// PerimeterSecurityGroupsForCluster resolves it.
	if err := pg.UpsertVMSecurityGroupAttachment(ctx, api.VMSecurityGroupAttachment{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-synth001",
		ProviderSGID:   "sg-synth-001",
	}); err != nil {
		t.Fatalf("upsert vm sg attachment: %v", err)
	}

	// --- 3. Endpoint groups: Internet (wide-open) + Corp-LAN -------------
	if _, err := pg.CreateEndpointGroup(ctx, api.EndpointGroupInput{
		Name:  "Internet",
		CIDRs: []string{"0.0.0.0/0"},
	}, nil); err != nil {
		t.Fatalf("create endpoint group Internet: %v", err)
	}
	if _, err := pg.CreateEndpointGroup(ctx, api.EndpointGroupInput{
		Name:  "Corp-LAN",
		CIDRs: []string{"10.0.0.0/8"},
	}, nil); err != nil {
		t.Fatalf("create endpoint group Corp-LAN: %v", err)
	}

	// --- 4. Perimeter references -----------------------------------------
	// Internet → service tcp/443 (declared, but wide-open still wins → large_ouvert)
	if _, err := pg.CreateFlowReference(ctx, clusterID, api.FlowReferenceInput{
		Layer:         "perimeter",
		Direction:     "ingress",
		SrcKind:       "endpoint_group",
		SrcRef:        "Internet",
		DstKind:       "service",
		DstRef:        "edge",
		Protocol:      "tcp",
		FromPort:      &p443,
		ToPort:        &p443,
		Justification: "public HTTPS ingress",
	}, nil); err != nil {
		t.Fatalf("create Internet reference: %v", err)
	}
	// Corp-LAN → service tcp/22 (declared, non-wide CIDR → conforme)
	if _, err := pg.CreateFlowReference(ctx, clusterID, api.FlowReferenceInput{
		Layer:         "perimeter",
		Direction:     "ingress",
		SrcKind:       "endpoint_group",
		SrcRef:        "Corp-LAN",
		DstKind:       "service",
		DstRef:        "ssh",
		Protocol:      "tcp",
		FromPort:      &p22,
		ToPort:        &p22,
		Justification: "admin SSH from corporate LAN",
	}, nil); err != nil {
		t.Fatalf("create Corp-LAN reference: %v", err)
	}

	// --- 5. Enable flow_matrix_enabled -----------------------------------
	on := true
	if _, err := pg.UpdateSettings(ctx, api.SettingsPatch{FlowMatrixEnabled: &on}); err != nil {
		t.Fatalf("enable flow_matrix: %v", err)
	}

	// --- 6. GET /v1/clusters/{id}/flow-matrix ----------------------------
	resp, err := http.Get(fmt.Sprintf("%s/v1/clusters/%s/flow-matrix", srv.URL, clusterID)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET flow-matrix: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flow-matrix: want 200, got %d", resp.StatusCode)
	}
	var out struct {
		ClusterID string      `json:"cluster_id"`
		Perimeter []synthFlow `json:"perimeter"`
		Internal  []synthFlow `json:"internal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode synthesis: %v", err)
	}

	// --- 7. Assert exact states ------------------------------------------
	// findPerim returns the perimeter flow matching the given CIDR + tcp port.
	findPerim := func(cidr string, port int) (synthFlow, bool) {
		for _, f := range out.Perimeter {
			if f.Src.CIDR != cidr || len(f.Ports) == 0 {
				continue
			}
			p := f.Ports[0]
			if p.Protocol == "tcp" && p.From != nil && *p.From == port {
				return f, true
			}
		}
		return synthFlow{}, false
	}

	// 0.0.0.0/0 tcp/443: wide-open peer ⇒ large_ouvert regardless of the
	// matching reference (ClassifyActual short-circuits on WideOpen).
	wide, ok := findPerim("0.0.0.0/0", 443)
	if !ok {
		t.Fatalf("no perimeter flow for 0.0.0.0/0 tcp/443; got %+v", out.Perimeter)
	}
	if wide.State != "large_ouvert" {
		t.Errorf("0.0.0.0/0 tcp/443: want state large_ouvert, got %q", wide.State)
	}
	if wide.Src.Name != "Internet" {
		t.Errorf("0.0.0.0/0 tcp/443: want zone Internet, got %q", wide.Src.Name)
	}

	// 10.0.0.0/8 tcp/22: matched by the Corp-LAN reference ⇒ conforme.
	corp, ok := findPerim("10.0.0.0/8", 22)
	if !ok {
		t.Fatalf("no perimeter flow for 10.0.0.0/8 tcp/22; got %+v", out.Perimeter)
	}
	if corp.State != "conforme" {
		t.Errorf("10.0.0.0/8 tcp/22: want state conforme, got %q", corp.State)
	}
	if corp.Src.Name != "Corp-LAN" {
		t.Errorf("10.0.0.0/8 tcp/22: want zone Corp-LAN, got %q", corp.Src.Name)
	}
}
