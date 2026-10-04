-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE carts
    ADD COLUMN quote_snapshot_raw bytea,
    ADD CONSTRAINT ck_carts_quote_snapshot_raw CHECK (quote_snapshot_raw IS NULL OR octet_length(quote_snapshot_raw) BETWEEN 2 AND 262144);

ALTER TABLE orders
    ADD COLUMN quote_snapshot_raw bytea,
    ADD CONSTRAINT ck_orders_quote_snapshot_raw CHECK (quote_snapshot_raw IS NULL OR octet_length(quote_snapshot_raw) BETWEEN 2 AND 262144);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE orders
    DROP CONSTRAINT ck_orders_quote_snapshot_raw,
    DROP COLUMN quote_snapshot_raw;
ALTER TABLE carts
    DROP CONSTRAINT ck_carts_quote_snapshot_raw,
    DROP COLUMN quote_snapshot_raw;
-- +goose StatementEnd
