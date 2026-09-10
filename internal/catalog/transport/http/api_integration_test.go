//go:build integration

package cataloghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

func TestCatalogAPIDeniesCrossStoreResourcesAndCursors(t *testing.T) {
	pool := apiPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	catalog := catalogpostgres.NewRepository(db)
	service := application.NewService(catalog, application.DefaultIDGenerator(), time.Now)
	productA := createAPIProduct(t, service, pair.A.Store.ID(), "product-a", "SKU-A", "BAR-A")
	productB := createAPIProduct(t, service, pair.B.Store.ID(), "product-b", "SKU-B", "BAR-B")
	_ = createAPIProduct(t, service, pair.B.Store.ID(), "product-b-2", "SKU-B-2", "BAR-B-2")
	categoryB, err := service.CreateCategory(context.Background(), pair.B.Store.ID(), nil, "Foreign", "foreign")
	if err != nil {
		t.Fatal(err)
	}
	pageB, err := service.ListProducts(context.Background(), pair.B.Store.ID(), 1, "")
	if err != nil || pageB.NextCursor == "" {
		t.Fatalf("B cursor = %q, error = %v", pageB.NextCursor, err)
	}
	engine := catalogEngine(t, db, service, authn.NewUserPrincipal(pair.A.Store.ID().String(), pair.A.UserID.String(), "session"))
	host := pair.A.Store.Subdomain().String() + ".example.test"
	requests := []struct{ name, method, path, body string }{
		{name: "product read", method: http.MethodGet, path: "/api/v1/admin/catalog/products/" + productB.ID.String()},
		{name: "product update", method: http.MethodPut, path: "/api/v1/admin/catalog/products/" + productB.ID.String(), body: productWriteBody(productB, productB.Variants[0].ID.String(), productB.Media[0].ID.String())},
		{name: "foreign variant", method: http.MethodPut, path: "/api/v1/admin/catalog/products/" + productA.ID.String(), body: productWriteBody(productA, productB.Variants[0].ID.String(), productA.Media[0].ID.String())},
		{name: "foreign media", method: http.MethodPut, path: "/api/v1/admin/catalog/products/" + productA.ID.String(), body: productWriteBody(productA, productA.Variants[0].ID.String(), productB.Media[0].ID.String())},
		{name: "category archive", method: http.MethodPost, path: "/api/v1/admin/catalog/categories/" + categoryB.ID.String() + "/archive", body: `{"expected_version":1}`},
		{name: "cursor", method: http.MethodGet, path: "/api/v1/admin/catalog/products?cursor=" + pageB.NextCursor},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			response := performCatalogRequest(engine, request.method, host, request.path, request.body)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	unchanged, err := service.GetProduct(context.Background(), pair.B.Store.ID(), productB.ID)
	if err != nil || unchanged.Version != productB.Version || unchanged.Slug != productB.Slug {
		t.Fatalf("foreign product changed: %#v, error = %v", unchanged, err)
	}
	localUnchanged, err := service.GetProduct(context.Background(), pair.A.Store.ID(), productA.ID)
	if err != nil || localUnchanged.Version != productA.Version || localUnchanged.Variants[0].ID != productA.Variants[0].ID || localUnchanged.Media[0].ID != productA.Media[0].ID {
		t.Fatalf("local product changed after foreign child request: %#v, error = %v", localUnchanged, err)
	}
	categories, err := service.ListCategories(context.Background(), pair.B.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	var categoryActive bool
	for _, category := range categories {
		if category.ID == categoryB.ID {
			categoryActive = category.Status == domain.ItemActive
		}
	}
	if !categoryActive {
		t.Fatal("foreign category changed after denied archive")
	}
}

func TestCatalogAPIDeniesUnauthorizedMutationsBeforeService(t *testing.T) {
	pool := apiPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	service := application.NewService(catalogpostgres.NewRepository(db), application.DefaultIDGenerator(), time.Now)
	host := pair.A.Store.Subdomain().String() + ".example.test"
	guestID, _ := ids.NewGuestID()
	tests := []struct {
		name      string
		principal authn.Principal
		prepare   func()
	}{
		{name: "unauthenticated"},
		{name: "guest", principal: authn.NewGuestPrincipal(pair.A.Store.ID().String(), guestID.String(), "session")},
		{name: "no membership", principal: authn.NewUserPrincipal(pair.A.Store.ID().String(), pair.B.UserID.String(), "session")},
		{name: "staff", principal: authn.NewUserPrincipal(pair.A.Store.ID().String(), pair.A.UserID.String(), "session"), prepare: func() {
			if _, err := pool.Exec(context.Background(), `UPDATE store_memberships SET role='staff' WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "inactive", principal: authn.NewUserPrincipal(pair.A.Store.ID().String(), pair.A.UserID.String(), "session"), prepare: func() {
			if _, err := pool.Exec(context.Background(), `UPDATE store_memberships SET status='inactive' WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "suspended", principal: authn.NewUserPrincipal(pair.A.Store.ID().String(), pair.A.UserID.String(), "session"), prepare: func() {
			if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='suspended' WHERE store_id=$1::uuid`, pair.A.Store.ID().String()); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='active' WHERE store_id=$1::uuid`, pair.A.Store.ID().String()); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `UPDATE store_memberships SET status='active',role='owner' WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
				t.Fatal(err)
			}
			if test.prepare != nil {
				test.prepare()
			}
			engine := catalogEngine(t, db, service, test.principal)
			response := performCatalogRequest(engine, http.MethodPost, host, "/api/v1/admin/catalog/categories", `{"name":"Denied","slug":"denied"}`)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var count int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM catalog_categories WHERE store_id=$1::uuid AND slug='denied'`, pair.A.Store.ID().String()).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied mutation count = %d, error = %v", count, err)
			}
		})
	}
}

func catalogEngine(t *testing.T, gormDB *gorm.DB, service application.Service, principal authn.Principal) *gin.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	stores := storespostgres.NewRepository(gormDB, auditpostgres.NewWriter())
	hosts := tenant.NewHostMiddleware(storesapplication.NewResolveStore(stores), "example.test", "auth.example.test", logger, time.Second)
	tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(stores), logger, time.Second)
	authenticate := func(ctx *gin.Context) {
		if principal.Kind() != "" {
			ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), principal))
		}
	}
	engine := httpserver.NewEngine(logger)
	NewHandler(service, authenticate, tenantMiddleware, logger, time.Second).Register(engine, hosts)
	return engine
}

func performCatalogRequest(engine http.Handler, method, host, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+path, bytes.NewBufferString(body))
	request.Host = host
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func createAPIProduct(t *testing.T, service application.Service, storeID ids.StoreID, slug, sku, barcode string) domain.Product {
	t.Helper()
	product, err := service.CreateProduct(context.Background(), storeID, domain.ProductDraft{Name: slug, Slug: slug, Variants: []domain.VariantDraft{{SKU: sku, Barcode: &barcode}}, Media: []domain.MediaDraft{{ObjectKey: slug + "/image.jpg", AltText: slug, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	return product
}

func productWriteBody(product domain.Product, variantID, mediaID string) string {
	body := map[string]any{"name": product.Name, "slug": product.Slug, "description": product.Description, "expected_version": product.Version, "variants": []map[string]any{{"variant_id": variantID, "sku": product.Variants[0].SKU, "barcode": product.Variants[0].Barcode, "option_values": map[string]string{}, "weight_grams": 0, "dimensions": map[string]any{}}}, "category_ids": []string{}, "media": []map[string]any{{"media_id": mediaID, "object_key": product.Media[0].ObjectKey, "alt_text": product.Media[0].AltText, "position": 0}}}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func apiPool(tb testing.TB) *pgxpool.Pool {
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
