-- +goose Up
ALTER TABLE payment_provider_configs ADD COLUMN public_identifier text NOT NULL DEFAULT '';
ALTER TABLE payment_provider_configs DROP CONSTRAINT payment_provider_configs_provider_valid;
ALTER TABLE payment_provider_configs DROP CONSTRAINT payment_provider_configs_bank_bin_valid;
ALTER TABLE payment_provider_configs DROP CONSTRAINT payment_provider_configs_last4_valid;
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_configs_provider_valid CHECK (provider IN ('vietqr', 'momo'));
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_configs_shape_valid CHECK ((provider = 'vietqr' AND bank_bin ~ '^[0-9]{6}$' AND account_last4 ~ '^[0-9]{4}$' AND public_identifier = '') OR (provider = 'momo' AND bank_bin = '' AND account_last4 = '' AND char_length(public_identifier) BETWEEN 2 AND 50));

ALTER TABLE payment_attempts ADD COLUMN provider_result_code integer;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_provider_valid;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_reference_valid;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_state_valid;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_payment_unique;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_provider_valid CHECK (provider IN ('vietqr', 'momo'));
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_reference_valid CHECK ((provider = 'vietqr' AND transfer_reference ~ '^VQ[A-Z2-7]{18}$') OR (provider = 'momo' AND transfer_reference ~ '^MM[0-9a-f]{32}$'));
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_state_valid CHECK (state IN ('creating', 'retryable', 'action_required', 'failed', 'succeeded', 'reconciliation_required'));
CREATE UNIQUE INDEX payment_attempts_active_payment_unique ON payment_attempts (store_id, payment_id) WHERE state IN ('creating', 'retryable', 'action_required');

CREATE TABLE payment_callback_routes (
    token_digest bytea PRIMARY KEY,
    store_id uuid NOT NULL,
    provider text NOT NULL,
    config_version bigint NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT payment_callback_routes_config_fk FOREIGN KEY (store_id, provider) REFERENCES payment_provider_configs (store_id, provider),
    CONSTRAINT payment_callback_routes_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT payment_callback_routes_provider_valid CHECK (provider = 'momo'),
    CONSTRAINT payment_callback_routes_config_version_valid CHECK (config_version > 0),
    CONSTRAINT payment_callback_routes_ciphertext_valid CHECK (octet_length(ciphertext) BETWEEN 17 AND 2048),
    CONSTRAINT payment_callback_routes_nonce_valid CHECK (octet_length(nonce) = 12),
    CONSTRAINT payment_callback_routes_key_version_valid CHECK (key_version > 0),
    CONSTRAINT payment_callback_routes_store_version_unique UNIQUE (store_id, provider, config_version)
);

CREATE TABLE payment_webhook_receipts (
    store_id uuid NOT NULL,
    provider text NOT NULL,
    provider_event_id text NOT NULL,
    attempt_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    body_digest bytea NOT NULL,
    result_code integer NOT NULL,
    outcome text NOT NULL,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, provider, provider_event_id),
    CONSTRAINT payment_webhook_receipts_attempt_fk FOREIGN KEY (store_id, attempt_id) REFERENCES payment_attempts (store_id, attempt_id),
    CONSTRAINT payment_webhook_receipts_payment_fk FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, payment_id),
    CONSTRAINT payment_webhook_receipts_provider_valid CHECK (provider = 'momo'),
    CONSTRAINT payment_webhook_receipts_event_valid CHECK (provider_event_id ~ '^[0-9A-Za-z._:-]{1,96}$'),
    CONSTRAINT payment_webhook_receipts_digest_valid CHECK (octet_length(body_digest) = 32),
    CONSTRAINT payment_webhook_receipts_outcome_valid CHECK (outcome IN ('paid', 'failed', 'retryable', 'reconciliation_required')),
    CONSTRAINT payment_webhook_receipts_timestamps_valid CHECK (created_at >= received_at - interval '5 minutes')
);

CREATE INDEX payment_webhook_receipts_received_idx ON payment_webhook_receipts (store_id, received_at DESC, provider_event_id DESC);

-- +goose Down
DROP TABLE payment_webhook_receipts;
DROP TABLE payment_callback_routes;
DROP INDEX payment_attempts_active_payment_unique;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_state_valid;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_reference_valid;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_provider_valid;
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_provider_valid CHECK (provider = 'vietqr');
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_reference_valid CHECK (transfer_reference ~ '^VQ[A-Z2-7]{18}$');
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_state_valid CHECK (state IN ('action_required', 'succeeded', 'reconciliation_required'));
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_payment_unique UNIQUE (store_id, payment_id);
ALTER TABLE payment_attempts DROP COLUMN provider_result_code;
ALTER TABLE payment_provider_configs DROP CONSTRAINT payment_provider_configs_shape_valid;
ALTER TABLE payment_provider_configs DROP CONSTRAINT payment_provider_configs_provider_valid;
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_configs_provider_valid CHECK (provider = 'vietqr');
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_configs_bank_bin_valid CHECK (bank_bin ~ '^[0-9]{6}$');
ALTER TABLE payment_provider_configs ADD CONSTRAINT payment_provider_configs_last4_valid CHECK (account_last4 ~ '^[0-9]{4}$');
ALTER TABLE payment_provider_configs DROP COLUMN public_identifier;
