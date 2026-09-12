//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	eventingdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	pricingapplication "github.com/ah2909/e-commerce/internal/pricing/application"
	pricingdomain "github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

func TestPricingPublicationAndPublicVisibilityAreTenantSafeAndAtomic(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalogRepository := catalogpostgres.NewRepository(db)
	pricingRepository := NewRepository(db, catalogpostgres.NewVariantLocker())
	catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
	pricingService := pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
	publication := catalogpostgres.NewPublicationCoordinator(db, pricingRepository, eventingpostgres.NewWriter(), ids.NewEventID)
	catalogService = catalogService.WithPublication(publication, pricingRepository)
	productA := createProduct(t, catalogService, pair.A.Store.ID(), "priced-a", "PRICE-A")
	productB := createProduct(t, catalogService, pair.B.Store.ID(), "priced-b", "PRICE-B")
	draftA := pricingdomain.PriceListDraft{Name: "Retail", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Priority: 10, Prices: []pricingdomain.PriceDraft{{VariantID: productA.Variants[0].ID, AmountMinor: 125000}}}
	listA, err := pricingService.Create(context.Background(), pair.A.Store.ID(), draftA)
	if err != nil {
		t.Fatal(err)
	}
	foreign := draftA
	foreign.Prices = []pricingdomain.PriceDraft{{VariantID: productB.Variants[0].ID, AmountMinor: 1}}
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), foreign); !errors.Is(err, pricingapplication.ErrNotFound) {
		t.Fatalf("foreign variant error = %v", err)
	}
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), draftA); !errors.Is(err, pricingapplication.ErrConflict) {
		t.Fatalf("ambiguous overlap error = %v", err)
	}
	if _, err := catalogService.PublishProduct(context.Background(), pair.A.Store.ID(), productB.ID, productB.Version, "cross-store"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store publication error = %v", err)
	}
	published, err := catalogService.PublishProduct(context.Background(), pair.A.Store.ID(), productA.ID, productA.Version, "publish-request")
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.ProductStatusPublished || published.Version != productA.Version+1 {
		t.Fatalf("published = %#v", published)
	}
	var events int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND aggregate_id=$2::uuid AND event_type='product.published.v1'`, pair.A.Store.ID().String(), productA.ID.String()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events = %d, error = %v", events, err)
	}
	public, err := catalogService.GetPublicProduct(context.Background(), pair.A.Store.ID(), productA.Slug)
	if err != nil || len(public.Variants) != 1 || public.Variants[0].Price.AmountMinor != 125000 || !public.Variants[0].Price.VATInclusive {
		t.Fatalf("public = %#v, error = %v", public, err)
	}
	if _, err := pricingService.Archive(context.Background(), pair.A.Store.ID(), listA.ID, listA.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogService.GetPublicProduct(context.Background(), pair.A.Store.ID(), productA.Slug); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("unpriced public error = %v", err)
	}
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), pricingdomain.PriceListDraft{Name: "Restored", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Priority: 20, Prices: []pricingdomain.PriceDraft{{VariantID: productA.Variants[0].ID, AmountMinor: 130000}}}); err != nil {
		t.Fatal(err)
	}
	if restored, err := catalogService.GetPublicProduct(context.Background(), pair.A.Store.ID(), productA.Slug); err != nil || restored.Variants[0].Price.AmountMinor != 130000 {
		t.Fatalf("restored public product = %#v, error = %v", restored, err)
	}
	if _, err := catalogService.PublishProduct(context.Background(), pair.B.Store.ID(), productB.ID, productB.Version, "unpriced"); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("unpriced publication error = %v", err)
	}
	productB2 := createProduct(t, catalogService, pair.B.Store.ID(), "priced-b-2", "PRICE-B-2")
	if _, err := pricingService.Create(context.Background(), pair.B.Store.ID(), pricingdomain.PriceListDraft{Name: "B Retail", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Prices: []pricingdomain.PriceDraft{{VariantID: productB.Variants[0].ID, AmountMinor: 1}, {VariantID: productB2.Variants[0].ID, AmountMinor: 2}}}); err != nil {
		t.Fatal(err)
	}
	for _, product := range []domain.Product{productB, productB2} {
		if _, err := catalogService.PublishProduct(context.Background(), pair.B.Store.ID(), product.ID, product.Version, "publish-b"); err != nil {
			t.Fatal(err)
		}
	}
	pageB, err := catalogService.ListPublicProducts(context.Background(), pair.B.Store.ID(), 1, "")
	if err != nil || pageB.NextCursor == "" {
		t.Fatalf("B public cursor = %q, error = %v", pageB.NextCursor, err)
	}
	if _, err := catalogService.ListPublicProducts(context.Background(), pair.A.Store.ID(), 1, pageB.NextCursor); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store public cursor error = %v", err)
	}
}

func TestCurrentPriceSelectionBoundariesAndTenantOperations(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalogRepository := catalogpostgres.NewRepository(db)
	repository := NewRepository(db, catalogpostgres.NewVariantLocker())
	service := pricingapplication.NewService(repository, ids.NewPriceListID, time.Now)
	catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
	product := createProduct(t, catalogService, pair.A.Store.ID(), "selection", "SELECTION")
	variantID := product.Variants[0].ID
	now := time.Now().UTC()
	pastFrom, pastUntil := now.Add(-2*time.Hour), now.Add(-time.Hour)
	futureFrom, futureUntil := now.Add(time.Hour), now.Add(2*time.Hour)
	create := func(name string, priority int, amount int64, from, until *time.Time) pricingdomain.PriceList {
		list, err := service.Create(context.Background(), pair.A.Store.ID(), pricingdomain.PriceListDraft{Name: name, Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Priority: priority, ValidFrom: from, ValidUntil: until, Prices: []pricingdomain.PriceDraft{{VariantID: variantID, AmountMinor: amount}}})
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	low := create("Low", 1, 100, nil, nil)
	high := create("High", 2, 200, nil, nil)
	_ = create("Expired", 3, 300, &pastFrom, &pastUntil)
	future := create("Future", 4, 400, &futureFrom, &futureUntil)
	resolved, err := repository.ResolveCurrent(context.Background(), pair.A.Store.ID(), []ids.VariantID{variantID})
	if err != nil || resolved[variantID].AmountMinor != 200 {
		t.Fatalf("current price = %#v, error = %v", resolved, err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE pricing_price_lists SET valid_until=clock_timestamp() WHERE store_id=$1::uuid AND price_list_id=$2::uuid`, pair.A.Store.ID().String(), high.ID.String()); err != nil {
		t.Fatal(err)
	}
	resolved, err = repository.ResolveCurrent(context.Background(), pair.A.Store.ID(), []ids.VariantID{variantID})
	if err != nil || resolved[variantID].AmountMinor != 100 {
		t.Fatalf("exclusive valid_until price = %#v, error = %v", resolved, err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE pricing_price_lists SET valid_from=clock_timestamp() WHERE store_id=$1::uuid AND price_list_id=$2::uuid`, pair.A.Store.ID().String(), future.ID.String()); err != nil {
		t.Fatal(err)
	}
	resolved, err = repository.ResolveCurrent(context.Background(), pair.A.Store.ID(), []ids.VariantID{variantID})
	if err != nil || resolved[variantID].AmountMinor != 400 {
		t.Fatalf("inclusive valid_from price = %#v, error = %v", resolved, err)
	}
	if _, err := service.Get(context.Background(), pair.B.Store.ID(), low.ID); !errors.Is(err, pricingapplication.ErrNotFound) {
		t.Fatalf("cross-store get error = %v", err)
	}
	if _, err := service.Replace(context.Background(), pair.B.Store.ID(), low.ID, low.Version, pricingdomain.PriceListDraft{}); !errors.Is(err, pricingapplication.ErrNotFound) {
		t.Fatalf("cross-store replace error = %v", err)
	}
	if _, err := service.Archive(context.Background(), pair.B.Store.ID(), low.ID, low.Version); !errors.Is(err, pricingapplication.ErrNotFound) {
		t.Fatalf("cross-store archive error = %v", err)
	}
	page, err := service.List(context.Background(), pair.A.Store.ID(), 1, "")
	if err != nil || page.NextCursor == "" {
		t.Fatalf("price-list cursor = %q, error = %v", page.NextCursor, err)
	}
	if _, err := service.List(context.Background(), pair.B.Store.ID(), 1, page.NextCursor); !errors.Is(err, pricingapplication.ErrNotFound) {
		t.Fatalf("cross-store cursor error = %v", err)
	}
	productB := createProduct(t, catalogService, pair.B.Store.ID(), "selection-b", "SELECTION-B")
	if _, err := pool.Exec(context.Background(), `INSERT INTO pricing_prices (store_id,price_list_id,variant_id,amount_minor,version,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,1,1,now(),now())`, pair.A.Store.ID().String(), low.ID.String(), productB.Variants[0].ID.String()); err == nil {
		t.Fatal("cross-store pricing relationship succeeded")
	}
	fixedID, _ := ids.NewPriceListID()
	for _, item := range []struct {
		store   ids.StoreID
		variant ids.VariantID
	}{{pair.A.Store.ID(), variantID}, {pair.B.Store.ID(), productB.Variants[0].ID}} {
		list, err := pricingdomain.NewPriceList(item.store, fixedID, pricingdomain.PriceListDraft{Name: "Shared ID", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Priority: 99, Prices: []pricingdomain.PriceDraft{{VariantID: item.variant, AmountMinor: 500}}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Create(context.Background(), item.store, list); err != nil {
			t.Fatalf("same price-list ID across stores: %v", err)
		}
	}
}

func TestPublicationUsesPostLockDatabaseTime(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalogRepository := catalogpostgres.NewRepository(db)
	pricingRepository := NewRepository(db, catalogpostgres.NewVariantLocker())
	catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
	pricingService := pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
	product := createProduct(t, catalogService, pair.A.Store.ID(), "expiry-lock", "EXPIRY-LOCK")
	until := time.Now().UTC().Add(800 * time.Millisecond)
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), pricingdomain.PriceListDraft{Name: "Expiring", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Prices: []pricingdomain.PriceDraft{{VariantID: product.Variants[0].ID, AmountMinor: 1000}}, ValidUntil: &until}); err != nil {
		t.Fatal(err)
	}
	holder, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	publication := catalogpostgres.NewPublicationCoordinator(db, pricingRepository, eventingpostgres.NewWriter(), ids.NewEventID)
	result := make(chan error, 1)
	go func() {
		_, err := publication.Publish(context.Background(), pair.A.Store.ID(), product.ID, product.Version, "expiry-lock")
		result <- err
	}()
	waitForAdvisoryWaiters(t, pool, 1)
	if remaining := time.Until(until.Add(100 * time.Millisecond)); remaining > 0 {
		time.Sleep(remaining)
	}
	if err := holder.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, application.ErrConflict) {
		t.Fatalf("post-expiry publication error = %v", err)
	}
}

func TestPublicationSerializesWithPriceArchiveAndReplacement(t *testing.T) {
	for _, operation := range []string{"archive", "replace"} {
		t.Run(operation, func(t *testing.T) {
			pool := pricingPool(t)
			pair := storefixture.NewPair(t, pool)
			db := gormtest.FromPool(t, pool)
			catalogRepository := catalogpostgres.NewRepository(db)
			pricingRepository := NewRepository(db, catalogpostgres.NewVariantLocker())
			catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
			pricingService := pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
			product := createProduct(t, catalogService, pair.A.Store.ID(), "price-race-"+operation, "PRICE-RACE-"+operation)
			draft := pricingdomain.PriceListDraft{Name: "Current", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Prices: []pricingdomain.PriceDraft{{VariantID: product.Variants[0].ID, AmountMinor: 1000}}}
			list, err := pricingService.Create(context.Background(), pair.A.Store.ID(), draft)
			if err != nil {
				t.Fatal(err)
			}
			holder, err := pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, pair.A.Store.ID().String()); err != nil {
				t.Fatal(err)
			}
			publication := catalogpostgres.NewPublicationCoordinator(db, pricingRepository, eventingpostgres.NewWriter(), ids.NewEventID)
			publishResult, priceResult := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := publication.Publish(context.Background(), pair.A.Store.ID(), product.ID, product.Version, "price-race")
				publishResult <- err
			}()
			go func() {
				if operation == "archive" {
					_, err := pricingService.Archive(context.Background(), pair.A.Store.ID(), list.ID, list.Version)
					priceResult <- err
					return
				}
				future := time.Now().UTC().Add(time.Hour)
				draft.ValidFrom = &future
				_, err := pricingService.Replace(context.Background(), pair.A.Store.ID(), list.ID, list.Version, draft)
				priceResult <- err
			}()
			waitForAdvisoryWaiters(t, pool, 2)
			if err := holder.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			priceErr, publishErr := <-priceResult, <-publishResult
			if priceErr != nil {
				t.Fatalf("price operation error = %v", priceErr)
			}
			if publishErr != nil && !errors.Is(publishErr, application.ErrConflict) {
				t.Fatalf("publication error = %v", publishErr)
			}
			loaded, err := catalogService.GetProduct(context.Background(), pair.A.Store.ID(), product.ID)
			if err != nil {
				t.Fatal(err)
			}
			var events int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND aggregate_id=$2::uuid`, pair.A.Store.ID().String(), product.ID.String()).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if publishErr == nil && (loaded.Status != domain.ProductStatusPublished || events != 1) {
				t.Fatalf("successful publication state = %#v, events = %d", loaded, events)
			}
			if publishErr != nil && (loaded.Status != domain.ProductStatusDraft || events != 0) {
				t.Fatalf("rejected publication state = %#v, events = %d", loaded, events)
			}
		})
	}
}

