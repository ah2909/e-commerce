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

func TestEventWorkersMigrationUpgradesPopulatedSchemaElevenAndRepeats(t *testing.T) {
	db, ctx, storeID, migrations := eventMigrationVersionEleven(t)
	eventID, _ := ids.NewEventID()
	aggregateID, _ := ids.NewOrderID()
	if _, err := db.ExecContext(ctx, `INSERT INTO outbox_events(store_id,event_id,event_type,schema_version,aggregate_type,aggregate_id,aggregate_version,occurred_at,correlation_id,payload,next_attempt_at) VALUES($1,$2,'order.created.v1',1,'order',$3,1,now(),'migration','{}',now())`, storeID, eventID, aggregateID); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 12); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 12)
	for _, table := range []string{"consumer_inbox", "event_dead_letters", "search_documents", "analytics_daily_counts", "notification_email_intents"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists=%t error=%v", table, exists, err)
		}
	}
	for _, constraint := range []string{"outbox_events_claim_valid", "outbox_events_requeue_valid", "consumer_inbox_outcome_valid", "event_dead_letters_replay_valid", "notification_email_intents_order_fk"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1 AND connamespace=current_schema()::regnamespace)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("constraint %s exists=%t error=%v", constraint, exists, err)
		}
	}
	var preserved int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_id=$2 AND claim_token IS NULL AND terminal_at IS NULL`, storeID, eventID).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("preserved=%d error=%v", preserved, err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 12); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 12)
}

func eventMigrationVersionEleven(t *testing.T) (*sql.DB, context.Context, ids.StoreID, string) {
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
	schema := "event_migration_" + strings.ReplaceAll(storeID.String(), "-", "")
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
	if err := goose.UpToContext(ctx, db, migrations, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1,$2,'Events','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, storeID, "events-"+storeID.String()); err != nil {
		t.Fatal(err)
	}
	return db, ctx, storeID, migrations
}
