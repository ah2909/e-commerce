//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	cartspostgres "github.com/ah2909/e-commerce/internal/carts/infrastructure/postgres"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	customerspostgres "github.com/ah2909/e-commerce/internal/customers/infrastructure/postgres"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	pricingpostgres "github.com/ah2909/e-commerce/internal/pricing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingpostgres "github.com/ah2909/e-commerce/internal/shipping/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

var errInjectedEvent = errors.New("injected order event failure")

type failingEvents struct{}

func (failingEvents) Append(context.Context, *gorm.DB, eventdomain.Event) error {
	return errInjectedEvent
}

func TestCheckoutRollsBackEveryEffectWhenOrderEventFails(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	service := orderServiceWithEvents(t, pool, failingEvents{})
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "rollback-event")

	if _, err := service.Checkout(context.Background(), command); !errors.Is(err, errInjectedEvent) {
		t.Fatalf("checkout error = %v", err)
	}
	assertCheckoutEffectCounts(t, pool, pair.A.Store.ID(), 0)
	var status string
	var convertedOrderID *string
	if err := pool.QueryRow(context.Background(), `SELECT status,converted_order_id::text FROM carts WHERE store_id=$1 AND user_id=$2`, pair.A.Store.ID(), pair.A.UserID).Scan(&status, &convertedOrderID); err != nil {
		t.Fatal(err)
	}
	if status != "active" || convertedOrderID != nil {
		t.Fatalf("cart state = %s, converted order = %v", status, convertedOrderID)
	}
}

func TestCheckoutInsufficientStockRollsBackEveryEffect(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 0)
	service := orderService(t, pool)

	if _, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "no-stock")); !errors.Is(err, application.ErrUnprocessable) {
		t.Fatalf("checkout error = %v", err)
	}
	assertCheckoutEffectCounts(t, pool, pair.A.Store.ID(), 0)
}

func TestConcurrentLastUnitCheckoutHasOneCompleteWinner(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	secondUser, secondAddress := insertAdditionalCheckoutOwner(t, pool, pair.A.Store.ID(), fixture.variantID)
	service := orderService(t, pool)
	commands := []application.CheckoutCommand{
		userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "last-unit-a"),
		userCheckoutCommand(pair.A.Store.ID(), secondUser, secondAddress, "last-unit-b"),
	}
	errValues := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range commands {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errValues[index] = service.Checkout(context.Background(), commands[index])
		}(index)
	}
	close(start)
	wait.Wait()

	successes, unavailable := 0, 0
	for _, err := range errValues {
		if err == nil {
			successes++
		} else if errors.Is(err, application.ErrUnprocessable) {
			unavailable++
		} else {
			t.Fatalf("unexpected checkout error = %v", err)
		}
	}
	if successes != 1 || unavailable != 1 {
		t.Fatalf("successes = %d, unavailable = %d", successes, unavailable)
	}
	assertCheckoutEffectCounts(t, pool, pair.A.Store.ID(), 1)
	var available int64
	if err := pool.QueryRow(context.Background(), `SELECT on_hand-COALESCE((SELECT sum(quantity) FROM inventory_reservations r WHERE r.store_id=s.store_id AND r.variant_id=s.variant_id AND r.status='active'),0) FROM inventory_stock_items s WHERE store_id=$1 AND variant_id=$2`, pair.A.Store.ID(), fixture.variantID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatalf("available stock = %d", available)
	}
}

func TestCheckoutKeysAndOrderNumbersAreStoreScoped(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	fixtureA := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	fixtureB := insertCheckoutFixture(t, pool, pair.B.Store.ID(), pair.B.UserID, 1)
	service := orderService(t, pool)
	resultA, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixtureA.addressID, "shared-key"))
	if err != nil {
		t.Fatal(err)
	}
	resultB, err := service.Checkout(context.Background(), userCheckoutCommand(pair.B.Store.ID(), pair.B.UserID, fixtureB.addressID, "shared-key"))
	if err != nil {
		t.Fatal(err)
	}
	if resultA.Order.ID == resultB.Order.ID || resultA.Order.Number != "ORD-000000000001" || resultB.Order.Number != "ORD-000000000001" {
		t.Fatalf("orders = %#v / %#v", resultA.Order, resultB.Order)
	}
}

