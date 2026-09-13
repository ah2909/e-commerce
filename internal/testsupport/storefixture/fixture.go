//go:build integration

package storefixture

import (
	"context"
	"fmt"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitypassword "github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	onboardingpostgres "github.com/ah2909/e-commerce/internal/onboarding/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Store  domain.Store
	UserID ids.UserID
}

type Pair struct {
	A Store
	B Store
}

func NewPair(tb testing.TB, pool *pgxpool.Pool) Pair {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	tb.Cleanup(cancel)
	now := time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC)
	db := gormtest.FromPool(tb, pool)
	stores := storepostgres.NewRepository(db, auditpostgres.NewWriter())
	identity := identitypostgres.NewRepository(db)
	hasher, err := identitypassword.NewArgon2ID(1)
	if err != nil {
		tb.Fatal(err)
	}
	identityService := identityapplication.NewService(identity, hasher, stores, token.Generator{}, identityapplication.DefaultSessionSettings(), func() time.Time { return now })
	onboarding := onboardingapplication.NewService(onboardingpostgres.NewRepository(db, stores, identity), identityService, ids.NewStoreID, func() time.Time { return now })
	created := make([]Store, 0, 2)
	tb.Cleanup(func() { cleanupStores(tb, pool, created) })
	pair, fixtures, err := buildPair(func() (Store, error) {
		suffix, err := ids.NewStoreID()
		if err != nil {
			return Store{}, err
		}
		result, err := onboarding.Execute(ctx, onboardingapplication.Command{Subdomain: "fixture-" + suffix.String(), StoreName: "Cửa hàng Ánh Dương", Email: "fixture@example.com", Password: "fixture-password-value", PlatformActorID: "deployment", RequestID: "store-fixture"})
		if err != nil {
			return Store{}, fmt.Errorf("create fixture store: %w", err)
		}
		return Store{Store: result.Store, UserID: result.OwnerUserID}, nil
	})
	created = fixtures
	if err != nil {
		tb.Fatalf("create store pair: %v", err)
	}
	return pair
}

func buildPair(create func() (Store, error)) (Pair, []Store, error) {
	created := make([]Store, 0, 2)
	a, err := create()
	if err != nil {
		return Pair{}, created, err
	}
	created = append(created, a)
	b, err := create()
	if err != nil {
		return Pair{A: a}, created, err
	}
	created = append(created, b)
	return Pair{A: a, B: b}, created, nil
}

func cleanupStores(tb testing.TB, pool *pgxpool.Pool, created []Store) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, fixture := range created {
		storeID := fixture.Store.ID().String()
		tx, err := pool.Begin(ctx)
		if err != nil {
			tb.Errorf("begin fixture cleanup: %v", err)
			continue
		}
		if _, err := tx.Exec(ctx, `SET LOCAL application_name = 'ecommerce-test-cleanup'`); err != nil {
			_ = tx.Rollback(ctx)
			tb.Errorf("identify fixture cleanup: %v", err)
			continue
		}
		failed := false
		for _, statement := range []string{
			`DELETE FROM order_guest_grants WHERE store_id = $1::uuid`,
			`DELETE FROM order_idempotency WHERE store_id = $1::uuid`,
			`DELETE FROM order_state_history WHERE store_id = $1::uuid`,
			`DELETE FROM payments WHERE store_id = $1::uuid`,
			`DELETE FROM inventory_ledger_entries WHERE store_id = $1::uuid`,
			`DELETE FROM inventory_reservations WHERE store_id = $1::uuid`,
			`DELETE FROM order_addresses WHERE store_id = $1::uuid`,
			`DELETE FROM order_lines WHERE store_id = $1::uuid`,
			`DELETE FROM cart_lines WHERE store_id = $1::uuid`,
			`DELETE FROM carts WHERE store_id = $1::uuid`,
			`DELETE FROM orders WHERE store_id = $1::uuid`,
			`DELETE FROM order_number_counters WHERE store_id = $1::uuid`,
			`DELETE FROM shipping_rules WHERE store_id = $1::uuid`,
			`DELETE FROM customer_addresses WHERE store_id = $1::uuid`,
			`DELETE FROM customers WHERE store_id = $1::uuid`,
			`DELETE FROM outbox_events WHERE store_id = $1::uuid`,
			`DELETE FROM inventory_stock_items WHERE store_id = $1::uuid`,
			`DELETE FROM pricing_prices WHERE store_id = $1::uuid`,
			`DELETE FROM pricing_price_lists WHERE store_id = $1::uuid`,
			`DELETE FROM catalog_media WHERE store_id = $1::uuid`,
			`DELETE FROM catalog_product_categories WHERE store_id = $1::uuid`,
			`DELETE FROM catalog_variants WHERE store_id = $1::uuid`,
			`DELETE FROM catalog_products WHERE store_id = $1::uuid`,
			`DELETE FROM catalog_categories WHERE store_id = $1::uuid`,
			`DELETE FROM audit_records WHERE store_id = $1::uuid`,
			`DELETE FROM store_memberships WHERE store_id = $1::uuid`,
			`DELETE FROM users WHERE store_id = $1::uuid`,
			`DELETE FROM stores WHERE store_id = $1::uuid`,
		} {
			if _, err := tx.Exec(ctx, statement, storeID); err != nil {
				tb.Errorf("clean fixture store: %v", err)
				failed = true
				break
			}
		}
		if failed {
			_ = tx.Rollback(ctx)
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			tb.Errorf("commit fixture cleanup: %v", err)
		}
	}
}
