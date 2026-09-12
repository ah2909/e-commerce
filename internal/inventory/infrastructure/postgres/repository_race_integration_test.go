//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

func TestConcurrentFirstAdjustmentsSerialize(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "first-race", "FIRST-RACE")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))

	start := make(chan struct{})
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range 2 {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "initial receipt", ExpectedVersion: 0, IdempotencyKey: "first-race-" + string(rune('a'+index)), ActorID: pair.A.UserID, RequestID: "first-race"})
		}(index)
	}
	close(start)
	wait.Wait()

	successes, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, application.ErrConflict):
			conflicts++
		default:
			t.Fatalf("adjustment error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d, errors = %#v", successes, conflicts, errs)
	}
	assertInventoryEffects(t, pool, pair.A.Store.ID(), variant, 1, 1, 1, 1)
}

func TestAdjustmentWriterFailuresRollBackEveryEffect(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Repository)
	}{
		{"ledger", func(repository *Repository) { repository.ledger = failingLedgerWriter{} }},
		{"audit", func(repository *Repository) { repository.audit = failingAuditWriter{} }},
		{"outbox", func(repository *Repository) { repository.events = failingEventWriter{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := inventoryPool(t)
			pair := storefixture.NewPair(t, pool)
			db := gormtest.FromPool(t, pool)
			catalog := catalogpostgres.NewRepository(db)
			variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "rollback-"+test.name, "ROLLBACK-"+test.name)
			repository := NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
			test.configure(repository)
			service := application.NewService(repository)

			_, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 4, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "rollback-" + test.name, ActorID: pair.A.UserID, RequestID: "rollback"})
			if err == nil {
				t.Fatal("expected writer failure")
			}
			assertInventoryEffects(t, pool, pair.A.Store.ID(), variant, 0, 0, 0, 0)
		})
	}
}

func TestTenantScopedReservationIdempotencyAndCursor(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variantA1 := createInventoryVariant(t, catalog, pair.A.Store.ID(), "tenant-a-one", "TENANT-A-ONE")
	variantA2 := createInventoryVariant(t, catalog, pair.A.Store.ID(), "tenant-a-two", "TENANT-A-TWO")
	variantB := createInventoryVariant(t, catalog, pair.B.Store.ID(), "tenant-b", "TENANT-B")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))

	for _, value := range []struct {
		store   ids.StoreID
		variant ids.VariantID
		actor   ids.UserID
	}{
		{pair.A.Store.ID(), variantA1, pair.A.UserID},
		{pair.A.Store.ID(), variantA2, pair.A.UserID},
		{pair.B.Store.ID(), variantB, pair.B.UserID},
	} {
		if _, err := service.Adjust(context.Background(), value.store, domain.Adjustment{VariantID: value.variant, QuantityDelta: 2, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "shared-key", ActorID: value.actor, RequestID: "tenant"}); err != nil && value.variant != variantA2 {
			t.Fatal(err)
		} else if value.variant == variantA2 && !errors.Is(err, application.ErrConflict) {
			t.Fatalf("same-store idempotency reuse error = %v", err)
		}
	}
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variantA2, QuantityDelta: 2, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "a-two", ActorID: pair.A.UserID, RequestID: "tenant"}); err != nil {
		t.Fatal(err)
	}

	page, err := service.List(context.Background(), pair.A.Store.ID(), 1, "")
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page = %#v, error = %v", page, err)
	}
	if _, err := service.List(context.Background(), pair.B.Store.ID(), 1, page.NextCursor); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store cursor error = %v", err)
	}

	orderID, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variantA1, Quantity: 1}}, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(context.Background(), pair.B.Store.ID(), orderID, []domain.Allocation{{VariantID: variantB, Quantity: 1}}, "tenant-b"); err != nil {
		t.Fatal(err)
	}
	foreignOrder, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), foreignOrder, []domain.Allocation{{VariantID: variantB, Quantity: 1}}, "foreign"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign reservation error = %v", err)
	}
	if _, err := service.Confirm(context.Background(), pair.A.Store.ID(), foreignOrder, "foreign"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign order error = %v", err)
	}
}

