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

func TestOperationalRetentionMigrationAddsPartialIndexesAndRepeats(t *testing.T) {
	db, ctx, migrations := retentionMigrationVersionTwelve(t)
	if err := goose.UpToContext(ctx, db, migrations, 13); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 13)
	for _, index := range []string{"outbox_events_published_retention_idx", "event_dead_letters_resolved_retention_idx"} {
		var definition string
		if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1`, index).Scan(&definition); err != nil {
			t.Fatalf("index %s: %v", index, err)
		}
		if !strings.Contains(definition, "WHERE") {
			t.Fatalf("index %s is not partial: %s", index, definition)
		}
	}
	if err := goose.UpToContext(ctx, db, migrations, 13); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 13)
}

func TestOperationalRetentionMigrationAcceptsValidatedConcurrentPrestep(t *testing.T) {
	db, ctx, migrations := retentionMigrationVersionTwelve(t)
	for _, statement := range []string{
		`CREATE INDEX CONCURRENTLY outbox_events_published_retention_idx ON outbox_events (published_at, store_id, event_id) WHERE published_at IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY event_dead_letters_resolved_retention_idx ON event_dead_letters (resolved_at, dead_letter_id) WHERE resolved_at IS NOT NULL`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.UpToContext(ctx, db, migrations, 13); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 13)
}

func retentionMigrationVersionTwelve(t *testing.T) (*sql.DB, context.Context, string) {
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
	id, _ := ids.NewStoreID()
	schema := "retention_migration_" + strings.ReplaceAll(id.String(), "-", "")
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
	if err := goose.UpToContext(ctx, db, migrations, 12); err != nil {
		t.Fatal(err)
	}
	return db, ctx, migrations
}
