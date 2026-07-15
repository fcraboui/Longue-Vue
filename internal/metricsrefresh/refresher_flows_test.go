package metricsrefresh

// Unit tests for the flow-gauge refresh pass (refreshFlows, ADR-0037 R3 B4).
// The Refresher's flowStore field is an api.Store, so the fake embeds the
// (large) api.Store interface and overrides only the handful of methods the
// flow pass actually touches: GetSettings, ListClustersWithFlowReferences, and
// the loaders LoadFlowInputsForMetrics reaches. Reads of the resulting gauges
// go through the exported metrics.Registry (the gauge vars are package-private).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// flowFakeStore satisfies api.Store via the embedded nil interface and
// overrides only the flow-relevant methods. Any unexpected method call would
// nil-panic, which is the desired signal that refreshFlows touched more than we
// model here.
type flowFakeStore struct {
	api.Store
	enabled  bool
	clusters []uuid.UUID
	refs     []api.FlowReference
}

func (f *flowFakeStore) GetSettings(context.Context) (api.Settings, error) {
	return api.Settings{FlowMatrixEnabled: f.enabled}, nil
}

func (f *flowFakeStore) ListClustersWithFlowReferences(context.Context) ([]uuid.UUID, error) {
	return f.clusters, nil
}

func (f *flowFakeStore) ListEndpointGroups(context.Context) ([]api.EndpointGroup, error) {
	return nil, nil
}

func (f *flowFakeStore) PerimeterSecurityGroupsForCluster(context.Context, uuid.UUID) ([]api.SecurityGroupRow, error) {
	return nil, nil
}

func (f *flowFakeStore) ListSecurityGroupRules(context.Context, uuid.UUID) ([]api.SecurityGroupRuleRow, error) {
	return nil, nil
}

func (f *flowFakeStore) ListNetworkPoliciesByCluster(
	_ context.Context,
	_ uuid.UUID,
	_ api.NetworkPolicyListFilter,
	_ api.ListPage,
) ([]api.NetworkPolicyRow, string, error) {
	return nil, "", nil
}

func (f *flowFakeStore) ListNetworkPolicyRules(context.Context, uuid.UUID) ([]api.NetworkPolicyRuleRow, error) {
	return nil, nil
}

func (f *flowFakeStore) ListFlowReferences(_ context.Context, _ uuid.UUID) ([]api.FlowReference, error) {
	return f.refs, nil
}

// flowStateSeriesCount returns the number of longue_vue_flow_states series
// currently exported by the shared metrics registry.
func flowStateSeriesCount(t *testing.T) int {
	t.Helper()
	mf, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, m := range mf {
		if m.GetName() == "longue_vue_flow_states" {
			return len(m.GetMetric())
		}
	}
	return 0
}

func TestRefreshFlows_ToggleOff_ResetsAndSetsNoGauges(t *testing.T) {
	// Seed a stale series so we can confirm refreshFlows clears it.
	metrics.SetFlowStates("stale-cluster", "perimeter", map[string]int{"conforme": 2})
	if flowStateSeriesCount(t) == 0 {
		t.Fatal("precondition: expected a seeded flow_states series")
	}

	r := &Refresher{
		flowStore: &flowFakeStore{enabled: false, clusters: []uuid.UUID{uuid.New()}},
		interval:  time.Minute,
	}
	r.refreshFlows(context.Background())

	if n := flowStateSeriesCount(t); n != 0 {
		t.Fatalf("toggle off: want 0 flow_states series after reset, got %d", n)
	}
}

func TestRefreshFlows_ToggleOn_SetsGaugeForClusterWithReferences(t *testing.T) {
	metrics.ResetFlowGauges()
	cid := uuid.New()
	// A single perimeter reference with no implementing rule synthesizes one
	// "manquant" perimeter flow -> a flow_states series gets set.
	r := &Refresher{
		flowStore: &flowFakeStore{
			enabled:  true,
			clusters: []uuid.UUID{cid},
			refs: []api.FlowReference{{
				ID:        uuid.New(),
				ClusterID: cid,
				Layer:     "perimeter",
				Direction: "ingress",
				SrcKind:   "endpoint_group",
				SrcRef:    "Internet",
				DstKind:   "cluster",
				DstRef:    "cluster",
				Protocol:  "tcp",
			}},
		},
		interval: time.Minute,
	}
	r.refreshFlows(context.Background())

	if n := flowStateSeriesCount(t); n == 0 {
		t.Fatal("toggle on with a referenced cluster: want >=1 flow_states series, got 0")
	}
	metrics.ResetFlowGauges()
}
