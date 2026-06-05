package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// NetworkPolicy is the in-store shape of a K8s NetworkPolicy. Selectors
// stay as JSONB / text[] so the engine (P2) can query them without a join.
type NetworkPolicy struct {
	ID          uuid.UUID
	ClusterID   uuid.UUID
	NamespaceID uuid.UUID
	Name        string
	PodSelector json.RawMessage
	PolicyTypes []string
	SpecRaw     json.RawMessage
}

const npSelect = `
	id, cluster_id, namespace_id, name,
	pod_selector, policy_types, spec_raw`

// UpsertNetworkPolicy inserts or updates by (cluster_id, namespace_id, name).
// Returns the stable row ID. Collector callers use this on every tick.
func (p *PG) UpsertNetworkPolicy(ctx context.Context, np NetworkPolicy) (uuid.UUID, error) {
	const q = `
		INSERT INTO network_policies
			(cluster_id, namespace_id, name, pod_selector, policy_types, spec_raw, reconcile_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (cluster_id, namespace_id, name) DO UPDATE SET
			pod_selector      = EXCLUDED.pod_selector,
			policy_types      = EXCLUDED.policy_types,
			spec_raw          = EXCLUDED.spec_raw,
			reconcile_seen_at = NOW()
		RETURNING id`
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, q,
		np.ClusterID, np.NamespaceID, np.Name, np.PodSelector,
		np.PolicyTypes, np.SpecRaw,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert network_policy: %w", err)
	}
	return id, nil
}

// GetNetworkPolicy returns ErrNotFound on miss.
func (p *PG) GetNetworkPolicy(ctx context.Context, id uuid.UUID) (NetworkPolicy, error) {
	const q = `SELECT ` + npSelect + ` FROM network_policies WHERE id = $1`
	var np NetworkPolicy
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&np.ID, &np.ClusterID, &np.NamespaceID, &np.Name,
		&np.PodSelector, &np.PolicyTypes, &np.SpecRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NetworkPolicy{}, api.ErrNotFound
	}
	if err != nil {
		return NetworkPolicy{}, fmt.Errorf("get network_policy: %w", err)
	}
	return np, nil
}
