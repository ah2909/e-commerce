-- +goose Up
ALTER TABLE store_membership_permissions ADD CONSTRAINT store_membership_permissions_name_valid_v11 CHECK (permission IN ('payment.confirm', 'payment.reconcile.view', 'order.cancel', 'payment.refund', 'order.fulfill')) NOT VALID;
ALTER TABLE store_membership_permissions VALIDATE CONSTRAINT store_membership_permissions_name_valid_v11;
ALTER TABLE store_membership_permissions DROP CONSTRAINT store_membership_permissions_name_valid;
ALTER TABLE store_membership_permissions RENAME CONSTRAINT store_membership_permissions_name_valid_v11 TO store_membership_permissions_name_valid;

ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid_v11 CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded', 'order.cancelled', 'refund.requested', 'refund.settled', 'shipment.prepared', 'shipment.updated', 'shipment.shipped', 'shipment.completed')) NOT VALID;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid_v11 CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment') OR (action = 'order.cancelled' AND subject_type = 'order') OR (action IN ('refund.requested', 'refund.settled') AND subject_type = 'refund') OR (action IN ('shipment.prepared', 'shipment.updated', 'shipment.shipped', 'shipment.completed') AND subject_type = 'shipment')) NOT VALID;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_action_valid_v11;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_subject_valid_v11;
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_action_valid_v11 TO audit_action_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_subject_valid_v11 TO audit_subject_valid;

CREATE TABLE shipments (
    store_id uuid NOT NULL,
    shipment_id uuid NOT NULL,
    order_id uuid NOT NULL,
    state text NOT NULL,
    carrier text NOT NULL,
    tracking_number text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    shipped_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (store_id, shipment_id),
    CONSTRAINT shipments_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT shipments_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT shipments_order_unique UNIQUE (store_id, order_id),
    CONSTRAINT shipments_identity_unique UNIQUE (store_id, shipment_id, order_id),
    CONSTRAINT shipments_state_valid CHECK (state IN ('processing', 'shipped', 'completed')),
    CONSTRAINT shipments_carrier_valid CHECK (carrier = btrim(carrier) AND char_length(carrier) BETWEEN 1 AND 120),
    CONSTRAINT shipments_tracking_valid CHECK (tracking_number = btrim(tracking_number) AND char_length(tracking_number) BETWEEN 1 AND 160 AND tracking_number !~ '[[:space:][:cntrl:]]'),
    CONSTRAINT shipments_version_valid CHECK (version > 0),
    CONSTRAINT shipments_timestamps_valid CHECK (updated_at >= created_at AND ((state = 'processing' AND shipped_at IS NULL AND completed_at IS NULL) OR (state = 'shipped' AND shipped_at IS NOT NULL AND completed_at IS NULL AND shipped_at >= created_at) OR (state = 'completed' AND shipped_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= shipped_at)))
);

CREATE UNIQUE INDEX shipments_tracking_uidx ON shipments (store_id, lower(carrier), tracking_number);
CREATE INDEX shipments_order_state_idx ON shipments (store_id, order_id, state);

CREATE TABLE shipment_commands (
    store_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    operation text NOT NULL,
    request_fingerprint bytea NOT NULL,
    shipment_id uuid NOT NULL,
    order_id uuid NOT NULL,
    result_state text NOT NULL,
    result_carrier text NOT NULL,
    result_tracking_number text NOT NULL,
    result_version bigint NOT NULL,
    result_created_at timestamptz NOT NULL,
    result_updated_at timestamptz NOT NULL,
    result_shipped_at timestamptz,
    result_completed_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, idempotency_key),
    CONSTRAINT shipment_commands_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT shipment_commands_shipment_order_fk FOREIGN KEY (store_id, shipment_id, order_id) REFERENCES shipments (store_id, shipment_id, order_id),
    CONSTRAINT shipment_commands_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT shipment_commands_operation_valid CHECK (operation IN ('create', 'update', 'dispatch', 'complete')),
    CONSTRAINT shipment_commands_key_valid CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128),
    CONSTRAINT shipment_commands_fingerprint_valid CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT shipment_commands_result_valid CHECK (result_state IN ('processing', 'shipped', 'completed') AND result_version > 0)
);

CREATE INDEX shipment_commands_shipment_idx ON shipment_commands (store_id, shipment_id, created_at);

-- +goose Down
DROP TABLE shipment_commands;
DROP TABLE shipments;

ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid_v10 CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded', 'order.cancelled', 'refund.requested', 'refund.settled')) NOT VALID;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid_v10 CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment') OR (action = 'order.cancelled' AND subject_type = 'order') OR (action IN ('refund.requested', 'refund.settled') AND subject_type = 'refund')) NOT VALID;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_action_valid_v10;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_subject_valid_v10;
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_action_valid_v10 TO audit_action_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_subject_valid_v10 TO audit_subject_valid;

ALTER TABLE store_membership_permissions ADD CONSTRAINT store_membership_permissions_name_valid_v10 CHECK (permission IN ('payment.confirm', 'payment.reconcile.view', 'order.cancel', 'payment.refund')) NOT VALID;
ALTER TABLE store_membership_permissions VALIDATE CONSTRAINT store_membership_permissions_name_valid_v10;
ALTER TABLE store_membership_permissions DROP CONSTRAINT store_membership_permissions_name_valid;
ALTER TABLE store_membership_permissions RENAME CONSTRAINT store_membership_permissions_name_valid_v10 TO store_membership_permissions_name_valid;
