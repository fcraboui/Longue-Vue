// Workload CRUD + upsert + reconcile (keyed on kind+name). Split out of pg.go.
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
	"github.com/sthalbert/longue-vue/internal/timetravel"
)

// CreateWorkload inserts a new workload.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Workload{}, err
	}
	specJSON, err := marshalSpec(in.Spec)
	if err != nil {
		return api.Workload{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Workload{}, err
	}

	const q = `
		INSERT INTO workloads (
			id, namespace_id, kind, name, replicas, ready_replicas,
			containers, labels, spec, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, string(in.Kind), in.Name, in.Replicas, in.ReadyReplicas,
		containersJSON, labelsJSON, specJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Workload{}, fmt.Errorf(
					"workload %s/%q in namespace %s already exists: %w",
					in.Kind, in.Name, in.NamespaceId, api.ErrConflict,
				)
			case "23503":
				return api.Workload{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Workload{}, fmt.Errorf("insert workload: %w", err)
	}

	return api.Workload{
		Id:            &id,
		NamespaceId:   in.NamespaceId,
		Kind:          in.Kind,
		Name:          in.Name,
		Replicas:      in.Replicas,
		ReadyReplicas: in.ReadyReplicas,
		Containers:    in.Containers,
		Labels:        in.Labels,
		Spec:          in.Spec,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}, nil
}

// Workload read-side projection with parent-name JOINs (ADR-0027) plus
// the operator-curated application_id soft-pointer (ADR-0029).
const workloadSelectColumns = `w.id, w.namespace_id, w.kind, w.name, w.replicas, w.ready_replicas,
	w.containers, w.labels, w.spec, w.application_id, w.created_at, w.updated_at,
	n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name,
	a.name AS application_name`

const workloadFromJoined = `FROM workloads w
	LEFT JOIN namespaces   n ON n.id = w.namespace_id
	LEFT JOIN clusters     c ON c.id = n.cluster_id
	LEFT JOIN applications a ON a.id = w.application_id`

// GetWorkload fetches a workload by id.
func (p *PG) GetWorkload(ctx context.Context, id uuid.UUID) (api.Workload, error) {
	q := `SELECT ` + workloadSelectColumns + ` ` + workloadFromJoined + ` WHERE w.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	w, err := scanWorkload(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Workload{}, api.ErrNotFound
		}
		return api.Workload{}, fmt.Errorf("select workload: %w", err)
	}
	return w, nil
}

// workloadSortSpec is the sort=<key> allowlist for GET /v1/workloads.
var workloadSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:      {expr: "LOWER(w.name)", kind: sortText},
		"kind":           {expr: "LOWER(w.kind)", kind: sortText},
		sortKeyCreatedAt: {expr: "w.created_at", kind: sortTime},
		sortKeyUpdatedAt: {expr: "w.updated_at", kind: sortTime},
	},
	defaultKey: sortKeyCreatedAt,
}

// workloadSortVal extracts the serialized sort value for cursor minting.
func workloadSortVal(w *api.Workload, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&w.Name)
	case "kind":
		s := string(w.Kind)
		return sortValText(&s)
	case sortKeyUpdatedAt:
		return sortValTime(w.UpdatedAt)
	default: // created_at
		return sortValTime(w.CreatedAt)
	}
}

