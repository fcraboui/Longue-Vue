// Ingress CRUD + upsert + reconcile. Split out of pg.go.
package store

import (
	"context"
	"database/sql"
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

// ingressColumns is the full INSERT / SELECT / RETURNING column list for
// the ingresses table. Kept as a single const so the several SQL paths
// stay in sync; the load_balancer JSONB sits between tls and labels.
const ingressColumns = `id, namespace_id, name, ingress_class_name,
	rules, tls, load_balancer, labels, created_at, updated_at`

// ingressSelectColumns is the read-side projection: every column from the
// ingresses row (aliased to `i.`) plus the denormalized parent names
// joined in via LEFT JOIN (ADR-0027). LEFT JOIN keeps orphan rows
// visible with NULL names — the UI renders that as an explicit badge.
const ingressSelectColumns = `i.id, i.namespace_id, i.name, i.ingress_class_name,
	i.rules, i.tls, i.load_balancer, i.labels, i.created_at, i.updated_at,
	n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name`

const ingressFromJoined = `FROM ingresses i
	LEFT JOIN namespaces n ON n.id = i.namespace_id
	LEFT JOIN clusters   c ON c.id = n.cluster_id`

// CreateIngress inserts a new ingress into the given namespace.
func (p *PG) CreateIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Ingress{}, err
	}
	rulesJSON, err := marshalPorts(in.Rules)
	if err != nil {
		return api.Ingress{}, err
	}
	tlsJSON, err := marshalPorts(in.Tls)
	if err != nil {
		return api.Ingress{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Ingress{}, err
	}

	q := `INSERT INTO ingresses (` + ingressColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, lbJSON, labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Ingress{}, fmt.Errorf("ingress %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
			case "23503":
				return api.Ingress{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Ingress{}, fmt.Errorf("insert ingress: %w", err)
	}

	return api.Ingress{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		IngressClassName: in.IngressClassName,
		Rules:            in.Rules,
		Tls:              in.Tls,
		LoadBalancer:     in.LoadBalancer,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}, nil
}

// GetIngress fetches an ingress by id.
func (p *PG) GetIngress(ctx context.Context, id uuid.UUID) (api.Ingress, error) {
	q := `SELECT ` + ingressSelectColumns + ` ` + ingressFromJoined + ` WHERE i.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	ing, err := scanIngress(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Ingress{}, api.ErrNotFound
		}
		return api.Ingress{}, fmt.Errorf("select ingress: %w", err)
	}
	return ing, nil
}

// ListIngresses returns up to limit ingresses, optionally filtered by namespace.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Ingress, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(ingressSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(ingressFromJoined)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if namespaceID != nil {
		args = append(args, *namespaceID)
		conds = append(conds, fmt.Sprintf("i.namespace_id = $%d", len(args)))
	}
	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		conds = append(conds, fmt.Sprintf("(i.created_at, i.id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY i.created_at DESC, i.id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query ingresses: %w", err)
	}
	defer rows.Close()

	items := make([]api.Ingress, 0, limit)
	for rows.Next() {
		ing, err := scanIngress(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan ingress: %w", err)
		}
		items = append(items, ing)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate ingresses: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateIngress applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateIngress(ctx context.Context, id uuid.UUID, in api.IngressUpdate) (api.Ingress, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.IngressClassName != nil {
		appendSet("ingress_class_name", *in.IngressClassName)
	}
	if in.Rules != nil {
		b, err := marshalPorts(in.Rules)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("rules", b)
	}
	if in.Tls != nil {
		b, err := marshalPorts(in.Tls)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("tls", b)
	}
	if in.LoadBalancer != nil {
		b, err := marshalPorts(in.LoadBalancer)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("load_balancer", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE ingresses SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Ingress{}, fmt.Errorf("update ingress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Ingress{}, api.ErrNotFound
	}
	return p.GetIngress(ctx, id)
}

// DeleteIngress removes an ingress by id.
func (p *PG) DeleteIngress(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM ingresses WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete ingress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertIngress inserts-or-updates an ingress keyed by (namespace_id, name).
//
//nolint:gocyclo // CTE drives branch count
func (p *PG) UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Ingress{}, api.OutcomeNoChange, err
	}
	rulesJSON, err := marshalPorts(in.Rules)
	if err != nil {
		return api.Ingress{}, api.OutcomeNoChange, err
	}
	tlsJSON, err := marshalPorts(in.Tls)
	if err != nil {
		return api.Ingress{}, api.OutcomeNoChange, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Ingress{}, api.OutcomeNoChange, err
	}

	// AUDIT_BUSINESS_FIELDS: ingress_class_name, rules, tls, load_balancer,
	// labels. updated_at is a clock field — excluded from business_changed.
	const q = `
		WITH old AS (
		  SELECT ingress_class_name, rules, tls, load_balancer, labels
		    FROM ingresses WHERE namespace_id = $2 AND name = $3
		),
		upserted AS (
		  INSERT INTO ingresses (
		      id, namespace_id, name, ingress_class_name,
		      rules, tls, load_balancer, labels, created_at, updated_at
		  ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		  ON CONFLICT (namespace_id, name) DO UPDATE SET
		      ingress_class_name = EXCLUDED.ingress_class_name,
		      rules              = EXCLUDED.rules,
		      tls                = EXCLUDED.tls,
		      load_balancer      = EXCLUDED.load_balancer,
		      labels             = EXCLUDED.labels,
		      updated_at         = EXCLUDED.updated_at
		  RETURNING id, namespace_id, name, ingress_class_name,
		            rules, tls, load_balancer, labels, created_at, updated_at,
		            xmax
		)
		SELECT u.id, u.namespace_id, u.name, u.ingress_class_name,
		       u.rules, u.tls, u.load_balancer, u.labels,
		       u.created_at, u.updated_at,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.ingress_class_name IS DISTINCT FROM u.ingress_class_name OR
		           o.rules              IS DISTINCT FROM u.rules              OR
		           o.tls                IS DISTINCT FROM u.tls                OR
		           o.load_balancer      IS DISTINCT FROM u.load_balancer      OR
		           o.labels             IS DISTINCT FROM u.labels
		       )) AS business_changed
		  FROM upserted u LEFT JOIN old o ON true
	`

	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, lbJSON, labelsJSON, now,
	)

	var (
		i                api.Ingress
		ingID            uuid.UUID
		namespaceID      uuid.UUID
		createdAt        time.Time
		updatedAt        time.Time
		ingressClassName sql.NullString
		rulesOut         []byte
		tlsOut           []byte
		lbOut            []byte
		labelsOut        []byte
		inserted         bool
		businessChanged  bool
	)
	if err := row.Scan(
		&ingID, &namespaceID, &i.Name,
		&ingressClassName,
		&rulesOut, &tlsOut, &lbOut, &labelsOut,
		&createdAt, &updatedAt,
		&inserted, &businessChanged,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("upsert ingress: %w", err)
	}
	i.Id = &ingID
	i.NamespaceId = namespaceID
	i.CreatedAt = &createdAt
	i.UpdatedAt = &updatedAt
	i.IngressClassName = nullableString(ingressClassName)
	if len(rulesOut) > 0 {
		var rules []map[string]interface{}
		if err := json.Unmarshal(rulesOut, &rules); err != nil {
			return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("unmarshal ingress rules: %w", err)
		}
		if len(rules) > 0 {
			i.Rules = &rules
		}
	}
	if len(tlsOut) > 0 {
		var tls []map[string]interface{}
		if err := json.Unmarshal(tlsOut, &tls); err != nil {
			return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("unmarshal ingress tls: %w", err)
		}
		if len(tls) > 0 {
			i.Tls = &tls
		}
	}
	if lb, err := unmarshalMapArray(lbOut); err != nil {
		return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("unmarshal ingress load_balancer: %w", err)
	} else {
		i.LoadBalancer = lb
	}
	if len(labelsOut) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsOut, &labels); err != nil {
			return api.Ingress{}, api.OutcomeNoChange, fmt.Errorf("unmarshal ingress labels: %w", err)
		}
		if len(labels) > 0 {
			i.Labels = &labels
		}
	}

	return i, classifyOutcome(inserted, businessChanged), nil
}

// DeleteIngressesNotIn mirrors DeleteServicesNotIn.
func (p *PG) DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM ingresses
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete ingresses not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanIngress(row pgx.Row) (api.Ingress, error) {
	var (
		i                  api.Ingress
		id                 uuid.UUID
		namespaceID        uuid.UUID
		createdAt          time.Time
		updatedAt          time.Time
		ingressClassName   sql.NullString
		rulesJSON          []byte
		tlsJSON            []byte
		lbJSON             []byte
		labelsJSON         []byte
		namespaceName      sql.NullString
		namespaceClusterID uuid.NullUUID
		clusterName        sql.NullString
	)
	if err := row.Scan(
		&id, &namespaceID, &i.Name,
		&ingressClassName,
		&rulesJSON, &tlsJSON, &lbJSON, &labelsJSON,
		&createdAt, &updatedAt,
		&namespaceName, &namespaceClusterID, &clusterName,
	); err != nil {
		return api.Ingress{}, fmt.Errorf("scan ingress: %w", err)
	}
	i.Id = &id
	i.NamespaceId = namespaceID
	i.CreatedAt = &createdAt
	i.UpdatedAt = &updatedAt
	i.IngressClassName = nullableString(ingressClassName)
	i.NamespaceName = nullableString(namespaceName)
	if namespaceClusterID.Valid {
		v := namespaceClusterID.UUID
		i.ClusterId = &v
	}
	i.ClusterName = nullableString(clusterName)
	if len(rulesJSON) > 0 {
		var rules []map[string]interface{}
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress rules: %w", err)
		}
		if len(rules) > 0 {
			i.Rules = &rules
		}
	}
	if len(tlsJSON) > 0 {
		var tls []map[string]interface{}
		if err := json.Unmarshal(tlsJSON, &tls); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress tls: %w", err)
		}
		if len(tls) > 0 {
			i.Tls = &tls
		}
	}
	if lb, err := unmarshalMapArray(lbJSON); err != nil {
		return api.Ingress{}, fmt.Errorf("unmarshal ingress load_balancer: %w", err)
	} else {
		i.LoadBalancer = lb
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress labels: %w", err)
		}
		if len(labels) > 0 {
			i.Labels = &labels
		}
	}
	return i, nil
}
