// Service CRUD + upsert + reconcile. Split out of pg.go.
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

// serviceColumns — same pattern as nodeColumns / ingressColumns.
const serviceColumns = `id, namespace_id, name, type, cluster_ip,
	selector, ports, load_balancer, labels, created_at, updated_at`

// Read-side projection with parent-name JOINs (ADR-0027).
const serviceSelectColumns = `s.id, s.namespace_id, s.name, s.type, s.cluster_ip,
	s.selector, s.ports, s.load_balancer, s.labels, s.created_at, s.updated_at,
	n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name`

const serviceFromJoined = `FROM services s
	LEFT JOIN namespaces n ON n.id = s.namespace_id
	LEFT JOIN clusters   c ON c.id = n.cluster_id`

// CreateService inserts a new service into the given namespace.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateService(ctx context.Context, in api.ServiceCreate) (api.Service, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Service{}, err
	}
	selectorJSON, err := marshalLabels(in.Selector)
	if err != nil {
		return api.Service{}, err
	}
	portsJSON, err := marshalPorts(in.Ports)
	if err != nil {
		return api.Service{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Service{}, err
	}

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	q := `INSERT INTO services (` + serviceColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, lbJSON, labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Service{}, fmt.Errorf("service %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
			case "23503":
				return api.Service{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Service{}, fmt.Errorf("insert service: %w", err)
	}

	return api.Service{
		Id:           &id,
		NamespaceId:  in.NamespaceId,
		Name:         in.Name,
		Type:         in.Type,
		ClusterIp:    in.ClusterIp,
		Selector:     in.Selector,
		Ports:        in.Ports,
		LoadBalancer: in.LoadBalancer,
		Labels:       in.Labels,
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}, nil
}

// GetService fetches a service by id.
func (p *PG) GetService(ctx context.Context, id uuid.UUID) (api.Service, error) {
	q := `SELECT ` + serviceSelectColumns + ` ` + serviceFromJoined + ` WHERE s.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	s, err := scanService(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Service{}, api.ErrNotFound
		}
		return api.Service{}, fmt.Errorf("select service: %w", err)
	}
	return s, nil
}

// serviceSortSpec is the sort=<key> allowlist for GET /v1/services.
var serviceSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:      {expr: "LOWER(s.name)", kind: sortText},
		sortKeyType:      {expr: "LOWER(s.type)", kind: sortText, nullable: true},
		sortKeyClusterIP: {expr: "LOWER(s.cluster_ip)", kind: sortText, nullable: true},
		sortKeyCreatedAt: {expr: "s.created_at", kind: sortTime},
		sortKeyUpdatedAt: {expr: "s.updated_at", kind: sortTime},
	},
	defaultKey: sortKeyCreatedAt,
}

// serviceSortVal extracts the serialized sort value for cursor minting.
func serviceSortVal(s *api.Service, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&s.Name)
	case sortKeyType:
		if s.Type == nil {
			return sortValText(nil)
		}
		t := string(*s.Type)
		return sortValText(&t)
	case sortKeyClusterIP:
		return sortValText(s.ClusterIp)
	case sortKeyUpdatedAt:
		return sortValTime(s.UpdatedAt)
	default: // created_at
		return sortValTime(s.CreatedAt)
	}
}

// ListServices returns up to limit services sorted by the requested sort
// key, optionally filtered by namespace id and/or name.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListServices(ctx context.Context, filter api.ServiceListFilter, page api.ListPage) ([]api.Service, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := serviceSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(serviceSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(serviceFromJoined)
	args := make([]any, 0, 6)
	conds := make([]string, 0, 4)

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("s.namespace_id = $%d", len(args)))
	}
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(s.name) LIKE $%d ESCAPE '\\'", len(args)))
	}
	if page.Cursor != "" {
		val, cid, err := decodeListCursor(page.Cursor, key, dir)
		if err != nil {
			return nil, "", err
		}
		if err := keysetCond(col, "s.id", dir, val, cid, &conds, &args); err != nil {
			return nil, "", err
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "s.id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query services: %w", err)
	}
	defer rows.Close()

	items := make([]api.Service, 0, limit)
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan service: %w", err)
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate services: %w", err)
	}

	var next string
	if len(items) > limit {
		last := &items[limit-1]
		if last.Id != nil {
			next = encodeListCursor(key, serviceSortVal(last, key), *last.Id, dir)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateService applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateService(ctx context.Context, id uuid.UUID, in api.ServiceUpdate) (api.Service, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Type != nil {
		appendSet("type", string(*in.Type))
	}
	if in.ClusterIp != nil {
		appendSet("cluster_ip", *in.ClusterIp)
	}
	if in.Selector != nil {
		b, err := marshalLabels(in.Selector)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("selector", b)
	}
	if in.Ports != nil {
		b, err := marshalPorts(in.Ports)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("ports", b)
	}
	if in.LoadBalancer != nil {
		b, err := marshalPorts(in.LoadBalancer)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("load_balancer", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE services SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Service{}, fmt.Errorf("update service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Service{}, api.ErrNotFound
	}
	return p.GetService(ctx, id)
}

// DeleteService removes a service by id.
func (p *PG) DeleteService(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM services WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertService inserts-or-updates a service keyed by (namespace_id, name).
//
//nolint:gocritic,gocyclo // hugeParam: Store interface requires value param; CTE drives branch count
func (p *PG) UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Service{}, api.OutcomeNoChange, err
	}
	selectorJSON, err := marshalLabels(in.Selector)
	if err != nil {
		return api.Service{}, api.OutcomeNoChange, err
	}
	portsJSON, err := marshalPorts(in.Ports)
	if err != nil {
		return api.Service{}, api.OutcomeNoChange, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Service{}, api.OutcomeNoChange, err
	}

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	// AUDIT_BUSINESS_FIELDS: type, cluster_ip, selector, ports, load_balancer,
	// labels. updated_at is a clock field — excluded from business_changed
	// detection so reconcile-only ticks turn into NoChange.
	const q = `
		WITH old AS (
		  SELECT type, cluster_ip, selector, ports, load_balancer, labels
		    FROM services WHERE namespace_id = $2 AND name = $3
		),
		upserted AS (
		  INSERT INTO services (
		      id, namespace_id, name, type, cluster_ip,
		      selector, ports, load_balancer, labels, created_at, updated_at
		  ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		  ON CONFLICT (namespace_id, name) DO UPDATE SET
		      type          = EXCLUDED.type,
		      cluster_ip    = EXCLUDED.cluster_ip,
		      selector      = EXCLUDED.selector,
		      ports         = EXCLUDED.ports,
		      load_balancer = EXCLUDED.load_balancer,
		      labels        = EXCLUDED.labels,
		      updated_at    = EXCLUDED.updated_at
		  RETURNING id, namespace_id, name, type, cluster_ip,
		            selector, ports, load_balancer, labels, created_at, updated_at,
		            xmax
		)
		SELECT u.id, u.namespace_id, u.name, u.type, u.cluster_ip,
		       u.selector, u.ports, u.load_balancer, u.labels,
		       u.created_at, u.updated_at,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.type          IS DISTINCT FROM u.type          OR
		           o.cluster_ip    IS DISTINCT FROM u.cluster_ip    OR
		           o.selector      IS DISTINCT FROM u.selector      OR
		           o.ports         IS DISTINCT FROM u.ports         OR
		           o.load_balancer IS DISTINCT FROM u.load_balancer OR
		           o.labels        IS DISTINCT FROM u.labels
		       )) AS business_changed
		  FROM upserted u LEFT JOIN old o ON true
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, lbJSON, labelsJSON, now,
	)

	var (
		s               api.Service
		svcID           uuid.UUID
		namespaceID     uuid.UUID
		createdAt       time.Time
		updatedAt       time.Time
		svcTypeOut      sql.NullString
		clusterIP       sql.NullString
		selectorOut     []byte
		portsOut        []byte
		lbOut           []byte
		labelsOut       []byte
		inserted        bool
		businessChanged bool
	)
	if err := row.Scan(
		&svcID, &namespaceID, &s.Name,
		&svcTypeOut, &clusterIP,
		&selectorOut, &portsOut, &lbOut, &labelsOut,
		&createdAt, &updatedAt,
		&inserted, &businessChanged,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Service{}, api.OutcomeNoChange, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return api.Service{}, api.OutcomeNoChange, fmt.Errorf("upsert service: %w", err)
	}
	s.Id = &svcID
	s.NamespaceId = namespaceID
	s.CreatedAt = &createdAt
	s.UpdatedAt = &updatedAt
	s.ClusterIp = nullableString(clusterIP)
	if svcTypeOut.Valid {
		t := api.ServiceType(svcTypeOut.String)
		s.Type = &t
	}
	if len(selectorOut) > 0 {
		var sel map[string]string
		if err := json.Unmarshal(selectorOut, &sel); err != nil {
			return api.Service{}, api.OutcomeNoChange, fmt.Errorf("unmarshal service selector: %w", err)
		}
		if len(sel) > 0 {
			s.Selector = &sel
		}
	}
	if len(portsOut) > 0 {
		var ports []map[string]interface{}
		if err := json.Unmarshal(portsOut, &ports); err != nil {
			return api.Service{}, api.OutcomeNoChange, fmt.Errorf("unmarshal service ports: %w", err)
		}
		if len(ports) > 0 {
			s.Ports = &ports
		}
	}
	if lb, err := unmarshalMapArray(lbOut); err != nil {
		return api.Service{}, api.OutcomeNoChange, fmt.Errorf("unmarshal service load_balancer: %w", err)
	} else {
		s.LoadBalancer = lb
	}
	if len(labelsOut) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsOut, &labels); err != nil {
			return api.Service{}, api.OutcomeNoChange, fmt.Errorf("unmarshal service labels: %w", err)
		}
		if len(labels) > 0 {
			s.Labels = &labels
		}
	}
	return s, classifyOutcome(inserted, businessChanged), nil
}

