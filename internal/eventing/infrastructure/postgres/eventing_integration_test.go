//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	analyticspostgres "github.com/ah2909/e-commerce/internal/analytics/infrastructure/postgres"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	notificationspostgres "github.com/ah2909/e-commerce/internal/notifications/infrastructure/postgres"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	orderspostgres "github.com/ah2909/e-commerce/internal/orders/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	searchpostgres "github.com/ah2909/e-commerce/internal/search/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

func TestOutboxClaimsRecoverAndRemainTenantScoped(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	outbox := eventingpostgres.NewOutbox(db)
	writer := eventingpostgres.NewWriter()
	now := time.Now().UTC().Add(-time.Minute)
	sharedID, _ := ids.NewEventID()
	for _, storeID := range []ids.StoreID{pair.A.Store.ID(), pair.B.Store.ID()} {
		event, err := eventdomain.New(storeID, sharedID, "order.created.v1", "order", mustEventID(t).String(), 1, now, "integration", map[string]any{"store_id": storeID.String()})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return writer.Append(context.Background(), tx, event) }); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := outbox.Claim(context.Background(), 1, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%d error=%v", len(claims), err)
	}
	second, err := outbox.Claim(context.Background(), 1, time.Minute)
	if err != nil || len(second) != 1 || second[0].Event.StoreID == claims[0].Event.StoreID {
		t.Fatalf("second=%#v error=%v", second, err)
	}
	bad := claims[0]
	bad.Token = mustEventID(t)
	if err := outbox.Complete(context.Background(), bad, "commerce.orders.v1", eventapplication.Publication{}); !errors.Is(err, eventingpostgres.ErrClaimLost) {
		t.Fatalf("claim theft error=%v", err)
	}
	if err := outbox.Fail(context.Background(), claims[0], "permanent", [32]byte{1}, now, true); err != nil {
		t.Fatal(err)
	}
	foreign := pair.A.Store.ID()
	if foreign == claims[0].Event.StoreID {
		foreign = pair.B.Store.ID()
	}
	if err := outbox.Requeue(context.Background(), foreign, sharedID, "foreign attempt"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign requeue error=%v", err)
	}
	if err := outbox.Requeue(context.Background(), claims[0].Event.StoreID, sharedID, "operator retry"); err != nil {
		t.Fatal(err)
	}
}

type acceptingBroker struct{ published int }

func (broker *acceptingBroker) Publish(context.Context, eventapplication.Message) (eventapplication.Publication, error) {
	broker.published++
	return eventapplication.Publication{Partition: 0, Offset: int64(broker.published)}, nil
}

