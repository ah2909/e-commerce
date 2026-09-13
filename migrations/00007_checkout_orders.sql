-- +goose Up
CREATE TABLE shipping_rules (
    store_id uuid NOT NULL,
    shipping_rule_id uuid NOT NULL,
    mode text NOT NULL,
    flat_amount_minor bigint NOT NULL,
    currency text NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, shipping_rule_id),
    CONSTRAINT shipping_rules_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT shipping_rules_mode_valid CHECK (mode IN ('free', 'flat')),
    CONSTRAINT shipping_rules_amount_valid CHECK (flat_amount_minor >= 0 AND (mode = 'flat' OR flat_amount_minor = 0)),
    CONSTRAINT shipping_rules_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT shipping_rules_status_valid CHECK (status IN ('active', 'archived')),
    CONSTRAINT shipping_rules_version_valid CHECK (version > 0),
    CONSTRAINT shipping_rules_lifecycle_valid CHECK ((status = 'active' AND archived_at IS NULL) OR (status = 'archived' AND archived_at IS NOT NULL)),
    CONSTRAINT shipping_rules_timestamps_valid CHECK (updated_at >= created_at AND (archived_at IS NULL OR archived_at >= created_at))
);

CREATE UNIQUE INDEX shipping_rules_current_uidx ON shipping_rules (store_id) WHERE status = 'active';

CREATE TABLE order_number_counters (
    store_id uuid PRIMARY KEY,
    next_value bigint NOT NULL,
    CONSTRAINT order_number_counters_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT order_number_counters_value_valid CHECK (next_value BETWEEN 1 AND 999999999999)
);

CREATE TABLE orders (
    store_id uuid NOT NULL,
    order_id uuid NOT NULL,
    order_number text NOT NULL,
    owner_kind text NOT NULL,
    user_id uuid,
    guest_id uuid,
    customer_id uuid,
    cart_id uuid NOT NULL,
    currency text NOT NULL,
    subtotal_minor bigint NOT NULL,
    shipping_total_minor bigint NOT NULL,
    grand_total_minor bigint NOT NULL,
    tax_presentation text NOT NULL,
    contact_name text NOT NULL,
    contact_email text NOT NULL,
    contact_phone text NOT NULL,
    shipping_mode text NOT NULL,
    shipping_rule_id uuid NOT NULL,
    shipping_rule_version bigint NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    reservation_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    closed_at timestamptz,
    PRIMARY KEY (store_id, order_id),
    CONSTRAINT orders_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT orders_user_fk FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id),
    CONSTRAINT orders_guest_fk FOREIGN KEY (store_id, guest_id) REFERENCES identity_guests (store_id, guest_id),
    CONSTRAINT orders_customer_fk FOREIGN KEY (store_id, customer_id) REFERENCES customers (store_id, customer_id),
    CONSTRAINT orders_cart_fk FOREIGN KEY (store_id, cart_id) REFERENCES carts (store_id, cart_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT orders_shipping_rule_fk FOREIGN KEY (store_id, shipping_rule_id) REFERENCES shipping_rules (store_id, shipping_rule_id),
    CONSTRAINT orders_number_unique UNIQUE (store_id, order_number),
    CONSTRAINT orders_cart_unique UNIQUE (store_id, cart_id),
    CONSTRAINT orders_owner_valid CHECK ((owner_kind = 'user' AND user_id IS NOT NULL AND guest_id IS NULL AND customer_id IS NOT NULL) OR (owner_kind = 'guest' AND guest_id IS NOT NULL AND user_id IS NULL AND customer_id IS NULL)),
    CONSTRAINT orders_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT orders_totals_valid CHECK (subtotal_minor >= 0 AND shipping_total_minor >= 0 AND grand_total_minor = subtotal_minor + shipping_total_minor),
    CONSTRAINT orders_tax_valid CHECK (tax_presentation = 'vat_inclusive'),
    CONSTRAINT orders_contact_valid CHECK (contact_name = btrim(contact_name) AND char_length(contact_name) BETWEEN 1 AND 160 AND contact_email = btrim(contact_email) AND char_length(contact_email) BETWEEN 3 AND 254 AND contact_phone = btrim(contact_phone) AND char_length(contact_phone) BETWEEN 8 AND 20),
    CONSTRAINT orders_shipping_valid CHECK (shipping_mode IN ('free', 'flat') AND shipping_rule_version > 0),
    CONSTRAINT orders_state_valid CHECK (state IN ('pending_payment', 'paid', 'processing', 'shipped', 'completed', 'expired', 'cancellation_requested', 'refund_pending', 'cancelled')),
    CONSTRAINT orders_version_valid CHECK (version > 0),
    CONSTRAINT orders_expiry_valid CHECK (reservation_expires_at > created_at),
    CONSTRAINT orders_lifecycle_valid CHECK ((state IN ('expired', 'completed', 'cancelled') AND closed_at IS NOT NULL) OR (state NOT IN ('expired', 'completed', 'cancelled') AND closed_at IS NULL)),
    CONSTRAINT orders_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX orders_user_history_idx ON orders (store_id, user_id, created_at DESC, order_id DESC) WHERE user_id IS NOT NULL;
CREATE INDEX orders_guest_history_idx ON orders (store_id, guest_id, created_at DESC, order_id DESC) WHERE guest_id IS NOT NULL;
CREATE INDEX orders_pending_expiry_idx ON orders (reservation_expires_at, store_id, order_id) WHERE state = 'pending_payment';

CREATE TABLE order_lines (
    store_id uuid NOT NULL,
    order_id uuid NOT NULL,
    line_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    product_name text NOT NULL,
    sku text NOT NULL,
    option_values jsonb NOT NULL,
    quantity bigint NOT NULL,
    unit_price_minor bigint NOT NULL,
    line_total_minor bigint NOT NULL,
    currency text NOT NULL,
    tax_presentation text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, order_id, line_id),
    CONSTRAINT order_lines_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_lines_variant_fk FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants (store_id, variant_id),
    CONSTRAINT order_lines_text_valid CHECK (product_name = btrim(product_name) AND char_length(product_name) BETWEEN 1 AND 200 AND sku = btrim(sku) AND char_length(sku) BETWEEN 1 AND 100),
    CONSTRAINT order_lines_options_valid CHECK (jsonb_typeof(option_values) = 'object' AND pg_column_size(option_values) <= 8192),
    CONSTRAINT order_lines_quantity_valid CHECK (quantity BETWEEN 1 AND 2147483647),
    CONSTRAINT order_lines_totals_valid CHECK (unit_price_minor >= 0 AND line_total_minor = unit_price_minor * quantity),
    CONSTRAINT order_lines_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT order_lines_tax_valid CHECK (tax_presentation = 'vat_inclusive')
);

