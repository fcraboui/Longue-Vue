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
}

type sweepCall struct {
	nsID  uuid.UUID
	names []string
}

func newFakeNetPolStore() *fakeNetPolStore {
	return &fakeNetPolStore{
		replaces: make(map[uuid.UUID][]store.NetworkPolicyRule),
	}
}

//nolint:gocritic // hugeParam: store.NetworkPolicy must match the NetPolStore interface signature
func (f *fakeNetPolStore) UpsertNetworkPolicy(_ context.Context, np store.NetworkPolicy, rules []store.NetworkPolicyRule) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.upserts = append(f.upserts, np)
	f.replaces[id] = rules
	return id, nil
}

func (f *fakeNetPolStore) SweepNetworkPoliciesByNamespace(_ context.Context, nsID uuid.UUID, seen []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(seen))
	copy(cp, seen)
	f.sweeps = append(f.sweeps, sweepCall{nsID: nsID, names: cp})
	return nil
}

// sweepNamesForNS returns the names from the most-recent sweep for nsID, and
// whether any sweep for that namespace was recorded at all. Helps assertions
// stay independent of sweep ordering.
func (f *fakeNetPolStore) sweepNamesForNS(nsID uuid.UUID) (names []string, found bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sweeps) - 1; i >= 0; i-- {
		if f.sweeps[i].nsID == nsID {
			return f.sweeps[i].names, true
		}
	}
	return nil, false
}


func TestCollectNetworkPolicies_HappyPath(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA, nsB := uuid.New(), uuid.New(), uuid.New()
	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{Name: "deny-all", Namespace: testNetpolNSTeamA, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
			{Name: "deny-egress", Namespace: testNetpolNSTeamA, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
			{Name: testNetpolAllowDNS, Namespace: testNetpolNSTeamB, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
		},
	}
	st := newFakeNetPolStore()
	nsByName := map[string]uuid.UUID{testNetpolNSTeamA: nsA, testNetpolNSTeamB: nsB}

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsByName); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}
	if len(st.upserts) != 3 {
		t.Errorf("upserts: got %d, want 3", len(st.upserts))
	}
	if len(st.sweeps) != 2 {
		t.Errorf("sweeps: got %d, want 2 (one per known ns)", len(st.sweeps))
	}
	if got, ok := st.sweepNamesForNS(nsA); !ok || len(got) != 2 {
		t.Errorf("nsA sweep seen: got %v (ok=%v), want 2 entries", got, ok)
	}
	if got, ok := st.sweepNamesForNS(nsB); !ok || len(got) != 1 || got[0] != testNetpolAllowDNS {
		t.Errorf("nsB sweep seen: got %v (ok=%v), want [allow-dns]", got, ok)
	}
}

func TestCollectNetworkPolicies_UnknownNamespaceSkipped(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA := uuid.New(), uuid.New()
	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{Name: "good", Namespace: testNetpolNSTeamA, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
			{Name: testNetpolOrphan, Namespace: testNetpolNSGhost, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
		},
	}
	st := newFakeNetPolStore()
	nsByName := map[string]uuid.UUID{testNetpolNSTeamA: nsA}

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsByName); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}
	if len(st.upserts) != 1 {
		t.Errorf("upserts: got %d, want 1 (orphan should be skipped)", len(st.upserts))
	}
	if got, ok := st.sweepNamesForNS(nsA); !ok || len(got) != 1 || got[0] != "good" {
		t.Errorf("nsA sweep seen: got %v (ok=%v), want [good]", got, ok)
	}
}

func TestCollectNetworkPolicies_EmptyNamespaceSweeps(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA, nsB := uuid.New(), uuid.New(), uuid.New()
	src := &fakeSource{
		netpols: []NetworkPolicyInfo{
			{Name: "only-one", Namespace: testNetpolNSTeamA, PodSelector: []byte(`{}`), SpecRaw: []byte(`{}`)},
		},
	}
	st := newFakeNetPolStore()
	nsByName := map[string]uuid.UUID{testNetpolNSTeamA: nsA, testNetpolNSTeamB: nsB}

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsByName); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}
	if len(st.sweeps) != 2 {
		t.Errorf("sweeps: got %d, want 2 (every known ns gets a sweep)", len(st.sweeps))
	}
	if got, ok := st.sweepNamesForNS(nsB); !ok || len(got) != 0 {
		t.Errorf("nsB sweep: got (%v, ok=%v), want ([], ok=true)", got, ok)
	}
}

func TestCollectNetworkPolicies_IPBlockPeer(t *testing.T) {
	ctx := t.Context()
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
	nsByName := map[string]uuid.UUID{testNSProd: nsID}

	if err := CollectNetworkPolicies(ctx, src, st, clusterID, nsByName); err != nil {
		t.Fatalf("CollectNetworkPolicies: %v", err)
	}

	if len(st.replaces) != 1 {
		t.Fatalf("want 1 replace call, got %d", len(st.replaces))
	}
	var rules []store.NetworkPolicyRule
	for _, r := range st.replaces {
		rules = r
	}
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
