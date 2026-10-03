//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	cartspostgres "github.com/ah2909/e-commerce/internal/carts/infrastructure/postgres"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	customerspostgres "github.com/ah2909/e-commerce/internal/customers/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	pricingpostgres "github.com/ah2909/e-commerce/internal/pricing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingpostgres "github.com/ah2909/e-commerce/internal/shipping/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCheckoutIsAtomicIdempotentSnapshotSafeAndTenantScoped(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	service := orderService(t, pool)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1)
	command := application.CheckoutCommand{StoreID: pair.A.Store.ID(), Owner: domain.UserOwner(pair.A.UserID), CartVersion: 2, PaymentProvider: "vietqr", AddressID: fixture.addressID, IdempotencyKey: "checkout-1", CorrelationID: "checkout-request"}

	results := make([]application.CheckoutResult, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for i := range results {
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] = service.Checkout(context.Background(), command)
		}(i)
	}
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Order.ID != results[1].Order.ID {
		t.Fatalf("results = %#v, errors = %#v", results, errs)
	}
	created := 0
	for _, result := range results {
		if result.Created {
			created++
		}
	}
	if created != 1 || results[0].Order.GrandTotal.AmountMinor != 11000 || results[0].Order.State != domain.StatePendingPayment {
		t.Fatalf("created = %d, order = %#v", created, results[0].Order)
	}

	changed := command
	changed.PaymentProvider = "momo"
	if _, err := service.Checkout(context.Background(), changed); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	orderID := results[0].Order.ID
	if _, err := service.GetForUser(context.Background(), pair.B.Store.ID(), orderID, pair.A.UserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store read error = %v", err)
	}
	if _, err := service.GetForUser(context.Background(), pair.A.Store.ID(), orderID, pair.B.UserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user read error = %v", err)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE catalog_products SET name='Changed' WHERE store_id=$1 AND product_id=$2`, pair.A.Store.ID(), fixture.productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE pricing_prices SET amount_minor=99999 WHERE store_id=$1 AND variant_id=$2`, pair.A.Store.ID(), fixture.variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE customer_addresses SET line1='Changed address' WHERE store_id=$1 AND address_id=$2`, pair.A.Store.ID(), fixture.addressID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE shipping_rules SET mode='free',flat_amount_minor=0,version=version+1 WHERE store_id=$1 AND shipping_rule_id=$2`, pair.A.Store.ID(), results[0].Order.Shipping.RuleID); err != nil {
		t.Fatal(err)
	}
	read, err := service.GetForUser(context.Background(), pair.A.Store.ID(), orderID, pair.A.UserID)
	if err != nil || read.Lines[0].ProductName != "Checkout product" || read.Lines[0].UnitPrice.AmountMinor != 10000 || read.DeliveryAddress.Line1 != "1 Original Street" || read.Shipping.Mode != "flat" || read.Shipping.Price.AmountMinor != 1000 {
		t.Fatalf("snapshot order = %#v, error = %v", read, err)
	}
	var eventCustomerID string
	if err := pool.QueryRow(context.Background(), `SELECT payload->>'customer_id' FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.created.v1'`, pair.A.Store.ID(), orderID).Scan(&eventCustomerID); err != nil || eventCustomerID != read.Owner.CustomerID.String() {
		t.Fatalf("event customer = %q, error = %v", eventCustomerID, err)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE orders SET created_at=clock_timestamp()-interval '20 minutes', reservation_expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '20 minutes', expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), orderID); err != nil {
		t.Fatal(err)
	}
	processed, err := service.ExpireDue(context.Background(), 10, "expiry-request")
	if err != nil || processed != 1 {
		t.Fatalf("processed = %d, error = %v", processed, err)
	}
	var state, paymentState, reservationState string
	var onHand int64
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), orderID).Scan(&state, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if state != "expired" || paymentState != "expired" || reservationState != "expired" || onHand != 1 {
		t.Fatalf("states = %s/%s/%s, on_hand = %d", state, paymentState, reservationState, onHand)
	}
	processed, err = service.ExpireDue(context.Background(), 10, "expiry-retry")
	if err != nil || processed != 0 {
		t.Fatalf("expiry replay processed = %d, error = %v", processed, err)
	}
}

