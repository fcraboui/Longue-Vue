package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
		ON CONFLICT (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name) DO UPDATE SET
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
		sortKeyName: {expr: "LOWER(name)", kind: sortText},
		sortKeyAction: {
			expr:     "LOWER(action)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyBackground: {
			expr:     "background::int",
			kind:     sortInt,
			nullable: true,
		},
		sortKeySeverity: {
			// NULL severity falls through to ELSE -1 in SQL, but the
			// nullable=true flag ensures NULL rows are placed in the
			// NULLS LAST region — beyond even rank -1.
			expr: `CASE LOWER(severity) ` +
				`WHEN 'critical' THEN 4 ` +
				`WHEN 'high' THEN 3 ` +
				`WHEN 'medium' THEN 2 ` +
				`WHEN 'low' THEN 1 ` +
				`WHEN 'info' THEN 0 ` +
				`ELSE -1 END`,
			kind:     sortInt,
			nullable: true,
		},
		sortKeyRulesCount: {
			expr:     "rules_count",
			kind:     sortInt,
			nullable: true,
		},
		sortKeyFailurePolicy: {
			expr:     "LOWER(failure_policy)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyCategory: {
			expr:     "LOWER(category)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyReady: {
			expr:     "ready::int",
			kind:     sortInt,
			nullable: true,
		},
		sortKeyResourceType:    {expr: "LOWER(resource_type)", kind: sortText},
		sortKeyScope:           {expr: "LOWER(scope)", kind: sortText},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyName,
}

func clusterPolicySortVal(r *api.ClusterPolicyRow, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.Name)
	case sortKeyResourceType:
		return sortValText(&r.ResourceType)
	case sortKeyScope:
		return sortValText(&r.Scope)
	case sortKeyAction:
		return sortValText(r.Action)
	case sortKeySeverity:
		return sortValInt(severityRank(r.Severity))
	case sortKeyFailurePolicy:
		return sortValText(r.FailurePolicy)
	case sortKeyCategory:
		return sortValText(r.Category)
	case sortKeyBackground:
		return sortValInt(boolToIntPtr(r.Background))
	case sortKeyReady:
		return sortValInt(boolToIntPtr(r.Ready))
	case sortKeyRulesCount:
		return sortValInt(r.RulesCount)
	default:
		return sortValTime(&r.ReconcileSeenAt)
	}
}

func severityRank(s *string) *int {
	if s == nil {
		return nil
	}
	switch strings.ToLower(*s) {
	case "critical":
		return intPtr(4)
	case "high":
		return intPtr(3)
	case "medium":
		return intPtr(2)
	case "low":
		return intPtr(1)
	case "info":
		return intPtr(0)
	default:
		return intPtr(-1)
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
		args = append(args, strings.ToLower(*filter.Action))
		conds = append(conds, fmt.Sprintf("LOWER(action) = $%d", len(args)))
	}

	if filter.Severity != nil {
		args = append(args, strings.ToLower(*filter.Severity))
		conds = append(conds, fmt.Sprintf("LOWER(severity) = $%d", len(args)))
	}

	if filter.FailurePolicy != nil {
		args = append(args, strings.ToLower(*filter.FailurePolicy))
		conds = append(conds, fmt.Sprintf("LOWER(failure_policy) = $%d", len(args)))
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

	raw := make([]api.ClusterPolicyRow, 0, limit+1)
	for rows.Next() {
		var r api.ClusterPolicyRow
		if err := rows.Scan(
			&r.ID, &r.ClusterID, &r.NamespaceID, &r.Name,
			&r.ResourceType, &r.Scope, &r.Description, &r.Category, &r.Severity,
			&r.Action, &r.FailurePolicy, &r.Background,
			&r.RuleTypes, &r.RulesCount, &r.TargetResources, &r.KeyExclusions,
			&r.Ready, &r.Annotations, &r.SpecRaw,
			&r.ReconcileSeenAt,
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
		next = encodeListCursor(key, clusterPolicySortVal(last, key), last.ID, dir)
		raw = raw[:limit]
	}
	return raw, next, nil
}
