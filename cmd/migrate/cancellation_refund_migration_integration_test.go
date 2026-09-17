//go:build integration

package main

import (
	"bytes"
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

func TestCancellationRefundMigrationUpgradesPopulatedVersionNineAndRepeats(t *testing.T) {
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
	schema := "refund_migration_" + strings.ReplaceAll(storeID.String(), "-", "")
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
	if err := goose.UpToContext(ctx, db, migrations, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1,'refund-migration','Refund','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, storeID.String()); err != nil {
		t.Fatal(err)
	}
	ciphertext := bytes.Repeat([]byte{0x42}, 32)
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_provider_configs(store_id,provider,bank_bin,account_last4,public_identifier,ciphertext,nonce,key_version,enabled,version,created_at,updated_at) VALUES($1,'momo','','','MO***ST',$2,$3,1,true,1,now(),now())`, storeID.String(), ciphertext, bytes.Repeat([]byte{0x24}, 12)); err != nil {
		t.Fatal(err)
	}
	setPaymentTriggers(t, ctx, db, false)
	insertSuccessfulMoMoAttempt(t, ctx, db, storeID)
	insertPaidMoMoReceipt(t, ctx, db, storeID, "4088878653")
	setPaymentTriggers(t, ctx, db, true)
	if err := goose.UpToContext(ctx, db, migrations, 10); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 10)
	for _, table := range []string{"refunds", "order_cancellations"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	for _, index := range []string{"payment_attempts_momo_transaction_unique", "refunds_provider_refund_transaction_unique"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s exists = %v, error = %v", index, exists, err)
		}
	}
	var preserved []byte
	if err := db.QueryRowContext(ctx, `SELECT ciphertext FROM payment_provider_configs WHERE store_id=$1 AND provider='momo'`, storeID.String()).Scan(&preserved); err != nil || !bytes.Equal(preserved, ciphertext) {
		t.Fatalf("MoMo configuration changed: ciphertext=%x error=%v", preserved, err)
	}
	var providerTransactionID int64
	if err := db.QueryRowContext(ctx, `SELECT provider_transaction_id FROM payment_attempts WHERE store_id=$1 AND attempt_id='11111111-1111-4111-8111-111111111111'`, storeID.String()).Scan(&providerTransactionID); err != nil || providerTransactionID != 4088878653 {
		t.Fatalf("provider transaction backfill = %d, error = %v", providerTransactionID, err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 10); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 10)
	if err := goose.DownToContext(ctx, db, migrations, 9); err != nil {
		t.Fatalf("migration 10 down parse/apply: %v", err)
	}
	assertMigrationVersion(t, ctx, db, 9)
	if err := goose.UpToContext(ctx, db, migrations, 10); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 10)
}

func TestCancellationRefundMigrationRejectsMissingOrAmbiguousMoMoIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		receipts []string
	}{{name: "missing"}, {name: "ambiguous", receipts: []string{"4088878653", "4088878654"}}} {
		t.Run(test.name, func(t *testing.T) {
			db, ctx, storeID, migrations := refundMigrationVersionNine(t)
			setPaymentTriggers(t, ctx, db, false)
			insertSuccessfulMoMoAttempt(t, ctx, db, storeID)
			for _, receipt := range test.receipts {
				insertPaidMoMoReceipt(t, ctx, db, storeID, receipt)
			}
			setPaymentTriggers(t, ctx, db, true)
			if err := goose.UpToContext(ctx, db, migrations, 10); err == nil {
				t.Fatal("migration 10 accepted an invalid successful MoMo identity")
			}
			assertMigrationVersion(t, ctx, db, 9)
		})
	}
}

func setPaymentTriggers(t *testing.T, ctx context.Context, db *sql.DB, enabled bool) {
	t.Helper()
	action := "DISABLE"
	if enabled {
		action = "ENABLE"
	}
	for _, table := range []string{"payment_attempts", "payment_webhook_receipts"} {
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" "+action+" TRIGGER ALL"); err != nil {
			t.Fatal(err)
		}
	}
}

func insertSuccessfulMoMoAttempt(t *testing.T, ctx context.Context, db *sql.DB, storeID ids.StoreID) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_attempts(store_id,attempt_id,payment_id,order_id,provider,idempotency_key,request_fingerprint,transfer_reference,amount_minor,currency,provider_config_version,instruction_ciphertext,instruction_nonce,instruction_key_version,state,provider_result_code,expires_at,created_at,updated_at) VALUES($1,'11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333','momo','migration-attempt',decode(repeat('42',32),'hex'),'MM0123456789abcdef0123456789abcdef',1000,'VND',1,decode(repeat('42',32),'hex'),decode(repeat('24',12),'hex'),1,'succeeded',0,now()+interval '1 hour',now(),now())`, storeID.String()); err != nil {
		t.Fatal(err)
	}
}

func insertPaidMoMoReceipt(t *testing.T, ctx context.Context, db *sql.DB, storeID ids.StoreID, providerEventID string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO payment_webhook_receipts(store_id,provider,provider_event_id,attempt_id,payment_id,body_digest,result_code,outcome,received_at,created_at) VALUES($1,'momo',$2,'11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',decode(repeat('33',32),'hex'),0,'paid',now(),now())`, storeID.String(), providerEventID); err != nil {
		t.Fatal(err)
	}
}

func refundMigrationVersionNine(t *testing.T) (*sql.DB, context.Context, ids.StoreID, string) {
	t.Helper()
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
	schema := "refund_invalid_" + strings.ReplaceAll(storeID.String(), "-", "")
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
	migrations := filepath.Join("..", "..", "migrations")
	if err := goose.UpToContext(ctx, db, migrations, 9); err != nil {
		t.Fatal(err)
	}
	return db, ctx, storeID, migrations
}
