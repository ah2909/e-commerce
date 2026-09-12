-- +goose Up
CREATE TABLE customers (
    store_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    user_id uuid NOT NULL,
    full_name text NOT NULL,
    email text NOT NULL,
    canonical_email text NOT NULL,
    phone text NOT NULL,
    canonical_phone text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, customer_id),
    CONSTRAINT customers_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT customers_user_fk FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id),
    CONSTRAINT customers_user_unique UNIQUE (store_id, user_id),
    CONSTRAINT customers_name_valid CHECK (full_name = btrim(full_name) AND char_length(full_name) BETWEEN 1 AND 160),
    CONSTRAINT customers_email_valid CHECK (email = btrim(email) AND char_length(email) BETWEEN 3 AND 254 AND canonical_email = lower(btrim(email))),
    CONSTRAINT customers_phone_valid CHECK (phone = btrim(phone) AND char_length(phone) BETWEEN 8 AND 20 AND canonical_phone ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT customers_version_valid CHECK (version > 0),
    CONSTRAINT customers_timestamps_valid CHECK (updated_at >= created_at AND (archived_at IS NULL OR archived_at >= created_at))
);

CREATE INDEX customers_contact_idx ON customers (store_id, canonical_email, canonical_phone) WHERE archived_at IS NULL;

CREATE TABLE customer_addresses (
    store_id uuid NOT NULL,
    address_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    label text NOT NULL,
    recipient_name text NOT NULL,
    phone text NOT NULL,
    canonical_phone text NOT NULL,
    line1 text NOT NULL,
    line2 text NOT NULL,
    ward text NOT NULL,
    district text NOT NULL,
    province text NOT NULL,
    postal_code text NOT NULL,
    country_code text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, address_id),
    CONSTRAINT customer_addresses_customer_fk FOREIGN KEY (store_id, customer_id) REFERENCES customers (store_id, customer_id),
    CONSTRAINT customer_addresses_text_valid CHECK (label = btrim(label) AND char_length(label) BETWEEN 1 AND 80 AND recipient_name = btrim(recipient_name) AND char_length(recipient_name) BETWEEN 1 AND 160 AND line1 = btrim(line1) AND char_length(line1) BETWEEN 1 AND 240 AND line2 = btrim(line2) AND char_length(line2) <= 240 AND ward = btrim(ward) AND char_length(ward) BETWEEN 1 AND 120 AND district = btrim(district) AND char_length(district) BETWEEN 1 AND 120 AND province = btrim(province) AND char_length(province) BETWEEN 1 AND 120 AND postal_code = btrim(postal_code) AND char_length(postal_code) <= 20),
    CONSTRAINT customer_addresses_phone_valid CHECK (phone = btrim(phone) AND char_length(phone) BETWEEN 8 AND 20 AND canonical_phone ~ '^\+[1-9][0-9]{7,14}$'),
    CONSTRAINT customer_addresses_country_valid CHECK (country_code = 'VN'),
    CONSTRAINT customer_addresses_version_valid CHECK (version > 0),
    CONSTRAINT customer_addresses_timestamps_valid CHECK (updated_at >= created_at AND (archived_at IS NULL OR archived_at >= created_at))
);

CREATE INDEX customer_addresses_customer_idx ON customer_addresses (store_id, customer_id, created_at DESC, address_id DESC) WHERE archived_at IS NULL;

CREATE TABLE carts (
    store_id uuid NOT NULL,
    cart_id uuid NOT NULL,
    owner_kind text NOT NULL,
    user_id uuid,
    guest_id uuid,
    currency text NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    closed_at timestamptz,
    PRIMARY KEY (store_id, cart_id),
    CONSTRAINT carts_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT carts_user_fk FOREIGN KEY (store_id, user_id) REFERENCES users (store_id, user_id),
    CONSTRAINT carts_guest_fk FOREIGN KEY (store_id, guest_id) REFERENCES identity_guests (store_id, guest_id),
    CONSTRAINT carts_owner_valid CHECK ((owner_kind = 'user' AND user_id IS NOT NULL AND guest_id IS NULL) OR (owner_kind = 'guest' AND guest_id IS NOT NULL AND user_id IS NULL)),
    CONSTRAINT carts_currency_valid CHECK (currency = 'VND'),
    CONSTRAINT carts_status_valid CHECK (status IN ('active', 'converted', 'expired', 'abandoned')),
    CONSTRAINT carts_version_valid CHECK (version > 0),
    CONSTRAINT carts_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT carts_lifecycle_valid CHECK ((status = 'active' AND closed_at IS NULL) OR (status <> 'active' AND closed_at IS NOT NULL)),
    CONSTRAINT carts_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX carts_active_user_uidx ON carts (store_id, user_id, currency) WHERE status = 'active' AND user_id IS NOT NULL;
CREATE UNIQUE INDEX carts_active_guest_uidx ON carts (store_id, guest_id, currency) WHERE status = 'active' AND guest_id IS NOT NULL;

CREATE INDEX carts_active_expiry_idx ON carts (expires_at, store_id, cart_id) WHERE status = 'active';

CREATE TABLE cart_lines (
    store_id uuid NOT NULL,
    cart_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    quantity bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, cart_id, variant_id),
    CONSTRAINT cart_lines_cart_fk FOREIGN KEY (store_id, cart_id) REFERENCES carts (store_id, cart_id),
    CONSTRAINT cart_lines_variant_fk FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants (store_id, variant_id),
    CONSTRAINT cart_lines_quantity_valid CHECK (quantity BETWEEN 1 AND 2147483647),
    CONSTRAINT cart_lines_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX cart_lines_cart_idx ON cart_lines (store_id, cart_id, created_at, variant_id);