func TestExpiryRollsBackWhenParticipantsCannotTransition(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(testing.TB, *pgxpool.Pool, ids.StoreID, ids.OrderID)
	}{
		{"missing reservation", func(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID) {
			if _, err := pool.Exec(context.Background(), `DELETE FROM inventory_reservations WHERE store_id=$1 AND order_id=$2`, storeID, orderID); err != nil {
				t.Fatal(err)
			}
		}},
		{"reservation not due", func(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID) {
			if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET expires_at=clock_timestamp()+interval '1 hour' WHERE store_id=$1 AND order_id=$2`, storeID, orderID); err != nil {
				t.Fatal(err)
			}
		}},
		{"payment succeeded", func(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID) {
			if _, err := pool.Exec(context.Background(), `UPDATE payments SET state='succeeded',version=version+1,updated_at=clock_timestamp(),closed_at=clock_timestamp() WHERE store_id=$1 AND order_id=$2`, storeID, orderID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := orderPool(t)
			pair := storefixture.NewPair(t, pool)
			service := orderService(t, pool)
			fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
			result, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "expiry-"+test.name))
			if err != nil {
				t.Fatal(err)
			}
			makeOrderDue(t, pool, pair.A.Store.ID(), result.Order.ID)
			test.mutate(t, pool, pair.A.Store.ID(), result.Order.ID)
			if processed, err := service.ExpireDue(context.Background(), 1, "expiry-failure"); err == nil || processed != 0 {
				t.Fatalf("processed = %d, error = %v", processed, err)
			}
			var orderState, paymentState string
			if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state FROM orders o JOIN payments p USING(store_id,order_id) WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), result.Order.ID).Scan(&orderState, &paymentState); err != nil {
				t.Fatal(err)
			}
			if orderState != "pending_payment" || (test.name != "payment succeeded" && paymentState != "pending") || (test.name == "payment succeeded" && paymentState != "succeeded") {
				t.Fatalf("states = %s/%s", orderState, paymentState)
			}
			var expiredEvents int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.expired.v1'`, pair.A.Store.ID(), result.Order.ID).Scan(&expiredEvents); err != nil || expiredEvents != 0 {
				t.Fatalf("expired events = %d, error = %v", expiredEvents, err)
			}
		})
	}
}

func TestConcurrentExpiryTransitionsOrderOnce(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	service := orderService(t, pool)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	result, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "expiry-race"))
	if err != nil {
		t.Fatal(err)
	}
	makeOrderDue(t, pool, pair.A.Store.ID(), result.Order.ID)
	processed := make([]int, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range processed {
		go func(index int) {
			defer wait.Done()
			<-start
			processed[index], errs[index] = service.ExpireDue(context.Background(), 1, fmt.Sprintf("expiry-worker-%d", index))
		}(index)
	}
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || processed[0]+processed[1] != 1 {
		t.Fatalf("processed = %v, errors = %v", processed, errs)
	}
	var history, events int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM order_state_history WHERE store_id=$1 AND order_id=$2 AND to_state='expired'`, pair.A.Store.ID(), result.Order.ID).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.expired.v1'`, pair.A.Store.ID(), result.Order.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if history != 1 || events != 1 {
		t.Fatalf("history = %d, events = %d", history, events)
	}
}

