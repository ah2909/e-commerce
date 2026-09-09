package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(os.Args[1:], os.LookupEnv); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string, lookup func(string) (string, bool)) error {
	if len(arguments) != 1 {
		return errors.New("usage: migrate up|status|test-up|test-status|test-reset")
	}
	command := arguments[0]
	databaseVariable := "DATABASE_URL"
	var testConfig *pgx.ConnConfig
	if command == "test-up" || command == "test-status" || command == "test-reset" {
		databaseVariable = "TEST_DATABASE_URL"
		var err error
		testConfig, err = databaseguard.Parse(value(lookup, databaseVariable), value(lookup, "ALLOW_TEST_DATABASE_MUTATION"))
		if err != nil {
			return err
		}
		command = strings.TrimPrefix(command, "test-")
	}
	if command != "up" && command != "status" && command != "reset" {
		return errors.New("usage: migrate up|status|test-up|test-status|test-reset")
	}
	databaseURL, ok := lookup(databaseVariable)
	if !ok || databaseURL == "" {
		return fmt.Errorf("%s is required", databaseVariable)
	}
	var database *sql.DB
	if testConfig != nil {
		database = stdlib.OpenDB(*testConfig)
	} else {
		var err error
		database, err = sql.Open("pgx", databaseURL)
		if err != nil {
			return errors.New("open migration database: invalid connection configuration")
		}
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure migration dialect: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch command {
	case "reset":
		if _, err := database.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			return fmt.Errorf("reset test database schema: %w", err)
		}
	case "up":
		var locked bool
		if err := database.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(7392152401)`).Scan(&locked); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if !locked {
			return errors.New("another migration process holds the database lock")
		}
		defer func() {
			unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer unlockCancel()
			_, _ = database.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(7392152401)`)
		}()
		if err := goose.UpContext(ctx, database, "migrations"); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
	case "status":
		if err := goose.StatusContext(ctx, database, "migrations"); err != nil {
			return fmt.Errorf("migration status: %w", err)
		}
	}
	return nil
}

func value(lookup func(string) (string, bool), key string) string {
	value, _ := lookup(key)
	return value
}
