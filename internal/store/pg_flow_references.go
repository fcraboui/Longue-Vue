package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

const flowRefCols = `id, cluster_id, layer, direction, src_kind, src_ref,
	dst_kind, dst_ref, protocol, from_port, to_port, justification,
	created_by, created_at, updated_at`

func scanFlowRef(row pgx.Row) (api.FlowReference, error) {
	var r api.FlowReference
	err := row.Scan(&r.ID, &r.ClusterID, &r.Layer, &r.Direction, &r.SrcKind, &r.SrcRef,
		&r.DstKind, &r.DstRef, &r.Protocol, &r.FromPort, &r.ToPort, &r.Justification,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, fmt.Errorf("scan flow reference: %w", err)
	}
	return r, nil
}

// ListFlowReferences returns every reference flow row for a cluster, ordered
// for stable display and export.
func (p *PG) ListFlowReferences(ctx context.Context, clusterID uuid.UUID) ([]api.FlowReference, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+flowRefCols+` FROM cluster_flow_references WHERE cluster_id=$1 ORDER BY layer, direction, src_ref, dst_ref`,
		clusterID)
	if err != nil {
		return nil, fmt.Errorf("list flow references: %w", err)
	}
	defer rows.Close()
	var out []api.FlowReference
	for rows.Next() {
		r, err := scanFlowRef(rows)
		if err != nil {
			return nil, fmt.Errorf("scan flow reference: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flow references: %w", err)
	}
	return out, nil
}

// CreateFlowReference inserts a new reference flow row for a cluster and
// returns the persisted record.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateFlowReference(
	ctx context.Context,
	clusterID uuid.UUID,
	in api.FlowReferenceInput,
	createdBy *uuid.UUID,
) (api.FlowReference, error) {
	id := uuid.New()
	row := p.pool.QueryRow(ctx,
		`INSERT INTO cluster_flow_references
		   (id, cluster_id, layer, direction, src_kind, src_ref, dst_kind, dst_ref,
		    protocol, from_port, to_port, justification, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 RETURNING `+flowRefCols,
		id, clusterID, in.Layer, in.Direction, in.SrcKind, in.SrcRef, in.DstKind, in.DstRef,
		in.Protocol, in.FromPort, in.ToPort, in.Justification, createdBy)
	r, err := scanFlowRef(row)
	if err != nil {
		return api.FlowReference{}, fmt.Errorf("insert flow reference: %w", err)
	}
	return r, nil
}

// UpdateFlowReference overwrites an existing reference flow row by id and
// returns the updated record, or api.ErrNotFound if no such row exists.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpdateFlowReference(ctx context.Context, id uuid.UUID, in api.FlowReferenceInput) (api.FlowReference, error) {
	row := p.pool.QueryRow(ctx,
		`UPDATE cluster_flow_references SET
		   layer=$2, direction=$3, src_kind=$4, src_ref=$5, dst_kind=$6, dst_ref=$7,
		   protocol=$8, from_port=$9, to_port=$10, justification=$11, updated_at=NOW()
		 WHERE id=$1 RETURNING `+flowRefCols,
		id, in.Layer, in.Direction, in.SrcKind, in.SrcRef, in.DstKind, in.DstRef,
		in.Protocol, in.FromPort, in.ToPort, in.Justification)
	r, err := scanFlowRef(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.FlowReference{}, api.ErrNotFound
	}
	if err != nil {
		return api.FlowReference{}, fmt.Errorf("update flow reference: %w", err)
	}
	return r, nil
}

// DeleteFlowReference removes a reference flow row by id, returning
// api.ErrNotFound if no row was deleted.
func (p *PG) DeleteFlowReference(ctx context.Context, id uuid.UUID) error {
	ct, err := p.pool.Exec(ctx, `DELETE FROM cluster_flow_references WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete flow reference: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// ReplaceFlowReferences performs a replace-all import: every existing reference
// row for the cluster is deleted and replaced by ins in a single transaction.
func (p *PG) ReplaceFlowReferences(
	ctx context.Context,
	clusterID uuid.UUID,
	ins []api.FlowReferenceInput,
	createdBy *uuid.UUID,
) ([]api.FlowReference, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `DELETE FROM cluster_flow_references WHERE cluster_id=$1`, clusterID); err != nil {
		return nil, fmt.Errorf("clear references: %w", err)
	}
	for i := range ins {
		in := &ins[i]
		if _, err := tx.Exec(ctx,
			`INSERT INTO cluster_flow_references
			   (id, cluster_id, layer, direction, src_kind, src_ref, dst_kind, dst_ref,
			    protocol, from_port, to_port, justification, created_by)
			 VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			clusterID, in.Layer, in.Direction, in.SrcKind, in.SrcRef, in.DstKind, in.DstRef,
			in.Protocol, in.FromPort, in.ToPort, in.Justification, createdBy); err != nil {
			return nil, fmt.Errorf("insert reference: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return p.ListFlowReferences(ctx, clusterID)
}

// RecordFlowDriftSeen upserts the (cluster, flow_key) marker and returns true
// when the prior last_seen_at was absent or older than `within`. The decision +
// stamp happen in one statement so concurrent callers emit at most one event
// per window. A fresh insert or a window-elapsed update returns a row (emit);
// a conflict whose WHERE filtered the update (seen within the window) returns
// no row → pgx.ErrNoRows → throttle.
func (p *PG) RecordFlowDriftSeen(ctx context.Context, clusterID uuid.UUID, flowKey string, within time.Duration) (bool, error) {
	var emit bool
	err := p.pool.QueryRow(ctx,
		`INSERT INTO flow_drift_seen (cluster_id, flow_key, last_seen_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (cluster_id, flow_key) DO UPDATE
		   SET last_seen_at = NOW()
		   WHERE flow_drift_seen.last_seen_at < NOW() - $3::interval
		 RETURNING true`,
		clusterID, flowKey, within.String()).Scan(&emit)
	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict whose WHERE filtered out the update (seen within window) →
		// no row returned → throttle, do not emit.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record flow drift seen: %w", err)
	}
	return emit, nil
}

// ListClustersWithFlowReferences returns clusters with >=1 reference row.
func (p *PG) ListClustersWithFlowReferences(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT DISTINCT cluster_id FROM cluster_flow_references ORDER BY cluster_id`)
	if err != nil {
		return nil, fmt.Errorf("list clusters with flow references: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan cluster id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cluster ids: %w", err)
	}
	return out, nil
}
