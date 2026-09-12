-- +goose Up
CREATE TABLE IF NOT EXISTS links (
    code text PRIMARY KEY,
    url text NOT NULL,
    created_at timestamptz DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS links;