func TestOutboxEventIdentityIsTenantScoped(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	eventID, _ := ids.NewEventID()
	aggregateA, _ := ids.NewProductID()
	aggregateB, _ := ids.NewProductID()
	writer := eventingpostgres.NewWriter()
	for _, item := range []struct {
		store     ids.StoreID
		aggregate ids.ProductID
	}{{pair.A.Store.ID(), aggregateA}, {pair.B.Store.ID(), aggregateB}} {
		event, err := eventingdomain.New(item.store, eventID, "product.published.v1", "product", item.aggregate.String(), 1, time.Now(), "same-event", map[string]any{"product_id": item.aggregate.String(), "version": 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return writer.Append(context.Background(), tx, event) }); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE event_id=$1::uuid`, eventID.String()).Scan(&count); err != nil || count != 2 {
		t.Fatalf("same event ID count = %d, error = %v", count, err)
	}
}

func waitForAdvisoryWaiters(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory' AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&count)
		if err == nil && count >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d advisory-lock waiters reached PostgreSQL", want)
}

func TestConcurrentPublicationHasOneWinnerAndOneOutboxFact(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalogRepository := catalogpostgres.NewRepository(db)
	pricingRepository := NewRepository(db, catalogpostgres.NewVariantLocker())
	catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
	pricingService := pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
	product := createProduct(t, catalogService, pair.A.Store.ID(), "concurrent-publish", "CONCURRENT-PUBLISH")
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), pricingdomain.PriceListDraft{Name: "Retail", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Prices: []pricingdomain.PriceDraft{{VariantID: product.Variants[0].ID, AmountMinor: 1000}}}); err != nil {
		t.Fatal(err)
	}
	publication := catalogpostgres.NewPublicationCoordinator(db, pricingRepository, eventingpostgres.NewWriter(), ids.NewEventID)
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range errorsByCall {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = publication.Publish(context.Background(), pair.A.Store.ID(), product.ID, product.Version, "concurrent")
		}(index)
	}
	close(start)
	wait.Wait()
	successes, conflicts := 0, 0
	for _, err := range errorsByCall {
		if err == nil {
			successes++
		} else if errors.Is(err, application.ErrConflict) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("publication results = %v", errorsByCall)
	}
	var events int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND aggregate_id=$2::uuid`, pair.A.Store.ID().String(), product.ID.String()).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events = %d, error = %v", events, err)
	}
}

