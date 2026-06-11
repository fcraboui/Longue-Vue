// PersistentVolume CRUD + upsert + reconcile. Split out of pg.go.
// accessModesValue/Pointer also serve pg_persistent_volume_claims.go.
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

// accessModesValue normalises a nullable *[]string access-modes slice into a
// never-nil []string that pgx will send as a TEXT[] (empty slice => empty
// array, not NULL). Matches the store's DEFAULT '{}' column definition.
func accessModesValue(modes *[]string) []string {
	if modes == nil {
		return []string{}
	}
	return *modes
}

func accessModesPointer(modes []string) *[]string {
	if len(modes) == 0 {
		return nil
	}
	return &modes
}

// CreatePersistentVolume inserts a cluster-scoped PV.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreatePersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolume{}, err
	}

	const q = `
		INSERT INTO persistent_volumes (
			id, cluster_id, name, capacity, access_modes,
			reclaim_policy, phase, storage_class_name,
			csi_driver, volume_handle,
			claim_ref_namespace, claim_ref_name,
			labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name,
		in.Capacity, accessModesValue(in.AccessModes),
		in.ReclaimPolicy, in.Phase, in.StorageClassName,
		in.CsiDriver, in.VolumeHandle,
		in.ClaimRefNamespace, in.ClaimRefName,
		labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.PersistentVolume{}, fmt.Errorf(
					"persistent volume %q in cluster %s already exists: %w",
					in.Name, in.ClusterId, api.ErrConflict,
				)
			case "23503":
				return api.PersistentVolume{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.PersistentVolume{}, fmt.Errorf("insert persistent volume: %w", err)
	}

	return api.PersistentVolume{
		Id:                &id,
		ClusterId:         in.ClusterId,
		Name:              in.Name,
		Capacity:          in.Capacity,
		AccessModes:       in.AccessModes,
		ReclaimPolicy:     in.ReclaimPolicy,
		Phase:             in.Phase,
		StorageClassName:  in.StorageClassName,
		CsiDriver:         in.CsiDriver,
		VolumeHandle:      in.VolumeHandle,
		ClaimRefNamespace: in.ClaimRefNamespace,
		ClaimRefName:      in.ClaimRefName,
		Labels:            in.Labels,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}, nil
}

// GetPersistentVolume fetches a PV by id.
func (p *PG) GetPersistentVolume(ctx context.Context, id uuid.UUID) (api.PersistentVolume, error) {
	const q = `
		SELECT id, cluster_id, name, capacity, access_modes,
		       reclaim_policy, phase, storage_class_name,
		       csi_driver, volume_handle,
		       claim_ref_namespace, claim_ref_name,
		       labels, created_at, updated_at
		FROM persistent_volumes
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	pv, err := scanPersistentVolume(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PersistentVolume{}, api.ErrNotFound
		}
		return api.PersistentVolume{}, fmt.Errorf("select persistent volume: %w", err)
	}
	return pv, nil
}

