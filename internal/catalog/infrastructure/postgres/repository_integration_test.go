//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type queryCounter struct{ count atomic.Int64 }

func (counter *queryCounter) LogMode(logger.LogLevel) logger.Interface      { return counter }
func (counter *queryCounter) Info(context.Context, string, ...interface{})  {}
func (counter *queryCounter) Warn(context.Context, string, ...interface{})  {}
func (counter *queryCounter) Error(context.Context, string, ...interface{}) {}
func (counter *queryCounter) Trace(context.Context, time.Time, func() (string, int64), error) {
	counter.count.Add(1)
}

func TestRepositoryScopesCatalogAndUniqueValuesByStore(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	now := time.Now().UTC()
	categoryA := newCategory(t, pair.A.Store.ID(), "category")
	categoryB := newCategory(t, pair.B.Store.ID(), "category")
	if _, err := repository.CreateCategory(context.Background(), pair.A.Store.ID(), categoryA); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCategory(context.Background(), pair.B.Store.ID(), categoryB); err != nil {
		t.Fatal(err)
	}
	productA := newProduct(t, pair.A.Store.ID(), "product", "SKU-1", "12345", []ids.CategoryID{categoryA.ID}, now)
	productB := newProduct(t, pair.B.Store.ID(), "product", "SKU-1", "12345", []ids.CategoryID{categoryB.ID}, now)
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), productA); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := repository.CreateProduct(context.Background(), pair.B.Store.ID(), productB); err != nil {
		t.Fatalf("create B with overlapping keys: %v", err)
	}
	if _, err := repository.GetProduct(context.Background(), pair.A.Store.ID(), productB.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store get error = %v", err)
	}
	foreignCategory := newProduct(t, pair.A.Store.ID(), "foreign", "SKU-2", "67890", []ids.CategoryID{categoryB.ID}, now)
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), foreignCategory); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store category error = %v", err)
	}
	duplicate := newProduct(t, pair.A.Store.ID(), "product", "SKU-3", "99999", nil, now)
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), duplicate); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("same-store duplicate error = %v", err)
	}
}

func TestRepositoryVersionGuardAndArchiveRetention(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	product := newProduct(t, pair.A.Store.ID(), "versioned", "VERSIONED", "777", nil, time.Now().UTC())
	created, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), product)
	if err != nil {
		t.Fatal(err)
	}
	created.Name = "Updated"
	created.UpdatedAt = created.UpdatedAt.Add(time.Second)
	if _, err := repository.ReplaceProduct(context.Background(), pair.A.Store.ID(), created.ID, 99, created); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale replace error = %v", err)
	}
	archived, err := repository.ArchiveProduct(context.Background(), pair.A.Store.ID(), created.ID, created.Version, created.UpdatedAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != domain.ProductStatusArchived || archived.Variants[0].Status != domain.ItemArchived {
		t.Fatalf("archived = %#v", archived)
	}
	reuse := newProduct(t, pair.A.Store.ID(), "versioned", "OTHER", "778", nil, time.Now().UTC())
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), reuse); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("archived slug reuse error = %v", err)
	}
}

func TestConcurrentProductReplacementHasOneWinner(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	created, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), newProduct(t, pair.A.Store.ID(), "concurrent", "CONCURRENT", "456", nil, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	lockTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockTransaction.Rollback(context.Background()) })
	if _, err := lockTransaction.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := range errorsByCall {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			candidate := created
			candidate.Name = "Winner " + string(rune('A'+index))
			candidate.UpdatedAt = created.UpdatedAt.Add(time.Duration(index+1) * time.Second)
			<-start
			_, errorsByCall[index] = repository.ReplaceProduct(context.Background(), pair.A.Store.ID(), created.ID, created.Version, candidate)
		}(index)
	}
	close(start)
	waitForBlockedQueries(t, pool, `%pg_advisory_xact_lock%`, 2)
	if err := lockTransaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("results = %v", errorsByCall)
	}
	loaded, err := repository.GetProduct(context.Background(), pair.A.Store.ID(), created.ID)
	if err != nil || loaded.Version != created.Version+1 {
		t.Fatalf("loaded = %#v, error = %v", loaded, err)
	}
}

