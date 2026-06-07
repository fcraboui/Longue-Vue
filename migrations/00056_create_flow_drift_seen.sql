-- +goose Up
-- +goose StatementBegin

-- flow_drift_seen throttles flow_drift audit_events to at most one per
-- (cluster, flow_key) per 24h. Derived bookkeeping only — safe to TRUNCATE;
-- it self-heals on the next synthesis.
CREATE TABLE flow_drift_seen (
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    flow_key     TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (cluster_id, flow_key)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS flow_drift_seen;
-- +goose StatementEnd
