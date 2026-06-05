package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// SecurityGroup and SecurityGroupRule are type aliases so that store-internal
// test helpers (pg_security_groups_test.go) can continue using the short name
// while the api.Store interface uses api.SecurityGroupRow / api.SecurityGroupRuleRow.
type SecurityGroup = api.SecurityGroupRow
type SecurityGroupRule = api.SecurityGroupRuleRow

const sgSelect = `id, cloud_account_id, provider_sg_id, name, COALESCE(vpc_id,''), tags`

// UpsertSecurityGroup inserts or updates by (cloud_account_id, provider_sg_id).
// Returns the stable row ID. Collector callers use this on every tick.
func (p *PG) UpsertSecurityGroup(ctx context.Context, sg api.SecurityGroupRow) (uuid.UUID, error) {
	const q = `
		INSERT INTO security_groups
			(cloud_account_id, provider_sg_id, name, vpc_id, tags, reconcile_seen_at)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, NOW())
		ON CONFLICT (cloud_account_id, provider_sg_id) DO UPDATE SET
			name              = EXCLUDED.name,
			vpc_id            = EXCLUDED.vpc_id,
			tags              = EXCLUDED.tags,
			reconcile_seen_at = NOW()
		RETURNING id`
	var id uuid.UUID
	if err := p.pool.QueryRow(ctx, q,
		sg.CloudAccountID, sg.ProviderSGID, sg.Name, sg.VPCID, sg.Tags,
	).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("upsert security_group: %w", err)
	}
	return id, nil
}

// GetSecurityGroup returns ErrNotFound on miss.
func (p *PG) GetSecurityGroup(ctx context.Context, id uuid.UUID) (api.SecurityGroupRow, error) {
	const q = `SELECT ` + sgSelect + ` FROM security_groups WHERE id = $1`
	var sg api.SecurityGroupRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&sg.ID, &sg.CloudAccountID, &sg.ProviderSGID, &sg.Name, &sg.VPCID, &sg.Tags,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.SecurityGroupRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.SecurityGroupRow{}, fmt.Errorf("get security_group: %w", err)
	}
	return sg, nil
}

// ReplaceSecurityGroupRules deletes then inserts in one transaction.
// Rule sets are small + atomic; finer diff is over-engineering.
func (p *PG) ReplaceSecurityGroupRules(ctx context.Context, sgID uuid.UUID, rules []api.SecurityGroupRuleRow) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin replace security_group_rules: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM security_group_rules WHERE security_group_id = $1`, sgID,
	); err != nil {
		return fmt.Errorf("delete security_group_rules: %w", err)
	}

	const ins = `
		INSERT INTO security_group_rules
		  (security_group_id, direction, protocol, from_port, to_port,
		   peer_kind, peer_cidr, peer_sg_provider_id, peer_prefix_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,'')::cidr, NULLIF($8,''), NULLIF($9,''), NULLIF($10,''))`

	for _, r := range rules {
		if _, err := tx.Exec(ctx, ins,
			sgID, r.Direction, r.Protocol, r.FromPort, r.ToPort,
			r.PeerKind, r.PeerCIDR, r.PeerSGProviderID, r.PeerPrefixID, r.Description,
		); err != nil {
			return fmt.Errorf("insert security_group_rule: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace security_group_rules: %w", err)
	}
	return nil
}

// ListSecurityGroupRules returns every rule for a single security group, in
// stable insertion order.
func (p *PG) ListSecurityGroupRules(ctx context.Context, sgID uuid.UUID) ([]api.SecurityGroupRuleRow, error) {
	const q = `
		SELECT id, security_group_id, direction, protocol, from_port, to_port,
		       peer_kind, COALESCE(host(peer_cidr), ''),
		       COALESCE(peer_sg_provider_id, ''),
		       COALESCE(peer_prefix_id, ''),
		       COALESCE(description, '')
		FROM security_group_rules
		WHERE security_group_id = $1
		ORDER BY id`

	rows, err := p.pool.Query(ctx, q, sgID)
	if err != nil {
		return nil, fmt.Errorf("query security_group_rules: %w", err)
	}
	defer rows.Close()

	var out []api.SecurityGroupRuleRow
	for rows.Next() {
		var r api.SecurityGroupRuleRow
		if err := rows.Scan(
			&r.ID, &r.SecurityGroupID, &r.Direction, &r.Protocol,
			&r.FromPort, &r.ToPort,
			&r.PeerKind, &r.PeerCIDR,
			&r.PeerSGProviderID,
			&r.PeerPrefixID,
			&r.Description,
		); err != nil {
			return nil, fmt.Errorf("scan security_group_rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate security_group_rules: %w", err)
	}
	return out, nil
}

// SweepSecurityGroupsByAccount deletes any security group in the account
// whose ProviderSGID is NOT in seenProviderIDs. CASCADE removes its rules.
// Caller MUST only invoke after a successful List (CLAUDE.md reconcile contract).
func (p *PG) SweepSecurityGroupsByAccount(ctx context.Context, accountID uuid.UUID, seenProviderIDs []string) error {
	_, err := p.pool.Exec(ctx,
		`DELETE FROM security_groups
		  WHERE cloud_account_id = $1
		    AND provider_sg_id <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		accountID, seenProviderIDs,
	)
	if err != nil {
		return fmt.Errorf("sweep security_groups: %w", err)
	}
	return nil
}

// ListSecurityGroupsByAccount returns a page + next_cursor, ordered by
// (reconcile_seen_at DESC, id DESC). Cursor format identical to
// ListNetworkPoliciesByCluster — REUSE encodeCursor/decodeCursor.
func (p *PG) ListSecurityGroupsByAccount(ctx context.Context, accountID uuid.UUID, limit int, cursor string) ([]api.SecurityGroupRow, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	conds := make([]string, 0, 2)
	args := make([]any, 0, 4)

	args = append(args, accountID)
	conds = append(conds, fmt.Sprintf("cloud_account_id = $%d", len(args)))

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
		`SELECT `+sgSelect+`, reconcile_seen_at FROM security_groups %s ORDER BY reconcile_seen_at DESC, id DESC LIMIT $%d`,
		where, len(args),
	)

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query security_groups by account: %w", err)
	}
	defer rows.Close()

	type sgWithTS struct {
		sg     api.SecurityGroupRow
		seenAt time.Time
	}
	raw := make([]sgWithTS, 0, limit+1)
	for rows.Next() {
		var r sgWithTS
		if err := rows.Scan(
			&r.sg.ID, &r.sg.CloudAccountID, &r.sg.ProviderSGID,
			&r.sg.Name, &r.sg.VPCID, &r.sg.Tags,
			&r.seenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan security_group: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate security_groups: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := raw[limit-1]
		next = encodeCursor(last.seenAt, last.sg.ID)
		raw = raw[:limit]
	}
	items := make([]api.SecurityGroupRow, len(raw))
	for i, r := range raw {
		items[i] = r.sg
	}
	return items, next, nil
}
