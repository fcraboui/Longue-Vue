package collector

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

type fakeKyvernoStore struct {
	mu                       sync.Mutex
	upsertedPolicies         []api.ClusterPolicyRow
	upsertedReports          []api.PolicyReportRow
	clusterScopedPolicySweep []uuid.UUID
	policySweepsByNS         map[uuid.UUID][]uuid.UUID
	clusterScopedReportSweep []uuid.UUID
	reportSweepsByNS         map[uuid.UUID][]uuid.UUID
	sweepClusterIDs          map[string]uuid.UUID
}

func newFakeKyvernoStore() *fakeKyvernoStore {
	return &fakeKyvernoStore{
		policySweepsByNS: make(map[uuid.UUID][]uuid.UUID),
		reportSweepsByNS: make(map[uuid.UUID][]uuid.UUID),
		sweepClusterIDs:  make(map[string]uuid.UUID),
	}
}

func (f *fakeKyvernoStore) UpsertClusterPolicy(_ context.Context, cp api.ClusterPolicyRow) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cp.Source != api.SourceCollector {
		return uuid.Nil, fmt.Errorf("upsert cluster_policy: unexpected source %q, want %q", cp.Source, api.SourceCollector)
	}
	id := uuid.New()
	f.upsertedPolicies = append(f.upsertedPolicies, cp)
	return id, nil
}

func (f *fakeKyvernoStore) DeleteClusterScopedPoliciesNotIn(_ context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]uuid.UUID, len(keepIDs))
	copy(cp, keepIDs)
	f.clusterScopedPolicySweep = cp
	f.sweepClusterIDs["cp_cluster"] = clusterID
	return 0, nil
}

func (f *fakeKyvernoStore) DeleteClusterPoliciesByNamespace(
	_ context.Context,
	clusterID uuid.UUID,
	nsID uuid.UUID,
	keepIDs []uuid.UUID,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]uuid.UUID, len(keepIDs))
	copy(cp, keepIDs)
	f.policySweepsByNS[nsID] = cp
	f.sweepClusterIDs["cp_ns_"+nsID.String()] = clusterID
	return 0, nil
}

func (f *fakeKyvernoStore) UpsertPolicyReport(_ context.Context, pr api.PolicyReportRow) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr.Source != api.SourceCollector {
		return uuid.Nil, fmt.Errorf("upsert policy_report: unexpected source %q, want %q", pr.Source, api.SourceCollector)
	}
	id := uuid.New()
	f.upsertedReports = append(f.upsertedReports, pr)
	return id, nil
}

func (f *fakeKyvernoStore) DeleteClusterScopedPolicyReportsNotIn(_ context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]uuid.UUID, len(keepIDs))
	copy(cp, keepIDs)
	f.clusterScopedReportSweep = cp
	f.sweepClusterIDs["pr_cluster"] = clusterID
	return 0, nil
}

func (f *fakeKyvernoStore) DeletePolicyReportsByNamespace(
	_ context.Context,
	clusterID uuid.UUID,
	nsID uuid.UUID,
	keepIDs []uuid.UUID,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]uuid.UUID, len(keepIDs))
	copy(cp, keepIDs)
	f.reportSweepsByNS[nsID] = cp
	f.sweepClusterIDs["pr_ns_"+nsID.String()] = clusterID
	return 0, nil
}

func (f *fakeKyvernoStore) sweepClusterID(key string) (uuid.UUID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.sweepClusterIDs[key]
	return id, ok
}

