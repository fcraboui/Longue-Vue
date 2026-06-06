package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// UpsertVMSecurityGroupAttachment inserts or refreshes one (account, vm, sg)
// attachment, stamping reconcile_seen_at.
func (p *PG) UpsertVMSecurityGroupAttachment(ctx context.Context, a api.VMSecurityGroupAttachment) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO vm_security_group_attachments
		   (cloud_account_id, provider_vm_id, provider_sg_id, reconcile_seen_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (cloud_account_id, provider_vm_id, provider_sg_id)
		 DO UPDATE SET reconcile_seen_at = NOW()`,
		a.CloudAccountID, a.ProviderVMID, a.ProviderSGID)
	if err != nil {
		return fmt.Errorf("upsert vm sg attachment: %w", err)
	}
	return nil
}

// SweepVMSecurityGroupAttachments deletes attachments for the account that are
// not in `seen`. Caller MUST only invoke this after a successful provider tick
// (CLAUDE.md reconcile rule) so a transient list error never wipes the table.
func (p *PG) SweepVMSecurityGroupAttachments(ctx context.Context, accountID uuid.UUID, seen []api.VMSecurityGroupAttachment) error {
	if len(seen) == 0 {
		_, err := p.pool.Exec(ctx,
			`DELETE FROM vm_security_group_attachments WHERE cloud_account_id = $1`, accountID)
		if err != nil {
			return fmt.Errorf("sweep all vm sg attachments: %w", err)
		}
		return nil
	}
	vmIDs := make([]string, len(seen))
	sgIDs := make([]string, len(seen))
	for i, a := range seen {
		vmIDs[i] = a.ProviderVMID
		sgIDs[i] = a.ProviderSGID
	}
	_, err := p.pool.Exec(ctx,
		`DELETE FROM vm_security_group_attachments a
		 WHERE a.cloud_account_id = $1
		   AND NOT EXISTS (
		     SELECT 1 FROM unnest($2::text[], $3::text[]) AS s(vm, sg)
		     WHERE s.vm = a.provider_vm_id AND s.sg = a.provider_sg_id)`,
		accountID, vmIDs, sgIDs)
	if err != nil {
		return fmt.Errorf("sweep vm sg attachments: %w", err)
	}
	return nil
}

// PerimeterSecurityGroupsForCluster resolves the security groups that protect a
// cluster's nodes. It joins nodes.provider_id to attachment provider_vm_id via
// the same substring match the VM dedup trusts (ADR-0015): a node's providerID
// (e.g. "outscale:///az/i-abc") embeds the cloud VM id ("i-abc"). The provider
// VM id is escaped and the LIKE uses an explicit ESCAPE clause for safety.
//
// Projection mirrors GetSecurityGroup's sgSelect (no reconcile_seen_at;
// vpc_id COALESCEd to empty string) so the scan targets match SecurityGroupRow.
func (p *PG) PerimeterSecurityGroupsForCluster(ctx context.Context, clusterID uuid.UUID) ([]api.SecurityGroupRow, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT DISTINCT sg.id, sg.cloud_account_id, sg.provider_sg_id, sg.name,
		        COALESCE(sg.vpc_id, ''), sg.tags
		   FROM nodes n
		   JOIN vm_security_group_attachments a
		     ON n.provider_id LIKE '%' || replace(replace(replace(a.provider_vm_id,'\','\\'),'%','\%'),'_','\_') || '%' ESCAPE '\'
		   JOIN security_groups sg
		     ON sg.cloud_account_id = a.cloud_account_id
		    AND sg.provider_sg_id   = a.provider_sg_id
		  WHERE n.cluster_id = $1
		  ORDER BY sg.name`,
		clusterID)
	if err != nil {
		return nil, fmt.Errorf("query perimeter SGs: %w", err)
	}
	defer rows.Close()

	var out []api.SecurityGroupRow
	for rows.Next() {
		var sg api.SecurityGroupRow
		if err := rows.Scan(&sg.ID, &sg.CloudAccountID, &sg.ProviderSGID, &sg.Name,
			&sg.VPCID, &sg.Tags); err != nil {
			return nil, fmt.Errorf("scan perimeter SG: %w", err)
		}
		out = append(out, sg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate perimeter SGs: %w", err)
	}
	return out, nil
}
