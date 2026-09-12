//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	catalogdomain "github.com/ah2909/e-commerce/internal/catalog/domain"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdjustmentIsAtomicIdempotentAndTenantSafe(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variantA := createInventoryVariant(t, catalog, pair.A.Store.ID(), "inventory-a", "INVENTORY-A")
	variantB := createInventoryVariant(t, catalog, pair.B.Store.ID(), "inventory-b", "INVENTORY-B")
	repository := NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := application.NewService(repository)
	command := domain.Adjustment{VariantID: variantA, QuantityDelta: 3, Reason: "initial receipt", ExpectedVersion: 0, IdempotencyKey: "receipt-1", ActorID: pair.A.UserID, RequestID: "adjustment-request"}
	first, err := service.Adjust(context.Background(), pair.A.Store.ID(), command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Adjust(context.Background(), pair.A.Store.ID(), command)
	if err != nil || first.Version != 1 || replay.Version != 1 || replay.OnHand != 3 {
		t.Fatalf("first = %#v, replay = %#v, error = %v", first, replay, err)
	}
	page, err := service.List(context.Background(), pair.A.Store.ID(), 25, "")
	if err != nil || len(page.StockItems) != 1 || page.StockItems[0].Available != 3 {
		t.Fatalf("stock page = %#v, error = %v", page, err)
	}
	availability, err := service.ResolveAvailability(context.Background(), pair.A.Store.ID(), []ids.VariantID{variantA, variantB})
	if err != nil || !availability[variantA] || availability[variantB] {
		t.Fatalf("availability = %#v, error = %v", availability, err)
	}
	changed := command
	changed.QuantityDelta = 2
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), changed); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	foreign := command
	foreign.VariantID = variantB
	foreign.IdempotencyKey = "foreign"
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), foreign); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign adjustment error = %v", err)
	}
	if _, err := service.Get(context.Background(), pair.B.Store.ID(), variantA); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store get error = %v", err)
	}
	for table, want := range map[string]int{"inventory_ledger_entries": 1, "outbox_events": 1} {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE store_id=$1::uuid`, pair.A.Store.ID().String()).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count = %d, want %d, error = %v", table, count, want, err)
		}
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_records WHERE store_id=$1::uuid AND action='inventory.adjusted'`, pair.A.Store.ID().String()).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("inventory audit count = %d, error = %v", audits, err)
	}
}

func TestLastUnitReservationAndTerminalTransitionsAreAtomic(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "last-unit", "LAST-UNIT")
	repository := NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := application.NewService(repository)
	stock, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "last-unit", ActorID: pair.A.UserID, RequestID: "last-unit"})
	if err != nil || stock.Available != 1 {
		t.Fatalf("stock = %#v, error = %v", stock, err)
	}
	orders := make([]ids.OrderID, 2)
	orders[0], _ = ids.NewOrderID()
	orders[1], _ = ids.NewOrderID()
	start := make(chan struct{})
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range 2 {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = service.Reserve(context.Background(), pair.A.Store.ID(), orders[index], []domain.Allocation{{VariantID: variant, Quantity: 1}}, "reserve")
		}(index)
	}
	close(start)
	wait.Wait()
	winners := 0
	winner := ids.OrderID("")
	for index, err := range errs {
		if err == nil {
			winners++
			winner = orders[index]
		} else if !errors.Is(err, domain.ErrInsufficientStock) {
			t.Fatalf("reserve %d error = %v", index, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, errors = %#v", winners, errs)
	}
	item, err := service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.Available != 0 || item.Reserved != 1 || item.OnHand != 1 {
		t.Fatalf("reserved stock = %#v, error = %v", item, err)
	}
	replay, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "last-unit", ActorID: pair.A.UserID, RequestID: "last-unit"})
	if err != nil || replay.OnHand != 1 || replay.Reserved != 0 || replay.Available != 1 || replay.Version != 1 {
		t.Fatalf("original adjustment replay = %#v, error = %v", replay, err)
	}
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: -1, Reason: "bad reduction", ExpectedVersion: 1, IdempotencyKey: "bad-reduction", ActorID: pair.A.UserID, RequestID: "bad-reduction"}); !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("reserved reduction error = %v", err)
	}
	if _, err := service.Confirm(context.Background(), pair.A.Store.ID(), winner, "confirm"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(context.Background(), pair.A.Store.ID(), winner, "confirm-retry"); err != nil {
		t.Fatalf("confirm retry: %v", err)
	}
	item, err = service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.OnHand != 0 || item.Reserved != 0 || item.Available != 0 {
		t.Fatalf("confirmed stock = %#v, error = %v", item, err)
	}
	var confirmationLedgers int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1::uuid AND entry_type='reservation_confirmation'`, pair.A.Store.ID().String()).Scan(&confirmationLedgers); err != nil || confirmationLedgers != 1 {
		t.Fatalf("confirmation ledgers = %d, error = %v", confirmationLedgers, err)
	}
}

func TestReleaseAndExpiryRestoreAvailabilityOnce(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "release-expire", "RELEASE-EXPIRE")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 2, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "release-expire", ActorID: pair.A.UserID, RequestID: "release-expire"}); err != nil {
		t.Fatal(err)
	}
	releasedOrder, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), releasedOrder, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "reserve-release"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(context.Background(), pair.A.Store.ID(), releasedOrder, "cancelled", "release"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(context.Background(), pair.A.Store.ID(), releasedOrder, "cancelled", "release-retry"); err != nil {
		t.Fatal(err)
	}
	expiredOrder, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), expiredOrder, []domain.Allocation{{VariantID: variant, Quantity: 2}}, "reserve-expire"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '11 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1::uuid AND order_id=$2::uuid`, pair.A.Store.ID().String(), expiredOrder.String()); err != nil {
		t.Fatal(err)
	}
	processed, err := service.ExpireDue(context.Background(), 10, "expiry")
	if err != nil || processed != 1 {
		t.Fatalf("processed = %d, error = %v", processed, err)
	}
	processed, err = service.ExpireDue(context.Background(), 10, "expiry-retry")
	if err != nil || processed != 0 {
		t.Fatalf("retry processed = %d, error = %v", processed, err)
	}
	item, err := service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.OnHand != 2 || item.Available != 2 || item.Reserved != 0 {
		t.Fatalf("stock = %#v, error = %v", item, err)
	}
}

func createInventoryVariant(t *testing.T, repository *catalogpostgres.Repository, storeID ids.StoreID, slug, sku string) ids.VariantID {
	t.Helper()
	service := catalogapplication.NewService(repository, catalogapplication.DefaultIDGenerator(), time.Now)
	product, err := service.CreateProduct(context.Background(), storeID, catalogdomain.ProductDraft{Name: "Inventory", Slug: slug, Variants: []catalogdomain.VariantDraft{{SKU: sku}}})
	if err != nil {
		t.Fatal(err)
	}
	return product.Variants[0].ID
}
func inventoryPool(t testing.TB) *pgxpool.Pool {
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
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}
