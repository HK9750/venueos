-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE refunds (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    order_id uuid NOT NULL,
    payment_attempt_id uuid NOT NULL,
    amount_minor bigint NOT NULL,
    currency text NOT NULL,
    state text NOT NULL DEFAULT 'requested',
    idempotency_key text NOT NULL,
    reason text NOT NULL,
    actor_type text NOT NULL,
    actor_id text,
    provider_refund_id text,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_refunds_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_refunds_order FOREIGN KEY (organization_id, order_id)
        REFERENCES orders (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_refunds_payment FOREIGN KEY (organization_id, payment_attempt_id)
        REFERENCES payment_attempts (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_refunds_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_refunds_order_idempotency UNIQUE (organization_id, order_id, idempotency_key),
    CONSTRAINT ck_refunds_amount CHECK (amount_minor > 0),
    CONSTRAINT ck_refunds_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_refunds_state CHECK (state IN ('requested', 'processing', 'succeeded', 'failed', 'cancelled', 'reconciliation_required')),
    CONSTRAINT ck_refunds_idempotency CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{16,128}$'),
    CONSTRAINT ck_refunds_reason CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1000),
    CONSTRAINT ck_refunds_actor CHECK (actor_type ~ '^[A-Za-z0-9._:-]{1,64}$' AND (actor_id IS NULL OR char_length(actor_id) BETWEEN 1 AND 255)),
    CONSTRAINT ck_refunds_provider_id CHECK (provider_refund_id IS NULL OR provider_refund_id ~ '^[A-Za-z0-9._:-]{1,255}$'),
    CONSTRAINT ck_refunds_version CHECK (version > 0),
    CONSTRAINT ck_refunds_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_refunds_provider_ref
    ON refunds (organization_id, provider_refund_id)
    WHERE provider_refund_id IS NOT NULL;
CREATE INDEX ix_refunds_order_state
    ON refunds (organization_id, order_id, state, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE refunds;
-- +goose StatementEnd
