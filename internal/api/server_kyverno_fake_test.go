package api

import (
	"context"

	"github.com/google/uuid"
)

func (m *memStore) GetClusterPolicy(_ context.Context, id uuid.UUID) (ClusterPolicyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ClusterPolicyRow{}, ErrNotFound
}

func (m *memStore) ListClusterPolicies(_ context.Context, _ ClusterPolicyListFilter, _ ListPage) ([]ClusterPolicyRow, string, error) {
	return nil, "", nil
}

func (m *memStore) UpsertClusterPolicy(_ context.Context, cp ClusterPolicyRow) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (m *memStore) DeleteClusterPoliciesNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) GetPolicyReport(_ context.Context, id uuid.UUID) (PolicyReportRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return PolicyReportRow{}, ErrNotFound
}

func (m *memStore) ListPolicyReports(_ context.Context, _ PolicyReportListFilter, _ ListPage) ([]PolicyReportRow, string, error) {
	return nil, "", nil
}

func (m *memStore) UpsertPolicyReport(_ context.Context, pr PolicyReportRow) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (m *memStore) DeletePolicyReportsNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}