func TestOrderReadLockExcludesCoordinatedExpiry(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := orderRepository(t, pool, eventingpostgres.NewWriter())
	service := application.NewService(repository)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	result, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "read-lock"))
	if err != nil {
		t.Fatal(err)
	}
	makeOrderDue(t, pool, pair.A.Store.ID(), result.Order.ID)
	tx := repository.db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	read, err := repository.load(tx, pair.A.Store.ID(), result.Order.ID)
	if err != nil || read.State != domain.StatePendingPayment || read.Payment.State != "pending" {
		t.Fatalf("read = %#v, error = %v", read, err)
	}
	processed, err := service.ExpireDue(context.Background(), 1, "expiry-while-read")
	if err != nil || processed != 0 {
		t.Fatalf("processed while read lock held = %d, error = %v", processed, err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	processed, err = service.ExpireDue(context.Background(), 1, "expiry-after-read")
	if err != nil || processed != 1 {
		t.Fatalf("processed after read = %d, error = %v", processed, err)
	}
}

func userCheckoutCommand(storeID ids.StoreID, userID ids.UserID, addressID ids.AddressID, key string) application.CheckoutCommand {
	return application.CheckoutCommand{StoreID: storeID, Owner: domain.UserOwner(userID), CartVersion: 2, PaymentProvider: "vietqr", AddressID: addressID, IdempotencyKey: key, CorrelationID: key}
}

func insertAdditionalCheckoutOwner(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, variantID ids.VariantID) (ids.UserID, ids.AddressID) {
	t.Helper()
	userID, _ := ids.NewUserID()
	customerID, _ := ids.NewCustomerID()
	addressID, _ := ids.NewAddressID()
	cartID, _ := ids.NewCartID()
	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1,$2,$3,$3,'active',$4,$4)`, []any{storeID, userID, userID.String() + "@example.com", now}},
		{`INSERT INTO customers(store_id,customer_id,user_id,full_name,email,canonical_email,phone,canonical_phone,version,created_at,updated_at) VALUES($1,$2,$3,'Second Buyer',$4,$4,'+84901234568','+84901234568',1,$5,$5)`, []any{storeID, customerID, userID, userID.String() + "@example.com", now}},
		{`INSERT INTO customer_addresses(store_id,address_id,customer_id,label,recipient_name,phone,canonical_phone,line1,line2,ward,district,province,postal_code,country_code,version,created_at,updated_at) VALUES($1,$2,$3,'Home','Second Buyer','+84901234568','+84901234568','2 Contention Street','','Ward 1','District 1','Ho Chi Minh City','','VN',1,$4,$4)`, []any{storeID, addressID, customerID, now}},
		{`INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1,$2,'user',$3,'VND','active',2,$4::timestamptz+interval '1 day',$4,$4)`, []any{storeID, cartID, userID, now}},
		{`INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, []any{storeID, cartID, variantID, now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(context.Background(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return userID, addressID
}

func makeOrderDue(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE orders SET created_at=clock_timestamp()-interval '20 minutes',reservation_expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`, storeID, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '20 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`, storeID, orderID); err != nil {
		t.Fatal(err)
	}
}

func assertCheckoutEffectCounts(t testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, expected int) {
	t.Helper()
	for _, table := range []string{"orders", "payments", "inventory_reservations", "order_state_history", "order_idempotency", "outbox_events"} {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE store_id=$1`, storeID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if table == "outbox_events" {
			if expected == 0 && count != 0 {
				t.Fatalf("%s count = %d", table, count)
			}
			continue
		}
		if count != expected {
			t.Fatalf("%s count = %d, expected %d", table, count, expected)
		}
	}
}

func orderServiceWithEvents(t testing.TB, pool *pgxpool.Pool, events Events) application.Service {
	return application.NewService(orderRepository(t, pool, events))
}

func orderRepository(t testing.TB, pool *pgxpool.Pool, events Events) *Repository {
	t.Helper()
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	pricing := pricingpostgres.NewRepository(db, catalogpostgres.NewVariantLocker())
	inventory := inventorypostgres.NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	return NewRepository(db, cartspostgres.NewRepository(db), catalog, pricing, customerspostgres.NewRepository(db), shippingpostgres.NewRepository(db), inventory, paymentspostgres.NewParticipant(), events)
}
