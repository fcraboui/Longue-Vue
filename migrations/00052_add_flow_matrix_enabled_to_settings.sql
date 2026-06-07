-- +goose Up
-- +goose StatementBegin
ALTER TABLE settings
  ADD COLUMN IF NOT EXISTS flow_matrix_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings DROP COLUMN IF EXISTS flow_matrix_enabled;
-- +goose StatementEnd
