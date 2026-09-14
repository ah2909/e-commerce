-- +goose Up
CREATE TABLE store_membership_permission_sets (
    store_id uuid NOT NULL,
    user_id uuid NOT NULL,
    version bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, user_id),
    CONSTRAINT store_membership_permission_sets_membership_fk FOREIGN KEY (store_id, user_id) REFERENCES store_memberships (store_id, user_id),
    CONSTRAINT store_membership_permission_sets_version_valid CHECK (version >= 0)
);

CREATE TABLE store_membership_permissions (
    store_id uuid NOT NULL,
    user_id uuid NOT NULL,
    permission text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, user_id, permission),
    CONSTRAINT store_membership_permissions_set_fk FOREIGN KEY (store_id, user_id) REFERENCES store_membership_permission_sets (store_id, user_id) ON DELETE CASCADE,
    CONSTRAINT store_membership_permissions_name_valid CHECK (permission IN ('payment.confirm', 'payment.reconcile.view')),
    CONSTRAINT store_membership_permissions_version_valid CHECK (version > 0),
    CONSTRAINT store_membership_permissions_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE payment_provider_configs (
    store_id uuid NOT NULL,
    provider text NOT NULL,
    bank_bin text NOT NULL,
    account_last4 text NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL,
    enabled boolean NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, provider),
    CONSTRAINT payment_provider_configs_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT payment_provider_configs_provider_valid CHECK (provider = 'vietqr'),
    CONSTRAINT payment_provider_configs_bank_bin_valid CHECK (bank_bin ~ '^[0-9]{6}$'),
    CONSTRAINT payment_provider_configs_last4_valid CHECK (account_last4 ~ '^[0-9]{4}$'),
    CONSTRAINT payment_provider_configs_ciphertext_valid CHECK (octet_length(ciphertext) BETWEEN 17 AND 2048),
    CONSTRAINT payment_provider_configs_nonce_valid CHECK (octet_length(nonce) = 12),
    CONSTRAINT payment_provider_configs_key_version_valid CHECK (key_version > 0),
    CONSTRAINT payment_provider_configs_version_valid CHECK (version > 0),
    CONSTRAINT payment_provider_configs_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE payment_attempts (
    store_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    order_id uuid NOT NULL,
    provider text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    transfer_reference text NOT NULL,
    amount_minor bigint NOT NULL,
    currency text NOT NULL,
    provider_config_version bigint NOT NULL,
    instruction_ciphertext bytea NOT NULL,
    instruction_nonce bytea NOT NULL,
    instruction_key_version integer NOT NULL,
    state text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, attempt_id),
    CONSTRAINT payment_attempts_payment_fk FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, payment_id),
    CONSTRAINT payment_attempts_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT payment_attempts_provider_valid CHECK (provider = 'vietqr'),
    CONSTRAINT payment_attempts_key_valid CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128),
    CONSTRAINT payment_attempts_fingerprint_valid CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT payment_attempts_reference_valid CHECK (transfer_reference ~ '^VQ[A-Z2-7]{18}$'),
    CONSTRAINT payment_attempts_amount_valid CHECK (amount_minor >= 0 AND currency = 'VND'),
    CONSTRAINT payment_attempts_config_version_valid CHECK (provider_config_version > 0),
    CONSTRAINT payment_attempts_instruction_ciphertext_valid CHECK (octet_length(instruction_ciphertext) BETWEEN 17 AND 2048),
    CONSTRAINT payment_attempts_instruction_nonce_valid CHECK (octet_length(instruction_nonce) = 12),
    CONSTRAINT payment_attempts_instruction_key_version_valid CHECK (instruction_key_version > 0),
    CONSTRAINT payment_attempts_state_valid CHECK (state IN ('action_required', 'succeeded', 'reconciliation_required')),
    CONSTRAINT payment_attempts_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT payment_attempts_timestamps_valid CHECK (updated_at >= created_at),
    CONSTRAINT payment_attempts_idempotency_unique UNIQUE (store_id, provider, idempotency_key),
    CONSTRAINT payment_attempts_payment_unique UNIQUE (store_id, payment_id),
    CONSTRAINT payment_attempts_reference_unique UNIQUE (store_id, provider, transfer_reference)
);

