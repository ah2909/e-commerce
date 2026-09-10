-- +goose Up
CREATE TABLE catalog_categories (
    store_id uuid NOT NULL,
    category_id uuid NOT NULL,
    parent_id uuid,
    name text NOT NULL,
    slug text NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, category_id),
    CONSTRAINT catalog_categories_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT catalog_categories_parent_fk FOREIGN KEY (store_id, parent_id) REFERENCES catalog_categories (store_id, category_id),
    CONSTRAINT catalog_categories_not_self_parent CHECK (parent_id IS NULL OR parent_id <> category_id),
    CONSTRAINT catalog_categories_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT catalog_categories_slug_valid CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) <= 120),
    CONSTRAINT catalog_categories_status_valid CHECK (status IN ('active', 'archived')),
    CONSTRAINT catalog_categories_version_valid CHECK (version > 0),
    CONSTRAINT catalog_categories_timestamps_valid CHECK (updated_at >= created_at AND ((status = 'archived') = (archived_at IS NOT NULL)))
);

CREATE UNIQUE INDEX catalog_categories_store_slug_uidx ON catalog_categories (store_id, slug);
CREATE INDEX catalog_categories_store_parent_idx ON catalog_categories (store_id, parent_id, category_id);

CREATE TABLE catalog_products (
    store_id uuid NOT NULL,
    product_id uuid NOT NULL,
    name text NOT NULL,
    slug text NOT NULL,
    description text NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL,
    published_at timestamptz,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, product_id),
    CONSTRAINT catalog_products_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT catalog_products_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT catalog_products_slug_valid CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) <= 160),
    CONSTRAINT catalog_products_description_valid CHECK (char_length(description) <= 10000),
    CONSTRAINT catalog_products_status_valid CHECK (status IN ('draft', 'published', 'archived')),
    CONSTRAINT catalog_products_version_valid CHECK (version > 0),
    CONSTRAINT catalog_products_timestamps_valid CHECK (updated_at >= created_at),
    CONSTRAINT catalog_products_lifecycle_valid CHECK ((status = 'draft' AND published_at IS NULL AND archived_at IS NULL) OR (status = 'published' AND published_at IS NOT NULL AND archived_at IS NULL) OR (status = 'archived' AND archived_at IS NOT NULL))
);

CREATE UNIQUE INDEX catalog_products_store_slug_uidx ON catalog_products (store_id, slug);
CREATE INDEX catalog_products_store_updated_idx ON catalog_products (store_id, updated_at DESC, product_id DESC);
CREATE INDEX catalog_products_published_idx ON catalog_products (store_id, published_at DESC, product_id DESC) WHERE status = 'published';

CREATE TABLE catalog_variants (
    store_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    product_id uuid NOT NULL,
    sku text NOT NULL,
    barcode text,
    option_values jsonb NOT NULL,
    weight_grams bigint NOT NULL,
    length_mm bigint,
    width_mm bigint,
    height_mm bigint,
    status text NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, variant_id),
    CONSTRAINT catalog_variants_product_fk FOREIGN KEY (store_id, product_id) REFERENCES catalog_products (store_id, product_id),
    CONSTRAINT catalog_variants_sku_valid CHECK (sku = upper(btrim(sku)) AND char_length(sku) BETWEEN 1 AND 100),
    CONSTRAINT catalog_variants_barcode_valid CHECK (barcode IS NULL OR (barcode = btrim(barcode) AND char_length(barcode) BETWEEN 1 AND 100)),
    CONSTRAINT catalog_variants_store_sku_unique UNIQUE (store_id, sku) DEFERRABLE INITIALLY IMMEDIATE,
    CONSTRAINT catalog_variants_store_barcode_unique UNIQUE (store_id, barcode) DEFERRABLE INITIALLY IMMEDIATE,
    CONSTRAINT catalog_variants_options_valid CHECK (jsonb_typeof(option_values) = 'object' AND pg_column_size(option_values) <= 8192),
    CONSTRAINT catalog_variants_measurements_valid CHECK (weight_grams BETWEEN 0 AND 1000000000 AND ((length_mm IS NULL AND width_mm IS NULL AND height_mm IS NULL) OR (length_mm BETWEEN 0 AND 10000000 AND width_mm BETWEEN 0 AND 10000000 AND height_mm BETWEEN 0 AND 10000000))),
    CONSTRAINT catalog_variants_status_valid CHECK (status IN ('active', 'archived')),
    CONSTRAINT catalog_variants_version_valid CHECK (version > 0),
    CONSTRAINT catalog_variants_timestamps_valid CHECK (updated_at >= created_at AND ((status = 'archived') = (archived_at IS NOT NULL)))
);

CREATE INDEX catalog_variants_store_product_idx ON catalog_variants (store_id, product_id, variant_id);

CREATE TABLE catalog_product_categories (
    store_id uuid NOT NULL,
    product_id uuid NOT NULL,
    category_id uuid NOT NULL,
    PRIMARY KEY (store_id, product_id, category_id),
    CONSTRAINT catalog_product_categories_product_fk FOREIGN KEY (store_id, product_id) REFERENCES catalog_products (store_id, product_id),
    CONSTRAINT catalog_product_categories_category_fk FOREIGN KEY (store_id, category_id) REFERENCES catalog_categories (store_id, category_id)
);

CREATE INDEX catalog_product_categories_category_idx ON catalog_product_categories (store_id, category_id, product_id);

CREATE TABLE catalog_media (
    store_id uuid NOT NULL,
    media_id uuid NOT NULL,
    product_id uuid NOT NULL,
    object_key text NOT NULL,
    alt_text text NOT NULL,
    position integer NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    PRIMARY KEY (store_id, media_id),
    CONSTRAINT catalog_media_product_fk FOREIGN KEY (store_id, product_id) REFERENCES catalog_products (store_id, product_id),
    CONSTRAINT catalog_media_object_key_valid CHECK (object_key = btrim(object_key) AND char_length(object_key) BETWEEN 1 AND 512),
    CONSTRAINT catalog_media_alt_text_valid CHECK (alt_text = btrim(alt_text) AND char_length(alt_text) BETWEEN 1 AND 500),
    CONSTRAINT catalog_media_position_valid CHECK (position >= 0),
    CONSTRAINT catalog_media_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX catalog_media_product_position_uidx ON catalog_media (store_id, product_id, position) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX catalog_media_product_key_uidx ON catalog_media (store_id, product_id, object_key) WHERE archived_at IS NULL;
