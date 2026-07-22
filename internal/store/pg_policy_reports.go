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

type PolicyReport = api.PolicyReportRow

const prSelect = `
	id, cluster_id, namespace_id, name,
	scope_kind, scope_name,
	summary_pass, summary_fail, summary_warn, summary_error, summary_skip,
	results_raw`

const prListSelect = `
	id, cluster_id, namespace_id, name,
	scope_kind, scope_name,
	summary_pass, summary_fail, summary_warn, summary_error, summary_skip`

func (p *PG) GetPolicyReport(ctx context.Context, id uuid.UUID) (api.PolicyReportRow, error) {
	const q = `SELECT ` + prSelect + ` FROM policy_reports WHERE id = $1`
	var pr api.PolicyReportRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&pr.ID, &pr.ClusterID, &pr.NamespaceID, &pr.Name,
		&pr.ScopeKind, &pr.ScopeName,
		&pr.SummaryPass, &pr.SummaryFail, &pr.SummaryWarn, &pr.SummaryError, &pr.SummarySkip,
		&pr.ResultsRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.PolicyReportRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.PolicyReportRow{}, fmt.Errorf("get policy_report: %w", err)
	}
	return pr, nil
}

func (p *PG) UpsertPolicyReport(ctx context.Context, pr PolicyReport) (uuid.UUID, error) {
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, `
		INSERT INTO policy_reports
			(cluster_id, namespace_id, name,
			 scope_kind, scope_name,
			 summary_pass, summary_fail, summary_warn, summary_error, summary_skip,
			 results_raw, reconcile_seen_at)
		VALUES ($1, $2, $3,
		       $4, $5,
		       $6, $7, $8, $9, $10,
		       $11, NOW())
		ON CONFLICT (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name) DO UPDATE SET
			scope_kind        = EXCLUDED.scope_kind,
			scope_name        = EXCLUDED.scope_name,
			summary_pass      = EXCLUDED.summary_pass,
			summary_fail      = EXCLUDED.summary_fail,
			summary_warn      = EXCLUDED.summary_warn,
			summary_error     = EXCLUDED.summary_error,
			summary_skip      = EXCLUDED.summary_skip,
			results_raw       = EXCLUDED.results_raw,
			reconcile_seen_at = NOW()
		RETURNING id`,
		pr.ClusterID, pr.NamespaceID, pr.Name,
		pr.ScopeKind, pr.ScopeName,
		pr.SummaryPass, pr.SummaryFail, pr.SummaryWarn, pr.SummaryError, pr.SummarySkip,
		pr.ResultsRaw,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert policy_report: %w", err)
	}
	return id, nil
}

func (p *PG) DeletePolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1`, clusterID)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1 AND id <> ALL($2)`,
			clusterID, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep policy_reports: %w", err)
	}
	return ct.RowsAffected(), nil
}

var policyReportSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:            {expr: "LOWER(name)", kind: sortText},
		sortKeySummaryPass:     {expr: "summary_pass", kind: sortInt},
		sortKeySummaryFail:     {expr: "summary_fail", kind: sortInt},
		sortKeySummaryWarn:     {expr: "summary_warn", kind: sortInt},
		sortKeySummaryError:    {expr: "summary_error", kind: sortInt},
		sortKeySummarySkip:     {expr: "summary_skip", kind: sortInt},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyName,
}

func policyReportSortVal(r *api.PolicyReportRow, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.Name)
	case sortKeySummaryPass:
		return sortValInt(intPtr(r.SummaryPass))
	case sortKeySummaryFail:
		return sortValInt(intPtr(r.SummaryFail))
	case sortKeySummaryWarn:
		return sortValInt(intPtr(r.SummaryWarn))
	case sortKeySummaryError:
		return sortValInt(intPtr(r.SummaryError))
	case sortKeySummarySkip:
		return sortValInt(intPtr(r.SummarySkip))
	default:
		return sortValTime(&r.ReconcileSeenAt)
	}
}

func (p *PG) ListPolicyReports(
	ctx context.Context,
	filter api.PolicyReportListFilter,
	page api.ListPage,
) ([]api.PolicyReportRow, string, error) {
	limit := clampLimit(page.Limit, 500)
	key, col, dir, err := policyReportSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(prListSelect)
	sb.WriteString(`, reconcile_seen_at FROM policy_reports`)

	conds := make([]string, 0, 4)
	args := make([]any, 0, 5)

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

	if filter.ScopeKind != nil {
		args = append(args, *filter.ScopeKind)
		conds = append(conds, fmt.Sprintf("scope_kind = $%d", len(args)))
	}

	if filter.ScopeName != nil {
		args = append(args, namePattern(*filter.ScopeName))
		conds = append(conds, fmt.Sprintf("LOWER(scope_name) LIKE $%d ESCAPE '\\'", len(args)))
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
		return nil, "", fmt.Errorf("query policy_reports: %w", err)
	}
	defer rows.Close()

	raw := make([]api.PolicyReportRow, 0, limit+1)
	for rows.Next() {
		var r api.PolicyReportRow
		if err := rows.Scan(
			&r.ID, &r.ClusterID, &r.NamespaceID, &r.Name,
			&r.ScopeKind, &r.ScopeName,
			&r.SummaryPass, &r.SummaryFail, &r.SummaryWarn, &r.SummaryError, &r.SummarySkip,
			&r.ReconcileSeenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan policy_report: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate policy_reports: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := &raw[limit-1]
		next = encodeListCursor(key, policyReportSortVal(last, key), last.ID, dir)
		raw = raw[:limit]
	}
	return raw, next, nil
}
