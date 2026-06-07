package store

import (
	"context"
	"errors"
	"fmt"

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
