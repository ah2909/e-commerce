-- +goose Up
ALTER TABLE store_membership_permissions ADD CONSTRAINT store_membership_permissions_name_valid_v10 CHECK (permission IN ('payment.confirm', 'payment.reconcile.view', 'order.cancel', 'payment.refund')) NOT VALID;
ALTER TABLE store_membership_permissions VALIDATE CONSTRAINT store_membership_permissions_name_valid_v10;
ALTER TABLE store_membership_permissions DROP CONSTRAINT store_membership_permissions_name_valid;
ALTER TABLE store_membership_permissions RENAME CONSTRAINT store_membership_permissions_name_valid_v10 TO store_membership_permissions_name_valid;

ALTER TABLE audit_records ADD CONSTRAINT audit_actor_type_valid_v10 CHECK (actor_type IN ('platform_admin', 'user', 'guest')) NOT VALID;
ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid_v10 CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded', 'order.cancelled', 'refund.requested', 'refund.settled')) NOT VALID;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid_v10 CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment') OR (action = 'order.cancelled' AND subject_type = 'order') OR (action IN ('refund.requested', 'refund.settled') AND subject_type = 'refund')) NOT VALID;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_actor_type_valid_v10;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_action_valid_v10;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_subject_valid_v10;
ALTER TABLE audit_records DROP CONSTRAINT audit_actor_type_valid;
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_actor_type_valid_v10 TO audit_actor_type_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_action_valid_v10 TO audit_action_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_subject_valid_v10 TO audit_subject_valid;

ALTER TABLE payments ADD CONSTRAINT payments_state_valid_v10 CHECK (state IN ('pending', 'succeeded', 'failed', 'expired', 'reconciliation_required', 'refund_pending', 'refunded')) NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT payments_state_valid_v10;
ALTER TABLE payments DROP CONSTRAINT payments_state_valid;
ALTER TABLE payments RENAME CONSTRAINT payments_state_valid_v10 TO payments_state_valid;
ALTER TABLE payments ADD CONSTRAINT payments_refund_binding_unique UNIQUE (store_id, payment_id, order_id, provider, amount_minor, currency);

ALTER TABLE payment_attempts ADD COLUMN provider_transaction_id bigint;
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM payment_webhook_receipts WHERE provider = 'momo' AND outcome = 'paid' GROUP BY store_id, attempt_id HAVING count(DISTINCT provider_event_id) <> 1) THEN
        RAISE EXCEPTION 'successful MoMo attempts require one unambiguous provider transaction ID';
    END IF;
END $$;
-- +goose StatementEnd
UPDATE payment_attempts a SET provider_transaction_id = r.provider_event_id::bigint FROM payment_webhook_receipts r WHERE r.store_id = a.store_id AND r.attempt_id = a.attempt_id AND r.provider = 'momo' AND r.outcome = 'paid' AND r.provider_event_id ~ '^[1-9][0-9]{0,18}$';
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM payment_attempts WHERE provider = 'momo' AND state = 'succeeded' AND provider_transaction_id IS NULL) THEN
        RAISE EXCEPTION 'successful MoMo attempts require one provider transaction ID';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_transaction_shape_valid CHECK ((provider = 'momo' AND state = 'succeeded' AND provider_transaction_id > 0) OR state <> 'succeeded' OR provider <> 'momo') NOT VALID;
ALTER TABLE payment_attempts VALIDATE CONSTRAINT payment_attempts_transaction_shape_valid;
CREATE UNIQUE INDEX payment_attempts_momo_transaction_unique ON payment_attempts (store_id, provider_transaction_id) WHERE provider = 'momo' AND provider_transaction_id IS NOT NULL;

