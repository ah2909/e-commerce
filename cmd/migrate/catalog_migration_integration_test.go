//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestPricingMigrationUpgradesPopulatedVersionThreeAndRepeats(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(poolConfig.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	id, _ := ids.NewStoreID()
	productID, _ := ids.NewProductID()
	variantID, _ := ids.NewVariantID()
	schema := "catalog_migration_" + strings.ReplaceAll(id.String(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	connectionConfig := poolConfig.ConnConfig.Copy()
	if connectionConfig.RuntimeParams == nil {
		connectionConfig.RuntimeParams = map[string]string{}
	}
	connectionConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*connectionConfig)
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrations := filepath.Join("..", "..", "migrations")
	if err := goose.UpToContext(ctx, db, migrations, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores (store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES ($1::uuid,'populated-v2','Populated','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_products (store_id,product_id,name,slug,description,status,version,created_at,updated_at) VALUES ($1::uuid,$2::uuid,'Existing','existing','existing','draft',1,now(),now())`, id.String(), productID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_variants (store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,'EXISTING','{}',0,'active',1,now(),now())`, id.String(), variantID.String(), productID.String()); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 4); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 4)
	var stores int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM stores WHERE store_id=$1::uuid`, id.String()).Scan(&stores); err != nil || stores != 1 {
		t.Fatalf("populated store count = %d, error = %v", stores, err)
	}
	assertCatalogSchemaShape(t, ctx, db)
	assertPricingPublicationSchemaShape(t, ctx, db)
	if err := goose.UpToContext(ctx, db, migrations, 4); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 4)
}

func assertPricingPublicationSchemaShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"pricing_price_lists", "pricing_prices", "outbox_events"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	wantConstraints := map[string]string{
		"outbox_events_aggregate_valid":          "CHECK (aggregate_type = btrim(aggregate_type) AND char_length(aggregate_type) >= 1 AND char_length(aggregate_type) <= 80)",
		"outbox_events_aggregate_version_unique": "UNIQUE (store_id, aggregate_type, aggregate_id, aggregate_version, event_type)",
		"outbox_events_attempts_valid":           "CHECK (attempts >= 0)",
		"outbox_events_correlation_valid":        "CHECK (correlation_id = btrim(correlation_id) AND char_length(correlation_id) >= 1 AND char_length(correlation_id) <= 200)",
		"outbox_events_payload_valid":            "CHECK (jsonb_typeof(payload) = 'object'::text AND pg_column_size(payload) <= 16384)",
		"outbox_events_pkey":                     "PRIMARY KEY (store_id, event_id)",
		"outbox_events_schema_valid":             "CHECK (schema_version > 0 AND aggregate_version > 0)",
		"outbox_events_store_fk":                 "FOREIGN KEY (store_id) REFERENCES stores(store_id)",
		"outbox_events_type_valid":               "CHECK (event_type = btrim(event_type) AND char_length(event_type) >= 1 AND char_length(event_type) <= 160)",
		"pricing_price_lists_channel_valid":      "CHECK (channel = 'storefront'::text)",
		"pricing_price_lists_currency_valid":     "CHECK (currency::text = 'VND'::text)",
		"pricing_price_lists_lifecycle_valid":    "CHECK (status = 'active'::text AND archived_at IS NULL OR status = 'archived'::text AND archived_at IS NOT NULL)",
		"pricing_price_lists_name_valid":         "CHECK (name = btrim(name) AND char_length(name) >= 1 AND char_length(name) <= 120)",
		"pricing_price_lists_pkey":               "PRIMARY KEY (store_id, price_list_id)",
		"pricing_price_lists_priority_valid":     "CHECK (priority >= '-1000000'::integer AND priority <= 1000000)",
		"pricing_price_lists_status_valid":       "CHECK (status = ANY (ARRAY['active'::text, 'archived'::text]))",
		"pricing_price_lists_store_fk":           "FOREIGN KEY (store_id) REFERENCES stores(store_id)",
		"pricing_price_lists_timestamps_valid":   "CHECK (updated_at >= created_at)",
		"pricing_price_lists_version_valid":      "CHECK (version > 0)",
		"pricing_price_lists_window_valid":       "CHECK (valid_from IS NULL OR valid_until IS NULL OR valid_from < valid_until)",
		"pricing_prices_amount_valid":            "CHECK (amount_minor >= 0)",
		"pricing_prices_list_fk":                 "FOREIGN KEY (store_id, price_list_id) REFERENCES pricing_price_lists(store_id, price_list_id)",
		"pricing_prices_pkey":                    "PRIMARY KEY (store_id, price_list_id, variant_id)",
		"pricing_prices_timestamps_valid":        "CHECK (updated_at >= created_at)",
		"pricing_prices_variant_fk":              "FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants(store_id, variant_id)",
		"pricing_prices_version_valid":           "CHECK (version > 0)",
	}
	actualConstraints := readDefinitions(t, ctx, db, `SELECT c.conname,pg_get_constraintdef(c.oid,true) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND r.relname IN ('pricing_price_lists','pricing_prices','outbox_events') AND c.contype <> 'n'`)
	assertDefinitions(t, "pricing constraints", wantConstraints, actualConstraints)
	wantIndexes := map[string]string{
		"outbox_events_aggregate_version_unique": "CREATE UNIQUE INDEX outbox_events_aggregate_version_unique ON outbox_events USING btree (store_id, aggregate_type, aggregate_id, aggregate_version, event_type)",
		"outbox_events_pkey":                     "CREATE UNIQUE INDEX outbox_events_pkey ON outbox_events USING btree (store_id, event_id)",
		"outbox_events_unpublished_idx":          "CREATE INDEX outbox_events_unpublished_idx ON outbox_events USING btree (next_attempt_at, occurred_at, store_id, event_id) WHERE (published_at IS NULL)",
		"pricing_price_lists_current_idx":        "CREATE INDEX pricing_price_lists_current_idx ON pricing_price_lists USING btree (store_id, channel, currency, priority DESC, price_list_id) WHERE (status = 'active'::text)",
		"pricing_price_lists_pkey":               "CREATE UNIQUE INDEX pricing_price_lists_pkey ON pricing_price_lists USING btree (store_id, price_list_id)",
		"pricing_price_lists_updated_idx":        "CREATE INDEX pricing_price_lists_updated_idx ON pricing_price_lists USING btree (store_id, updated_at DESC, price_list_id DESC)",
		"pricing_prices_pkey":                    "CREATE UNIQUE INDEX pricing_prices_pkey ON pricing_prices USING btree (store_id, price_list_id, variant_id)",
		"pricing_prices_variant_idx":             "CREATE INDEX pricing_prices_variant_idx ON pricing_prices USING btree (store_id, variant_id, price_list_id)",
	}
	actualIndexes := readDefinitions(t, ctx, db, `SELECT indexname,replace(indexdef,current_schema() || '.','') FROM pg_indexes WHERE schemaname=current_schema() AND tablename IN ('pricing_price_lists','pricing_prices','outbox_events')`)
	assertDefinitions(t, "pricing indexes", wantIndexes, actualIndexes)
}

func assertMigrationVersion(t *testing.T, ctx context.Context, db *sql.DB, want int64) {
	t.Helper()
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != want {
		t.Fatalf("schema version = %d, error = %v", version, err)
	}
}

func assertCatalogSchemaShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	wantConstraints := map[string]string{
		"catalog_categories_name_valid":          "CHECK (name = btrim(name) AND char_length(name) >= 1 AND char_length(name) <= 120)",
		"catalog_categories_not_self_parent":     "CHECK (parent_id IS NULL OR parent_id <> category_id)",
		"catalog_categories_parent_fk":           "FOREIGN KEY (store_id, parent_id) REFERENCES catalog_categories(store_id, category_id)",
		"catalog_categories_pkey":                "PRIMARY KEY (store_id, category_id)",
		"catalog_categories_slug_valid":          "CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'::text AND char_length(slug) <= 120)",
		"catalog_categories_status_valid":        "CHECK (status = ANY (ARRAY['active'::text, 'archived'::text]))",
		"catalog_categories_store_fk":            "FOREIGN KEY (store_id) REFERENCES stores(store_id)",
		"catalog_categories_timestamps_valid":    "CHECK (updated_at >= created_at AND (status = 'archived'::text) = (archived_at IS NOT NULL))",
		"catalog_categories_version_valid":       "CHECK (version > 0)",
		"catalog_media_alt_text_valid":           "CHECK (alt_text = btrim(alt_text) AND char_length(alt_text) >= 1 AND char_length(alt_text) <= 500)",
		"catalog_media_object_key_valid":         "CHECK (object_key = btrim(object_key) AND char_length(object_key) >= 1 AND char_length(object_key) <= 512)",
		"catalog_media_pkey":                     "PRIMARY KEY (store_id, media_id)",
		"catalog_media_position_valid":           "CHECK (\"position\" >= 0)",
		"catalog_media_product_fk":               "FOREIGN KEY (store_id, product_id) REFERENCES catalog_products(store_id, product_id)",
		"catalog_media_timestamps_valid":         "CHECK (updated_at >= created_at)",
		"catalog_product_categories_category_fk": "FOREIGN KEY (store_id, category_id) REFERENCES catalog_categories(store_id, category_id)",
		"catalog_product_categories_pkey":        "PRIMARY KEY (store_id, product_id, category_id)",
		"catalog_product_categories_product_fk":  "FOREIGN KEY (store_id, product_id) REFERENCES catalog_products(store_id, product_id)",
		"catalog_products_description_valid":     "CHECK (char_length(description) <= 10000)",
		"catalog_products_lifecycle_valid":       "CHECK (status = 'draft'::text AND published_at IS NULL AND archived_at IS NULL OR status = 'published'::text AND published_at IS NOT NULL AND archived_at IS NULL OR status = 'archived'::text AND archived_at IS NOT NULL)",
		"catalog_products_name_valid":            "CHECK (name = btrim(name) AND char_length(name) >= 1 AND char_length(name) <= 200)",
		"catalog_products_pkey":                  "PRIMARY KEY (store_id, product_id)",
		"catalog_products_slug_valid":            "CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'::text AND char_length(slug) <= 160)",
		"catalog_products_status_valid":          "CHECK (status = ANY (ARRAY['draft'::text, 'published'::text, 'archived'::text]))",
		"catalog_products_store_fk":              "FOREIGN KEY (store_id) REFERENCES stores(store_id)",
		"catalog_products_timestamps_valid":      "CHECK (updated_at >= created_at)",
		"catalog_products_version_valid":         "CHECK (version > 0)",
		"catalog_variants_barcode_valid":         "CHECK (barcode IS NULL OR barcode = btrim(barcode) AND char_length(barcode) >= 1 AND char_length(barcode) <= 100)",
		"catalog_variants_measurements_valid":    "CHECK (weight_grams >= 0 AND weight_grams <= 1000000000 AND (length_mm IS NULL AND width_mm IS NULL AND height_mm IS NULL OR length_mm >= 0 AND length_mm <= 10000000 AND width_mm >= 0 AND width_mm <= 10000000 AND height_mm >= 0 AND height_mm <= 10000000))",
		"catalog_variants_options_valid":         "CHECK (jsonb_typeof(option_values) = 'object'::text AND pg_column_size(option_values) <= 8192)",
		"catalog_variants_pkey":                  "PRIMARY KEY (store_id, variant_id)",
		"catalog_variants_product_fk":            "FOREIGN KEY (store_id, product_id) REFERENCES catalog_products(store_id, product_id)",
		"catalog_variants_sku_valid":             "CHECK (sku = upper(btrim(sku)) AND char_length(sku) >= 1 AND char_length(sku) <= 100)",
		"catalog_variants_status_valid":          "CHECK (status = ANY (ARRAY['active'::text, 'archived'::text]))",
		"catalog_variants_store_barcode_unique":  "UNIQUE (store_id, barcode) DEFERRABLE",
		"catalog_variants_store_sku_unique":      "UNIQUE (store_id, sku) DEFERRABLE",
		"catalog_variants_timestamps_valid":      "CHECK (updated_at >= created_at AND (status = 'archived'::text) = (archived_at IS NOT NULL))",
		"catalog_variants_version_valid":         "CHECK (version > 0)",
	}
	actualConstraints := readDefinitions(t, ctx, db, `SELECT c.conname, pg_get_constraintdef(c.oid, true) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND r.relname LIKE 'catalog_%' AND c.contype <> 'n'`)
	assertDefinitions(t, "constraints", wantConstraints, actualConstraints)

	wantIndexes := map[string]string{
		"catalog_categories_pkey":                 "CREATE UNIQUE INDEX catalog_categories_pkey ON catalog_categories USING btree (store_id, category_id)",
		"catalog_categories_store_parent_idx":     "CREATE INDEX catalog_categories_store_parent_idx ON catalog_categories USING btree (store_id, parent_id, category_id)",
		"catalog_categories_store_slug_uidx":      "CREATE UNIQUE INDEX catalog_categories_store_slug_uidx ON catalog_categories USING btree (store_id, slug)",
		"catalog_media_pkey":                      "CREATE UNIQUE INDEX catalog_media_pkey ON catalog_media USING btree (store_id, media_id)",
		"catalog_media_product_key_uidx":          "CREATE UNIQUE INDEX catalog_media_product_key_uidx ON catalog_media USING btree (store_id, product_id, object_key) WHERE (archived_at IS NULL)",
		"catalog_media_product_position_uidx":     "CREATE UNIQUE INDEX catalog_media_product_position_uidx ON catalog_media USING btree (store_id, product_id, \"position\") WHERE (archived_at IS NULL)",
		"catalog_product_categories_category_idx": "CREATE INDEX catalog_product_categories_category_idx ON catalog_product_categories USING btree (store_id, category_id, product_id)",
		"catalog_product_categories_pkey":         "CREATE UNIQUE INDEX catalog_product_categories_pkey ON catalog_product_categories USING btree (store_id, product_id, category_id)",
		"catalog_products_pkey":                   "CREATE UNIQUE INDEX catalog_products_pkey ON catalog_products USING btree (store_id, product_id)",
		"catalog_products_published_idx":          "CREATE INDEX catalog_products_published_idx ON catalog_products USING btree (store_id, published_at DESC, product_id DESC) WHERE (status = 'published'::text)",
		"catalog_products_store_slug_uidx":        "CREATE UNIQUE INDEX catalog_products_store_slug_uidx ON catalog_products USING btree (store_id, slug)",
		"catalog_products_store_updated_idx":      "CREATE INDEX catalog_products_store_updated_idx ON catalog_products USING btree (store_id, updated_at DESC, product_id DESC)",
		"catalog_variants_pkey":                   "CREATE UNIQUE INDEX catalog_variants_pkey ON catalog_variants USING btree (store_id, variant_id)",
		"catalog_variants_store_barcode_unique":   "CREATE UNIQUE INDEX catalog_variants_store_barcode_unique ON catalog_variants USING btree (store_id, barcode)",
		"catalog_variants_store_product_idx":      "CREATE INDEX catalog_variants_store_product_idx ON catalog_variants USING btree (store_id, product_id, variant_id)",
		"catalog_variants_store_sku_unique":       "CREATE UNIQUE INDEX catalog_variants_store_sku_unique ON catalog_variants USING btree (store_id, sku)",
	}
	actualIndexes := readDefinitions(t, ctx, db, `SELECT indexname,replace(indexdef, current_schema() || '.', '') FROM pg_indexes WHERE schemaname=current_schema() AND tablename LIKE 'catalog_%'`)
	assertDefinitions(t, "indexes", wantIndexes, actualIndexes)
}

func readDefinitions(t *testing.T, ctx context.Context, db *sql.DB, query string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	definitions := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		definitions[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return definitions
}

func assertDefinitions(t *testing.T, kind string, want, actual map[string]string) {
	t.Helper()
	if len(actual) != len(want) {
		t.Fatalf("%s count = %d, want %d: %#v", kind, len(actual), len(want), actual)
	}
	for name, definition := range want {
		if actual[name] != definition {
			t.Fatalf("%s %s = %q, want %q", kind, name, actual[name], definition)
		}
	}
}