func TestGuestGrantIsDigestOnlyAndStoreBound(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	service := orderService(t, pool)
	guestID, _ := ids.NewGuestID()
	fixture := insertGuestCheckoutFixture(t, pool, pair.A.Store.ID(), guestID)
	command := application.CheckoutCommand{StoreID: pair.A.Store.ID(), Owner: domain.GuestOwner(guestID), CartVersion: 2, PaymentProvider: "momo", GuestContact: domain.Contact{Name: "Guest Buyer", Email: "guest@example.com", Phone: "+84901234567"}, GuestAddress: domain.Address{RecipientName: "Guest Buyer", Phone: "+84901234567", Line1: "2 Guest Street", Ward: "Ward 1", District: "District 1", Province: "Ho Chi Minh City", CountryCode: "VN"}, IdempotencyKey: "guest-checkout", CorrelationID: "guest-request"}
	result, err := service.Checkout(context.Background(), command)
	if err != nil || result.OrderAccessToken == "" || !result.Created {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	var digest []byte
	if err := pool.QueryRow(context.Background(), `SELECT token_digest FROM order_guest_grants WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), result.Order.ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 || string(digest) == result.OrderAccessToken {
		t.Fatalf("stored grant is not a digest")
	}
	var eventGuestID string
	if err := pool.QueryRow(context.Background(), `SELECT payload->>'guest_id' FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.created.v1'`, pair.A.Store.ID(), result.Order.ID).Scan(&eventGuestID); err != nil || eventGuestID != guestID.String() {
		t.Fatalf("event guest = %q, error = %v", eventGuestID, err)
	}
	if _, err := service.GetForGrant(context.Background(), pair.A.Store.ID(), result.Order.ID, result.OrderAccessToken); err != nil {
		t.Fatalf("valid grant error = %v", err)
	}
	if _, err := service.GetForGrant(context.Background(), pair.B.Store.ID(), result.Order.ID, result.OrderAccessToken); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store grant error = %v", err)
	}
	wrongOrderID, _ := ids.NewOrderID()
	if _, err := service.GetForGrant(context.Background(), pair.A.Store.ID(), wrongOrderID, result.OrderAccessToken); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("wrong-order grant error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE order_guest_grants SET created_at=clock_timestamp()-interval '30 days',expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), result.Order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetForGrant(context.Background(), pair.A.Store.ID(), result.Order.ID, result.OrderAccessToken); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired grant error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE order_guest_grants SET expires_at=clock_timestamp()+interval '1 hour',revoked_at=clock_timestamp() WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), result.Order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetForGrant(context.Background(), pair.A.Store.ID(), result.Order.ID, result.OrderAccessToken); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("revoked grant error = %v", err)
	}
	replay, err := service.Checkout(context.Background(), command)
	if err != nil || replay.OrderAccessToken != "" || replay.Created {
		t.Fatalf("guest replay = %#v, error = %v", replay, err)
	}
	_ = fixture
}

type checkoutFixture struct {
	productID ids.ProductID
	variantID ids.VariantID
	addressID ids.AddressID
}

