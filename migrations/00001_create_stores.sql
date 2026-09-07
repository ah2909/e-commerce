-- +goose Up
CREATE TABLE stores (
    store_id uuid PRIMARY KEY,
    name text NOT NULL,
    status text NOT NULL,
    default_currency varchar(3) NOT NULL,
    locale text NOT NULL,
    timezone text NOT NULL,
    settings_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT stores_name_valid CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT stores_status_valid CHECK (status IN ('active', 'suspended')),
    CONSTRAINT stores_currency_valid CHECK (default_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT stores_locale_valid CHECK (char_length(locale) BETWEEN 2 AND 35),
    CONSTRAINT stores_timezone_valid CHECK (char_length(timezone) BETWEEN 1 AND 64),
    CONSTRAINT stores_settings_version_valid CHECK (settings_version > 0),
    CONSTRAINT stores_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE store_memberships (
    store_id uuid NOT NULL,
    user_id uuid NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_id, user_id),
    CONSTRAINT store_memberships_store_fk FOREIGN KEY (store_id) REFERENCES stores (store_id),
    CONSTRAINT store_memberships_status_valid CHECK (status IN ('active', 'inactive')),
    CONSTRAINT store_memberships_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX store_memberships_user_store_idx ON store_memberships (user_id, store_id);
