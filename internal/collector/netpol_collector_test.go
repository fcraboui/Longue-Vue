package collector

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/store"
)

// fakeNetPolStore is a minimal in-memory NetPolStore for testing
// CollectNetworkPolicies without a live database.
type fakeNetPolStore struct {
	mu       sync.Mutex
	upserts  []store.NetworkPolicy
	replaces map[uuid.UUID][]store.NetworkPolicyRule
	sweeps   []sweepCall
	upsertID uuid.UUID // id returned for every upsert (defaults to a fixed uuid)
}

type sweepCall struct {
	nsID  uuid.UUID
	names []string
}

func newFakeNetPolStore() *fakeNetPolStore {
	return &fakeNetPolStore{
		replaces: make(map[uuid.UUID][]store.NetworkPolicyRule),
		upsertID: uuid.New(),
	}
}

func (f *fakeNetPolStore) UpsertNetworkPolicy(_ context.Context, np store.NetworkPolicy) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, np)
	return f.upsertID, nil
}

func (f *fakeNetPolStore) ReplaceNetworkPolicyRules(_ context.Context, policyID uuid.UUID, rules []store.NetworkPolicyRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaces[policyID] = rules
	return nil
}

func (f *fakeNetPolStore) SweepNetworkPoliciesByNamespace(_ context.Context, nsID uuid.UUID, seen []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(seen))
	copy(cp, seen)
	f.sweeps = append(f.sweeps, sweepCall{nsID: nsID, names: cp})
	return nil
}

func TestCollectNetworkPolicies_SinglePolicy(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()
	nsID := uuid.New()

	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{
				Name:        testNetPolName,
				Namespace:   testNSProd,
				PolicyTypes: []string{"Ingress"},
				Ingress: []NetworkPolicyRuleInfo{
					{
						Peers: []NetworkPolicyPeerInfo{{Kind: peerKindSelector}},
						Ports: []byte(`[{"port":8080}]`),
					},
				},
			},
		},
	}
	st := newFakeNetPolStore()

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsID, testNSProd); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}

	if len(st.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(st.upserts))
	}
	if st.upserts[0].Name != testNetPolName {
		t.Fatalf("upserted name: %q", st.upserts[0].Name)
	}
	if len(st.replaces) != 1 {
		t.Fatalf("want 1 replace call, got %d", len(st.replaces))
	}
	if len(st.sweeps) != 1 {
		t.Fatalf("want 1 sweep call, got %d", len(st.sweeps))
	}
	if len(st.sweeps[0].names) != 1 || st.sweeps[0].names[0] != testNetPolName {
		t.Fatalf("sweep names: %v", st.sweeps[0].names)
	}
}

func TestCollectNetworkPolicies_SweepDropsGone(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()
	nsID := uuid.New()

	// Round 1: two policies.
	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{Name: testNetPolPola, Namespace: testNSProd},
			{Name: "pol-b", Namespace: testNSProd},
		},
	}
	st := newFakeNetPolStore()

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsID, testNSProd); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if len(st.upserts) != 2 {
		t.Fatalf("round 1: want 2 upserts, got %d", len(st.upserts))
	}
	sweep1 := st.sweeps[0].names
	if len(sweep1) != 2 {
		t.Fatalf("round 1 sweep names: %v", sweep1)
	}

	// Round 2: only pol-a remains.
	src.netpols = []NetworkPolicyInfo{
		{Name: testNetPolPola, Namespace: testNSProd},
	}

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsID, testNSProd); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	sweep2 := st.sweeps[1].names
	if len(sweep2) != 1 || sweep2[0] != testNetPolPola {
		t.Fatalf("round 2 sweep should contain only pol-a, got %v", sweep2)
	}
}

func TestCollectNetworkPolicies_EmptyNamespace(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()
	nsID := uuid.New()

	src := &fakeSource{netpols: nil}
	st := newFakeNetPolStore()

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsID, testNSProd); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}

	if len(st.upserts) != 0 {
		t.Fatalf("want 0 upserts, got %d", len(st.upserts))
	}
	if len(st.sweeps) != 1 {
		t.Fatalf("want 1 sweep, got %d", len(st.sweeps))
	}
	if len(st.sweeps[0].names) != 0 {
		t.Fatalf("want empty sweep names, got %v", st.sweeps[0].names)
	}
}

func TestCollectNetworkPolicies_IPBlockPeer(t *testing.T) {
	ctx := context.Background()
	clusterID := uuid.New()
	nsID := uuid.New()

	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{
				Name:        "egress-ext",
				Namespace:   testNSProd,
				PolicyTypes: []string{"Egress"},
				Egress: []NetworkPolicyRuleInfo{
					{
						Peers: []NetworkPolicyPeerInfo{{
							Kind:          peerKindIPBlock,
							IPBlockCIDR:   testNetCIDR10,
							IPBlockExcept: []byte(`["10.1.0.0/16"]`),
						}},
					},
				},
			},
		},
	}
	st := newFakeNetPolStore()

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsID, testNSProd); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}

	rules := st.replaces[st.upsertID]
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
	}
	r := rules[0]
	if r.Direction != "egress" {
		t.Fatalf("direction: %q", r.Direction)
	}
	if r.PeerKind != peerKindIPBlock {
		t.Fatalf("peer_kind: %q", r.PeerKind)
	}
	if r.PeerIPBlockCIDR != testNetCIDR10 {
		t.Fatalf("cidr: %q", r.PeerIPBlockCIDR)
	}
}
