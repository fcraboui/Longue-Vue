package api

import (
	"context"

	"github.com/google/uuid"
)

func (m *memStore) GetClusterPolicy(_ context.Context, id uuid.UUID) (ClusterPolicyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.clusterPolicies[id]
	if !ok {
		return ClusterPolicyRow{}, ErrNotFound
	}
	return cp, nil
}

func (m *memStore) ListClusterPolicies(_ context.Context, _ ClusterPolicyListFilter, _ ListPage) ([]ClusterPolicyRow, string, error) {
	return nil, "", nil
}

func (m *memStore) UpsertClusterPolicy(_ context.Context, cp ClusterPolicyRow) (uuid.UUID, error) {
	if cp.Source == "" {
		cp.Source = SourceAPI
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	cp.ID = id
	m.clusterPolicies[id] = cp
	return id, nil
}

func (m *memStore) DeleteClusterScopedPoliciesNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeleteClusterPoliciesByNamespace(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) GetPolicyReport(_ context.Context, id uuid.UUID) (PolicyReportRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.policyReports[id]
	if !ok {
		return PolicyReportRow{}, ErrNotFound
	}
	return pr, nil
}

func (m *memStore) ListPolicyReports(_ context.Context, _ PolicyReportListFilter, _ ListPage) ([]PolicyReportRow, string, error) {
	return nil, "", nil
}

func (m *memStore) UpsertPolicyReport(_ context.Context, pr PolicyReportRow) (uuid.UUID, error) {
	if pr.Source == "" {
		pr.Source = SourceAPI
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.New()
	pr.ID = id
	m.policyReports[id] = pr
	return id, nil
}

func (m *memStore) DeleteClusterScopedPolicyReportsNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeletePolicyReportsByNamespace(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}
