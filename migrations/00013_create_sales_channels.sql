-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE sales_channels (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    channel_key text NOT NULL,
    display_name text NOT NULL,
    channel_type text NOT NULL,
    configuration jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'active',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_sales_channels_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_sales_channels_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_sales_channels_organization_key UNIQUE (organization_id, channel_key),
    CONSTRAINT ck_sales_channels_key CHECK (channel_key ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(channel_key) BETWEEN 2 AND 63),
    CONSTRAINT ck_sales_channels_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_sales_channels_type CHECK (channel_type IN ('public', 'box_office', 'partner', 'private_link')),
    CONSTRAINT ck_sales_channels_configuration CHECK (jsonb_typeof(configuration) = 'object'),
    CONSTRAINT ck_sales_channels_status CHECK (status IN ('active', 'inactive', 'archived')),
    CONSTRAINT ck_sales_channels_version CHECK (version > 0),
    CONSTRAINT ck_sales_channels_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_sales_channels_organization_status_created_id
    ON sales_channels (organization_id, status, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE sales_channels;
-- +goose StatementEnd
