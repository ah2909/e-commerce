-- +goose Up
ALTER TABLE outbox_events
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_until timestamptz,
    ADD COLUMN terminal_at timestamptz,
    ADD COLUMN published_topic text,
    ADD COLUMN published_partition integer,
    ADD COLUMN published_offset bigint,
    ADD COLUMN last_error_digest bytea,
    ADD COLUMN requeued_at timestamptz,
    ADD COLUMN requeue_reason text,
    ADD CONSTRAINT outbox_events_claim_valid CHECK ((claim_token IS NULL) = (claimed_until IS NULL)),
    ADD CONSTRAINT outbox_events_publication_valid CHECK ((published_at IS NULL AND published_topic IS NULL AND published_partition IS NULL AND published_offset IS NULL) OR (published_at IS NOT NULL AND published_topic IS NOT NULL AND published_partition >= 0 AND published_offset >= 0)),
    ADD CONSTRAINT outbox_events_terminal_valid CHECK (terminal_at IS NULL OR published_at IS NULL),
    ADD CONSTRAINT outbox_events_error_digest_valid CHECK (last_error_digest IS NULL OR octet_length(last_error_digest) = 32),
    ADD CONSTRAINT outbox_events_requeue_valid CHECK ((requeued_at IS NULL AND requeue_reason IS NULL) OR (requeued_at IS NOT NULL AND requeue_reason = btrim(requeue_reason) AND char_length(requeue_reason) BETWEEN 1 AND 240));

DROP INDEX outbox_events_unpublished_idx;
CREATE INDEX outbox_events_due_idx ON outbox_events (next_attempt_at, occurred_at, store_id, event_id) WHERE published_at IS NULL AND terminal_at IS NULL;
CREATE INDEX outbox_events_terminal_idx ON outbox_events (terminal_at, store_id, event_id) WHERE terminal_at IS NOT NULL;

CREATE TABLE consumer_inbox (
    store_id uuid NOT NULL,
    consumer_name text NOT NULL,
    event_id uuid NOT NULL,
    event_type text NOT NULL,
    outcome text NOT NULL,
    processed_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, consumer_name, event_id),
    CONSTRAINT consumer_inbox_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT consumer_inbox_consumer_valid CHECK (consumer_name = btrim(consumer_name) AND char_length(consumer_name) BETWEEN 1 AND 120),
    CONSTRAINT consumer_inbox_type_valid CHECK (event_type = btrim(event_type) AND char_length(event_type) BETWEEN 1 AND 160),
    CONSTRAINT consumer_inbox_outcome_valid CHECK (outcome IN ('applied', 'ignored'))
);

CREATE INDEX consumer_inbox_processed_idx ON consumer_inbox (processed_at, store_id, consumer_name, event_id);

CREATE TABLE event_dead_letters (
    dead_letter_id uuid PRIMARY KEY,
    store_id uuid,
    consumer_name text NOT NULL,
    event_id uuid,
    topic text NOT NULL,
    partition integer NOT NULL,
    message_offset bigint NOT NULL,
    message_digest bytea NOT NULL,
    event_type text,
    classification text NOT NULL,
    diagnostic text NOT NULL,
    validated_envelope jsonb,
    attempts integer NOT NULL DEFAULT 1,
    first_failed_at timestamptz NOT NULL,
    last_failed_at timestamptz NOT NULL,
    replay_requested_at timestamptz,
    replay_reason text,
    resolved_at timestamptz,
    UNIQUE (consumer_name, topic, partition, message_offset),
    CONSTRAINT event_dead_letters_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT event_dead_letters_consumer_valid CHECK (consumer_name = btrim(consumer_name) AND char_length(consumer_name) BETWEEN 1 AND 120),
    CONSTRAINT event_dead_letters_topic_valid CHECK (topic = btrim(topic) AND char_length(topic) BETWEEN 1 AND 200),
    CONSTRAINT event_dead_letters_position_valid CHECK (partition >= 0 AND message_offset >= 0),
    CONSTRAINT event_dead_letters_digest_valid CHECK (octet_length(message_digest) = 32),
    CONSTRAINT event_dead_letters_type_valid CHECK (event_type IS NULL OR (event_type = btrim(event_type) AND char_length(event_type) BETWEEN 1 AND 160)),
    CONSTRAINT event_dead_letters_class_valid CHECK (classification IN ('retryable', 'malformed', 'unsupported', 'permanent', 'exhausted')),
    CONSTRAINT event_dead_letters_diagnostic_valid CHECK (diagnostic = btrim(diagnostic) AND char_length(diagnostic) BETWEEN 1 AND 240),
    CONSTRAINT event_dead_letters_envelope_valid CHECK (validated_envelope IS NULL OR jsonb_typeof(validated_envelope) = 'object'),
    CONSTRAINT event_dead_letters_attempts_valid CHECK (attempts > 0),
    CONSTRAINT event_dead_letters_replay_valid CHECK ((replay_requested_at IS NULL AND replay_reason IS NULL) OR (replay_requested_at IS NOT NULL AND validated_envelope IS NOT NULL AND replay_reason = btrim(replay_reason) AND char_length(replay_reason) BETWEEN 1 AND 240)),
    CONSTRAINT event_dead_letters_timestamps_valid CHECK (last_failed_at >= first_failed_at AND (resolved_at IS NULL OR resolved_at >= first_failed_at))
);

