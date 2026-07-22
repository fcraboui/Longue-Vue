package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

type ClusterPolicy = api.ClusterPolicyRow

const cpSelect = `
	id, cluster_id, namespace_id, name,
	resource_type, scope, description, category, severity,
	action, failure_policy, background,
	rule_types, rules_count, target_resources, key_exclusions,
	ready, annotations, spec_raw`

func (p *PG) GetClusterPolicy(ctx context.Context, id uuid.UUID) (api.ClusterPolicyRow, error) {
	const q = `SELECT ` + cpSelect + ` FROM cluster_policies WHERE id = $1`
	var cp api.ClusterPolicyRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&cp.ID, &cp.ClusterID, &cp.NamespaceID, &cp.Name,
		&cp.ResourceType, &cp.Scope, &cp.Description, &cp.Category, &cp.Severity,
		&cp.Action, &cp.FailurePolicy, &cp.Background,
		&cp.RuleTypes, &cp.RulesCount, &cp.TargetResources, &cp.KeyExclusions,
		&cp.Ready, &cp.Annotations, &cp.SpecRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ClusterPolicyRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.ClusterPolicyRow{}, fmt.Errorf("get cluster_policy: %w", err)
	}
	return cp, nil
}

func (p *PG) UpsertClusterPolicy(ctx context.Context, cp ClusterPolicy) (uuid.UUID, error) {
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, `
		INSERT INTO cluster_policies
			(cluster_id, namespace_id, name,
			 resource_type, scope, description, category, severity,
			 action, failure_policy, background,
			 rule_types, rules_count, target_resources, key_exclusions,
			 ready, annotations, spec_raw, reconcile_seen_at)
		VALUES ($1, $2, $3,
		        $4, $5, $6, $7, $8,
		        $9, $10, $11,
		        $12, $13, $14, $15,
		        $16, $17, $18, NOW())
		ON CONFLICT ON CONSTRAINT uq_cluster_policies_scope DO UPDATE SET
			resource_type      = EXCLUDED.resource_type,
			scope              = EXCLUDED.scope,
			description        = EXCLUDED.description,
			category           = EXCLUDED.category,
			severity           = EXCLUDED.severity,
			action             = EXCLUDED.action,
			failure_policy     = EXCLUDED.failure_policy,
			background         = EXCLUDED.background,
			rule_types         = EXCLUDED.rule_types,
			rules_count        = EXCLUDED.rules_count,
			target_resources   = EXCLUDED.target_resources,
			key_exclusions     = EXCLUDED.key_exclusions,
			ready              = EXCLUDED.ready,
			annotations        = EXCLUDED.annotations,
			spec_raw           = EXCLUDED.spec_raw,
			reconcile_seen_at  = NOW()
		RETURNING id`,
		cp.ClusterID, cp.NamespaceID, cp.Name,
		cp.ResourceType, cp.Scope, cp.Description, cp.Category, cp.Severity,
		cp.Action, cp.FailurePolicy, cp.Background,
		cp.RuleTypes, cp.RulesCount, cp.TargetResources, cp.KeyExclusions,
		cp.Ready, cp.Annotations, cp.SpecRaw,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert cluster_policy: %w", err)
	}
	return id, nil
}

func (p *PG) DeleteClusterPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1`, clusterID)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1 AND id <> ALL($2)`,
			clusterID, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep cluster_policies: %w", err)
	}
	return ct.RowsAffected(), nil
}

var clusterPolicySortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:            {expr: "LOWER(name)", kind: sortText},
		sortKeyAction:          {expr: "LOWER(action)", kind: sortText, nullable: true},
		sortKeyBackground:      {expr: "background::text", kind: sortText, nullable: true},
		sortKeySeverity:        {expr: "LOWER(severity)", kind: sortText, nullable: true},
		sortKeyRulesCount:      {expr: "rules_count::text", kind: sortText, nullable: true},
		sortKeyFailurePolicy:   {expr: "LOWER(failure_policy)", kind: sortText, nullable: true},
		sortKeyCategory:        {expr: "LOWER(category)", kind: sortText, nullable: true},
		sortKeyReady:           {expr: "ready::text", kind: sortText, nullable: true},
		sortKeyResourceType:    {expr: "LOWER(resource_type)", kind: sortText},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyName,
}

type cpWithSeenAt struct {
	cp     api.ClusterPolicyRow
	seenAt time.Time
}

func clusterPolicySortVal(r *cpWithSeenAt, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.cp.Name)
	case sortKeyResourceType:
		return sortValText(&r.cp.ResourceType)
	case sortKeyAction:
		return sortValText(r.cp.Action)
	case sortKeySeverity:
		return sortValText(r.cp.Severity)
	case sortKeyFailurePolicy:
		return sortValText(r.cp.FailurePolicy)
	case sortKeyCategory:
		return sortValText(r.cp.Category)
	case sortKeyBackground:
		return sortValBool(r.cp.Background)
	case sortKeyReady:
		return sortValBool(r.cp.Ready)
	case sortKeyRulesCount:
		return sortValInt(r.cp.RulesCount)
	default:
		return sortValTime(&r.seenAt)
	}
}

func (p *PG) ListClusterPolicies(
	ctx context.Context,
	filter api.ClusterPolicyListFilter,
	page api.ListPage,
) ([]api.ClusterPolicyRow, string, error) {
	limit := clampLimit(page.Limit, 500)
	key, col, dir, err := clusterPolicySortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(cpSelect)
	sb.WriteString(`, reconcile_seen_at FROM cluster_policies`)

	conds := make([]string, 0, 8)
	args := make([]any, 0, 9)

	if filter.ClusterID != nil {
		args = append(args, *filter.ClusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
	}

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
	}

	if filter.Name != nil {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE $%d ESCAPE '\\'", len(args)))
	}

	if filter.ResourceType != nil {
		args = append(args, *filter.ResourceType)
		conds = append(conds, fmt.Sprintf("resource_type = $%d", len(args)))
	}

	if filter.Action != nil {
		args = append(args, *filter.Action)
		conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
	}

	if filter.Severity != nil {
		args = append(args, *filter.Severity)
		conds = append(conds, fmt.Sprintf("severity = $%d", len(args)))
	}

	if filter.Category != nil {
		args = append(args, namePattern(*filter.Category))
		conds = append(conds, fmt.Sprintf("LOWER(category) LIKE $%d ESCAPE '\\'", len(args)))
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
		return nil, "", fmt.Errorf("query cluster_policies: %w", err)
	}
	defer rows.Close()

	raw := make([]cpWithSeenAt, 0, limit+1)
	for rows.Next() {
		var r cpWithSeenAt
		if err := rows.Scan(
			&r.cp.ID, &r.cp.ClusterID, &r.cp.NamespaceID, &r.cp.Name,
			&r.cp.ResourceType, &r.cp.Scope, &r.cp.Description, &r.cp.Category, &r.cp.Severity,
			&r.cp.Action, &r.cp.FailurePolicy, &r.cp.Background,
			&r.cp.RuleTypes, &r.cp.RulesCount, &r.cp.TargetResources, &r.cp.KeyExclusions,
			&r.cp.Ready, &r.cp.Annotations, &r.cp.SpecRaw,
			&r.seenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan cluster_policy: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate cluster_policies: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := &raw[limit-1]
		next = encodeListCursor(key, clusterPolicySortVal(last, key), last.cp.ID, dir)
		raw = raw[:limit]
	}
	items := make([]api.ClusterPolicyRow, len(raw))
	for i, r := range raw {
		items[i] = r.cp
	}
	return items, next, nil
}
