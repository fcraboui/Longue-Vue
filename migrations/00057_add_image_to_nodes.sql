-- +goose Up
-- The OS image (OMI) name does not come back via the Kubernetes API
-- (status.nodeInfo.osImage is only a generic OS string), and the VMs that
-- back cluster nodes are deliberately excluded from virtual_machines
-- (ADR-0015). The VM collector resolves each node-VM's image name from the
-- cloud provider and backfills it here, matched by provider_id (ADR-0040).
-- NULL until the collector first reports it.
ALTER TABLE nodes
    ADD COLUMN image_id   TEXT,
    ADD COLUMN image_name TEXT;

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS image_name,
    DROP COLUMN IF EXISTS image_id;