// DeleteServicesNotIn mirrors DeletePodsNotIn.
func (p *PG) DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM services
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete services not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanService(row pgx.Row) (api.Service, error) {
	var (
		s                  api.Service
		id                 uuid.UUID
		namespaceID        uuid.UUID
		createdAt          time.Time
		updatedAt          time.Time
		svcType            sql.NullString
		clusterIP          sql.NullString
		selectorJSON       []byte
		portsJSON          []byte
		lbJSON             []byte
		labelsJSON         []byte
		namespaceName      sql.NullString
		namespaceClusterID uuid.NullUUID
		clusterName        sql.NullString
	)
	if err := row.Scan(
		&id, &namespaceID, &s.Name,
		&svcType, &clusterIP,
		&selectorJSON, &portsJSON, &lbJSON, &labelsJSON,
		&createdAt, &updatedAt,
		&namespaceName, &namespaceClusterID, &clusterName,
	); err != nil {
		return api.Service{}, fmt.Errorf("scan service: %w", err)
	}
	s.Id = &id
	s.NamespaceId = namespaceID
	s.CreatedAt = &createdAt
	s.UpdatedAt = &updatedAt
	s.ClusterIp = nullableString(clusterIP)
	s.NamespaceName = nullableString(namespaceName)
	if namespaceClusterID.Valid {
		v := namespaceClusterID.UUID
		s.ClusterId = &v
	}
	s.ClusterName = nullableString(clusterName)
	if svcType.Valid {
		t := api.ServiceType(svcType.String)
		s.Type = &t
	}
	if len(selectorJSON) > 0 {
		var sel map[string]string
		if err := json.Unmarshal(selectorJSON, &sel); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service selector: %w", err)
		}
		if len(sel) > 0 {
			s.Selector = &sel
		}
	}
	if len(portsJSON) > 0 {
		var ports []map[string]interface{}
		if err := json.Unmarshal(portsJSON, &ports); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service ports: %w", err)
		}
		if len(ports) > 0 {
			s.Ports = &ports
		}
	}
	if lb, err := unmarshalMapArray(lbJSON); err != nil {
		return api.Service{}, fmt.Errorf("unmarshal service load_balancer: %w", err)
	} else {
		s.LoadBalancer = lb
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service labels: %w", err)
		}
		if len(labels) > 0 {
			s.Labels = &labels
		}
	}
	return s, nil
}