CREATE TABLE refunds (
    store_id uuid NOT NULL,
    refund_id uuid NOT NULL,
    order_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    provider text NOT NULL,
    amount_minor bigint NOT NULL,
    currency text NOT NULL,
    reason text NOT NULL,
    state text NOT NULL,
    requested_by uuid NOT NULL,
    request_id text NOT NULL,
    cancellation_key text NOT NULL,
    cancellation_fingerprint bytea NOT NULL,
    execution_key text,
    execution_fingerprint bytea,
    provider_order_id text,
    provider_request_id text,
    provider_transaction_id bigint,
    provider_refund_transaction_id bigint,
    provider_result_code integer,
    external_reference text,
    settled_at timestamptz,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, refund_id),
    CONSTRAINT refunds_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT refunds_payment_fk FOREIGN KEY (store_id, payment_id) REFERENCES payments (store_id, payment_id),
    CONSTRAINT refunds_payment_binding_fk FOREIGN KEY (store_id, payment_id, order_id, provider, amount_minor, currency) REFERENCES payments (store_id, payment_id, order_id, provider, amount_minor, currency),
    CONSTRAINT refunds_actor_fk FOREIGN KEY (store_id, requested_by) REFERENCES store_memberships (store_id, user_id),
    CONSTRAINT refunds_provider_valid CHECK (provider IN ('vietqr', 'momo')),
    CONSTRAINT refunds_amount_valid CHECK (amount_minor > 0 AND currency = 'VND'),
    CONSTRAINT refunds_reason_valid CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 240),
    CONSTRAINT refunds_state_valid CHECK (state IN ('pending', 'processing', 'succeeded', 'failed', 'reconciliation_required')),
    CONSTRAINT refunds_request_valid CHECK (char_length(request_id) BETWEEN 1 AND 128 AND cancellation_key = btrim(cancellation_key) AND char_length(cancellation_key) BETWEEN 1 AND 128 AND octet_length(cancellation_fingerprint) = 32),
    CONSTRAINT refunds_execution_valid CHECK ((execution_key IS NULL AND execution_fingerprint IS NULL) OR (execution_key = btrim(execution_key) AND char_length(execution_key) BETWEEN 1 AND 128 AND octet_length(execution_fingerprint) = 32)),
    CONSTRAINT refunds_provider_identity_valid CHECK ((provider = 'momo' AND provider_transaction_id > 0) OR (provider = 'vietqr' AND provider_transaction_id IS NULL)),
    CONSTRAINT refunds_version_valid CHECK (version > 0),
    CONSTRAINT refunds_timestamps_valid CHECK (updated_at >= created_at AND (settled_at IS NULL OR (settled_at >= created_at AND settled_at <= updated_at))),
    CONSTRAINT refunds_payment_unique UNIQUE (store_id, payment_id),
    CONSTRAINT refunds_cancellation_key_unique UNIQUE (store_id, cancellation_key),
    CONSTRAINT refunds_provider_order_unique UNIQUE (store_id, provider, provider_order_id),
    CONSTRAINT refunds_external_reference_unique UNIQUE (store_id, provider, external_reference)
);
CREATE INDEX refunds_state_idx ON refunds (store_id, state, updated_at, refund_id);
CREATE UNIQUE INDEX refunds_provider_refund_transaction_unique ON refunds (store_id, provider, provider_refund_transaction_id) WHERE provider_refund_transaction_id IS NOT NULL;

CREATE TABLE order_cancellations (
    store_id uuid NOT NULL,
    cancellation_id uuid NOT NULL,
    order_id uuid NOT NULL,
    refund_id uuid,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    reason text NOT NULL,
    outcome text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, cancellation_id),
    CONSTRAINT order_cancellations_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_cancellations_refund_fk FOREIGN KEY (store_id, refund_id) REFERENCES refunds (store_id, refund_id),
    CONSTRAINT order_cancellations_key_valid CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128),
    CONSTRAINT order_cancellations_fingerprint_valid CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT order_cancellations_actor_valid CHECK (actor_type IN ('user', 'guest', 'workforce') AND char_length(actor_id) BETWEEN 1 AND 128),
    CONSTRAINT order_cancellations_reason_valid CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 240),
    CONSTRAINT order_cancellations_outcome_valid CHECK (outcome IN ('cancelled', 'refund_pending')),
    CONSTRAINT order_cancellations_shape_valid CHECK ((outcome = 'cancelled' AND refund_id IS NULL) OR (outcome = 'refund_pending' AND refund_id IS NOT NULL)),
    CONSTRAINT order_cancellations_key_unique UNIQUE (store_id, idempotency_key)
);