func insertCheckoutFixture(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, userID ids.UserID, stock int64) checkoutFixture {
	t.Helper()
	productID, _ := ids.NewProductID()
	variantID, _ := ids.NewVariantID()
	priceListID, _ := ids.NewPriceListID()
	customerID, _ := ids.NewCustomerID()
	addressID, _ := ids.NewAddressID()
	shippingRuleID, _ := ids.NewShippingRuleID()
	cartID, _ := ids.NewCartID()
	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,published_at,version,created_at,updated_at) VALUES($1,$2,'Checkout product',$3,'','published',$4,1,$4,$4)`, []any{storeID, productID, "checkout-" + productID.String(), now}},
		{`INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES($1,$2,$3,$4,'{"size":"M"}',0,'active',1,$5,$5)`, []any{storeID, variantID, productID, "ORDER-" + strings.ToUpper(variantID.String()), now}},
		{`INSERT INTO pricing_price_lists(store_id,price_list_id,name,channel,currency,priority,status,version,created_at,updated_at) VALUES($1,$2,'Checkout','storefront','VND',1,'active',1,$3,$3)`, []any{storeID, priceListID, now}},
		{`INSERT INTO pricing_prices(store_id,price_list_id,variant_id,amount_minor,version,created_at,updated_at) VALUES($1,$2,$3,10000,1,$4,$4)`, []any{storeID, priceListID, variantID, now}},
		{`INSERT INTO inventory_stock_items(store_id,variant_id,on_hand,version,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, []any{storeID, variantID, stock, now}},
		{`INSERT INTO customers(store_id,customer_id,user_id,full_name,email,canonical_email,phone,canonical_phone,version,created_at,updated_at) VALUES($1,$2,$3,'Buyer','buyer@example.com','buyer@example.com','+84901234567','+84901234567',1,$4,$4)`, []any{storeID, customerID, userID, now}},
		{`INSERT INTO customer_addresses(store_id,address_id,customer_id,label,recipient_name,phone,canonical_phone,line1,line2,ward,district,province,postal_code,country_code,version,created_at,updated_at) VALUES($1,$2,$3,'Home','Buyer','+84901234567','+84901234567','1 Original Street','','Ward 1','District 1','Ho Chi Minh City','','VN',1,$4,$4)`, []any{storeID, addressID, customerID, now}},
		{`INSERT INTO shipping_rules(store_id,shipping_rule_id,mode,flat_amount_minor,currency,status,version,created_at,updated_at) VALUES($1,$2,'flat',1000,'VND','active',1,$3,$3)`, []any{storeID, shippingRuleID, now}},
		{`INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1,$2,'user',$3,'VND','active',2,$4::timestamptz+interval '1 day',$4,$4)`, []any{storeID, cartID, userID, now}},
		{`INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, []any{storeID, cartID, variantID, now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(context.Background(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return checkoutFixture{productID: productID, variantID: variantID, addressID: addressID}
}

func insertGuestCheckoutFixture(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, guestID ids.GuestID) checkoutFixture {
	t.Helper()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `INSERT INTO identity_guests(store_id,guest_id,created_at,expires_at) VALUES($1,$2,$3::timestamptz,$3::timestamptz+interval '30 days')`, storeID, guestID, now); err != nil {
		t.Fatal(err)
	}
	fixture := insertCheckoutFixture(t, pool, storeID, storefixtureUser(t, pool, storeID), 2)
	if _, err := pool.Exec(context.Background(), `DELETE FROM cart_lines WHERE store_id=$1 AND cart_id IN (SELECT cart_id FROM carts WHERE store_id=$1 AND owner_kind='user')`, storeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM carts WHERE store_id=$1 AND owner_kind='user'`, storeID); err != nil {
		t.Fatal(err)
	}
	cartID, _ := ids.NewCartID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1,$2,'guest',$3,'VND','active',2,$4::timestamptz+interval '1 day',$4,$4)`, storeID, cartID, guestID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, storeID, cartID, fixture.variantID, now); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func storefixtureUser(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID) ids.UserID {
	t.Helper()
	var value string
	if err := pool.QueryRow(context.Background(), `SELECT user_id FROM users WHERE store_id=$1 ORDER BY created_at LIMIT 1`, storeID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return ids.UserID(value)
}

func orderService(t *testing.T, pool *pgxpool.Pool) application.Service {
	t.Helper()
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	pricing := pricingpostgres.NewRepository(db, catalogpostgres.NewVariantLocker())
	inventory := inventorypostgres.NewRepository(db, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	repository := NewRepository(db, cartspostgres.NewRepository(db), catalog, pricing, customerspostgres.NewRepository(db), shippingpostgres.NewRepository(db), inventory, paymentspostgres.NewParticipant(), eventingpostgres.NewWriter())
	return application.NewService(repository)
}

func orderPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
