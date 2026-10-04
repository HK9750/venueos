-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE inbox_messages
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN lease_owner text,
    ADD COLUMN lease_expires_at timestamptz;

ALTER TABLE inbox_messages
    ADD CONSTRAINT ck_inbox_messages_lease CHECK (
        (
            state = 'processing'
            AND lease_owner IS NOT NULL
            AND char_length(lease_owner) BETWEEN 1 AND 128
            AND lease_expires_at IS NOT NULL
        )
        OR (
            state <> 'processing'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
        )
    );

CREATE INDEX ix_inbox_messages_claim
    ON inbox_messages (available_at, received_at, id)
    WHERE state IN ('received', 'failed')
       OR state = 'processing';

CREATE INDEX ix_inbox_messages_expired_lease
    ON inbox_messages (lease_expires_at, id)
    WHERE state = 'processing';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP INDEX ix_inbox_messages_expired_lease;
DROP INDEX ix_inbox_messages_claim;
ALTER TABLE inbox_messages
    DROP CONSTRAINT ck_inbox_messages_lease,
    DROP COLUMN lease_expires_at,
    DROP COLUMN lease_owner,
    DROP COLUMN available_at;
-- +goose StatementEnd
