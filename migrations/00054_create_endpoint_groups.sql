-- +goose Up
-- +goose StatementBegin
CREATE TABLE endpoint_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    notes       TEXT,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE endpoint_group_cidrs (
    endpoint_group_id UUID NOT NULL REFERENCES endpoint_groups(id) ON DELETE CASCADE,
    cidr              CIDR NOT NULL,
    PRIMARY KEY (endpoint_group_id, cidr)
);
CREATE INDEX endpoint_group_cidrs_cidr_idx ON endpoint_group_cidrs USING gist (cidr inet_ops);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS endpoint_group_cidrs;
DROP TABLE IF EXISTS endpoint_groups;
-- +goose StatementEnd
