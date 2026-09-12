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

func TestInventoryMigrationUpgradesPopulatedVersionFourAndRepeats(t *testing.T) {
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
	storeID, _ := ids.NewStoreID()
	productID, _ := ids.NewProductID()
	variantID, _ := ids.NewVariantID()
	schema := "inventory_migration_" + strings.ReplaceAll(storeID.String(), "-", "")
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
	if err := goose.UpToContext(ctx, db, migrations, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1::uuid,'inventory-migration','Inventory','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, storeID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,'Existing','existing','existing','draft',1,now(),now())`, storeID.String(), productID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,'INVENTORY','{}',0,'active',1,now(),now())`, storeID.String(), variantID.String(), productID.String()); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 5)
	assertInventoryTables(t, ctx, db)
	if err := goose.UpContext(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 5)
}

func assertInventoryTables(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"inventory_stock_items", "inventory_reservations", "inventory_ledger_entries"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var definition string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(c.oid,true) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND c.conname='inventory_stock_items_variant_fk'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if definition != "FOREIGN KEY (store_id, variant_id) REFERENCES catalog_variants(store_id, variant_id)" {
		t.Fatalf("variant FK = %q", definition)
	}
	var immutable bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_class r ON r.oid=t.tgrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname=current_schema() AND r.relname='inventory_ledger_entries' AND t.tgname='inventory_ledger_entries_immutable' AND NOT t.tgisinternal)`).Scan(&immutable); err != nil || !immutable {
		t.Fatalf("immutable ledger trigger = %v, error = %v", immutable, err)
	}
}
