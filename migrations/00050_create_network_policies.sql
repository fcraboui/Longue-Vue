-- +goose Up
-- +goose StatementBegin

-- network_policies: one row per collected K8s NetworkPolicy object.
-- Carries the pod_selector + policy_types so the engine (P2) can decide
-- which workloads each policy selects, plus spec_raw for forensic access.
CREATE TABLE network_policies (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL REFERENCES clusters(id)   ON DELETE CASCADE,
    namespace_id        UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    pod_selector        JSONB NOT NULL,
    policy_types        TEXT[] NOT NULL,
    spec_raw            JSONB NOT NULL,
    reconcile_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (cluster_id, namespace_id, name)
);

CREATE INDEX network_policies_namespace_idx ON network_policies(namespace_id);
CREATE INDEX network_policies_cluster_idx   ON network_policies(cluster_id);

-- network_policy_rules: one row per (rule, peer) pair. Ports stay as JSONB
-- on the row to keep row count bounded.
CREATE TABLE network_policy_rules (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network_policy_id       UUID NOT NULL REFERENCES network_policies(id) ON DELETE CASCADE,
    direction               TEXT NOT NULL CHECK (direction IN ('ingress','egress')),
    peer_kind               TEXT NOT NULL CHECK (peer_kind IN ('selector','ip_block')),
    peer_pod_selector       JSONB,
    peer_namespace_selector JSONB,
    peer_ip_block_cidr      CIDR,
    peer_ip_block_except    JSONB,
    ports                   JSONB NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX npol_rules_pol_idx  ON network_policy_rules(network_policy_id);
CREATE INDEX npol_rules_cidr_idx ON network_policy_rules USING gist (peer_ip_block_cidr inet_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS network_policy_rules;
DROP TABLE IF EXISTS network_policies;
-- +goose StatementEnd