func TestConcurrentProductEditAndArchiveHaveOneWinner(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	created, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), newProduct(t, pair.A.Store.ID(), "edit-archive", "EDIT-ARCHIVE", "EA-1", nil, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	candidate := created
	candidate.Name = "Edited"
	candidate.UpdatedAt = candidate.UpdatedAt.Add(time.Second)
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	lockTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockTransaction.Rollback(context.Background()) })
	if _, err := lockTransaction.Exec(context.Background(), `SELECT 1 FROM catalog_products WHERE store_id=$1::uuid AND product_id=$2::uuid FOR UPDATE`, pair.A.Store.ID().String(), created.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTransaction.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, errorsByCall[0] = repository.ReplaceProduct(context.Background(), pair.A.Store.ID(), created.ID, created.Version, candidate)
	}()
	go func() {
		defer wait.Done()
		<-start
		_, errorsByCall[1] = repository.ArchiveProduct(context.Background(), pair.A.Store.ID(), created.ID, created.Version, candidate.UpdatedAt.Add(time.Second))
	}()
	close(start)
	waitForBlockedQueries(t, pool, `%pg_advisory_xact_lock%`, 2)
	if err := lockTransaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("results = %v", errorsByCall)
	}
	loaded, err := repository.GetProduct(context.Background(), pair.A.Store.ID(), created.ID)
	if err != nil || loaded.Version != created.Version+1 {
		t.Fatalf("loaded = %#v, error = %v", loaded, err)
	}
}

func TestMigrationCreatesTenantConstraints(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	category := newCategory(t, pair.B.Store.ID(), "foreign-category")
	if _, err := repository.CreateCategory(context.Background(), pair.B.Store.ID(), category); err != nil {
		t.Fatal(err)
	}
	product := newProduct(t, pair.A.Store.ID(), "owned", "OWNED", "234", nil, time.Now().UTC())
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), product); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO catalog_product_categories (store_id,product_id,category_id) VALUES ($1::uuid,$2::uuid,$3::uuid)`, pair.A.Store.ID().String(), product.ID.String(), category.ID.String()); err == nil {
		t.Fatal("cross-store category assignment succeeded")
	}
	var version int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != database.CurrentSchemaVersion {
		t.Fatalf("schema version = %d, error = %v", version, err)
	}
}

func TestOmittedVariantRetainsReservedIdentifiers(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	productID, _ := ids.NewProductID()
	firstID, _ := ids.NewVariantID()
	omittedID, _ := ids.NewVariantID()
	barcodeA, barcodeB := "111000", "222000"
	product, err := domain.NewProduct(pair.A.Store.ID(), productID, domain.ProductDraft{Name: "Variants", Slug: "variants", Variants: []domain.VariantDraft{{ID: firstID, SKU: "FIRST", Barcode: &barcodeA}, {ID: omittedID, SKU: "RESERVED", Barcode: &barcodeB}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), product)
	if err != nil {
		t.Fatal(err)
	}
	replacement := created
	replacement.Variants = nil
	for _, variant := range created.Variants {
		if variant.ID == firstID {
			replacement.Variants = append(replacement.Variants, variant)
		}
	}
	if len(replacement.Variants) != 1 {
		t.Fatalf("retained variants = %#v", replacement.Variants)
	}
	replacement.UpdatedAt = replacement.UpdatedAt.Add(time.Second)
	if _, err := repository.ReplaceProduct(context.Background(), pair.A.Store.ID(), created.ID, created.Version, replacement); err != nil {
		t.Fatal(err)
	}
	var sku string
	var barcode *string
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT sku,barcode,status FROM catalog_variants WHERE store_id=$1::uuid AND variant_id=$2::uuid`, pair.A.Store.ID().String(), omittedID.String()).Scan(&sku, &barcode, &status); err != nil {
		t.Fatal(err)
	}
	if sku != "RESERVED" || barcode == nil || *barcode != barcodeB || status != "archived" {
		t.Fatalf("omitted variant = %s %#v %s", sku, barcode, status)
	}
	reuse := newProduct(t, pair.A.Store.ID(), "reuse", "RESERVED", barcodeB, nil, time.Now().UTC())
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), reuse); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("reserved identifier reuse error = %v", err)
	}
}