func TestOutboxFailureRollsBackPublication(t *testing.T) {
	pool := pricingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalogRepository := catalogpostgres.NewRepository(db)
	pricingRepository := NewRepository(db, catalogpostgres.NewVariantLocker())
	catalogService := application.NewService(catalogRepository, application.DefaultIDGenerator(), time.Now)
	pricingService := pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
	product := createProduct(t, catalogService, pair.A.Store.ID(), "rollback-publish", "ROLLBACK-PUBLISH")
	if _, err := pricingService.Create(context.Background(), pair.A.Store.ID(), pricingdomain.PriceListDraft{Name: "Retail", Channel: pricingdomain.ChannelStorefront, Currency: pricingdomain.CurrencyVND, Prices: []pricingdomain.PriceDraft{{VariantID: product.Variants[0].ID, AmountMinor: 1000}}}); err != nil {
		t.Fatal(err)
	}
	publication := catalogpostgres.NewPublicationCoordinator(db, pricingRepository, eventingpostgres.NewWriter(), func() (ids.EventID, error) { return "", errors.New("entropy unavailable") })
	if _, err := publication.Publish(context.Background(), pair.A.Store.ID(), product.ID, product.Version, "rollback"); err == nil {
		t.Fatal("publication succeeded despite event failure")
	}
	loaded, err := catalogService.GetProduct(context.Background(), pair.A.Store.ID(), product.ID)
	if err != nil || loaded.Status != domain.ProductStatusDraft || loaded.Version != product.Version {
		t.Fatalf("product after rollback = %#v, error = %v", loaded, err)
	}
	var events int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1::uuid AND aggregate_id=$2::uuid`, pair.A.Store.ID().String(), product.ID.String()).Scan(&events); err != nil || events != 0 {
		t.Fatalf("events = %d, error = %v", events, err)
	}
}

func createProduct(t *testing.T, service application.Service, storeID ids.StoreID, slug, sku string) domain.Product {
	t.Helper()
	product, err := service.CreateProduct(context.Background(), storeID, domain.ProductDraft{Name: slug, Slug: slug, Variants: []domain.VariantDraft{{SKU: sku}}})
	if err != nil {
		t.Fatal(err)
	}
	return product
}

func pricingPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		tb.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return pool
}
