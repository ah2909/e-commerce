//go:build integration

package main

import (
	"bytes"
	"context"
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

func TestMoMoMigrationUpgradesPopulatedVersionEightAndRepeats(t *testing.T) {
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
	schema := "momo_migration_" + strings.ReplaceAll(storeID.String(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	connectionConfig := poolConfig.ConnConfig.Copy()
	connectionConfig.RuntimeParams = map[string]string{"search_path": schema}
	db := stdlib.OpenDB(*connectionConfig)
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrations := filepath.Join("..", "..", "migrations")
	if err := goose.UpToContext(ctx, db, migrations, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1,'momo-migration','MoMo','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, storeID.String()); err != nil {
		t.Fatal(err)
	}
	ciphertext := bytes.Repeat([]byte{0x42}, 32)
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_provider_configs(store_id,provider,bank_bin,account_last4,ciphertext,nonce,key_version,enabled,version,created_at,updated_at) VALUES($1,'vietqr','970436','2738',$2,$3,1,true,1,now(),now())`, storeID.String(), ciphertext, bytes.Repeat([]byte{0x24}, 12)); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 9); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 9)
	var preserved []byte
	var identifierValue string
	if err := db.QueryRowContext(ctx, `SELECT ciphertext,public_identifier FROM payment_provider_configs WHERE store_id=$1 AND provider='vietqr'`, storeID.String()).Scan(&preserved, &identifierValue); err != nil || !bytes.Equal(preserved, ciphertext) || identifierValue != "" {
		t.Fatalf("VietQR row changed: ciphertext=%x identifier=%q error=%v", preserved, identifierValue, err)
	}
	for _, table := range []string{"payment_callback_routes", "payment_webhook_receipts"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_provider_configs(store_id,provider,bank_bin,account_last4,public_identifier,ciphertext,nonce,key_version,enabled,version,created_at,updated_at) VALUES($1,'momo','','','MO***ST',$2,$3,1,true,1,now(),now())`, storeID.String(), ciphertext, bytes.Repeat([]byte{0x24}, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE payment_provider_configs SET bank_bin='970436' WHERE store_id=$1 AND provider='momo'`, storeID.String()); err == nil {
		t.Fatal("MoMo provider shape accepted VietQR bank fields")
	}
	if err := goose.UpToContext(ctx, db, migrations, 9); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 9)
	if _, err := db.ExecContext(ctx, `DELETE FROM payment_provider_configs WHERE store_id=$1 AND provider='momo'`, storeID.String()); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, db, migrations, 8); err != nil {
		t.Fatalf("migration 9 down parse/apply: %v", err)
	}
	assertMigrationVersion(t, ctx, db, 8)
	if err := goose.UpToContext(ctx, db, migrations, 9); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 9)
}
