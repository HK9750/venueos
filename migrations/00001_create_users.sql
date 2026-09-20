-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_not_blank CHECK (btrim(email) <> ''),
    CONSTRAINT users_name_not_blank CHECK (btrim(name) <> '')
);

CREATE UNIQUE INDEX users_email_lower_unique_idx ON users (lower(email));
CREATE INDEX users_created_at_id_idx ON users (created_at, id);

-- +goose Down
DROP TABLE users;

