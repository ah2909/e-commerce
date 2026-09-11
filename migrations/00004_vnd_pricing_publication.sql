-- +goose Up
CREATE TABLE pricing_price_lists (
    store_id uuid NOT NULL,
    price_list_id uuid NOT NULL,
    name text NOT NULL,
    channel text NOT NULL,
    currency varchar(3) NOT NULL,
    priority integer NOT NULL,
    valid_from timestamptz,
    valid_until timestamptz,
    status text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, price_list_id),
    CONSTRAINT pricing_price_lists_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT pricing_price_lists_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT pricing_price_lists_channel_valid CHECK (channel = 'storefront'),
    CONSTRAINT pricing_price_lists_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT pricing_price_lists_priority_valid CHECK (priority BETWEEN -1000000 AND 1000000),
    CONSTRAINT pricing_price_lists_window_valid CHECK (valid_from IS NULL OR valid_until IS NULL OR valid_from < valid_until),
    CONSTRAINT pricing_price_lists_status_valid CHECK (status IN ('active', 'archived')),
    CONSTRAINT pricing_price_lists_version_valid CHECK (version > 0),
    CONSTRAINT pricing_price_lists_lifecycle_valid CHECK ((status = 'active' AND archived_at IS NULL) OR (status = 'archived' AND archived_at IS NOT NULL)),
    CONSTRAINT pricing_price_lists_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX pricing_price_lists_current_idx ON pricing_price_lists (store_id, channel, currency, priority DESC, price_list_id) WHERE status = 'active';
CREATE INDEX pricing_price_lists_updated_idx ON pricing_price_lists (store_id, updated_at DESC, price_list_id DESC);

CREATE TABLE pricing_prices (
    store_id uuid NOT NULL,
    price_list_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    amount_minor bigint NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, price_list_id, variant_id),
    CONSTRAINT pricing_prices_list_fk FOREIGN KEY (store_id, price_list_id) REFERENCES pricing_price_lists (store_id, price_list_id),
    CONSTRAINT pricing_prices_variant_fk FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants (store_id, variant_id),
    CONSTRAINT pricing_prices_amount_valid CHECK (amount_minor >= 0),
    CONSTRAINT pricing_prices_version_valid CHECK (version > 0),
    CONSTRAINT pricing_prices_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX pricing_prices_variant_idx ON pricing_prices (store_id, variant_id, price_list_id);

CREATE TABLE outbox_events (
    store_id uuid NOT NULL,
    event_id uuid NOT NULL,
    event_type text NOT NULL,
    schema_version integer NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    aggregate_version bigint NOT NULL,
    occurred_at timestamptz NOT NULL,
    correlation_id text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    published_at timestamptz,
    last_failure_class text,
    PRIMARY KEY (store_id, event_id),
    CONSTRAINT outbox_events_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT outbox_events_type_valid CHECK (event_type = btrim(event_type) AND char_length(event_type) BETWEEN 1 AND 160),
    CONSTRAINT outbox_events_schema_valid CHECK (schema_version > 0 AND aggregate_version > 0),
    CONSTRAINT outbox_events_aggregate_valid CHECK (aggregate_type = btrim(aggregate_type) AND char_length(aggregate_type) BETWEEN 1 AND 80),
    CONSTRAINT outbox_events_correlation_valid CHECK (correlation_id = btrim(correlation_id) AND char_length(correlation_id) BETWEEN 1 AND 200),
    CONSTRAINT outbox_events_payload_valid CHECK (jsonb_typeof(payload) = 'object' AND pg_column_size(payload) <= 16384),
    CONSTRAINT outbox_events_attempts_valid CHECK (attempts >= 0),
    CONSTRAINT outbox_events_aggregate_version_unique UNIQUE (store_id, aggregate_type, aggregate_id, aggregate_version, event_type)
);

CREATE INDEX outbox_events_unpublished_idx ON outbox_events (next_attempt_at, occurred_at, store_id, event_id) WHERE published_at IS NULL;
