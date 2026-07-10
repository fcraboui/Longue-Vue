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
	"github.com/jackc/pgx/v5/pgconn"

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

// NetworkPolicyExists returns true when a row matching (clusterID,
// namespaceID, name) already exists. Used by CreateNetworkPolicy to
// distinguish 201 (insert) from 200 (update) without re-reading the whole row.
func (p *PG) NetworkPolicyExists(
	ctx context.Context, clusterID, namespaceID uuid.UUID, name string,
) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM network_policies
	                          WHERE cluster_id=$1 AND namespace_id=$2 AND name=$3)`
	var exists bool
	if err := p.pool.QueryRow(ctx, q, clusterID, namespaceID, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("network_policy exists check: %w", err)
	}
	return exists, nil
}

// UpsertNetworkPolicy upserts a NetworkPolicy by (cluster_id, namespace_id,
// name) and replaces its rules atomically in one transaction (ADR-0038).
// Used by both the in-process pull collector and the HTTP push handler.
//
//nolint:gocritic // hugeParam: NetworkPolicy mirrors the NetPolStore interface
func (p *PG) UpsertNetworkPolicy(
	ctx context.Context, np NetworkPolicy, rules []NetworkPolicyRule,
) (uuid.UUID, error) {
	var id uuid.UUID
	if err := p.withTx(ctx, "upsert network_policy", func(tx pgx.Tx) error {
		var err error
		id, err = upsertNetworkPolicyTx(ctx, tx, np)
		if err != nil {
			return err
		}
		return replaceNetworkPolicyRulesTx(ctx, tx, id, rules)
	}); err != nil {
		return uuid.Nil, err
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

// SweepNetworkPoliciesByNamespaceWithCount deletes any policy in the namespace
// not in the keep-list and returns the count of deleted rows. Mirror of
// SweepNetworkPoliciesByNamespace but exposes the count for the HTTP
// reconcile handler (ADR-0038).
func (p *PG) SweepNetworkPoliciesByNamespaceWithCount(
	ctx context.Context, nsID uuid.UUID, keep []string,
) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keep) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM network_policies WHERE namespace_id=$1`, nsID)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM network_policies WHERE namespace_id=$1 AND name <> ALL($2)`,
			nsID, keep)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep network_policies: %w", err)
	}
	return ct.RowsAffected(), nil
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

// networkPolicySortSpec is the sort=<key> allowlist for GET /v1/network-policies.
var networkPolicySortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:            {expr: "LOWER(name)", kind: sortText},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyReconcileSeenAt,
}

// npWithSeenAt wraps a NetworkPolicyRow with the scanned reconcile_seen_at
// timestamp needed for cursor minting when sort=reconcile_seen_at.
type npWithSeenAt struct {
	np     api.NetworkPolicyRow
	seenAt time.Time
}

// networkPolicySortVal extracts the serialised sort value for cursor minting.
func networkPolicySortVal(r *npWithSeenAt, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.np.Name)
	default: // reconcile_seen_at
		return sortValTime(&r.seenAt)
	}
}

// ListNetworkPoliciesByCluster returns a page + next_cursor. Optional
// namespaceID filter is in filter.NamespaceID (nil = all namespaces).
// Satisfies api.Store.
//
//nolint:gocyclo // pagination + optional filters + cursor logic; matches existing list patterns in this package
func (p *PG) ListNetworkPoliciesByCluster(
	ctx context.Context,
	clusterID uuid.UUID,
	filter api.NetworkPolicyListFilter,
	page api.ListPage,
) ([]api.NetworkPolicyRow, string, error) {
	limit := clampLimit(page.Limit, 500)
	key, col, dir, err := networkPolicySortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(npSelect)
	// Always project reconcile_seen_at for cursor minting.
	sb.WriteString(`, reconcile_seen_at FROM network_policies`)

	conds := make([]string, 0, 4)
	args := make([]any, 0, 5)

	args = append(args, clusterID)
	conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))

	args = append(args, filter.NamespaceID)
	conds = append(conds, fmt.Sprintf("($%d::uuid IS NULL OR namespace_id = $%d)", len(args), len(args)))

	if filter.Name != nil {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE $%d ESCAPE '\\'", len(args)))
	}

	if page.Cursor != "" {
		val, cid, curErr := decodeListCursor(page.Cursor, key, dir)
		if curErr != nil {
			return nil, "", curErr
		}
		if curErr := keysetCond(col, "id", dir, val, cid, &conds, &args); curErr != nil {
			return nil, "", curErr
		}
	}

	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query network_policies by cluster: %w", err)
	}
	defer rows.Close()

	raw := make([]npWithSeenAt, 0, limit+1)
	for rows.Next() {
		var r npWithSeenAt
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
		last := &raw[limit-1]
		next = encodeListCursor(key, networkPolicySortVal(last, key), last.np.ID, dir)
		raw = raw[:limit]
	}
	items := make([]api.NetworkPolicyRow, len(raw))
	for i, r := range raw { //nolint:gocritic // rangeValCopy: cursor wrapper struct; copy is intentional
		items[i] = r.np
	}
	return items, next, nil
}