func TestCollectKyvernoPolicies_PerNamespaceSweep(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA, nsB := uuid.New(), uuid.New(), uuid.New()

	src := &fakeSource{
		kyvernoClusterPolicies: []KyvernoClusterPolicyInfo{
			{Name: "restrict-ingress", ResourceType: "ClusterPolicy", Scope: "cluster"},
		},
		kyvernoPolicies: []KyvernoClusterPolicyInfo{
			{Name: "require-labels", Namespace: "team-a", ResourceType: "Policy", Scope: "namespace"},
			{Name: "deny-privilege", Namespace: "team-a", ResourceType: "Policy", Scope: "namespace"},
			{Name: "check-image", Namespace: "team-b", ResourceType: "Policy", Scope: "namespace"},
		},
		kyvernoClusterReports: []KyvernoPolicyReportInfo{
			{Name: "cpr-cluster"},
		},
		kyvernoPolicyReports: []KyvernoPolicyReportInfo{
			{Name: "pr-team-a", Namespace: "team-a"},
			{Name: "pr-team-b", Namespace: "team-b"},
		},
	}

	st := newFakeKyvernoStore()
	nsByName := map[string]uuid.UUID{"team-a": nsA, "team-b": nsB}

	if err := CollectKyvernoPolicies(ctx, src, st, clusterID, "test-cluster", nsByName); err != nil {
		t.Fatalf("CollectKyvernoPolicies: %v", err)
	}

	if len(st.upsertedPolicies) != 4 {
		t.Errorf("policy upserts: got %d, want 4", len(st.upsertedPolicies))
	}
	if len(st.upsertedReports) != 3 {
		t.Errorf("report upserts: got %d, want 3", len(st.upsertedReports))
	}

	if st.clusterScopedPolicySweep == nil {
		t.Error("cluster-scoped policy sweep not called")
	} else if len(st.clusterScopedPolicySweep) != 1 {
		t.Errorf("cluster-scoped policy sweep keep: got %d, want 1", len(st.clusterScopedPolicySweep))
	}

	if _, ok := st.policySweepsByNS[nsA]; !ok {
		t.Error("nsA policy sweep not called")
	} else if len(st.policySweepsByNS[nsA]) != 2 {
		t.Errorf("nsA policy sweep keep: got %d, want 2", len(st.policySweepsByNS[nsA]))
	}

	if _, ok := st.policySweepsByNS[nsB]; !ok {
		t.Error("nsB policy sweep not called")
	} else if len(st.policySweepsByNS[nsB]) != 1 {
		t.Errorf("nsB policy sweep keep: got %d, want 1", len(st.policySweepsByNS[nsB]))
	}

	if st.clusterScopedReportSweep == nil {
		t.Error("cluster-scoped report sweep not called")
	}
	if _, ok := st.reportSweepsByNS[nsA]; !ok {
		t.Error("nsA report sweep not called")
	}
	if _, ok := st.reportSweepsByNS[nsB]; !ok {
		t.Error("nsB report sweep not called")
	}

	if id, ok := st.sweepClusterID("cp_cluster"); !ok || id != clusterID {
		t.Errorf("cluster-scoped policy sweep clusterID: got (%s, ok=%v), want %s", id, ok, clusterID)
	}
	if id, ok := st.sweepClusterID("pr_cluster"); !ok || id != clusterID {
		t.Errorf("cluster-scoped report sweep clusterID: got (%s, ok=%v), want %s", id, ok, clusterID)
	}
}

func TestCollectKyvernoPolicies_UnknownNamespaceNotSwept(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA := uuid.New(), uuid.New()

	src := &fakeSource{
		kyvernoClusterPolicies: []KyvernoClusterPolicyInfo{},
		kyvernoPolicies: []KyvernoClusterPolicyInfo{
			{Name: "good", Namespace: "team-a", ResourceType: "Policy", Scope: "namespace"},
			{Name: "orphan", Namespace: "ghost-ns", ResourceType: "Policy", Scope: "namespace"},
		},
		kyvernoClusterReports: []KyvernoPolicyReportInfo{},
		kyvernoPolicyReports:  []KyvernoPolicyReportInfo{},
	}

	st := newFakeKyvernoStore()
	nsByName := map[string]uuid.UUID{"team-a": nsA}

	if err := CollectKyvernoPolicies(ctx, src, st, clusterID, "test-cluster", nsByName); err != nil {
		t.Fatalf("CollectKyvernoPolicies: %v", err)
	}

	if len(st.upsertedPolicies) != 1 {
		t.Errorf("policy upserts: got %d, want 1 (orphan skipped)", len(st.upsertedPolicies))
	}

	if len(st.policySweepsByNS) != 1 {
		t.Errorf("policy sweeps: got %d namespaces, want 1 (only known ns swept)", len(st.policySweepsByNS))
	}

	if keep, ok := st.policySweepsByNS[nsA]; !ok || len(keep) != 1 {
		t.Errorf("nsA policy sweep keep: got %v (ok=%v), want 1 entry", keep, ok)
	}
}

func TestCollectKyvernoPolicies_EmptyNamespaceStillSwept(t *testing.T) {
	ctx := t.Context()
	clusterID, nsA, nsB := uuid.New(), uuid.New(), uuid.New()

	src := &fakeSource{
		kyvernoClusterPolicies: []KyvernoClusterPolicyInfo{},
		kyvernoPolicies: []KyvernoClusterPolicyInfo{
			{Name: "only-one", Namespace: "team-a", ResourceType: "Policy", Scope: "namespace"},
		},
		kyvernoClusterReports: []KyvernoPolicyReportInfo{},
		kyvernoPolicyReports:  []KyvernoPolicyReportInfo{},
	}

	st := newFakeKyvernoStore()
	nsByName := map[string]uuid.UUID{"team-a": nsA, "team-b": nsB}

	if err := CollectKyvernoPolicies(ctx, src, st, clusterID, "test-cluster", nsByName); err != nil {
		t.Fatalf("CollectKyvernoPolicies: %v", err)
	}

	if len(st.policySweepsByNS) != 2 {
		t.Errorf("policy sweeps: got %d namespaces, want 2 (every known ns gets a sweep)", len(st.policySweepsByNS))
	}

	if keep, ok := st.policySweepsByNS[nsB]; !ok || len(keep) != 0 {
		t.Errorf("nsB policy sweep: got (%v, ok=%v), want empty slice", keep, ok)
	}
}
