-- +goose Up
-- +goose StatementBegin
ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS policies_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS policy_prometheus_url  TEXT    NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings DROP COLUMN IF EXISTS policy_prometheus_url;
ALTER TABLE settings DROP COLUMN IF EXISTS policies_enabled;
-- +goose StatementEnd
