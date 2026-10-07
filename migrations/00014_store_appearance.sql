-- +goose Up
CREATE TABLE store_appearances (
    store_id uuid PRIMARY KEY REFERENCES stores(store_id),
    content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    version bigint NOT NULL CHECK (version > 0 AND version <= 9007199254740991),
    updated_at timestamptz NOT NULL
);

ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid_v14 CHECK (action IN ('store.appearance_updated', 'store.created', 'membership.created', 'membership.updated', 'membership.permissions_updated', 'inventory.adjusted', 'payment.provider_configured', 'payment.confirmed', 'payment.reconciliation_recorded', 'order.cancelled', 'refund.requested', 'refund.settled', 'shipment.prepared', 'shipment.updated', 'shipment.shipped', 'shipment.completed')) NOT VALID;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid_v14 CHECK ((action IN ('store.created', 'store.appearance_updated') AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated', 'membership.permissions_updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item') OR (action = 'payment.provider_configured' AND subject_type = 'payment_provider_config') OR (action IN ('payment.confirmed', 'payment.reconciliation_recorded') AND subject_type = 'payment') OR (action = 'order.cancelled' AND subject_type = 'order') OR (action IN ('refund.requested', 'refund.settled') AND subject_type = 'refund') OR (action IN ('shipment.prepared', 'shipment.updated', 'shipment.shipped', 'shipment.completed') AND subject_type = 'shipment')) NOT VALID;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_action_valid_v14;
ALTER TABLE audit_records VALIDATE CONSTRAINT audit_subject_valid_v14;
ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_action_valid_v14 TO audit_action_valid;
ALTER TABLE audit_records RENAME CONSTRAINT audit_subject_valid_v14 TO audit_subject_valid;
