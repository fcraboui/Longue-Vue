-- +goose Up
-- +goose StatementBegin
CREATE TABLE cluster_flow_references (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    layer         TEXT NOT NULL CHECK (layer IN ('perimeter','internal')),
    direction     TEXT NOT NULL CHECK (direction IN ('ingress','egress')),
    src_kind      TEXT NOT NULL CHECK (src_kind IN ('workload','service','namespace','endpoint_group')),
    src_ref       TEXT NOT NULL,
    dst_kind      TEXT NOT NULL CHECK (dst_kind IN ('workload','service','namespace','endpoint_group')),
    dst_ref       TEXT NOT NULL,
    protocol      TEXT NOT NULL CHECK (protocol IN ('tcp','udp','icmp','any')),
    from_port     INT,
    to_port       INT,
    justification TEXT NOT NULL,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX cluster_flow_refs_cluster_idx ON cluster_flow_references(cluster_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_flow_references;
-- +goose StatementEnd
