-- +goose Up
-- +goose StatementBegin
-- Uniform list search & sort (spec 2026-07-10, ADR-0039).
--
-- (LOWER(name), id) composites on the three large tables back the new
-- sort-by-name keyset pagination AND left-anchored `name=du*` glob
-- filters. Smaller tables (clusters, namespaces, services, …) stay
-- top-N sorts — the seq-scan tradeoff is documented in
-- pg_virtual_machines.go and acceptable at the target scale.
--
-- security_groups / network_policies get the composite their EXISTING
-- (reconcile_seen_at DESC, id DESC) keyset order always needed —
-- 00050/00051 shipped without any index on the sort key.
CREATE INDEX pods_name_lower_id_idx
    ON pods (LOWER(name), id);
CREATE INDEX workloads_name_lower_id_idx
    ON workloads (LOWER(name), id);
CREATE INDEX virtual_machines_name_lower_id_idx
    ON virtual_machines (LOWER(name), id);
CREATE INDEX security_groups_seen_id_idx
    ON security_groups (reconcile_seen_at DESC, id DESC);
CREATE INDEX network_policies_seen_id_idx
    ON network_policies (reconcile_seen_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS network_policies_seen_id_idx;
DROP INDEX IF EXISTS security_groups_seen_id_idx;
DROP INDEX IF EXISTS virtual_machines_name_lower_id_idx;
DROP INDEX IF EXISTS workloads_name_lower_id_idx;
DROP INDEX IF EXISTS pods_name_lower_id_idx;
-- +goose StatementEnd
