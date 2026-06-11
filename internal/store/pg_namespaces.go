// Namespace CRUD + upsert + soft-delete + reconcile. Split out of pg.go.
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

// DeleteNamespacesNotIn mirrors DeleteNodesNotIn: soft-deletes namespaces of
// the given cluster not in keepNames and not already terminated. Per
// ADR-0021 §5. For each soft-deleted namespace, this mirrors
// SoftDeleteNamespace by cascade-soft-deleting its live workloads and
// hard-deleting its unhistoried children (pods, services, ingresses, PVCs)
// — otherwise workloads in a vanished namespace would linger forever in
// list/EOL outputs because reconcileWorkloads only walks namespaces that
// were present in the current tick.
//
//nolint:gocyclo // cascade + history capture adds branches; mirrors SoftDeleteNamespace
func (p *PG) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	var affected int64
	if err := p.withTx(ctx, "delete namespaces not in", func(tx pgx.Tx) error {
		toDeleteRows, err := tx.Query(ctx,
			`SELECT id FROM namespaces
			  WHERE cluster_id = $1
			    AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
			    AND terminated_at IS NULL`,
			clusterID, keepNames)
		if err != nil {
			return fmt.Errorf("list namespaces to soft-delete: %w", err)
		}
		toDelete, err := scanUUIDs(toDeleteRows)
		if err != nil {
			return fmt.Errorf("scan namespaces to soft-delete: %w", err)
		}

		actor := timetravel.ActorFromContext(ctx)

		// Per-namespace cascade: capture each namespace's live workload IDs
		// before mutating, hard-delete unhistoried children, soft-delete
		// workloads, then capture workload snapshots so history reflects the
		// final terminated state.
		wlIDsByNS := make(map[uuid.UUID][]uuid.UUID, len(toDelete))
		for _, nsID := range toDelete {
			wlIDs, err := liveWorkloadIDsForNamespace(ctx, tx, nsID)
			if err != nil {
				return fmt.Errorf("list workloads for soft-delete namespace %s: %w", nsID, err)
			}
			wlIDsByNS[nsID] = wlIDs

			if _, err := tx.Exec(ctx,
				`DELETE FROM pods WHERE namespace_id = $1`, nsID); err != nil {
				return fmt.Errorf("cascade-delete pods for %s: %w", nsID, err)
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM services WHERE namespace_id = $1`, nsID); err != nil {
				return fmt.Errorf("cascade-delete services for %s: %w", nsID, err)
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM ingresses WHERE namespace_id = $1`, nsID); err != nil {
				return fmt.Errorf("cascade-delete ingresses for %s: %w", nsID, err)
			}
			if _, err := tx.Exec(ctx,
				`DELETE FROM persistent_volume_claims WHERE namespace_id = $1`, nsID); err != nil {
				return fmt.Errorf("cascade-delete persistent_volume_claims for %s: %w", nsID, err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE workloads SET terminated_at = NOW(), updated_at = NOW()
				   WHERE namespace_id = $1 AND terminated_at IS NULL`, nsID); err != nil {
				return fmt.Errorf("soft-delete workloads for %s: %w", nsID, err)
			}
		}

		tag, err := tx.Exec(ctx,
			`UPDATE namespaces
			    SET terminated_at = NOW(), updated_at = NOW()
			  WHERE cluster_id = $1
			    AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
			    AND terminated_at IS NULL`,
			clusterID, keepNames,
		)
		if err != nil {
			return fmt.Errorf("soft-delete namespaces not in: %w", err)
		}

		for _, nsID := range toDelete {
			for _, wlID := range wlIDsByNS[nsID] {
				if snap, err := workloadRowMapNoLock(ctx, tx, wlID); err == nil {
					_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, wlID, nil, snap, changeTypeSoftDelete, actor)
				}
			}
			if snap, err := namespaceRowMapNoLock(ctx, tx, nsID); err == nil {
				_ = timetravel.Capture(ctx, tx, timetravel.KindNamespace, nsID, nil, snap, changeTypeSoftDelete, actor)
			}
		}

		affected = tag.RowsAffected()
		return nil
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

// CreateNamespace inserts a new namespace.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Namespace{}, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return api.Namespace{}, fmt.Errorf("marshal namespace annotations: %w", err)
	}

	const q = `
		INSERT INTO namespaces (
			id, cluster_id, name, display_name, phase, labels,
			owner, criticality, notes, runbook_url, annotations,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.Phase, labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON,
		now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Namespace{}, fmt.Errorf("namespace %q in cluster %s already exists: %w", in.Name, in.ClusterId, api.ErrConflict)
			case "23503":
				return api.Namespace{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.Namespace{}, fmt.Errorf("insert namespace: %w", err)
	}

	return api.Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		Owner:       in.Owner,
		Criticality: in.Criticality,
		Notes:       in.Notes,
		RunbookUrl:  in.RunbookUrl,
		Annotations: in.Annotations,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// GetNamespace fetches a namespace by id.
// Namespace read-side projection with cluster_name JOINed in (ADR-0027).
const namespaceSelectColumns = `n.id, n.cluster_id, n.name, n.display_name, n.phase, n.labels,
	n.owner, n.criticality, n.notes, n.runbook_url, n.annotations,
	n.created_at, n.updated_at,
	c.name AS cluster_name`

const namespaceFromJoined = `FROM namespaces n
	LEFT JOIN clusters c ON c.id = n.cluster_id`

// GetNamespace fetches a namespace by id, including the denormalized
// cluster_name from the namespace's cluster (ADR-0027).
func (p *PG) GetNamespace(ctx context.Context, id uuid.UUID) (api.Namespace, error) {
	q := `SELECT ` + namespaceSelectColumns + ` ` + namespaceFromJoined + ` WHERE n.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	n, err := scanNamespace(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Namespace{}, api.ErrNotFound
		}
		return api.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}
	return n, nil
}

// ListNamespaces returns up to limit namespaces sorted (created_at DESC, id DESC).
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListNamespaces(
	ctx context.Context,
	clusterID *uuid.UUID,
	limit int,
	cursor string,
	includeTerminated bool,
) ([]api.Namespace, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(namespaceSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(namespaceFromJoined)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 3)

	if !includeTerminated {
		conds = append(conds, "n.terminated_at IS NULL")
	}
	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("n.cluster_id = $%d", len(args)))
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
		conds = append(conds, fmt.Sprintf("(n.created_at, n.id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY n.created_at DESC, n.id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query namespaces: %w", err)
	}
	defer rows.Close()

	items := make([]api.Namespace, 0, limit)
	for rows.Next() {
		n, err := scanNamespace(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan namespace: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate namespaces: %w", err)
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

// UpdateNamespace applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateNamespace(ctx context.Context, id uuid.UUID, in api.NamespaceUpdate) (api.Namespace, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Namespace{}, err
		}
		appendSet("labels", b)
	}
	// Curated metadata — collector never writes these, so merge-patch
	// omission is enough to keep operator edits safe across polls.
	if in.Owner != nil {
		appendSet("owner", *in.Owner)
	}
	if in.Criticality != nil {
		appendSet("criticality", *in.Criticality)
	}
	if in.Notes != nil {
		appendSet("notes", *in.Notes)
	}
	if in.RunbookUrl != nil {
		appendSet("runbook_url", *in.RunbookUrl)
	}
	if in.Annotations != nil {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.Namespace{}, fmt.Errorf("marshal namespace annotations: %w", err)
		}
		appendSet("annotations", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	if err := p.withTx(ctx, "update namespace", func(tx pgx.Tx) error {
		prev, _ := namespaceRowMap(ctx, tx, id) // FOR UPDATE

		q := fmt.Sprintf("UPDATE namespaces SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("update namespace: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return api.ErrNotFound
		}

		if prev != nil {
			if next, err := namespaceRowMapNoLock(ctx, tx, id); err == nil {
				actor := timetravel.ActorFromContext(ctx)
				_ = timetravel.Capture(ctx, tx, timetravel.KindNamespace, id, prev, next, changeTypeUpdate, actor)
			}
		}
		return nil
	}); err != nil {
		return api.Namespace{}, err
	}
	return p.GetNamespace(ctx, id)
}

// DeleteNamespace removes a namespace by id.
func (p *PG) DeleteNamespace(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM namespaces WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// SoftDeleteNamespace soft-deletes the namespace and its workloads, and
// hard-deletes its unhistoried children (pods, services, ingresses, PVCs).
// PVs are cluster-scoped, not namespace-scoped, so they are unaffected.
//
//nolint:gocyclo // history capture adds branches; acceptable here
func (p *PG) SoftDeleteNamespace(ctx context.Context, id uuid.UUID) error {
	return p.withTx(ctx, "soft-delete namespace", func(tx pgx.Tx) error {
		actor := timetravel.ActorFromContext(ctx)

		wlIDs, err := liveWorkloadIDsForNamespace(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("list workloads for soft-delete namespace: %w", err)
		}

		// Hard-delete the unhistoried namespace-scoped children. FK ON DELETE
		// CASCADE does not fire under soft-delete; do it manually.
		if _, err := tx.Exec(ctx,
			`DELETE FROM pods WHERE namespace_id = $1`, id); err != nil {
			return fmt.Errorf("cascade-delete pods: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM services WHERE namespace_id = $1`, id); err != nil {
			return fmt.Errorf("cascade-delete services: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM ingresses WHERE namespace_id = $1`, id); err != nil {
			return fmt.Errorf("cascade-delete ingresses: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM persistent_volume_claims WHERE namespace_id = $1`, id); err != nil {
			return fmt.Errorf("cascade-delete persistent_volume_claims: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE workloads SET terminated_at = NOW(), updated_at = NOW()
			   WHERE namespace_id = $1 AND terminated_at IS NULL`, id); err != nil {
			return fmt.Errorf("soft-delete workloads: %w", err)
		}
		tag, err := tx.Exec(ctx,
			`UPDATE namespaces SET terminated_at = NOW(), updated_at = NOW()
			   WHERE id = $1 AND terminated_at IS NULL`, id)
		if err != nil {
			return fmt.Errorf("soft-delete namespace: %w", err)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM namespaces WHERE id = $1)`, id).Scan(&exists); err != nil {
				return fmt.Errorf("check namespace exists: %w", err)
			}
			if !exists {
				return api.ErrNotFound
			}
		}

		for _, wlID := range wlIDs {
			if snap, err := workloadRowMapNoLock(ctx, tx, wlID); err == nil {
				_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, wlID, nil, snap, changeTypeSoftDelete, actor)
			}
		}
		if snap, err := namespaceRowMapNoLock(ctx, tx, id); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindNamespace, id, nil, snap, changeTypeSoftDelete, actor)
		}

		return nil
	})
}

func liveWorkloadIDsForNamespace(ctx context.Context, tx pgx.Tx, namespaceID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM workloads WHERE namespace_id = $1 AND terminated_at IS NULL`,
		namespaceID)
	if err != nil {
		return nil, fmt.Errorf("query live workloads for namespace: %w", err)
	}
	return scanUUIDs(rows)
}

// UpsertNamespace inserts-or-updates a namespace keyed by (cluster_id, name).
//
//nolint:gocritic,gocyclo // hugeParam: Store interface requires value param; history capture adds branches
func (p *PG) UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Namespace{}, api.OutcomeNoChange, err
	}

	var (
		n                         api.Namespace
		inserted, businessChanged bool
	)
	if err := p.withTx(ctx, "upsert namespace", func(tx pgx.Tx) error {
		// Snapshot the row before the upsert to detect create vs update vs restore.
		var prevTerminatedAt *time.Time
		var prevID *uuid.UUID
		_ = tx.QueryRow(ctx,
			`SELECT id, terminated_at FROM namespaces WHERE cluster_id=$1 AND name=$2`,
			in.ClusterId, in.Name,
		).Scan(&prevID, &prevTerminatedAt)
		isCreate := prevID == nil
		isRestore := prevID != nil && prevTerminatedAt != nil

		// AUDIT_BUSINESS_FIELDS: display_name, phase, labels, terminated_at
		// (restore flips it). updated_at is a clock field — excluded.
		// Curator fields (owner/criticality/notes/runbook_url/annotations) are
		// not touched by the collector upsert and excluded from the OR-chain.
		const q = `
		WITH old AS (
		  SELECT display_name, phase, labels, terminated_at
		    FROM namespaces WHERE cluster_id=$2 AND name=$3
		),
		upserted AS (
		  INSERT INTO namespaces (
		    id, cluster_id, name, display_name, phase,
		    labels, created_at, updated_at
		  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		  ON CONFLICT (cluster_id, name) DO UPDATE SET
		      display_name  = EXCLUDED.display_name,
		      phase         = EXCLUDED.phase,
		      labels        = EXCLUDED.labels,
		      terminated_at = NULL,
		      updated_at    = EXCLUDED.updated_at
		  RETURNING id, cluster_id, name, display_name, phase, labels,
		            owner, criticality, notes, runbook_url, annotations,
		            created_at, updated_at, terminated_at, xmax
		)
		SELECT u.id, u.cluster_id, u.name, u.display_name, u.phase, u.labels,
		       u.owner, u.criticality, u.notes, u.runbook_url, u.annotations,
		       u.created_at, u.updated_at,
		       c.name AS cluster_name,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.display_name  IS DISTINCT FROM u.display_name  OR
		           o.phase         IS DISTINCT FROM u.phase         OR
		           o.labels        IS DISTINCT FROM u.labels        OR
		           o.terminated_at IS DISTINCT FROM u.terminated_at
		       )) AS business_changed
		  FROM upserted u
		  LEFT JOIN old o      ON true
		  LEFT JOIN clusters c ON c.id = u.cluster_id
	`
		row := tx.QueryRow(ctx, q,
			id, in.ClusterId, in.Name, in.DisplayName, in.Phase,
			labelsJSON, now,
		)
		var scanErr error
		n, scanErr = scanNamespace(scanRowWith{row: row, extra: []any{&inserted, &businessChanged}})
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == "23503" {
				return fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
			return fmt.Errorf("upsert namespace: %w", scanErr)
		}

		actualID := *n.Id
		if snap, err := namespaceRowMapNoLock(ctx, tx, actualID); err == nil {
			actor := timetravel.ActorFromContext(ctx)
			changeType := changeTypeUpdate
			if isCreate {
				changeType = changeTypeCreate
			} else if isRestore {
				changeType = changeTypeRestore
			}
			_ = timetravel.Capture(ctx, tx, timetravel.KindNamespace, actualID, nil, snap, changeType, actor)
		}
		return nil
	}); err != nil {
		return api.Namespace{}, api.OutcomeNoChange, err
	}
	return n, classifyOutcome(inserted, businessChanged), nil
}

func scanNamespace(row pgx.Row) (api.Namespace, error) {
	var (
		n               api.Namespace
		id              uuid.UUID
		clusterID       uuid.UUID
		createdAt       time.Time
		updatedAt       time.Time
		displayName     sql.NullString
		phase           sql.NullString
		labelsJSON      []byte
		owner           sql.NullString
		criticality     sql.NullString
		notes           sql.NullString
		runbookURL      sql.NullString
		annotationsJSON []byte
		clusterName     sql.NullString
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name,
		&displayName, &phase, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON,
		&createdAt, &updatedAt,
		&clusterName,
	); err != nil {
		return api.Namespace{}, fmt.Errorf("scan namespace: %w", err)
	}
	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.DisplayName = nullableString(displayName)
	n.Phase = nullableString(phase)
	n.Owner = nullableString(owner)
	n.Criticality = nullableString(criticality)
	n.Notes = nullableString(notes)
	n.RunbookUrl = nullableString(runbookURL)
	n.ClusterName = nullableString(clusterName)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Namespace{}, fmt.Errorf("unmarshal namespace labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}
	if len(annotationsJSON) > 0 {
		var annotations map[string]string
		if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
			return api.Namespace{}, fmt.Errorf("unmarshal namespace annotations: %w", err)
		}
		if len(annotations) > 0 {
			n.Annotations = &annotations
		}
	}
	return n, nil
}
