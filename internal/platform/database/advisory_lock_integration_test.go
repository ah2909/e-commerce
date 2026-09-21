//go:build integration

package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPaymentKeyWriterLockBlocksRetirementUntilWriterDrains(t *testing.T) {
	config, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	writer := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = writer.Close() })
	retirer := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = retirer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writerLock, err := database.HoldPaymentKeyWriterLock(ctx, writer, 17)
	if err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := database.TryPaymentKeyRetirementLock(ctx, retirer, 17); err != nil || acquired {
		t.Fatalf("retirement lock acquired=%t error=%v", acquired, err)
	}
	if err := writerLock.Release(ctx); err != nil {
		t.Fatal(err)
	}
	retirementLock, acquired, err := database.TryPaymentKeyRetirementLock(ctx, retirer, 17)
	if err != nil || !acquired {
		t.Fatalf("retirement lock acquired=%t error=%v", acquired, err)
	}
	if err := retirementLock.Release(ctx); err != nil {
		t.Fatal(err)
	}
}