CREATE TABLE order_addresses (
    store_id uuid NOT NULL,
    order_id uuid NOT NULL,
    address_type text NOT NULL,
    recipient_name text NOT NULL,
    phone text NOT NULL,
    line1 text NOT NULL,
    line2 text NOT NULL,
    ward text NOT NULL,
    district text NOT NULL,
    province text NOT NULL,
    postal_code text NOT NULL,
    country_code text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, order_id, address_type),
    CONSTRAINT order_addresses_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_addresses_type_valid CHECK (address_type = 'delivery'),
    CONSTRAINT order_addresses_text_valid CHECK (recipient_name = btrim(recipient_name) AND char_length(recipient_name) BETWEEN 1 AND 160 AND phone = btrim(phone) AND char_length(phone) BETWEEN 8 AND 20 AND line1 = btrim(line1) AND char_length(line1) BETWEEN 1 AND 240 AND line2 = btrim(line2) AND char_length(line2) <= 240 AND ward = btrim(ward) AND char_length(ward) BETWEEN 1 AND 120 AND district = btrim(district) AND char_length(district) BETWEEN 1 AND 120 AND province = btrim(province) AND char_length(province) BETWEEN 1 AND 120 AND postal_code = btrim(postal_code) AND char_length(postal_code) <= 20),
    CONSTRAINT order_addresses_country_valid CHECK (country_code = 'VN')
);

CREATE TABLE order_guest_grants (
    store_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    order_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (store_id, token_digest),
    CONSTRAINT order_guest_grants_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_guest_grants_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT order_guest_grants_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT order_guest_grants_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX order_guest_grants_active_order_uidx ON order_guest_grants (store_id, order_id) WHERE revoked_at IS NULL;

CREATE TABLE order_idempotency (
    store_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    order_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, idempotency_key),
    CONSTRAINT order_idempotency_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_idempotency_key_valid CHECK (idempotency_key = btrim(idempotency_key) AND char_length(idempotency_key) BETWEEN 1 AND 128),
    CONSTRAINT order_idempotency_fingerprint_valid CHECK (octet_length(request_fingerprint) = 32)
);

CREATE TABLE order_state_history (
    store_id uuid NOT NULL,
    history_id uuid NOT NULL,
    order_id uuid NOT NULL,
    from_state text,
    to_state text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, history_id),
    CONSTRAINT order_state_history_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT order_state_history_states_valid CHECK ((from_state IS NULL OR from_state IN ('pending_payment', 'paid', 'processing', 'shipped', 'completed', 'expired', 'cancellation_requested', 'refund_pending', 'cancelled')) AND to_state IN ('pending_payment', 'paid', 'processing', 'shipped', 'completed', 'expired', 'cancellation_requested', 'refund_pending', 'cancelled')),
    CONSTRAINT order_state_history_reason_valid CHECK (reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 120)
);

CREATE INDEX order_state_history_order_idx ON order_state_history (store_id, order_id, occurred_at, history_id);

CREATE TABLE payments (
    store_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    order_id uuid NOT NULL,
    provider text NOT NULL,
    amount_minor bigint NOT NULL,
    currency text NOT NULL,
    state text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    closed_at timestamptz,
    PRIMARY KEY (store_id, payment_id),
    CONSTRAINT payments_order_fk FOREIGN KEY (store_id, order_id) REFERENCES orders (store_id, order_id),
    CONSTRAINT payments_order_unique UNIQUE (store_id, order_id),
    CONSTRAINT payments_provider_valid CHECK (provider IN ('vietqr', 'momo')),
    CONSTRAINT payments_amount_valid CHECK (amount_minor >= 0),
    CONSTRAINT payments_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT payments_state_valid CHECK (state IN ('pending', 'succeeded', 'failed', 'expired', 'reconciliation_required')),
    CONSTRAINT payments_version_valid CHECK (version > 0),
    CONSTRAINT payments_lifecycle_valid CHECK ((state = 'pending' AND closed_at IS NULL) OR (state <> 'pending' AND closed_at IS NOT NULL)),
    CONSTRAINT payments_timestamps_valid CHECK (updated_at >= created_at)
);

ALTER TABLE carts ADD COLUMN converted_order_id uuid;
ALTER TABLE carts ADD CONSTRAINT carts_converted_order_fk FOREIGN KEY (store_id, converted_order_id) REFERENCES orders (store_id, order_id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE carts ADD CONSTRAINT carts_conversion_valid CHECK ((status = 'converted') = (converted_order_id IS NOT NULL)) NOT VALID;
