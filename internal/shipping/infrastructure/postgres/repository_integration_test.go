//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestShippingRuleReplacementIsTenantSafeAndOptimistic(t *testing.T) {
	pool := shippingPool(t)
	pair := storefixture.NewPair(t, pool)
	service := application.NewService(NewRepository(gormtest.FromPool(t, pool)))
	first, err := service.Replace(context.Background(), pair.A.Store.ID(), 0, domain.Draft{Mode: "free"})
	if err != nil || first.Version != 1 || first.Price.AmountMinor != 0 {
		t.Fatalf("first = %#v, error = %v", first, err)
	}
	second, err := service.Replace(context.Background(), pair.A.Store.ID(), 1, domain.Draft{Mode: "flat", AmountMinor: 25000})
	if err != nil || second.Version != 2 || second.ID == first.ID {
		t.Fatalf("second = %#v, error = %v", second, err)
	}
	if _, err := service.Replace(context.Background(), pair.A.Store.ID(), 1, domain.Draft{Mode: "free"}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale replacement error = %v", err)
	}
	if _, err := service.Get(context.Background(), pair.B.Store.ID()); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store read error = %v", err)
	}
}

func shippingPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
