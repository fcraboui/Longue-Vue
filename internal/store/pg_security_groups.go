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

// SecurityGroup is a type alias for api.SecurityGroupRow, allowing store-internal
// test helpers to use the short name without the api. prefix.
type SecurityGroup = api.SecurityGroupRow

// SecurityGroupRule is a type alias for api.SecurityGroupRuleRow, allowing
// store-internal test helpers to use the short name without the api. prefix.
type SecurityGroupRule = api.SecurityGroupRuleRow

const sgSelect = `id, cloud_account_id, provider_sg_id, name, COALESCE(vpc_id,''), tags`

// UpsertSecurityGroup inserts or updates by (cloud_account_id, provider_sg_id).
// Returns the stable row ID. Collector callers use this on every tick.
//
//nolint:gocritic // hugeParam: api.SecurityGroupRow matches the Store interface; changing to pointer would break callers
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

// GetSecurityGroupByProviderID fetches a security group by
// (cloud_account_id, provider_sg_id). Returns ErrNotFound on miss.
func (p *PG) GetSecurityGroupByProviderID(ctx context.Context, accountID uuid.UUID, providerSGID string) (api.SecurityGroupRow, error) {
	const q = `SELECT ` + sgSelect + ` FROM security_groups WHERE cloud_account_id = $1 AND provider_sg_id = $2 LIMIT 1`
	var sg api.SecurityGroupRow
	err := p.pool.QueryRow(ctx, q, accountID, providerSGID).Scan(
		&sg.ID, &sg.CloudAccountID, &sg.ProviderSGID, &sg.Name, &sg.VPCID, &sg.Tags,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.SecurityGroupRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.SecurityGroupRow{}, fmt.Errorf("get security_group by provider id: %w", err)
	}
	return sg, nil
}

// ReplaceSecurityGroupRules deletes then inserts in one transaction.
// Rule sets are small + atomic; finer diff is over-engineering.
func (p *PG) ReplaceSecurityGroupRules(ctx context.Context, sgID uuid.UUID, rules []api.SecurityGroupRuleRow) error {
	return p.withTx(ctx, "replace security_group_rules", func(tx pgx.Tx) error {
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

		for _, r := range rules { //nolint:gocritic // rangeValCopy: SecurityGroupRuleRow is a small struct; indexing would reduce clarity
			if _, err := tx.Exec(ctx, ins,
				sgID, r.Direction, r.Protocol, r.FromPort, r.ToPort,
				r.PeerKind, r.PeerCIDR, r.PeerSGProviderID, r.PeerPrefixID, r.Description,
			); err != nil {
				return fmt.Errorf("insert security_group_rule: %w", err)
			}
		}
		return nil
	})
}

// ListSecurityGroupRules returns every rule for a single security group, in
// stable insertion order.
func (p *PG) ListSecurityGroupRules(ctx context.Context, sgID uuid.UUID) ([]api.SecurityGroupRuleRow, error) {
	const q = `
		SELECT id, security_group_id, direction, protocol, from_port, to_port,
		       peer_kind, COALESCE(text(peer_cidr), ''),
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

// securityGroupSortSpec is the sort=<key> allowlist for GET /v1/security-groups.
var securityGroupSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:            {expr: "LOWER(name)", kind: sortText},
		sortKeyVPCID:           {expr: "LOWER(vpc_id)", kind: sortText, nullable: true},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyReconcileSeenAt,
}

// sgWithSeenAt wraps a SecurityGroupRow with the scanned reconcile_seen_at
// timestamp needed for cursor minting when sort=reconcile_seen_at.
type sgWithSeenAt struct {
	sg     api.SecurityGroupRow
	seenAt time.Time
}

// securityGroupSortVal extracts the serialised sort value for cursor minting.
func securityGroupSortVal(r *sgWithSeenAt, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.sg.Name)
	case sortKeyVPCID:
		// vpc_id is projected through COALESCE(vpc_id,'') — an empty string
		// here means the column is NULL (provider VPC ids are never empty).
		// Mint nil so the cursor resumes inside the NULLS LAST region.
		if r.sg.VPCID == "" {
			return nil
		}
		return sortValText(&r.sg.VPCID)
	default: // reconcile_seen_at
		return sortValTime(&r.seenAt)
	}
}

// ListSecurityGroupsByAccount returns a page + next_cursor filtered and
// sorted per filter/page.
//
//nolint:gocyclo // pagination + optional filters + cursor logic; matches other list patterns
func (p *PG) ListSecurityGroupsByAccount(
	ctx context.Context,
	accountID uuid.UUID,
	filter api.SecurityGroupListFilter,
	page api.ListPage,
) ([]api.SecurityGroupRow, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := securityGroupSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(sgSelect)
	// Always project reconcile_seen_at for cursor minting.
	sb.WriteString(`, reconcile_seen_at FROM security_groups`)

	args := make([]any, 0, 4)
	conds := make([]string, 0, 3)

	args = append(args, accountID)
	conds = append(conds, fmt.Sprintf("cloud_account_id = $%d", len(args)))

	if filter.Name != nil {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE $%d ESCAPE '\\'", len(args)))
	}

	if filter.VpcID != nil {
		args = append(args, *filter.VpcID)
		conds = append(conds, fmt.Sprintf("vpc_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query security_groups by account: %w", err)
	}
	defer rows.Close()

	raw := make([]sgWithSeenAt, 0, limit+1)
	for rows.Next() {
		var r sgWithSeenAt
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
		last := &raw[limit-1]
		next = encodeListCursor(key, securityGroupSortVal(last, key), last.sg.ID, dir)
		raw = raw[:limit]
	}
	items := make([]api.SecurityGroupRow, len(raw))
	for i, r := range raw { //nolint:gocritic // rangeValCopy: cursor wrapper struct; copy is intentional
		items[i] = r.sg
	}
	return items, next, nil
}