func TestOverlappingMultiLineReservationsDoNotDeadlockOrPartiallyReserve(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	first := createInventoryVariant(t, catalog, pair.A.Store.ID(), "multi-first", "MULTI-FIRST")
	second := createInventoryVariant(t, catalog, pair.A.Store.ID(), "multi-second", "MULTI-SECOND")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
	for index, variant := range []ids.VariantID{first, second} {
		if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "multi-" + string(rune('a'+index)), ActorID: pair.A.UserID, RequestID: "multi"}); err != nil {
			t.Fatal(err)
		}
	}
	orders := make([]ids.OrderID, 2)
	orders[0], _ = ids.NewOrderID()
	orders[1], _ = ids.NewOrderID()
	allocations := [][]domain.Allocation{{{VariantID: first, Quantity: 1}, {VariantID: second, Quantity: 1}}, {{VariantID: second, Quantity: 1}, {VariantID: first, Quantity: 1}}}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range 2 {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = service.Reserve(context.Background(), pair.A.Store.ID(), orders[index], allocations[index], "multi")
		}(index)
	}
	close(start)
	wait.Wait()
	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
		} else if !errors.Is(err, domain.ErrInsufficientStock) {
			t.Fatalf("reservation error = %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, errors = %#v", winners, errs)
	}
	var active int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_reservations WHERE store_id=$1::uuid AND status='active'`, pair.A.Store.ID().String()).Scan(&active); err != nil || active != 2 {
		t.Fatalf("active reservations = %d, error = %v", active, err)
	}
}

func TestTerminalTransitionsAndAdjustmentResolveConcurrentRaces(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "terminal-race", "TERMINAL-RACE")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 3, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "terminal-stock", ActorID: pair.A.UserID, RequestID: "terminal"}); err != nil {
		t.Fatal(err)
	}

	orderID, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "terminal"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, errs[0] = service.Confirm(context.Background(), pair.A.Store.ID(), orderID, "confirm")
	}()
	go func() {
		defer wait.Done()
		<-start
		_, errs[1] = service.Release(context.Background(), pair.A.Store.ID(), orderID, "cancelled", "release")
	}()
	close(start)
	wait.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, application.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("terminal race error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("terminal successes/conflicts = %d/%d, errors = %#v", successes, conflicts, errs)
	}

	confirmOrder, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), confirmOrder, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "adjust-race"); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	errs = make([]error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, errs[0] = service.Confirm(context.Background(), pair.A.Store.ID(), confirmOrder, "confirm")
	}()
	go func() {
		defer wait.Done()
		<-start
		_, errs[1] = service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: -2, Reason: "cycle count", ExpectedVersion: 1, IdempotencyKey: "adjust-race", ActorID: pair.A.UserID, RequestID: "adjust-race"})
	}()
	close(start)
	wait.Wait()
	if errs[0] != nil {
		t.Fatalf("confirmation error = %v", errs[0])
	}
	if errs[1] != nil && !errors.Is(errs[1], application.ErrConflict) && !errors.Is(errs[1], domain.ErrInsufficientStock) {
		t.Fatalf("adjustment race error = %v", errs[1])
	}
	item, err := service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.Available < 0 || item.Reserved != 0 {
		t.Fatalf("post-race stock = %#v, error = %v", item, err)
	}
}

func TestDuplicateExpiryWorkersAndDatabaseBoundaryCloseOnce(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "expiry-race", "EXPIRY-RACE")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "expiry-stock", ActorID: pair.A.UserID, RequestID: "expiry"}); err != nil {
		t.Fatal(err)
	}
	orderID, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "expiry"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '11 minutes',expires_at=clock_timestamp() WHERE store_id=$1::uuid AND order_id=$2::uuid`, pair.A.Store.ID().String(), orderID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(context.Background(), pair.A.Store.ID(), orderID, "late-confirm"); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("boundary confirmation error = %v", err)
	}
	start := make(chan struct{})
	processed := make([]int, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range 2 {
		go func(index int) {
			defer wait.Done()
			<-start
			processed[index], errs[index] = service.ExpireDue(context.Background(), 10, "expiry-race")
		}(index)
	}
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || processed[0]+processed[1] != 1 {
		t.Fatalf("expiry processed = %#v, errors = %#v", processed, errs)
	}
	var events int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND event_type='inventory.reservation_expired.v1' AND aggregate_id IN (SELECT reservation_id FROM inventory_reservations WHERE store_id=$1::uuid AND order_id=$2::uuid)`, pair.A.Store.ID().String(), orderID.String()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("expiry events = %d, error = %v", events, err)
	}
}

func TestLedgerRowsAreImmutableAndReconcileOnHand(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "immutable-ledger", "IMMUTABLE-LEDGER")
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 2, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "immutable-stock", ActorID: pair.A.UserID, RequestID: "immutable"}); err != nil {
		t.Fatal(err)
	}
	orderID, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Confirm(context.Background(), pair.A.Store.ID(), orderID, "immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_ledger_entries SET reason='tampered' WHERE store_id=$1::uuid AND variant_id=$2::uuid`, pair.A.Store.ID().String(), variant.String()); err == nil {
		t.Fatal("ledger update succeeded")
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM inventory_ledger_entries WHERE store_id=$1::uuid AND variant_id=$2::uuid`, pair.A.Store.ID().String(), variant.String()); err == nil {
		t.Fatal("ledger delete succeeded")
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `SET LOCAL app.allow_inventory_ledger_mutation = 'on'`); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `UPDATE inventory_ledger_entries SET reason='bypassed' WHERE store_id=$1::uuid AND variant_id=$2::uuid`, pair.A.Store.ID().String(), variant.String()); err == nil {
		_ = tx.Rollback(context.Background())
		t.Fatal("runtime setting bypassed ledger immutability")
	}
	_ = tx.Rollback(context.Background())
	var onHand, delta int
	if err := pool.QueryRow(context.Background(), `SELECT s.on_hand,(SELECT sum(l.quantity_delta) FROM inventory_ledger_entries l WHERE l.store_id=s.store_id AND l.variant_id=s.variant_id) FROM inventory_stock_items s WHERE s.store_id=$1::uuid AND s.variant_id=$2::uuid`, pair.A.Store.ID().String(), variant.String()).Scan(&onHand, &delta); err != nil || onHand != delta || onHand != 1 {
		t.Fatalf("on_hand/delta = %d/%d, error = %v", onHand, delta, err)
	}
}

func TestGetUsesOneConsistentSnapshotDuringConfirmation(t *testing.T) {
	pool := inventoryPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	events := &blockingConfirmationWriter{delegate: eventingpostgres.NewWriter(), entered: entered, release: release}
	service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), events))
	variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "snapshot", "SNAPSHOT")
	if _, err := service.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "snapshot", ActorID: pair.A.UserID, RequestID: "snapshot"}); err != nil {
		t.Fatal(err)
	}
	orderID, _ := ids.NewOrderID()
	if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "snapshot"); err != nil {
		t.Fatal(err)
	}
	confirmErr := make(chan error, 1)
	go func() {
		_, err := service.Confirm(context.Background(), pair.A.Store.ID(), orderID, "snapshot")
		confirmErr <- err
	}()
	<-entered
	item, err := service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.OnHand != 1 || item.Reserved != 1 || item.Available != 0 {
		t.Fatalf("uncommitted confirmation snapshot = %#v, error = %v", item, err)
	}
	unblock()
	if err := <-confirmErr; err != nil {
		t.Fatal(err)
	}
	item, err = service.Get(context.Background(), pair.A.Store.ID(), variant)
	if err != nil || item.OnHand != 0 || item.Reserved != 0 || item.Available != 0 {
		t.Fatalf("committed confirmation snapshot = %#v, error = %v", item, err)
	}
}

func TestReservationLifecycleWriterFailuresRollBack(t *testing.T) {
	t.Run("reservation outbox", func(t *testing.T) {
		pool := inventoryPool(t)
		pair := storefixture.NewPair(t, pool)
		db := gormtest.FromPool(t, pool)
		catalog := catalogpostgres.NewRepository(db)
		variants := []ids.VariantID{
			createInventoryVariant(t, catalog, pair.A.Store.ID(), "reserve-rollback-one", "RESERVE-ROLLBACK-ONE"),
			createInventoryVariant(t, catalog, pair.A.Store.ID(), "reserve-rollback-two", "RESERVE-ROLLBACK-TWO"),
		}
		normal := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
		for index, variant := range variants {
			if _, err := normal.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "reserve-rollback-" + string(rune('a'+index)), ActorID: pair.A.UserID, RequestID: "reserve-rollback"}); err != nil {
				t.Fatal(err)
			}
		}
		failingEvents := &selectiveFailEventWriter{delegate: eventingpostgres.NewWriter(), eventType: "inventory.reserved.v1", failOn: 2}
		service := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), failingEvents))
		orderID, _ := ids.NewOrderID()
		if _, err := service.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variants[0], Quantity: 1}, {VariantID: variants[1], Quantity: 1}}, "reserve-rollback"); err == nil {
			t.Fatal("expected reservation event failure")
		}
		assertReservationState(t, pool, pair.A.Store.ID(), orderID, 0, "")
		for _, variant := range variants {
			item, err := normal.Get(context.Background(), pair.A.Store.ID(), variant)
			if err != nil || item.OnHand != 1 || item.Reserved != 0 || item.Available != 1 {
				t.Fatalf("stock after reservation rollback = %#v, error = %v", item, err)
			}
		}
	})

	t.Run("confirmation ledger and outbox", func(t *testing.T) {
		for _, failure := range []string{"ledger", "outbox"} {
			t.Run(failure, func(t *testing.T) {
				pool := inventoryPool(t)
				pair := storefixture.NewPair(t, pool)
				db := gormtest.FromPool(t, pool)
				catalog := catalogpostgres.NewRepository(db)
				variants := []ids.VariantID{
					createInventoryVariant(t, catalog, pair.A.Store.ID(), "confirm-rollback-one-"+failure, "CONFIRM-ROLLBACK-ONE-"+failure),
					createInventoryVariant(t, catalog, pair.A.Store.ID(), "confirm-rollback-two-"+failure, "CONFIRM-ROLLBACK-TWO-"+failure),
				}
				normal := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
				for index, variant := range variants {
					if _, err := normal.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "confirm-rollback-" + failure + string(rune('a'+index)), ActorID: pair.A.UserID, RequestID: "confirm-rollback"}); err != nil {
						t.Fatal(err)
					}
				}
				orderID, _ := ids.NewOrderID()
				if _, err := normal.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variants[0], Quantity: 1}, {VariantID: variants[1], Quantity: 1}}, "confirm-rollback"); err != nil {
					t.Fatal(err)
				}
				repository := NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
				if failure == "ledger" {
					repository.ledger = failingLedgerWriter{}
				} else {
					repository.events = &selectiveFailEventWriter{delegate: eventingpostgres.NewWriter(), eventType: "inventory.changed.v1", failOn: 2}
				}
				if _, err := application.NewService(repository).Confirm(context.Background(), pair.A.Store.ID(), orderID, "confirm-rollback"); err == nil {
					t.Fatal("expected confirmation writer failure")
				}
				assertReservationState(t, pool, pair.A.Store.ID(), orderID, 2, "active")
				for _, variant := range variants {
					item, err := normal.Get(context.Background(), pair.A.Store.ID(), variant)
					if err != nil || item.OnHand != 1 || item.Reserved != 1 || item.Available != 0 {
						t.Fatalf("stock after confirmation rollback = %#v, error = %v", item, err)
					}
				}
				var ledgers, confirmedEvents int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1::uuid AND entry_type='reservation_confirmation'`, pair.A.Store.ID().String()).Scan(&ledgers); err != nil || ledgers != 0 {
					t.Fatalf("confirmation ledgers = %d, error = %v", ledgers, err)
				}
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND event_type='inventory.reservation_confirmed.v1'`, pair.A.Store.ID().String()).Scan(&confirmedEvents); err != nil || confirmedEvents != 0 {
					t.Fatalf("confirmation events = %d, error = %v", confirmedEvents, err)
				}
			})
		}
	})

	for _, transition := range []struct {
		name      string
		key       string
		eventType string
		expire    bool
	}{
		{"release outbox", "release", "inventory.reservation_released.v1", false},
		{"expiry outbox", "expiry", "inventory.reservation_expired.v1", true},
	} {
		t.Run(transition.name, func(t *testing.T) {
			pool := inventoryPool(t)
			pair := storefixture.NewPair(t, pool)
			db := gormtest.FromPool(t, pool)
			catalog := catalogpostgres.NewRepository(db)
			variant := createInventoryVariant(t, catalog, pair.A.Store.ID(), "transition-rollback-"+transition.key, "TRANSITION-ROLLBACK-"+transition.key)
			normal := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter()))
			if _, err := normal.Adjust(context.Background(), pair.A.Store.ID(), domain.Adjustment{VariantID: variant, QuantityDelta: 1, Reason: "receipt", ExpectedVersion: 0, IdempotencyKey: "transition-rollback-" + transition.key, ActorID: pair.A.UserID, RequestID: "transition-rollback"}); err != nil {
				t.Fatal(err)
			}
			orderID, _ := ids.NewOrderID()
			if _, err := normal.Reserve(context.Background(), pair.A.Store.ID(), orderID, []domain.Allocation{{VariantID: variant, Quantity: 1}}, "transition-rollback"); err != nil {
				t.Fatal(err)
			}
			if transition.expire {
				if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '11 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1::uuid AND order_id=$2::uuid`, pair.A.Store.ID().String(), orderID.String()); err != nil {
					t.Fatal(err)
				}
			}
			failingEvents := &selectiveFailEventWriter{delegate: eventingpostgres.NewWriter(), eventType: transition.eventType, failOn: 1}
			failing := application.NewService(NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), failingEvents))
			var err error
			if transition.expire {
				_, err = failing.ExpireDue(context.Background(), 10, "transition-rollback")
			} else {
				_, err = failing.Release(context.Background(), pair.A.Store.ID(), orderID, "cancelled", "transition-rollback")
			}
			if err == nil {
				t.Fatal("expected transition event failure")
			}
			assertReservationState(t, pool, pair.A.Store.ID(), orderID, 1, "active")
			item, err := normal.Get(context.Background(), pair.A.Store.ID(), variant)
			if err != nil || item.OnHand != 1 || item.Reserved != 1 || item.Available != 0 {
				t.Fatalf("stock after transition rollback = %#v, error = %v", item, err)
			}
		})
	}
}

