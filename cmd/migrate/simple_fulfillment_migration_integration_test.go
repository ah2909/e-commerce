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

func TestSimpleFulfillmentMigrationUpgradesPopulatedSchemaTenAndRepeats(t *testing.T) {
	db, ctx, storeID, userID, migrations := fulfillmentMigrationVersionTen(t, true)
	if _, err := db.ExecContext(ctx, `INSERT INTO store_membership_permission_sets(store_id,user_id,version,updated_at) VALUES($1,$2,1,now())`, storeID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO store_membership_permissions(store_id,user_id,permission,version,created_at,updated_at) VALUES($1,$2,'order.cancel',1,now(),now())`, storeID, userID); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 11); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 11)
	for _, table := range []string{"shipments", "shipment_commands"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	for _, constraint := range []string{"shipments_order_unique", "shipments_identity_unique", "shipments_timestamps_valid", "shipment_commands_fingerprint_valid", "shipment_commands_shipment_order_fk"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1 AND connamespace=current_schema()::regnamespace)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("constraint %s exists = %v, error = %v", constraint, exists, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO store_membership_permissions(store_id,user_id,permission,version,created_at,updated_at) VALUES($1,$2,'order.fulfill',1,now(),now())`, storeID, userID); err != nil {
		t.Fatalf("new permission rejected: %v", err)
	}
	var preserved int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM store_membership_permissions WHERE store_id=$1 AND user_id=$2 AND permission='order.cancel'`, storeID, userID).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("existing permission count = %d, error = %v", preserved, err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 11); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 11)
}

func TestSimpleFulfillmentMigrationUpgradesEmptySchemaTen(t *testing.T) {
	db, ctx, _, _, migrations := fulfillmentMigrationVersionTen(t, false)
	if err := goose.UpToContext(ctx, db, migrations, 11); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 11)
}

func fulfillmentMigrationVersionTen(t *testing.T, populate bool) (*sql.DB, context.Context, ids.StoreID, ids.UserID, string) {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
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
	userID, _ := ids.NewUserID()
	schema := "fulfillment_migration_" + strings.ReplaceAll(storeID.String(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	connectionConfig := poolConfig.ConnConfig.Copy()
	connectionConfig.RuntimeParams = map[string]string{"search_path": schema}
	db := stdlib.OpenDB(*connectionConfig)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	migrations := filepath.Join("..", "..", "migrations")
	if err := goose.UpToContext(ctx, db, migrations, 10); err != nil {
		t.Fatal(err)
	}
	if !populate {
		return db, ctx, storeID, userID, migrations
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1,$2,'Fulfillment','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, storeID, "fulfillment-"+storeID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1,$2,'fulfillment@example.com','fulfillment@example.com','active',now(),now())`, storeID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO store_memberships(store_id,user_id,status,role,created_at,updated_at) VALUES($1,$2,'active','staff',now(),now())`, storeID, userID); err != nil {
		t.Fatal(err)
	}
	return db, ctx, storeID, userID, migrations
}
