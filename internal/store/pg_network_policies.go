package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// NetworkPolicy is a type alias for api.NetworkPolicyRow, allowing store-internal
// test helpers to use the short name without the api. prefix.
type NetworkPolicy = api.NetworkPolicyRow

// NetworkPolicyRule is a type alias for api.NetworkPolicyRuleRow, allowing
// store-internal test helpers to use the short name without the api. prefix.
type NetworkPolicyRule = api.NetworkPolicyRuleRow

const npSelect = `
	id, cluster_id, namespace_id, name,
	pod_selector, policy_types, spec_raw`

// UpsertNetworkPolicy inserts or updates by (cluster_id, namespace_id, name).
// Returns the stable row ID. Collector callers use this on every tick.
//
// NOTE: prefer UpsertNetworkPolicyAtomic which writes the policy + its rules
// in one transaction. This method exists for backward compatibility with the
// pre-ADR-0038 NetPolStore interface and will be deleted in Task 9.
//
//nolint:gocritic // hugeParam: NetworkPolicy matches the legacy NetPolStore interface
func (p *PG) UpsertNetworkPolicy(ctx context.Context, np NetworkPolicy) (uuid.UUID, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin upsert network_policy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := upsertNetworkPolicyTx(ctx, tx, np)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit upsert network_policy: %w", err)
	}
	return id, nil
}

// GetNetworkPolicy returns ErrNotFound on miss. Satisfies api.Store.
func (p *PG) GetNetworkPolicy(ctx context.Context, id uuid.UUID) (api.NetworkPolicyRow, error) {
	const q = `SELECT ` + npSelect + ` FROM network_policies WHERE id = $1`
	var np api.NetworkPolicyRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&np.ID, &np.ClusterID, &np.NamespaceID, &np.Name,
		&np.PodSelector, &np.PolicyTypes, &np.SpecRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.NetworkPolicyRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.NetworkPolicyRow{}, fmt.Errorf("get network_policy: %w", err)
	}
	return np, nil
}

// ReplaceNetworkPolicyRules deletes then inserts in one transaction.
//
// NOTE: prefer UpsertNetworkPolicyAtomic. Will be deleted in Task 9.
func (p *PG) ReplaceNetworkPolicyRules(ctx context.Context, policyID uuid.UUID, rules []NetworkPolicyRule) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin replace network_policy_rules: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceNetworkPolicyRulesTx(ctx, tx, policyID, rules); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace network_policy_rules: %w", err)
	}
	return nil
}

// UpsertNetworkPolicyAtomic upserts the policy and replaces its rules in one
// transaction — both writes commit together or neither does. This is the
// canonical write path (ADR-0038) used by both the in-process collector and
// the HTTP push handler.
//
//nolint:gocritic // hugeParam: NetworkPolicy mirrors the NetPolStore interface
func (p *PG) UpsertNetworkPolicyAtomic(
	ctx context.Context, np NetworkPolicy, rules []NetworkPolicyRule,
) (uuid.UUID, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin upsert network_policy atomic: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := upsertNetworkPolicyTx(ctx, tx, np)
	if err != nil {
		return uuid.Nil, err
	}
	if err := replaceNetworkPolicyRulesTx(ctx, tx, id, rules); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit upsert network_policy atomic: %w", err)
	}
	return id, nil
}

//nolint:gocritic // hugeParam: NetworkPolicy mirrors the NetPolStore interface
func upsertNetworkPolicyTx(ctx context.Context, tx pgx.Tx, np NetworkPolicy) (uuid.UUID, error) {
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
	err := tx.QueryRow(ctx, q,
		np.ClusterID, np.NamespaceID, np.Name, np.PodSelector,
		np.PolicyTypes, np.SpecRaw,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert network_policy: %w", err)
	}
	return id, nil
}

func replaceNetworkPolicyRulesTx(ctx context.Context, tx pgx.Tx, policyID uuid.UUID, rules []NetworkPolicyRule) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM network_policy_rules WHERE network_policy_id = $1`, policyID,
	); err != nil {
		return fmt.Errorf("delete network_policy_rules: %w", err)
	}
	const ins = `
		INSERT INTO network_policy_rules
		  (network_policy_id, direction, peer_kind, peer_pod_selector,
		   peer_namespace_selector, peer_ip_block_cidr, peer_ip_block_except, ports)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,'')::cidr, $7, $8)`
	for _, r := range rules { //nolint:gocritic // rangeValCopy: NetworkPolicyRuleRow contains JSONB slices
		if _, err := tx.Exec(ctx, ins,
			policyID, r.Direction, r.PeerKind,
			r.PeerPodSelector, r.PeerNamespaceSelector,
			r.PeerIPBlockCIDR, r.PeerIPBlockExcept, r.Ports,
		); err != nil {
			return fmt.Errorf("insert network_policy_rule: %w", err)
		}
	}
	return nil
}

// ListNetworkPolicyRules returns every rule for a single policy, in stable
// insertion order. Satisfies api.Store.
func (p *PG) ListNetworkPolicyRules(ctx context.Context, policyID uuid.UUID) ([]api.NetworkPolicyRuleRow, error) {
	const q = `
		SELECT id, network_policy_id, direction, peer_kind,
		       peer_pod_selector, peer_namespace_selector,
		       COALESCE(host(peer_ip_block_cidr), ''),
		       peer_ip_block_except, ports
		FROM network_policy_rules
		WHERE network_policy_id = $1
		ORDER BY id`

	rows, err := p.pool.Query(ctx, q, policyID)
	if err != nil {
		return nil, fmt.Errorf("query network_policy_rules: %w", err)
	}
	defer rows.Close()

	var out []api.NetworkPolicyRuleRow
	for rows.Next() {
		var r api.NetworkPolicyRuleRow
		if err := rows.Scan(
			&r.ID, &r.NetworkPolicyID, &r.Direction, &r.PeerKind,
			&r.PeerPodSelector, &r.PeerNamespaceSelector,
			&r.PeerIPBlockCIDR,
			&r.PeerIPBlockExcept, &r.Ports,
		); err != nil {
			return nil, fmt.Errorf("scan network_policy_rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate network_policy_rules: %w", err)
	}
	return out, nil
}

// SweepNetworkPoliciesByNamespace deletes any policy in the namespace
// whose Name is NOT in seenNames. CASCADE removes its rules. Caller MUST
// only invoke after a successful List (CLAUDE.md reconcile contract).
func (p *PG) SweepNetworkPoliciesByNamespace(ctx context.Context, namespaceID uuid.UUID, seenNames []string) error {
	_, err := p.pool.Exec(ctx,
		`DELETE FROM network_policies
		  WHERE namespace_id = $1
		    AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, seenNames,
	)
	if err != nil {
		return fmt.Errorf("sweep network_policies: %w", err)
	}
	return nil
}