CREATE INDEX event_dead_letters_open_idx ON event_dead_letters (last_failed_at, consumer_name, dead_letter_id) WHERE resolved_at IS NULL;
CREATE INDEX event_dead_letters_store_idx ON event_dead_letters (store_id, last_failed_at, dead_letter_id) WHERE store_id IS NOT NULL;

CREATE TABLE search_documents (
    store_id uuid NOT NULL,
    product_id uuid NOT NULL,
    aggregate_version bigint NOT NULL,
    name text NOT NULL,
    slug text NOT NULL,
    description text NOT NULL,
    published_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, product_id),
    CONSTRAINT search_documents_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT search_documents_product_fk FOREIGN KEY (store_id, product_id) REFERENCES catalog_products (store_id, product_id),
    CONSTRAINT search_documents_version_valid CHECK (aggregate_version > 0),
    CONSTRAINT search_documents_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 240),
    CONSTRAINT search_documents_slug_valid CHECK (slug = btrim(slug) AND char_length(slug) BETWEEN 1 AND 160),
    CONSTRAINT search_documents_timestamps_valid CHECK (updated_at >= published_at)
);

CREATE UNIQUE INDEX search_documents_slug_uidx ON search_documents (store_id, slug);

CREATE TABLE analytics_daily_counts (
    store_id uuid NOT NULL,
    event_date date NOT NULL,
    event_type text NOT NULL,
    event_count bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, event_date, event_type),
    CONSTRAINT analytics_daily_counts_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT analytics_daily_counts_type_valid CHECK (event_type = btrim(event_type) AND char_length(event_type) BETWEEN 1 AND 160),
    CONSTRAINT analytics_daily_counts_count_valid CHECK (event_count > 0)
);

CREATE TABLE notification_email_intents (
    store_id uuid NOT NULL,
    event_id uuid NOT NULL,
    order_id uuid NOT NULL,
    event_type text NOT NULL,
    recipient_email text NOT NULL,
    recipient_name text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, event_id),
    CONSTRAINT notification_email_intents_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT notification_email_intents_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT notification_email_intents_type_valid CHECK (event_type IN ('order.created.v1', 'order.paid.v1', 'order.expired.v1', 'order.cancelled.v1', 'order.shipped.v1', 'order.completed.v1')),
    CONSTRAINT notification_email_intents_email_valid CHECK (recipient_email = lower(btrim(recipient_email)) AND char_length(recipient_email) BETWEEN 3 AND 254),
    CONSTRAINT notification_email_intents_name_valid CHECK (recipient_name = btrim(recipient_name) AND char_length(recipient_name) BETWEEN 1 AND 160),
    CONSTRAINT notification_email_intents_status_valid CHECK (status = 'pending')
);

CREATE INDEX notification_email_intents_pending_idx ON notification_email_intents (created_at, store_id, event_id) WHERE status = 'pending';

-- +goose Down
DROP TABLE notification_email_intents;
DROP TABLE analytics_daily_counts;
DROP TABLE search_documents;
DROP TABLE event_dead_letters;
DROP TABLE consumer_inbox;

DROP INDEX outbox_events_terminal_idx;
DROP INDEX outbox_events_due_idx;
ALTER TABLE outbox_events
    DROP CONSTRAINT outbox_events_error_digest_valid,
    DROP CONSTRAINT outbox_events_requeue_valid,
    DROP CONSTRAINT outbox_events_terminal_valid,
    DROP CONSTRAINT outbox_events_publication_valid,
    DROP CONSTRAINT outbox_events_claim_valid,
    DROP COLUMN last_error_digest,
    DROP COLUMN requeue_reason,
    DROP COLUMN requeued_at,
    DROP COLUMN published_offset,
    DROP COLUMN published_partition,
    DROP COLUMN published_topic,
    DROP COLUMN terminal_at,
    DROP COLUMN claimed_until,
    DROP COLUMN claim_token;
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (next_attempt_at, occurred_at, store_id, event_id) WHERE published_at IS NULL;
