-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE orders (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cart_id uuid NOT NULL,
    session_id uuid NOT NULL,
    hold_id uuid NOT NULL,
    order_number text NOT NULL,
    owner_token_hash bytea NOT NULL,
    owner_user_id uuid,
    currency text NOT NULL,
    state text NOT NULL DEFAULT 'payment_pending',
    subtotal_minor bigint NOT NULL,
    discount_minor bigint NOT NULL DEFAULT 0,
    fees_minor bigint NOT NULL DEFAULT 0,
    taxes_minor bigint NOT NULL DEFAULT 0,
    total_minor bigint NOT NULL,
    quote_snapshot jsonb NOT NULL,
    quote_sha256 bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    confirmed_at timestamptz,
    CONSTRAINT fk_orders_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_cart FOREIGN KEY (organization_id, cart_id)
        REFERENCES carts (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_hold FOREIGN KEY (organization_id, hold_id)
        REFERENCES holds (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_orders_owner_user FOREIGN KEY (owner_user_id)
        REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_orders_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_orders_organization_number UNIQUE (organization_id, order_number),
    CONSTRAINT uq_orders_organization_cart UNIQUE (organization_id, cart_id),
    CONSTRAINT uq_orders_organization_hold UNIQUE (organization_id, hold_id),
    CONSTRAINT ck_orders_number CHECK (order_number ~ '^[A-Z0-9-]{8,64}$'),
    CONSTRAINT ck_orders_owner_token_hash CHECK (octet_length(owner_token_hash) = 32),
    CONSTRAINT ck_orders_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_orders_state CHECK (state IN ('draft', 'payment_pending', 'confirmed', 'payment_failed', 'cancelled', 'reconciliation_required', 'partially_refunded', 'refunded', 'fulfilment_issue')),
    CONSTRAINT ck_orders_amounts CHECK (subtotal_minor >= 0 AND discount_minor >= 0 AND fees_minor >= 0 AND taxes_minor >= 0 AND total_minor >= 0 AND discount_minor <= subtotal_minor),
    CONSTRAINT ck_orders_quote_snapshot CHECK (
        jsonb_typeof(quote_snapshot) = 'object'
        AND octet_length(quote_snapshot::text) BETWEEN 2 AND 262144
    ),
    CONSTRAINT ck_orders_quote_sha256 CHECK (octet_length(quote_sha256) = 32),
    CONSTRAINT ck_orders_version CHECK (version > 0),
    CONSTRAINT ck_orders_timestamps CHECK (updated_at >= created_at AND (confirmed_at IS NULL OR confirmed_at >= created_at))
);

CREATE INDEX ix_orders_owner
    ON orders (organization_id, owner_token_hash, created_at DESC, id DESC);
CREATE INDEX ix_orders_session_state_created
    ON orders (organization_id, session_id, state, created_at DESC, id DESC);

CREATE TABLE order_lines (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    order_id uuid NOT NULL,
    line_number integer NOT NULL,
    price_tier_id uuid NOT NULL,
    quantity bigint NOT NULL,
    unit_minor bigint NOT NULL,
    subtotal_minor bigint NOT NULL,
    snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_order_lines_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_order_lines_order FOREIGN KEY (organization_id, order_id)
        REFERENCES orders (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_order_lines_price_tier FOREIGN KEY (organization_id, price_tier_id)
        REFERENCES price_tiers (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_order_lines_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_order_lines_order_number UNIQUE (organization_id, order_id, line_number),
    CONSTRAINT ck_order_lines_number CHECK (line_number > 0 AND line_number <= 100),
    CONSTRAINT ck_order_lines_amounts CHECK (quantity > 0 AND quantity <= 1000 AND unit_minor >= 0 AND subtotal_minor >= 0),
    CONSTRAINT ck_order_lines_snapshot CHECK (
        jsonb_typeof(snapshot) = 'object'
        AND octet_length(snapshot::text) BETWEEN 2 AND 65536
    )
);

CREATE INDEX ix_order_lines_order
    ON order_lines (organization_id, order_id, line_number);

CREATE TABLE payment_attempts (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    order_id uuid NOT NULL,
    provider text NOT NULL,
    provider_object_id text,
    amount_minor bigint NOT NULL,
    currency text NOT NULL,
    state text NOT NULL DEFAULT 'created',
    idempotency_key text NOT NULL,
    failure_class text,
    failure_code text,
    failure_message text,
    client_action_reference text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_payment_attempts_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_payment_attempts_order FOREIGN KEY (organization_id, order_id)
        REFERENCES orders (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_payment_attempts_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_payment_attempts_order_idempotency UNIQUE (organization_id, order_id, idempotency_key),
    CONSTRAINT ck_payment_attempts_provider CHECK (provider ~ '^[a-z0-9][a-z0-9._-]{1,63}$'),
    CONSTRAINT ck_payment_attempts_provider_object CHECK (provider_object_id IS NULL OR provider_object_id ~ '^[A-Za-z0-9._:-]{1,255}$'),
    CONSTRAINT ck_payment_attempts_amount CHECK (amount_minor > 0),
    CONSTRAINT ck_payment_attempts_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_payment_attempts_state CHECK (state IN ('created', 'requires_action', 'processing', 'authorized', 'captured', 'failed', 'cancelled', 'unknown')),
    CONSTRAINT ck_payment_attempts_idempotency CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{16,128}$'),
    CONSTRAINT ck_payment_attempts_failure_class CHECK (failure_class IS NULL OR failure_class IN ('permanent', 'transient', 'unknown')),
    CONSTRAINT ck_payment_attempts_failure_fields CHECK ((failure_class IS NULL AND failure_code IS NULL AND failure_message IS NULL) OR (failure_class IS NOT NULL AND failure_code IS NOT NULL AND failure_message IS NOT NULL AND char_length(failure_code) BETWEEN 1 AND 64 AND char_length(failure_message) BETWEEN 1 AND 1000)),
    CONSTRAINT ck_payment_attempts_metadata CHECK (jsonb_typeof(metadata) = 'object' AND octet_length(metadata::text) BETWEEN 2 AND 65536),
    CONSTRAINT ck_payment_attempts_version CHECK (version > 0),
    CONSTRAINT ck_payment_attempts_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_payment_attempts_provider_object
    ON payment_attempts (organization_id, provider, provider_object_id)
    WHERE provider_object_id IS NOT NULL;
CREATE INDEX ix_payment_attempts_order_state
    ON payment_attempts (organization_id, order_id, state, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE payment_attempts;
DROP TABLE order_lines;
DROP TABLE orders;
-- +goose StatementEnd