// ListNetworkPoliciesForWorkload returns every NetworkPolicy in the
// workload's namespace whose pod_selector matches the workload's labels.
// Matching is done in Postgres with @> on the matchLabels subobject —
// the simple case that covers ~95% of real policies. matchExpressions
// support is deferred to P2 (where the engine does full selector eval).
func (p *PG) ListNetworkPoliciesForWorkload(
	ctx context.Context,
	namespaceID uuid.UUID,
	workloadLabels json.RawMessage,
) ([]api.NetworkPolicyRow, error) {
	const q = `
		SELECT id, cluster_id, namespace_id, name, pod_selector, policy_types, spec_raw
		FROM network_policies
		WHERE namespace_id = $1
		  AND (
		    pod_selector = '{}'::jsonb
		    OR (pod_selector->'matchLabels') IS NULL
		    OR (pod_selector->'matchLabels') @> ($2::jsonb)
		  )
		ORDER BY name`
	rows, err := p.pool.Query(ctx, q, namespaceID, workloadLabels)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []api.NetworkPolicyRow
	for rows.Next() {
		var np api.NetworkPolicyRow
		if err := rows.Scan(&np.ID, &np.ClusterID, &np.NamespaceID, &np.Name,
			&np.PodSelector, &np.PolicyTypes, &np.SpecRaw); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, np)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return out, nil
}

// ListNetworkPoliciesByCluster returns a page + next_cursor. Optional
// namespaceID filter (nil = all namespaces). Cursor format identical to
// ListApplications in pg_applications.go — REUSE encodeCursor/decodeCursor.
// Order: reconcile_seen_at DESC, id DESC. Satisfies api.Store.
//
//nolint:gocyclo // pagination + optional filter + cursor decoding; matches existing list patterns in this package
func (p *PG) ListNetworkPoliciesByCluster(
	ctx context.Context,
	clusterID uuid.UUID,
	namespaceID *uuid.UUID,
	limit int,
	cursor string,
) ([]api.NetworkPolicyRow, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	conds := make([]string, 0, 3)
	args := make([]any, 0, 5)

	args = append(args, clusterID)
	conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))

	args = append(args, namespaceID)
	conds = append(conds, fmt.Sprintf("($%d::uuid IS NULL OR namespace_id = $%d)", len(args), len(args)))

	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		conds = append(conds, fmt.Sprintf("(reconcile_seen_at, id) < ($%d, $%d)", tsIdx, idIdx))
	}

	where := "WHERE " + strings.Join(conds, " AND ")
	args = append(args, limit+1)
	// Include reconcile_seen_at in the projection so we can encode the cursor
	// from the last returned row without a second round-trip.
	q := fmt.Sprintf(
		`SELECT `+npSelect+`, reconcile_seen_at FROM network_policies %s ORDER BY reconcile_seen_at DESC, id DESC LIMIT $%d`,
		where, len(args),
	)

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query network_policies by cluster: %w", err)
	}
	defer rows.Close()

	type npWithTS struct {
		np     api.NetworkPolicyRow
		seenAt time.Time
	}
	raw := make([]npWithTS, 0, limit+1)
	for rows.Next() {
		var r npWithTS
		if err := rows.Scan(
			&r.np.ID, &r.np.ClusterID, &r.np.NamespaceID, &r.np.Name,
			&r.np.PodSelector, &r.np.PolicyTypes, &r.np.SpecRaw,
			&r.seenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan network_policy: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate network_policies: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := raw[limit-1]
		next = encodeCursor(last.seenAt, last.np.ID)
		raw = raw[:limit]
	}
	items := make([]api.NetworkPolicyRow, len(raw))
	for i, r := range raw { //nolint:gocritic // rangeValCopy: cursor wrapper struct; copy is intentional
		items[i] = r.np
	}
	return items, next, nil
}
