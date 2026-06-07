package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

func (p *PG) ListEndpointGroups(ctx context.Context) ([]api.EndpointGroup, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id, name, COALESCE(notes,''), created_by, created_at, updated_at
		   FROM endpoint_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list endpoint groups: %w", err)
	}
	defer rows.Close()
	var out []api.EndpointGroup
	for rows.Next() {
		var eg api.EndpointGroup
		if err := rows.Scan(&eg.ID, &eg.Name, &eg.Notes, &eg.CreatedBy, &eg.CreatedAt, &eg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan endpoint group: %w", err)
		}
		out = append(out, eg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoint groups: %w", err)
	}
	for i := range out {
		cidrs, err := p.endpointGroupCIDRs(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].CIDRs = cidrs
	}
	return out, nil
}

func (p *PG) endpointGroupCIDRs(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT cidr::text FROM endpoint_group_cidrs WHERE endpoint_group_id=$1 ORDER BY cidr`, id)
	if err != nil {
		return nil, fmt.Errorf("query cidrs: %w", err)
	}
	defer rows.Close()
	var cidrs []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan cidr: %w", err)
		}
		cidrs = append(cidrs, c)
	}
	return cidrs, rows.Err()
}

func (p *PG) GetEndpointGroup(ctx context.Context, id uuid.UUID) (api.EndpointGroup, error) {
	var eg api.EndpointGroup
	err := p.pool.QueryRow(ctx,
		`SELECT id, name, COALESCE(notes,''), created_by, created_at, updated_at
		   FROM endpoint_groups WHERE id=$1`, id).
		Scan(&eg.ID, &eg.Name, &eg.Notes, &eg.CreatedBy, &eg.CreatedAt, &eg.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EndpointGroup{}, api.ErrNotFound
	}
	if err != nil {
		return api.EndpointGroup{}, fmt.Errorf("get endpoint group: %w", err)
	}
	cidrs, err := p.endpointGroupCIDRs(ctx, id)
	if err != nil {
		return api.EndpointGroup{}, err
	}
	eg.CIDRs = cidrs
	return eg, nil
}

func (p *PG) CreateEndpointGroup(ctx context.Context, in api.EndpointGroupInput, createdBy *uuid.UUID) (api.EndpointGroup, error) {
	id := uuid.New()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.EndpointGroup{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	if _, err := tx.Exec(ctx,
		`INSERT INTO endpoint_groups (id, name, notes, created_by) VALUES ($1,$2,$3,$4)`,
		id, in.Name, in.Notes, createdBy); err != nil {
		return api.EndpointGroup{}, fmt.Errorf("insert endpoint group: %w", err)
	}
	for _, c := range in.CIDRs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO endpoint_group_cidrs (endpoint_group_id, cidr) VALUES ($1,$2)`, id, c); err != nil {
			return api.EndpointGroup{}, fmt.Errorf("insert cidr %q: %w", c, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EndpointGroup{}, fmt.Errorf("commit: %w", err)
	}
	return p.GetEndpointGroup(ctx, id)
}

func (p *PG) UpdateEndpointGroup(ctx context.Context, id uuid.UUID, in api.EndpointGroupInput) (api.EndpointGroup, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.EndpointGroup{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	ct, err := tx.Exec(ctx,
		`UPDATE endpoint_groups SET name=$2, notes=$3, updated_at=NOW() WHERE id=$1`, id, in.Name, in.Notes)
	if err != nil {
		return api.EndpointGroup{}, fmt.Errorf("update endpoint group: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.EndpointGroup{}, api.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM endpoint_group_cidrs WHERE endpoint_group_id=$1`, id); err != nil {
		return api.EndpointGroup{}, fmt.Errorf("clear cidrs: %w", err)
	}
	for _, c := range in.CIDRs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO endpoint_group_cidrs (endpoint_group_id, cidr) VALUES ($1,$2)`, id, c); err != nil {
			return api.EndpointGroup{}, fmt.Errorf("insert cidr %q: %w", c, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EndpointGroup{}, fmt.Errorf("commit: %w", err)
	}
	return p.GetEndpointGroup(ctx, id)
}

func (p *PG) DeleteEndpointGroup(ctx context.Context, id uuid.UUID) error {
	ct, err := p.pool.Exec(ctx, `DELETE FROM endpoint_groups WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete endpoint group: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}