func TestPublisherTerminalizesUnknownSchemaWithoutBlockingValidRows(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	now := time.Now().UTC().Add(-time.Minute)
	invalidID, validID, orderID, guestID := mustEventID(t), mustEventID(t), mustEventID(t), mustEventID(t)
	payload := `{"store_id":"` + pair.A.Store.ID().String() + `","order_id":"` + orderID.String() + `","owner_kind":"guest","guest_id":"` + guestID.String() + `"}`
	if err := db.Exec(`INSERT INTO outbox_events (store_id,event_id,event_type,schema_version,aggregate_type,aggregate_id,aggregate_version,occurred_at,correlation_id,payload,next_attempt_at) VALUES (?,?,?,2,'order',?,1,?,'unknown-schema',?::jsonb,?)`, pair.A.Store.ID().String(), invalidID.String(), "order.created.v1", orderID.String(), now.Add(-time.Second), payload, now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	event, _ := eventdomain.New(pair.A.Store.ID(), validID, "order.created.v1", "order", orderID.String(), 2, now, "valid-schema", map[string]any{"store_id": pair.A.Store.ID().String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	if err := db.Transaction(func(tx *gorm.DB) error { return eventingpostgres.NewWriter().Append(context.Background(), tx, event) }); err != nil {
		t.Fatal(err)
	}
	broker := &acceptingBroker{}
	publisher, err := eventapplication.NewPublisher(eventingpostgres.NewOutbox(db), broker, eventapplication.PublisherSettings{Batch: 10, Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute, PublishTimeout: time.Second}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := publisher.ProcessBatch(context.Background())
	if err != nil || processed != 2 || broker.published != 1 {
		t.Fatalf("processed=%d published=%d error=%v", processed, broker.published, err)
	}
	var terminal, published int64
	if err := db.Raw(`SELECT count(*) FILTER (WHERE event_id=? AND terminal_at IS NOT NULL),count(*) FILTER (WHERE event_id=? AND published_at IS NOT NULL) FROM outbox_events WHERE store_id=?`, invalidID.String(), validID.String(), pair.A.Store.ID().String()).Row().Scan(&terminal, &published); err != nil || terminal != 1 || published != 1 {
		t.Fatalf("terminal=%d published=%d error=%v", terminal, published, err)
	}
}

func TestOperationalInspectionIsBoundedScopedAndDueAware(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	outbox := eventingpostgres.NewOutbox(db)
	now := time.Now().UTC()
	eventID, aggregateID := mustEventID(t), mustEventID(t)
	if err := db.Exec(`INSERT INTO outbox_events (store_id,event_id,event_type,schema_version,aggregate_type,aggregate_id,aggregate_version,occurred_at,correlation_id,payload,next_attempt_at) VALUES (?,?,?,1,'order',?,1,?,'future-retry','{}'::jsonb,?)`, pair.A.Store.ID().String(), eventID.String(), "order.created.v1", aggregateID.String(), now, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	backlog, err := outbox.Backlog(context.Background())
	if err != nil || backlog.Due != 0 || backlog.Retrying != 1 {
		t.Fatalf("backlog=%#v error=%v", backlog, err)
	}
	items, err := outbox.List(context.Background(), pair.A.Store.ID(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v error=%v", items, err)
	}
	delivery := eventingpostgres.NewDelivery(db, nil)
	scopedID, unscopedID := mustEventID(t), mustEventID(t)
	consumer := "inspect-" + unscopedID.String()
	if err := delivery.RecordDeadLetter(context.Background(), eventapplication.DeadLetter{ID: scopedID, StoreID: pair.A.Store.ID(), EventID: eventID, Consumer: consumer, Topic: eventapplication.TopicOrders, Partition: 0, Offset: 1, Digest: [32]byte{1}, EventType: "order.created.v1", Classification: "permanent", Diagnostic: "contract failure", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := delivery.RecordDeadLetter(context.Background(), eventapplication.DeadLetter{ID: unscopedID, Consumer: consumer, Topic: eventapplication.TopicOrders, Partition: 0, Offset: 2, Digest: [32]byte{2}, Classification: "malformed", Diagnostic: "malformed message", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	storeID := pair.A.Store.ID()
	scoped, err := delivery.ListDeadLetters(context.Background(), &storeID, 10)
	if err != nil || len(scoped) != 1 || scoped[0].DeadLetterID != scopedID.String() {
		t.Fatalf("scoped=%#v error=%v", scoped, err)
	}
	unscoped, err := delivery.ListDeadLetters(context.Background(), nil, 10)
	found := false
	for _, item := range unscoped {
		found = found || item.DeadLetterID == unscopedID.String()
	}
	if err != nil || !found {
		t.Fatalf("unscoped=%#v error=%v", unscoped, err)
	}
}

type searchResolver map[ids.StoreID]catalogapplication.SearchProduct

func (resolver searchResolver) ResolveSearchProduct(_ context.Context, storeID ids.StoreID, _ ids.ProductID) (catalogapplication.SearchProduct, bool, error) {
	product, ok := resolver[storeID]
	return product, ok, nil
}

func TestSearchProjectionUsesPublicResolverAndDoesNotRegress(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	productID, _ := ids.NewProductID()
	now := time.Now().UTC()
	for index, storeID := range []ids.StoreID{pair.A.Store.ID(), pair.B.Store.ID()} {
		if err := db.Exec(`INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,version,published_at,created_at,updated_at) VALUES(?,?,?,?,?,'published',2,?,?,?)`, storeID.String(), productID.String(), "Product", "product-"+string(rune('a'+index)), "Public", now, now, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	resolver := searchResolver{
		pair.A.Store.ID(): {ProductID: productID, AggregateVersion: 2, Name: "Store A", Slug: "product-a", Description: "Public A", PublishedAt: now},
		pair.B.Store.ID(): {ProductID: productID, AggregateVersion: 2, Name: "Store B", Slug: "product-b", Description: "Public B", PublishedAt: now},
	}
	delivery := eventingpostgres.NewDelivery(db, searchpostgres.NewEffect(resolver))
	sharedID := mustEventID(t)
	for _, storeID := range []ids.StoreID{pair.A.Store.ID(), pair.B.Store.ID()} {
		event, err := eventdomain.New(storeID, sharedID, "product.published.v1", "product", productID.String(), 2, now, "search", map[string]any{"product_id": productID.String()})
		if err != nil {
			t.Fatal(err)
		}
		if err := delivery.Process(context.Background(), "search-projection-v1", event); err != nil {
			t.Fatal(err)
		}
	}
	for storeID, expected := range map[ids.StoreID]string{pair.A.Store.ID(): "Store A", pair.B.Store.ID(): "Store B"} {
		var name string
		if err := db.Raw(`SELECT name FROM search_documents WHERE store_id=? AND product_id=?`, storeID.String(), productID.String()).Scan(&name).Error; err != nil || name != expected {
			t.Fatalf("store=%s name=%q error=%v", storeID, name, err)
		}
	}
}

func TestEmailIntentUsesOrderOwnedContactAndDeniesForeignOrder(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	orderID := insertNotificationOrder(t, db, pair.A.Store.ID(), pair.A.UserID)
	repository := orderspostgres.NewRepository(db, nil, nil, nil, nil, nil, nil, nil, nil)
	delivery := eventingpostgres.NewDelivery(db, notificationspostgres.NewEffect(ordersapplication.NewService(repository)))
	now := time.Now().UTC()
	eventID := mustEventID(t)
	event, _ := eventdomain.New(pair.A.Store.ID(), eventID, "order.created.v1", "order", orderID.String(), 1, now, "email", map[string]any{"order_id": orderID.String()})
	if err := delivery.Process(context.Background(), "email-intents-v1", event); err != nil {
		t.Fatal(err)
	}
	var email string
	if err := db.Raw(`SELECT recipient_email FROM notification_email_intents WHERE store_id=? AND event_id=?`, pair.A.Store.ID().String(), eventID.String()).Scan(&email).Error; err != nil || email != "buyer@example.com" {
		t.Fatalf("email=%q error=%v", email, err)
	}
	foreignEvent, _ := eventdomain.New(pair.B.Store.ID(), eventID, "order.created.v1", "order", orderID.String(), 1, now, "email", map[string]any{"order_id": orderID.String()})
	if err := delivery.Process(context.Background(), "email-intents-v1", foreignEvent); err == nil {
		t.Fatal("foreign order notification succeeded")
	}
	var foreignInbox int64
	if err := db.Raw(`SELECT count(*) FROM consumer_inbox WHERE store_id=? AND event_id=?`, pair.B.Store.ID().String(), eventID.String()).Scan(&foreignInbox).Error; err != nil || foreignInbox != 0 {
		t.Fatalf("foreign inbox=%d error=%v", foreignInbox, err)
	}
}

func insertNotificationOrder(t *testing.T, db *gorm.DB, storeID ids.StoreID, userID ids.UserID) ids.OrderID {
	t.Helper()
	now := time.Now().UTC()
	customerID, _ := ids.NewCustomerID()
	cartID, _ := ids.NewCartID()
	shippingRuleID, _ := ids.NewShippingRuleID()
	orderID, _ := ids.NewOrderID()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO customers(store_id,customer_id,user_id,full_name,email,canonical_email,phone,canonical_phone,version,created_at,updated_at) VALUES(?,?,?,'Buyer','buyer@example.com','buyer@example.com','+84901234567','+84901234567',1,?,?)`, []any{storeID.String(), customerID.String(), userID.String(), now, now}},
		{`INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at,closed_at,converted_order_id) VALUES(?,?, 'user',?,'VND','converted',1,?,?,?,?,?)`, []any{storeID.String(), cartID.String(), userID.String(), now.Add(time.Hour), now, now, now, orderID.String()}},
		{`INSERT INTO shipping_rules(store_id,shipping_rule_id,mode,flat_amount_minor,currency,status,version,created_at,updated_at) VALUES(?,?,'free',0,'VND','active',1,?,?)`, []any{storeID.String(), shippingRuleID.String(), now, now}},
		{`INSERT INTO orders(store_id,order_id,order_number,owner_kind,user_id,customer_id,cart_id,currency,subtotal_minor,shipping_total_minor,grand_total_minor,tax_presentation,contact_name,contact_email,contact_phone,shipping_mode,shipping_rule_id,shipping_rule_version,state,version,reservation_expires_at,created_at,updated_at) VALUES(?,?,'ORD-EVENT','user',?,?,?,'VND',0,0,0,'vat_inclusive','Buyer','buyer@example.com','+84901234567','free',?,1,'pending_payment',1,?,?,?)`, []any{storeID.String(), orderID.String(), userID.String(), customerID.String(), cartID.String(), shippingRuleID.String(), now.Add(time.Hour), now, now}},
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, statement := range statements {
			if err := tx.Exec(statement.query, statement.args...).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return orderID
}

func TestConsumerInboxAndAnalyticsEffectAreAtomicAndTenantScoped(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	delivery := eventingpostgres.NewDelivery(db, analyticspostgres.NewEffect())
	sharedID := mustEventID(t)
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, storeID := range []ids.StoreID{pair.A.Store.ID(), pair.A.Store.ID(), pair.B.Store.ID()} {
		event, err := eventdomain.New(storeID, sharedID, "order.created.v1", "order", mustEventID(t).String(), 1, now, "integration", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if err := delivery.Process(context.Background(), "analytics-projection-v1", event); err != nil {
			t.Fatal(err)
		}
	}
	for _, storeID := range []ids.StoreID{pair.A.Store.ID(), pair.B.Store.ID()} {
		var count int64
		if err := db.Raw(`SELECT event_count FROM analytics_daily_counts WHERE store_id=? AND event_date='2026-09-20' AND event_type='order.created.v1'`, storeID.String()).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("store=%s count=%d error=%v", storeID, count, err)
		}
	}
}

type failingEffect struct{}

func (failingEffect) Apply(context.Context, *gorm.DB, eventdomain.Event) (bool, error) {
	return false, errors.New("temporary projection failure")
}

func TestConsumerRetriesBecomeDurableTerminalWithoutInboxCommit(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	delivery := eventingpostgres.NewDelivery(db, failingEffect{})
	consumer, err := eventapplication.NewConsumer("failing-projection-v1", delivery, 2, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	eventID := mustEventID(t)
	orderID := mustEventID(t)
	guestID := mustEventID(t)
	event, _ := eventdomain.New(pair.A.Store.ID(), eventID, "order.created.v1", "order", orderID.String(), 1, time.Now().UTC(), "failure", map[string]any{"store_id": pair.A.Store.ID().String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	message, _ := eventapplication.MessageFor(event)
	record := eventapplication.Record{Topic: message.Topic, Partition: 2, Offset: 42, Key: message.Key, Headers: message.Headers, Value: message.Value}
	if err := consumer.Process(context.Background(), record); err == nil {
		t.Fatal("first delivery unexpectedly terminal")
	}
	if err := consumer.Process(context.Background(), record); err != nil {
		t.Fatalf("exhausted delivery error=%v", err)
	}
	var row struct {
		Classification string
		Attempts       int
	}
	if err := db.Raw(`SELECT classification,attempts FROM event_dead_letters WHERE consumer_name='failing-projection-v1' AND topic=? AND partition=2 AND message_offset=42`, eventapplication.TopicOrders).Scan(&row).Error; err != nil || row.Classification != "exhausted" || row.Attempts != 2 {
		t.Fatalf("dead letter=%#v error=%v", row, err)
	}
	var inbox int64
	if err := db.Raw(`SELECT count(*) FROM consumer_inbox WHERE store_id=? AND consumer_name='failing-projection-v1' AND event_id=?`, pair.A.Store.ID().String(), eventID.String()).Scan(&inbox).Error; err != nil || inbox != 0 {
		t.Fatalf("inbox=%d error=%v", inbox, err)
	}
}

func eventingPool(t *testing.T) *pgxpool.Pool {
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

func mustEventID(t *testing.T) ids.EventID {
	t.Helper()
	id, err := ids.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
