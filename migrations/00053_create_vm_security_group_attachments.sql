-- +goose Up
-- +goose StatementBegin

-- vm_security_group_attachments: SG attachments keyed by raw provider VM id,
-- independent of the virtual_machines table. Kubernetes node VMs are never
-- inventoried as virtual_machines (ADR-0015 dedup), so their SG links must
-- live here. Used at read time to resolve a cluster's perimeter SGs via
-- nodes.provider_id substring match.
CREATE TABLE vm_security_group_attachments (
    cloud_account_id  UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider_vm_id    TEXT NOT NULL,
    provider_sg_id    TEXT NOT NULL,
    reconcile_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (cloud_account_id, provider_vm_id, provider_sg_id)
);
CREATE INDEX vm_sg_attach_account_idx ON vm_security_group_attachments(cloud_account_id);
CREATE INDEX vm_sg_attach_pvid_idx    ON vm_security_group_attachments(provider_vm_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vm_security_group_attachments;
-- +goose StatementEnd