// ListWorkloads returns a cursor-paginated page of workloads, optionally
// filtered by namespace, kind, and/or container image substring.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListWorkloads(ctx context.Context, filter api.WorkloadListFilter, page api.ListPage) ([]api.Workload, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := workloadSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(workloadSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(workloadFromJoined)
	args := make([]any, 0, 8)
	conds := make([]string, 0, 7)

	if !filter.IncludeTerminated {
		conds = append(conds, "w.terminated_at IS NULL")
	}
	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("w.namespace_id = $%d", len(args)))
	}
	if filter.Kind != nil {
		args = append(args, string(*filter.Kind))
		conds = append(conds, fmt.Sprintf("w.kind = $%d", len(args)))
	}
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(w.name) LIKE $%d ESCAPE '\\'", len(args)))
	}
	if filter.ImageSubstring != nil && *filter.ImageSubstring != "" {
		// ILIKE handles case-insensitivity; escapeLike + ESCAPE makes
		// operator-pasted % / _ literal (was unescaped before ADR-0042).
		args = append(args, "%"+escapeLike(*filter.ImageSubstring)+"%")
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM jsonb_array_elements(w.containers) elem WHERE elem->>'image' ILIKE $%d ESCAPE '\\')",
			len(args),
		))
	}
	// ADR-0029: link-aware filters. application_id wins on conflict with
	// application_name (mirrors the cloud_account_id / cloud_account_name
	// precedence from ADR-0019). application_name is normalised server-side
	// to match the kebab-case stored in applications.name.
	if filter.ApplicationID != nil {
		args = append(args, *filter.ApplicationID)
		conds = append(conds, fmt.Sprintf("w.application_id = $%d", len(args)))
	} else if filter.ApplicationName != nil && *filter.ApplicationName != "" {
		args = append(args, api.NormalizeApplicationName(*filter.ApplicationName))
		conds = append(conds, fmt.Sprintf(
			"w.application_id = (SELECT id FROM applications WHERE name = $%d)",
			len(args),
		))
	}
	if filter.Unlinked != nil && *filter.Unlinked {
		conds = append(conds, "w.application_id IS NULL")
	}
	// ADR-0029 §2.4: substring match on linked application name (used by
	// the Search endpoint). LIKE metacharacters are escaped; case-insensitive
	// via LOWER on both sides.
	if filter.ApplicationNameSubstring != nil && *filter.ApplicationNameSubstring != "" {
		args = append(args, "%"+escapeLike(strings.ToLower(*filter.ApplicationNameSubstring))+"%")
		conds = append(conds, fmt.Sprintf(
			"w.application_id IN (SELECT id FROM applications WHERE LOWER(name) LIKE $%d ESCAPE '\\')",
			len(args),
		))
	}
	if page.Cursor != "" {
		val, cid, err := decodeListCursor(page.Cursor, key, dir)
		if err != nil {
			return nil, "", err
		}
		if err := keysetCond(col, "w.id", dir, val, cid, &conds, &args); err != nil {
			return nil, "", err
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "w.id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query workloads: %w", err)
	}
	defer rows.Close()

	items := make([]api.Workload, 0, limit)
	for rows.Next() {
		w, err := scanWorkload(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan workload: %w", err)
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate workloads: %w", err)
	}

	var next string
	if len(items) > limit {
		last := &items[limit-1]
		if last.Id != nil {
			next = encodeListCursor(key, workloadSortVal(last, key), *last.Id, dir)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateWorkload applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateWorkload(ctx context.Context, id uuid.UUID, in api.WorkloadUpdate, clearApplication bool) (api.Workload, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Replicas != nil {
		appendSet("replicas", *in.Replicas)
	}
	if in.ReadyReplicas != nil {
		appendSet("ready_replicas", *in.ReadyReplicas)
	}
	if in.Containers != nil {
		b, err := marshalPorts(in.Containers)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("containers", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("labels", b)
	}
	if in.Spec != nil {
		b, err := marshalSpec(in.Spec)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("spec", b)
	}
	// ADR-0029: operator-curated soft-pointer with three-state merge-patch
	// semantics (RFC 7396). The codegen-generated field is *uuid.UUID, which
	// can't distinguish absent from JSON null, so the handler detects an
	// explicit `"application_id": null` from the raw body and threads it here
	// via clearApplication:
	//   - clearApplication=true  → SET application_id = NULL (unlink),
	//   - in.ApplicationId != nil → SET application_id = <value> (link/relink),
	//   - otherwise               → leave the column unchanged.
	// id wins over null: an explicit value alongside null still links.
	switch {
	case in.ApplicationId != nil:
		appendSet("application_id", *in.ApplicationId)
	case clearApplication:
		appendSet("application_id", nil)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	if err := p.withTx(ctx, "update workload", func(tx pgx.Tx) error {
		prev, _ := workloadRowMap(ctx, tx, id) // FOR UPDATE

		q := fmt.Sprintf("UPDATE workloads SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("update workload: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return api.ErrNotFound
		}

		if prev != nil {
			if next, err := workloadRowMapNoLock(ctx, tx, id); err == nil {
				actor := timetravel.ActorFromContext(ctx)
				_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, id, prev, next, changeTypeUpdate, actor)
			}
		}
		return nil
	}); err != nil {
		return api.Workload{}, err
	}
	return p.GetWorkload(ctx, id)
}

// DeleteWorkload removes a workload by id.
func (p *PG) DeleteWorkload(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM workloads WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete workload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertWorkload inserts-or-updates a workload keyed by (namespace_id, kind, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Workload{}, api.OutcomeNoChange, err
	}
	specJSON, err := marshalSpec(in.Spec)
	if err != nil {
		return api.Workload{}, api.OutcomeNoChange, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Workload{}, api.OutcomeNoChange, err
	}

	var (
		w                         api.Workload
		inserted, businessChanged bool
	)
	if err := p.withTx(ctx, "upsert workload", func(tx pgx.Tx) error {
		var err error
		w, inserted, businessChanged, err = upsertWorkloadTx(ctx, tx, &in, id, now, containersJSON, labelsJSON, specJSON)
		return err
	}); err != nil {
		return api.Workload{}, api.OutcomeNoChange, err
	}
	return w, classifyOutcome(inserted, businessChanged), nil
}

// upsertWorkloadTx runs the audit-noop-detecting upsert CTE inside the
// caller's transaction and hydrates the returned row. The bools feed
// classifyOutcome (ADR-0024).
//
//nolint:gocyclo // restore detection + JSONB hydration add branches; acceptable here
func upsertWorkloadTx(
	ctx context.Context, tx pgx.Tx, in *api.WorkloadCreate,
	id uuid.UUID, now time.Time,
	containersJSON, labelsJSON, specJSON []byte,
) (w api.Workload, inserted, businessChanged bool, err error) {
	var prevTerminatedAt *time.Time
	var prevWLID *uuid.UUID
	_ = tx.QueryRow(ctx,
		`SELECT id, terminated_at FROM workloads WHERE namespace_id=$1 AND kind=$2 AND name=$3`,
		in.NamespaceId, string(in.Kind), in.Name,
	).Scan(&prevWLID, &prevTerminatedAt)
	isCreate := prevWLID == nil
	isRestore := prevWLID != nil && prevTerminatedAt != nil

	// AUDIT_BUSINESS_FIELDS: replicas, ready_replicas, containers, labels, spec,
	// terminated_at (restore flips it). updated_at is a clock field — excluded.
	//
	// COLLECTOR INVARIANT (ADR-0029 §7): application_id is operator-curated and
	// MUST NOT be touched by collector ticks. The upsert deliberately omits it
	// from both the INSERT column list and the ON CONFLICT SET list so that
	// re-running reconcile preserves the curator's link. The PATCH path
	// (UpdateWorkload) is the only place that writes this column.
	const q = `
	WITH old AS (
	  SELECT replicas, ready_replicas, containers, labels, spec, terminated_at
	    FROM workloads WHERE namespace_id=$2 AND kind=$3 AND name=$4
	),
	upserted AS (
	  INSERT INTO workloads (
	      id, namespace_id, kind, name, replicas, ready_replicas,
	      containers, labels, spec, created_at, updated_at
	  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	  ON CONFLICT (namespace_id, kind, name) DO UPDATE SET
	      replicas       = EXCLUDED.replicas,
	      ready_replicas = EXCLUDED.ready_replicas,
	      containers     = EXCLUDED.containers,
	      labels         = EXCLUDED.labels,
	      spec           = EXCLUDED.spec,
	      terminated_at  = NULL,
	      updated_at     = EXCLUDED.updated_at
	  RETURNING id, namespace_id, kind, name, replicas, ready_replicas,
	            containers, labels, spec, created_at, updated_at,
	            terminated_at, xmax
	)
	SELECT u.id, u.namespace_id, u.kind, u.name, u.replicas, u.ready_replicas,
	       u.containers, u.labels, u.spec, u.created_at, u.updated_at,
	       (u.xmax = 0) AS inserted,
	       (u.xmax <> 0 AND (
	           o.replicas       IS DISTINCT FROM u.replicas       OR
	           o.ready_replicas IS DISTINCT FROM u.ready_replicas OR
	           o.containers     IS DISTINCT FROM u.containers     OR
	           o.labels         IS DISTINCT FROM u.labels         OR
	           o.spec           IS DISTINCT FROM u.spec           OR
	           o.terminated_at  IS DISTINCT FROM u.terminated_at
	       )) AS business_changed
	  FROM upserted u LEFT JOIN old o ON true
`
	row := tx.QueryRow(ctx, q,
		id, in.NamespaceId, string(in.Kind), in.Name, in.Replicas, in.ReadyReplicas,
		containersJSON, labelsJSON, specJSON, now,
	)

	var (
		wID           uuid.UUID
		namespaceID   uuid.UUID
		kind          string
		replicas      sql.NullInt32
		readyReplicas sql.NullInt32
		createdAt     time.Time
		updatedAt     time.Time
		containersOut []byte
		labelsOut     []byte
		specOut       []byte
	)
	if err := row.Scan(
		&wID, &namespaceID, &kind, &w.Name,
		&replicas, &readyReplicas,
		&containersOut, &labelsOut, &specOut,
		&createdAt, &updatedAt,
		&inserted, &businessChanged,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return w, inserted, businessChanged, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return w, inserted, businessChanged, fmt.Errorf("upsert workload: %w", err)
	}
	w.Id = &wID
	w.NamespaceId = namespaceID
	w.Kind = api.WorkloadKind(kind)
	w.CreatedAt = &createdAt
	w.UpdatedAt = &updatedAt
	if replicas.Valid {
		v := int(replicas.Int32)
		w.Replicas = &v
	}
	if readyReplicas.Valid {
		v := int(readyReplicas.Int32)
		w.ReadyReplicas = &v
	}
	if cs, err := unmarshalContainers(containersOut); err != nil {
		return w, inserted, businessChanged, fmt.Errorf("unmarshal workload containers: %w", err)
	} else if cs != nil {
		w.Containers = cs
	}
	if len(labelsOut) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsOut, &labels); err != nil {
			return w, inserted, businessChanged, fmt.Errorf("unmarshal workload labels: %w", err)
		}
		if len(labels) > 0 {
			w.Labels = &labels
		}
	}
	if len(specOut) > 0 {
		var spec map[string]interface{}
		if err := json.Unmarshal(specOut, &spec); err != nil {
			return w, inserted, businessChanged, fmt.Errorf("unmarshal workload spec: %w", err)
		}
		if len(spec) > 0 {
			w.Spec = &spec
		}
	}

	actualID := *w.Id
	if snap, err := workloadRowMapNoLock(ctx, tx, actualID); err == nil {
		actor := timetravel.ActorFromContext(ctx)
		changeType := changeTypeUpdate
		if isCreate {
			changeType = changeTypeCreate
		} else if isRestore {
			changeType = changeTypeRestore
		}
		_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, actualID, nil, snap, changeType, actor)
	}
	return w, inserted, businessChanged, nil
}

// DeleteWorkloadsNotIn soft-deletes workloads in the namespace whose
// (kind, name) tuple is not in the parallel keep arrays and that are not
// already terminated. Per ADR-0021 §5; same semantics as DeleteNodesNotIn.
// COALESCE guards against pgx encoding nil slices as SQL NULL.
func (p *PG) DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	var affected int64
	if err := p.withTx(ctx, "delete workloads not in", func(tx pgx.Tx) error {
		toDeleteRows, err := tx.Query(ctx,
			`SELECT id FROM workloads
			  WHERE namespace_id = $1
			    AND (kind, name) NOT IN (
			      SELECT k, n FROM UNNEST(
			        COALESCE($2::text[], ARRAY[]::text[]),
			        COALESCE($3::text[], ARRAY[]::text[])
			      ) AS t(k, n)
			    )
			    AND terminated_at IS NULL`,
			namespaceID, keepKinds, keepNames)
		if err != nil {
			return fmt.Errorf("list workloads to soft-delete: %w", err)
		}
		toDelete, err := scanUUIDs(toDeleteRows)
		if err != nil {
			return fmt.Errorf("scan workloads to soft-delete: %w", err)
		}

		tag, err := tx.Exec(ctx,
			`UPDATE workloads
			    SET terminated_at = NOW(), updated_at = NOW()
			  WHERE namespace_id = $1
			    AND (kind, name) NOT IN (
			      SELECT k, n FROM UNNEST(
			        COALESCE($2::text[], ARRAY[]::text[]),
			        COALESCE($3::text[], ARRAY[]::text[])
			      ) AS t(k, n)
			    )
			    AND terminated_at IS NULL`,
			namespaceID, keepKinds, keepNames,
		)
		if err != nil {
			return fmt.Errorf("soft-delete workloads not in: %w", err)
		}

		actor := timetravel.ActorFromContext(ctx)
		for _, wlID := range toDelete {
			if snap, err := workloadRowMapNoLock(ctx, tx, wlID); err == nil {
				_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, wlID, nil, snap, changeTypeSoftDelete, actor)
			}
		}

		affected = tag.RowsAffected()
		return nil
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func marshalSpec(spec *map[string]interface{}) ([]byte, error) { //nolint:gocritic // ptrToRefParam: callers pass *map from generated API types
	if spec == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	return b, nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanWorkload(row pgx.Row) (api.Workload, error) {
	var (
		w                  api.Workload
		id                 uuid.UUID
		namespaceID        uuid.UUID
		kind               string
		replicas           sql.NullInt32
		readyReplicas      sql.NullInt32
		createdAt          time.Time
		updatedAt          time.Time
		containersJSON     []byte
		labelsJSON         []byte
		specJSON           []byte
		applicationID      uuid.NullUUID
		namespaceName      sql.NullString
		namespaceClusterID uuid.NullUUID
		clusterName        sql.NullString
		applicationName    sql.NullString
	)
	if err := row.Scan(
		&id, &namespaceID, &kind, &w.Name,
		&replicas, &readyReplicas,
		&containersJSON, &labelsJSON, &specJSON, &applicationID,
		&createdAt, &updatedAt,
		&namespaceName, &namespaceClusterID, &clusterName,
		&applicationName,
	); err != nil {
		return api.Workload{}, fmt.Errorf("scan workload: %w", err)
	}
	w.Id = &id
	w.NamespaceId = namespaceID
	w.Kind = api.WorkloadKind(kind)
	w.CreatedAt = &createdAt
	w.UpdatedAt = &updatedAt
	w.NamespaceName = nullableString(namespaceName)
	if namespaceClusterID.Valid {
		v := namespaceClusterID.UUID
		w.ClusterId = &v
	}
	w.ClusterName = nullableString(clusterName)
	if applicationID.Valid {
		v := applicationID.UUID
		w.ApplicationId = &v
	}
	w.ApplicationName = nullableString(applicationName)
	if replicas.Valid {
		v := int(replicas.Int32)
		w.Replicas = &v
	}
	if readyReplicas.Valid {
		v := int(readyReplicas.Int32)
		w.ReadyReplicas = &v
	}
	if cs, err := unmarshalContainers(containersJSON); err != nil {
		return api.Workload{}, fmt.Errorf("unmarshal workload containers: %w", err)
	} else if cs != nil {
		w.Containers = cs
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Workload{}, fmt.Errorf("unmarshal workload labels: %w", err)
		}
		if len(labels) > 0 {
			w.Labels = &labels
		}
	}
	if len(specJSON) > 0 {
		var spec map[string]interface{}
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return api.Workload{}, fmt.Errorf("unmarshal workload spec: %w", err)
		}
		if len(spec) > 0 {
			w.Spec = &spec
		}
	}
	return w, nil
}