func TestCategoryHierarchyRejectsCyclesDepthAndUnsafeArchive(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	root := newCategory(t, pair.A.Store.ID(), "root")
	root, err := repository.CreateCategory(context.Background(), pair.A.Store.ID(), root)
	if err != nil {
		t.Fatal(err)
	}
	childID, _ := ids.NewCategoryID()
	child, _ := domain.NewCategory(pair.A.Store.ID(), childID, &root.ID, "Child", "child", time.Now().UTC())
	child, err = repository.CreateCategory(context.Background(), pair.A.Store.ID(), child)
	if err != nil {
		t.Fatal(err)
	}
	cycled, _ := root.Replaced(&child.ID, root.Name, root.Slug, time.Now().UTC())
	if _, err := repository.ReplaceCategory(context.Background(), pair.A.Store.ID(), root.ID, root.Version, cycled); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("cycle error = %v", err)
	}
	if _, err := repository.ArchiveCategory(context.Background(), pair.A.Store.ID(), root.ID, root.Version, time.Now().UTC()); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("parent archive error = %v", err)
	}
	assigned := newCategory(t, pair.A.Store.ID(), "assigned")
	assigned, err = repository.CreateCategory(context.Background(), pair.A.Store.ID(), assigned)
	if err != nil {
		t.Fatal(err)
	}
	product := newProduct(t, pair.A.Store.ID(), "assigned-product", "ASSIGNED", "ASSIGNED-1", []ids.CategoryID{assigned.ID}, time.Now().UTC())
	if _, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), product); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ArchiveCategory(context.Background(), pair.A.Store.ID(), assigned.ID, assigned.Version, time.Now().UTC()); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("assigned category archive error = %v", err)
	}
	parent := child
	for depth := 2; depth <= 8; depth++ {
		id, _ := ids.NewCategoryID()
		candidate, _ := domain.NewCategory(pair.A.Store.ID(), id, &parent.ID, "Depth", fmt.Sprintf("depth-%d", depth), time.Now().UTC())
		created, createErr := repository.CreateCategory(context.Background(), pair.A.Store.ID(), candidate)
		if depth == 8 {
			if !errors.Is(createErr, application.ErrConflict) {
				t.Fatalf("depth %d error = %v", depth, createErr)
			}
			break
		}
		if createErr != nil {
			t.Fatalf("depth %d: %v", depth, createErr)
		}
		parent = created
	}
}

func TestConcurrentCategoryReparentAndArchiveStayCoherent(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	target, err := repository.CreateCategory(context.Background(), pair.A.Store.ID(), newCategory(t, pair.A.Store.ID(), "target"))
	if err != nil {
		t.Fatal(err)
	}
	moving, err := repository.CreateCategory(context.Background(), pair.A.Store.ID(), newCategory(t, pair.A.Store.ID(), "moving"))
	if err != nil {
		t.Fatal(err)
	}
	reparented, _ := moving.Replaced(&target.ID, moving.Name, moving.Slug, time.Now().UTC())
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	lockTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockTransaction.Rollback(context.Background()) })
	if _, err := lockTransaction.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, errorsByCall[0] = repository.ReplaceCategory(context.Background(), pair.A.Store.ID(), moving.ID, moving.Version, reparented)
	}()
	go func() {
		defer wait.Done()
		<-start
		_, errorsByCall[1] = repository.ArchiveCategory(context.Background(), pair.A.Store.ID(), target.ID, target.Version, time.Now().UTC())
	}()
	close(start)
	waitForBlockedQueries(t, pool, `%pg_advisory_xact_lock%`, 2)
	if err := lockTransaction.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("results = %v", errorsByCall)
	}
	categories, err := repository.ListCategories(context.Background(), pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[ids.CategoryID]domain.Category{}
	for _, category := range categories {
		byID[category.ID] = category
	}
	if byID[target.ID].Status == domain.ItemArchived && byID[moving.ID].ParentID != nil {
		t.Fatalf("active child retained archived parent: %#v", byID)
	}
}

