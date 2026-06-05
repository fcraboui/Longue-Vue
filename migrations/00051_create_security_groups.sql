-- +goose Up
-- +goose StatementBegin

-- security_groups: canonical, provider-neutral, deduped per cloud_account.
-- provider_sg_id is the cloud's own ID (e.g. "sg-abcd1234"); uniqueness
-- is keyed on (cloud_account_id, provider_sg_id) so multiple providers
-- can coexist.
CREATE TABLE security_groups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cloud_account_id    UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    provider_sg_id      TEXT NOT NULL,
    name                TEXT NOT NULL,
    vpc_id              TEXT,
    tags                JSONB NOT NULL DEFAULT '{}'::jsonb,
    reconcile_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (cloud_account_id, provider_sg_id)
);
CREATE INDEX security_groups_cloud_account_id_idx ON security_groups(cloud_account_id);

-- security_group_rules: canonical rule rows. peer_kind discriminates the
-- four shapes; only the matching column on each row is non-null.
CREATE TABLE security_group_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    security_group_id   UUID NOT NULL REFERENCES security_groups(id) ON DELETE CASCADE,
    direction           TEXT NOT NULL CHECK (direction IN ('ingress','egress')),
    protocol            TEXT NOT NULL,
    from_port           INT,
    to_port             INT,
    peer_kind           TEXT NOT NULL CHECK (peer_kind IN ('cidr','sg_ref','prefix_list_ref','self')),
    peer_cidr           CIDR,
    peer_sg_provider_id TEXT,
    peer_prefix_id      TEXT,
    description         TEXT
);
CREATE INDEX security_group_rules_security_group_id_idx ON security_group_rules(security_group_id);
CREATE INDEX security_group_rules_peer_cidr_idx ON security_group_rules USING gist (peer_cidr inet_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS security_group_rules;
DROP TABLE IF EXISTS security_groups;
-- +goose StatementEnd