type failingLedgerWriter struct{}

func (failingLedgerWriter) Insert(context.Context, *gorm.DB, ledgerRow) error {
	return errors.New("injected ledger failure")
}

type failingAuditWriter struct{}

func (failingAuditWriter) Insert(context.Context, *gorm.DB, auditdomain.Record) error {
	return errors.New("injected audit failure")
}

type failingEventWriter struct{}

func (failingEventWriter) Append(context.Context, *gorm.DB, eventdomain.Event) error {
	return errors.New("injected outbox failure")
}

type blockingConfirmationWriter struct {
	delegate EventWriter
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

type selectiveFailEventWriter struct {
	delegate  EventWriter
	eventType string
	failOn    int
	seen      int
}

func (writer *selectiveFailEventWriter) Append(ctx context.Context, tx *gorm.DB, event eventdomain.Event) error {
	if event.Type == writer.eventType {
		writer.seen++
		if writer.seen == writer.failOn {
			return errors.New("injected lifecycle event failure")
		}
	}
	return writer.delegate.Append(ctx, tx, event)
}

func (writer *blockingConfirmationWriter) Append(ctx context.Context, tx *gorm.DB, event eventdomain.Event) error {
	if event.Type == "inventory.changed.v1" && event.Payload["reason"] == "reservation_confirmation" {
		writer.once.Do(func() {
			close(writer.entered)
			<-writer.release
		})
	}
	return writer.delegate.Append(ctx, tx, event)
}

func assertInventoryEffects(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, variantID ids.VariantID, onHand, ledgerCount, auditCount, eventCount int) {
	t.Helper()
	var stockCount, actualOnHand int
	if err := pool.QueryRow(context.Background(), `SELECT count(*),COALESCE(max(on_hand),0) FROM inventory_stock_items WHERE store_id=$1::uuid AND variant_id=$2::uuid`, storeID.String(), variantID.String()).Scan(&stockCount, &actualOnHand); err != nil {
		t.Fatal(err)
	}
	wantStock := 0
	if onHand > 0 {
		wantStock = 1
	}
	if stockCount != wantStock || actualOnHand != onHand {
		t.Fatalf("stock count/on_hand = %d/%d, want %d/%d", stockCount, actualOnHand, wantStock, onHand)
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1::uuid AND variant_id=$2::uuid`:                                                                  ledgerCount,
		`SELECT count(*) FROM audit_records WHERE store_id=$1::uuid AND action='inventory.adjusted' AND subject_id=$2`:                                                   auditCount,
		`SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND aggregate_type='inventory_stock_item' AND aggregate_id=$2 AND event_type='inventory.changed.v1'`: eventCount,
	} {
		var count int
		if err := pool.QueryRow(context.Background(), query, storeID.String(), variantID.String()).Scan(&count); err != nil || count != want {
			t.Fatalf("effect count = %d, want %d, error = %v", count, want, err)
		}
	}
}

func assertReservationState(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID, wantCount int, wantStatus string) {
	t.Helper()
	var count int
	var statuses []string
	rows, err := pool.Query(context.Background(), `SELECT status FROM inventory_reservations WHERE store_id=$1::uuid AND order_id=$2::uuid ORDER BY reservation_id`, storeID.String(), orderID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != wantCount {
		t.Fatalf("reservation count = %d, want %d", count, wantCount)
	}
	for _, status := range statuses {
		if status != wantStatus {
			t.Fatalf("reservation status = %q, want %q", status, wantStatus)
		}
	}
}