// ListPersistentVolumes returns up to limit PVs, optionally filtered by cluster.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPersistentVolumes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolume, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, cluster_id, name, capacity, access_modes,
	                       reclaim_policy, phase, storage_class_name,
	                       csi_driver, volume_handle,
	                       claim_ref_namespace, claim_ref_name,
	                       labels, created_at, updated_at
	                FROM persistent_volumes`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
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
		conds = append(conds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query persistent volumes: %w", err)
	}
	defer rows.Close()

	items := make([]api.PersistentVolume, 0, limit)
	for rows.Next() {
		pv, err := scanPersistentVolume(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan persistent volume: %w", err)
		}
		items = append(items, pv)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate persistent volumes: %w", err)
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

// UpdatePersistentVolume applies merge-patch on mutable PV fields.
//
//nolint:gocyclo,gocritic // merge-patch nil checks are inherently repetitive; hugeParam: Store interface requires value param
func (p *PG) UpdatePersistentVolume(ctx context.Context, id uuid.UUID, in api.PersistentVolumeUpdate) (api.PersistentVolume, error) {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 10)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Capacity != nil {
		appendSet("capacity", *in.Capacity)
	}
	if in.AccessModes != nil {
		appendSet("access_modes", accessModesValue(in.AccessModes))
	}
	if in.ReclaimPolicy != nil {
		appendSet("reclaim_policy", *in.ReclaimPolicy)
	}
	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.StorageClassName != nil {
		appendSet("storage_class_name", *in.StorageClassName)
	}
	if in.CsiDriver != nil {
		appendSet("csi_driver", *in.CsiDriver)
	}
	if in.VolumeHandle != nil {
		appendSet("volume_handle", *in.VolumeHandle)
	}
	if in.ClaimRefNamespace != nil {
		appendSet("claim_ref_namespace", *in.ClaimRefNamespace)
	}
	if in.ClaimRefName != nil {
		appendSet("claim_ref_name", *in.ClaimRefName)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.PersistentVolume{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE persistent_volumes SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.PersistentVolume{}, fmt.Errorf("update persistent volume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.PersistentVolume{}, api.ErrNotFound
	}
	return p.GetPersistentVolume(ctx, id)
}

// DeletePersistentVolume removes a PV by id.
func (p *PG) DeletePersistentVolume(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM persistent_volumes WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete persistent volume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertPersistentVolume inserts-or-updates a PV keyed by (cluster_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolume{}, api.OutcomeNoChange, err
	}

	// AUDIT_BUSINESS_FIELDS: capacity, access_modes, reclaim_policy, phase,
	// storage_class_name, csi_driver, volume_handle, claim_ref_namespace,
	// claim_ref_name, labels. updated_at is a clock field — excluded.
	const q = `
		WITH old AS (
		  SELECT capacity, access_modes, reclaim_policy, phase,
		         storage_class_name, csi_driver, volume_handle,
		         claim_ref_namespace, claim_ref_name, labels
		    FROM persistent_volumes WHERE cluster_id=$2 AND name=$3
		),
		upserted AS (
		  INSERT INTO persistent_volumes (
		    id, cluster_id, name, capacity, access_modes,
		    reclaim_policy, phase, storage_class_name,
		    csi_driver, volume_handle,
		    claim_ref_namespace, claim_ref_name,
		    labels, created_at, updated_at
		  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		  ON CONFLICT (cluster_id, name) DO UPDATE SET
		      capacity            = EXCLUDED.capacity,
		      access_modes        = EXCLUDED.access_modes,
		      reclaim_policy      = EXCLUDED.reclaim_policy,
		      phase               = EXCLUDED.phase,
		      storage_class_name  = EXCLUDED.storage_class_name,
		      csi_driver          = EXCLUDED.csi_driver,
		      volume_handle       = EXCLUDED.volume_handle,
		      claim_ref_namespace = EXCLUDED.claim_ref_namespace,
		      claim_ref_name      = EXCLUDED.claim_ref_name,
		      labels              = EXCLUDED.labels,
		      updated_at          = EXCLUDED.updated_at
		  RETURNING id, cluster_id, name, capacity, access_modes,
		            reclaim_policy, phase, storage_class_name,
		            csi_driver, volume_handle,
		            claim_ref_namespace, claim_ref_name,
		            labels, created_at, updated_at, xmax
		)
		SELECT u.id, u.cluster_id, u.name, u.capacity, u.access_modes,
		       u.reclaim_policy, u.phase, u.storage_class_name,
		       u.csi_driver, u.volume_handle,
		       u.claim_ref_namespace, u.claim_ref_name,
		       u.labels, u.created_at, u.updated_at,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.capacity            IS DISTINCT FROM u.capacity            OR
		           o.access_modes        IS DISTINCT FROM u.access_modes        OR
		           o.reclaim_policy      IS DISTINCT FROM u.reclaim_policy      OR
		           o.phase               IS DISTINCT FROM u.phase               OR
		           o.storage_class_name  IS DISTINCT FROM u.storage_class_name  OR
		           o.csi_driver          IS DISTINCT FROM u.csi_driver          OR
		           o.volume_handle       IS DISTINCT FROM u.volume_handle       OR
		           o.claim_ref_namespace IS DISTINCT FROM u.claim_ref_namespace OR
		           o.claim_ref_name      IS DISTINCT FROM u.claim_ref_name      OR
		           o.labels              IS DISTINCT FROM u.labels
		       )) AS business_changed
		  FROM upserted u LEFT JOIN old o ON true
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.ClusterId, in.Name,
		in.Capacity, accessModesValue(in.AccessModes),
		in.ReclaimPolicy, in.Phase, in.StorageClassName,
		in.CsiDriver, in.VolumeHandle,
		in.ClaimRefNamespace, in.ClaimRefName,
		labelsJSON, now,
	)
	var inserted, businessChanged bool
	pv, err := scanPersistentVolume(scanRowWith{row: row, extra: []any{&inserted, &businessChanged}})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.PersistentVolume{}, api.OutcomeNoChange, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
		}
		return api.PersistentVolume{}, api.OutcomeNoChange, fmt.Errorf("upsert persistent volume: %w", err)
	}
	return pv, classifyOutcome(inserted, businessChanged), nil
}

// DeletePersistentVolumesNotIn removes cluster-scoped PVs whose name is not in keepNames.
func (p *PG) DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM persistent_volumes
		 WHERE cluster_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		clusterID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete persistent volumes not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanPersistentVolume(row pgx.Row) (api.PersistentVolume, error) {
	var (
		pv                api.PersistentVolume
		id                uuid.UUID
		clusterID         uuid.UUID
		createdAt         time.Time
		updatedAt         time.Time
		capacity          sql.NullString
		accessModes       []string
		reclaimPolicy     sql.NullString
		phase             sql.NullString
		storageClassName  sql.NullString
		csiDriver         sql.NullString
		volumeHandle      sql.NullString
		claimRefNamespace sql.NullString
		claimRefName      sql.NullString
		labelsJSON        []byte
	)
	if err := row.Scan(
		&id, &clusterID, &pv.Name,
		&capacity, &accessModes,
		&reclaimPolicy, &phase, &storageClassName,
		&csiDriver, &volumeHandle,
		&claimRefNamespace, &claimRefName,
		&labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.PersistentVolume{}, fmt.Errorf("scan persistent volume: %w", err)
	}
	pv.Id = &id
	pv.ClusterId = clusterID
	pv.CreatedAt = &createdAt
	pv.UpdatedAt = &updatedAt
	pv.Capacity = nullableString(capacity)
	pv.AccessModes = accessModesPointer(accessModes)
	pv.ReclaimPolicy = nullableString(reclaimPolicy)
	pv.Phase = nullableString(phase)
	pv.StorageClassName = nullableString(storageClassName)
	pv.CsiDriver = nullableString(csiDriver)
	pv.VolumeHandle = nullableString(volumeHandle)
	pv.ClaimRefNamespace = nullableString(claimRefNamespace)
	pv.ClaimRefName = nullableString(claimRefName)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.PersistentVolume{}, fmt.Errorf("unmarshal pv labels: %w", err)
		}
		if len(labels) > 0 {
			pv.Labels = &labels
		}
	}
	return pv, nil
}