func waitForBlockedQueries(t *testing.T, pool *pgxpool.Pool, queryPattern string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1`, queryPattern).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d queries matching %q reached the database lock", want, queryPattern)
}

func TestProductReplacementRollsBackOnChildConflict(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	first, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), newProduct(t, pair.A.Store.ID(), "first", "FIRST", "100", nil, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.CreateProduct(context.Background(), pair.A.Store.ID(), newProduct(t, pair.A.Store.ID(), "second", "SECOND", "200", nil, time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	candidate := first
	candidate.Variants = append([]domain.Variant(nil), first.Variants...)
	candidate.Name = "Should Roll Back"
	candidate.Variants[0].SKU = second.Variants[0].SKU
	candidate.UpdatedAt = candidate.UpdatedAt.Add(time.Second)
	if _, err := repository.ReplaceProduct(context.Background(), pair.A.Store.ID(), first.ID, first.Version, candidate); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("replacement error = %v", err)
	}
	loaded, err := repository.GetProduct(context.Background(), pair.A.Store.ID(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != first.Name || loaded.Version != first.Version || loaded.Variants[0].SKU != first.Variants[0].SKU {
		t.Fatalf("partial replacement persisted: %#v", loaded)
	}
}

func TestProductPaginationUsesStableValidatedKeysets(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := catalogpostgres.NewRepository(gormtest.FromPool(t, pool))
	clock := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	service := application.NewService(repository, application.DefaultIDGenerator(), func() time.Time { clock = clock.Add(time.Second); return clock })
	for index := range 3 {
		barcode := fmt.Sprintf("P-%d", index)
		if _, err := service.CreateProduct(context.Background(), pair.A.Store.ID(), domain.ProductDraft{Name: "Page", Slug: fmt.Sprintf("page-%d", index), Variants: []domain.VariantDraft{{SKU: fmt.Sprintf("PAGE-%d", index), Barcode: &barcode}}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.ListProducts(context.Background(), pair.A.Store.ID(), 2, "")
	if err != nil || len(first.Products) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, error = %v", first, err)
	}
	if _, err := service.CreateProduct(context.Background(), pair.A.Store.ID(), domain.ProductDraft{Name: "New", Slug: "new-page", Variants: []domain.VariantDraft{{SKU: "NEW-PAGE"}}}); err != nil {
		t.Fatal(err)
	}
	second, err := service.ListProducts(context.Background(), pair.A.Store.ID(), 2, first.NextCursor)
	if err != nil || len(second.Products) != 1 {
		t.Fatalf("second page = %#v, error = %v", second, err)
	}
	for _, product := range second.Products {
		for _, previous := range first.Products {
			if product.ID == previous.ID {
				t.Fatalf("duplicate product %s", product.ID)
			}
		}
		if product.Slug == "new-page" {
			t.Fatal("newer insert crossed the keyset boundary")
		}
	}
	cursor, _ := application.DecodeCursor(first.NextCursor)
	cursor.UpdatedAt = cursor.UpdatedAt.Add(time.Second)
	tampered, _ := application.EncodeCursor(*cursor)
	if _, err := service.ListProducts(context.Background(), pair.A.Store.ID(), 2, tampered); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func TestPublishedProductHydrationUsesConstantQueries(t *testing.T) {
	pool := catalogPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	setup := catalogpostgres.NewRepository(db)
	now := time.Now().UTC()
	for index := range 3 {
		product := newProduct(t, pair.A.Store.ID(), fmt.Sprintf("published-%d", index), fmt.Sprintf("PUBLISHED-%d", index), fmt.Sprintf("900%d", index), nil, now.Add(time.Duration(index)*time.Second))
		created, err := setup.CreateProduct(context.Background(), pair.A.Store.ID(), product)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), `UPDATE catalog_products SET status='published', published_at=$1 WHERE store_id=$2::uuid AND product_id=$3::uuid`, now.Add(time.Duration(index)*time.Second), pair.A.Store.ID().String(), created.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	counter := &queryCounter{}
	repository := catalogpostgres.NewRepository(db.Session(&gorm.Session{Logger: counter}))
	products, err := repository.ListPublished(context.Background(), pair.A.Store.ID(), 3, nil)
	if err != nil || len(products) != 3 {
		t.Fatalf("products = %d, error = %v", len(products), err)
	}
	if queries := counter.count.Load(); queries != 4 {
		t.Fatalf("query count = %d, want 4", queries)
	}
}

func newCategory(tb testing.TB, storeID ids.StoreID, slug string) domain.Category {
	tb.Helper()
	id, _ := ids.NewCategoryID()
	category, err := domain.NewCategory(storeID, id, nil, "Category", slug, time.Now().UTC())
	if err != nil {
		tb.Fatal(err)
	}
	return category
}

func newProduct(tb testing.TB, storeID ids.StoreID, slug, sku, barcode string, categories []ids.CategoryID, now time.Time) domain.Product {
	tb.Helper()
	productID, _ := ids.NewProductID()
	variantID, _ := ids.NewVariantID()
	mediaID, _ := ids.NewMediaID()
	product, err := domain.NewProduct(storeID, productID, domain.ProductDraft{Name: "Product", Slug: slug, Variants: []domain.VariantDraft{{ID: variantID, SKU: sku, Barcode: &barcode}}, CategoryIDs: categories, Media: []domain.MediaDraft{{ID: mediaID, ObjectKey: "products/image.jpg", AltText: "Product image", Position: 0}}}, now)
	if err != nil {
		tb.Fatal(err)
	}
	return product
}

func catalogPool(tb testing.TB) *pgxpool.Pool {
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

var _ application.Repository = (*catalogpostgres.Repository)(nil)