ALTER TABLE inventory_ledger_entries ADD CONSTRAINT inventory_ledger_entries_type_valid_v10 CHECK (entry_type IN ('manual_adjustment', 'reservation_confirmation', 'refund_restock')) NOT VALID;
ALTER TABLE inventory_ledger_entries ADD CONSTRAINT inventory_ledger_entries_shape_valid_v10 CHECK ((entry_type = 'manual_adjustment' AND reservation_id IS NULL AND actor_id IS NOT NULL AND idempotency_key IS NOT NULL AND expected_version IS NOT NULL) OR (entry_type = 'reservation_confirmation' AND reservation_id IS NOT NULL AND actor_id IS NULL AND idempotency_key IS NULL AND expected_version IS NULL) OR (entry_type = 'refund_restock' AND reservation_id IS NULL AND actor_id IS NULL AND idempotency_key IS NULL AND expected_version IS NULL)) NOT VALID;
ALTER TABLE inventory_ledger_entries VALIDATE CONSTRAINT inventory_ledger_entries_type_valid_v10;
ALTER TABLE inventory_ledger_entries VALIDATE CONSTRAINT inventory_ledger_entries_shape_valid_v10;
ALTER TABLE inventory_ledger_entries DROP CONSTRAINT inventory_ledger_entries_type_valid;
ALTER TABLE inventory_ledger_entries DROP CONSTRAINT inventory_ledger_entries_shape_valid;
ALTER TABLE inventory_ledger_entries RENAME CONSTRAINT inventory_ledger_entries_type_valid_v10 TO inventory_ledger_entries_type_valid;
ALTER TABLE inventory_ledger_entries RENAME CONSTRAINT inventory_ledger_entries_shape_valid_v10 TO inventory_ledger_entries_shape_valid;

-- +goose Down
ALTER TABLE inventory_ledger_entries DROP CONSTRAINT inventory_ledger_entries_shape_valid;
ALTER TABLE inventory_ledger_entries ADD CONSTRAINT inventory_ledger_entries_shape_valid CHECK ((entry_type = 'manual_adjustment' AND reservation_id IS NULL AND actor_id IS NOT NULL AND idempotency_key IS NOT NULL AND expected_version IS NOT NULL) OR (entry_type = 'reservation_confirmation' AND reservation_id IS NOT NULL AND actor_id IS NULL AND idempotency_key IS NULL AND expected_version IS NULL));
ALTER TABLE inventory_ledger_entries DROP CONSTRAINT inventory_ledger_entries_type_valid;
ALTER TABLE inventory_ledger_entries ADD CONSTRAINT inventory_ledger_entries_type_valid CHECK (entry_type IN ('manual_adjustment', 'reservation_confirmation'));
DROP TABLE order_cancellations;
DROP TABLE refunds;
ALTER TABLE payments DROP CONSTRAINT payments_refund_binding_unique;
ALTER TABLE payment_attempts DROP CONSTRAINT payment_attempts_transaction_shape_valid;
DROP INDEX payment_attempts_momo_transaction_unique;
ALTER TABLE payment_attempts DROP COLUMN provider_transaction_id;
ALTER TABLE payments DROP CONSTRAINT payments_state_valid;
ALTER TABLE payments ADD CONSTRAINT payments_state_valid CHECK (state IN ('pending', 'succeeded', 'failed', 'expired', 'reconciliation_required'));
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment'));
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded'));
ALTER TABLE audit_records DROP CONSTRAINT audit_actor_type_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_actor_type_valid CHECK (actor_type IN ('platform_admin', 'user'));
ALTER TABLE store_membership_permissions DROP CONSTRAINT store_membership_permissions_name_valid;
ALTER TABLE store_membership_permissions ADD CONSTRAINT store_membership_permissions_name_valid CHECK (permission IN ('payment.confirm', 'payment.reconcile.view'));