CREATE INDEX payment_attempts_payment_idx ON payment_attempts (store_id, payment_id, created_at DESC);

CREATE TABLE payment_manual_confirmations (
    store_id uuid NOT NULL,
    confirmation_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    attempt_id uuid,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    transfer_reference text NOT NULL,
    observed_amount_minor bigint NOT NULL,
    currency text NOT NULL,
    actor_id uuid NOT NULL,
    received_at timestamptz NOT NULL,
    outcome text NOT NULL,
    reconciliation_id uuid,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, confirmation_id),
    CONSTRAINT payment_manual_confirmations_payment_fk FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, payment_id),
    CONSTRAINT payment_manual_confirmations_attempt_fk FOREIGN KEY (store_id, attempt_id) REFERENCES payment_attempts (store_id, attempt_id),
    CONSTRAINT payment_manual_confirmations_actor_fk FOREIGN KEY (store_id, actor_id) REFERENCES store_memberships (store_id, user_id),
    CONSTRAINT payment_manual_confirmations_key_valid CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128),
    CONSTRAINT payment_manual_confirmations_fingerprint_valid CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT payment_manual_confirmations_reference_valid CHECK (transfer_reference = btrim(transfer_reference) AND char_length(transfer_reference) BETWEEN 1 AND 64),
    CONSTRAINT payment_manual_confirmations_amount_valid CHECK (observed_amount_minor >= 0 AND currency = 'VND'),
    CONSTRAINT payment_manual_confirmations_outcome_valid CHECK (outcome IN ('paid', 'reconciliation_required')),
    CONSTRAINT payment_manual_confirmations_shape_valid CHECK ((outcome = 'paid' AND attempt_id IS NOT NULL AND reconciliation_id IS NULL) OR (outcome = 'reconciliation_required' AND reconciliation_id IS NOT NULL)),
    CONSTRAINT payment_manual_confirmations_idempotency_unique UNIQUE (store_id, payment_id, idempotency_key),
    CONSTRAINT payment_manual_confirmations_reference_unique UNIQUE (store_id, payment_id, transfer_reference)
);

CREATE TABLE payment_reconciliation_items (
    store_id uuid NOT NULL,
    reconciliation_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    attempt_id uuid,
    transfer_reference text NOT NULL,
    observed_amount_minor bigint NOT NULL,
    expected_amount_minor bigint NOT NULL,
    currency text NOT NULL,
    reason text NOT NULL,
    status text NOT NULL,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, reconciliation_id),
    CONSTRAINT payment_reconciliation_items_payment_fk FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, payment_id),
    CONSTRAINT payment_reconciliation_items_attempt_fk FOREIGN KEY (store_id, attempt_id) REFERENCES payment_attempts (store_id, attempt_id),
    CONSTRAINT payment_reconciliation_items_amounts_valid CHECK (observed_amount_minor >= 0 AND expected_amount_minor >= 0 AND currency = 'VND'),
    CONSTRAINT payment_reconciliation_items_reason_valid CHECK (reason IN ('late', 'payment_closed', 'wrong_reference', 'wrong_amount')),
    CONSTRAINT payment_reconciliation_items_status_valid CHECK (status = 'unresolved')
);

CREATE INDEX payment_reconciliation_items_unresolved_idx ON payment_reconciliation_items (store_id, created_at DESC, reconciliation_id DESC) WHERE status = 'unresolved';

ALTER TABLE payment_manual_confirmations ADD CONSTRAINT payment_manual_confirmations_reconciliation_fk FOREIGN KEY (store_id, reconciliation_id) REFERENCES payment_reconciliation_items (store_id, reconciliation_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded'));
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment'));

-- +goose Down
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item'));
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'inventory.adjusted'));
DROP TABLE payment_manual_confirmations;
DROP TABLE payment_reconciliation_items;
DROP TABLE payment_attempts;
DROP TABLE payment_provider_configs;
DROP TABLE store_membership_permissions;
DROP TABLE store_membership_permission_sets;
