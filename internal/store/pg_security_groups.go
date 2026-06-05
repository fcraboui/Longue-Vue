package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// SecurityGroup is the in-store shape of a cloud provider security group.
type SecurityGroup struct {
	ID             uuid.UUID
	CloudAccountID uuid.UUID
	ProviderSGID   string
	Name           string
	VPCID          string
	Tags           json.RawMessage
}

const sgSelect = `id, cloud_account_id, provider_sg_id, name, COALESCE(vpc_id,''), tags`

// UpsertSecurityGroup inserts or updates by (cloud_account_id, provider_sg_id).
// Returns the stable row ID. Collector callers use this on every tick.
func (p *PG) UpsertSecurityGroup(ctx context.Context, sg SecurityGroup) (uuid.UUID, error) {
	const q = `
		INSERT INTO security_groups
			(cloud_account_id, provider_sg_id, name, vpc_id, tags, reconcile_seen_at)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, NOW())
		ON CONFLICT (cloud_account_id, provider_sg_id) DO UPDATE SET
			name              = EXCLUDED.name,
			vpc_id            = EXCLUDED.vpc_id,
			tags              = EXCLUDED.tags,
			reconcile_seen_at = NOW()
		RETURNING id`
	var id uuid.UUID
	if err := p.pool.QueryRow(ctx, q,
		sg.CloudAccountID, sg.ProviderSGID, sg.Name, sg.VPCID, sg.Tags,
	).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("upsert security_group: %w", err)
	}
	return id, nil
}

// GetSecurityGroup returns ErrNotFound on miss.
func (p *PG) GetSecurityGroup(ctx context.Context, id uuid.UUID) (SecurityGroup, error) {
	const q = `SELECT ` + sgSelect + ` FROM security_groups WHERE id = $1`
	var sg SecurityGroup
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&sg.ID, &sg.CloudAccountID, &sg.ProviderSGID, &sg.Name, &sg.VPCID, &sg.Tags,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SecurityGroup{}, api.ErrNotFound
	}
	if err != nil {
		return SecurityGroup{}, fmt.Errorf("get security_group: %w", err)
	}
	return sg, nil
}
