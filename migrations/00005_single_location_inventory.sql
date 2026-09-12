-- +goose Up
CREATE TABLE inventory_stock_items (
    store_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    on_hand bigint NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, variant_id),
    CONSTRAINT inventory_stock_items_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT inventory_stock_items_variant_fk FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants (store_id, variant_id),
    CONSTRAINT inventory_stock_items_quantity_valid CHECK (on_hand >= 0 AND on_hand <= 2147483647),
    CONSTRAINT inventory_stock_items_version_valid CHECK (version > 0),
    CONSTRAINT inventory_stock_items_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX inventory_stock_items_updated_idx ON inventory_stock_items (store_id, updated_at DESC, variant_id DESC);

CREATE TABLE inventory_reservations (
    store_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    order_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    quantity bigint NOT NULL,
    status text NOT NULL,
    expires_at timestamptz NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    closed_at timestamptz,
    close_reason text,
    PRIMARY KEY (store_id, reservation_id),
    CONSTRAINT inventory_reservations_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT inventory_reservations_variant_fk FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants (store_id, variant_id),
    CONSTRAINT inventory_reservations_order_variant_unique UNIQUE (store_id, order_id, variant_id),
    CONSTRAINT inventory_reservations_quantity_valid CHECK (quantity > 0 AND quantity <= 2147483647),
    CONSTRAINT inventory_reservations_status_valid CHECK (status IN ('active', 'confirmed', 'released', 'expired')),
    CONSTRAINT inventory_reservations_version_valid CHECK (version > 0),
    CONSTRAINT inventory_reservations_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT inventory_reservations_timestamps_valid CHECK (updated_at >= created_at),
    CONSTRAINT inventory_reservations_lifecycle_valid CHECK ((status = 'active' AND closed_at IS NULL AND close_reason IS NULL) OR (status <> 'active' AND closed_at IS NOT NULL AND close_reason IS NOT NULL AND char_length(close_reason) BETWEEN 1 AND 120))
);

CREATE INDEX inventory_reservations_order_idx ON inventory_reservations (store_id, order_id, reservation_id);
CREATE INDEX inventory_reservations_active_expiry_idx ON inventory_reservations (expires_at, store_id, order_id, reservation_id) WHERE status = 'active';
CREATE INDEX inventory_reservations_active_variant_idx ON inventory_reservations (store_id, variant_id, reservation_id) WHERE status = 'active';

CREATE TABLE inventory_ledger_entries (
    store_id uuid NOT NULL,
    ledger_entry_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    reservation_id uuid,
    actor_id uuid,
    entry_type text NOT NULL,
    quantity_delta bigint NOT NULL,
    resulting_on_hand bigint NOT NULL,
    resulting_reserved bigint NOT NULL,
    reason text NOT NULL,
    business_reference text NOT NULL,
    idempotency_key text,
    expected_version bigint,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, ledger_entry_id),
    CONSTRAINT inventory_ledger_entries_stock_fk FOREIGN KEY (store_id, variant_id) REFERENCES inventory_stock_items (store_id, variant_id),
    CONSTRAINT inventory_ledger_entries_reservation_fk FOREIGN KEY (store_id, reservation_id) REFERENCES inventory_reservations (store_id, reservation_id),
    CONSTRAINT inventory_ledger_entries_type_valid CHECK (entry_type IN ('manual_adjustment', 'reservation_confirmation')),
    CONSTRAINT inventory_ledger_entries_delta_valid CHECK (quantity_delta <> 0 AND quantity_delta BETWEEN -2147483647 AND 2147483647),
    CONSTRAINT inventory_ledger_entries_balance_valid CHECK (resulting_on_hand BETWEEN 0 AND 2147483647),
    CONSTRAINT inventory_ledger_entries_reserved_valid CHECK (resulting_reserved BETWEEN 0 AND resulting_on_hand),
    CONSTRAINT inventory_ledger_entries_reason_valid CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 240),
    CONSTRAINT inventory_ledger_entries_reference_valid CHECK (business_reference = btrim(business_reference) AND char_length(business_reference) BETWEEN 1 AND 200),
    CONSTRAINT inventory_ledger_entries_idempotency_valid CHECK (idempotency_key IS NULL OR (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128)),
    CONSTRAINT inventory_ledger_entries_shape_valid CHECK ((entry_type = 'manual_adjustment' AND reservation_id IS NULL AND actor_id IS NOT NULL AND idempotency_key IS NOT NULL AND expected_version IS NOT NULL) OR (entry_type = 'reservation_confirmation' AND reservation_id IS NOT NULL AND actor_id IS NULL AND idempotency_key IS NULL AND expected_version IS NULL))
);

CREATE UNIQUE INDEX inventory_ledger_entries_adjustment_key_uidx ON inventory_ledger_entries (store_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX inventory_ledger_entries_variant_time_idx ON inventory_ledger_entries (store_id, variant_id, created_at DESC, ledger_entry_id DESC);
CREATE INDEX inventory_ledger_entries_reservation_idx ON inventory_ledger_entries (store_id, reservation_id) WHERE reservation_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION reject_inventory_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
	IF TG_OP = 'DELETE' AND current_database() = 'ecommerce_test' AND current_setting('application_name') = 'ecommerce-test-cleanup' THEN
		RETURN OLD;
	END IF;
    RAISE EXCEPTION 'inventory ledger entries are immutable';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER inventory_ledger_entries_immutable
BEFORE UPDATE OR DELETE ON inventory_ledger_entries
FOR EACH ROW EXECUTE FUNCTION reject_inventory_ledger_mutation();

ALTER TABLE audit_records DROP CONSTRAINT audit_action_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_action_valid CHECK (action IN ('store.created', 'membership.created', 'membership.updated', 'inventory.adjusted'));
ALTER TABLE audit_records DROP CONSTRAINT audit_subject_valid;
ALTER TABLE audit_records ADD CONSTRAINT audit_subject_valid CHECK ((action = 'store.created' AND subject_type = 'store') OR (action IN ('membership.created', 'membership.updated') AND subject_type = 'membership') OR (action = 'inventory.adjusted' AND subject_type = 'inventory_stock_item'));
