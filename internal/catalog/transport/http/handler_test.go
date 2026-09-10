package cataloghttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

func TestOptionValuesRejectDuplicateCanonicalKeys(t *testing.T) {
	var request variantRequest
	err := json.NewDecoder(strings.NewReader(`{"sku":"SKU","weight_grams":0,"dimensions":{},"option_values":{" Size ":"M","size":"L"}}`)).Decode(&request)
	if err == nil {
		t.Fatal("duplicate canonical option key was accepted")
	}
}

func TestOptionValuesAcceptStringObject(t *testing.T) {
	var request variantRequest
	err := json.NewDecoder(strings.NewReader(`{"sku":"SKU","weight_grams":0,"dimensions":{},"option_values":{"size":"M"}}`)).Decode(&request)
	if err != nil {
		t.Fatal(err)
	}
	if request.OptionValues["size"] != "M" {
		t.Fatalf("options = %#v", request.OptionValues)
	}
}

func TestCatalogRoutesEnforceHostPrincipalAndRole(t *testing.T) {
	storeID, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	otherStoreID, _ := ids.ParseStoreID("22222222-2222-4222-8222-222222222222")
	userID, _ := ids.ParseUserID("33333333-3333-4333-8333-333333333333")
	tests := []struct {
		name      string
		host      string
		principal authn.Principal
		role      storesdomain.Role
		want      int
	}{
		{name: "owner", host: "shop.example.test", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleOwner, want: http.StatusOK},
		{name: "admin", host: "shop.example.test", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleAdmin, want: http.StatusOK},
		{name: "staff", host: "shop.example.test", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleStaff, want: http.StatusForbidden},
		{name: "guest", host: "shop.example.test", principal: authn.NewGuestPrincipal(storeID.String(), "44444444-4444-4444-8444-444444444444", "session"), role: storesdomain.RoleOwner, want: http.StatusUnauthorized},
		{name: "cross store", host: "shop.example.test", principal: authn.NewUserPrincipal(otherStoreID.String(), userID.String(), "session"), role: storesdomain.RoleOwner, want: http.StatusUnauthorized},
		{name: "wrong host", host: "example.test", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleOwner, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			engine := httpserver.NewEngine(logger)
			hosts := tenant.NewHostMiddleware(hostResolverStub{storeID: storeID}, "example.test", "auth.example.test", logger, time.Second)
			resolver := storesapplication.NewResolveTenant(tenantRepositoryStub{storeID: storeID, userID: userID, role: test.role})
			tenantMiddleware := tenant.NewMiddleware(resolver, logger, time.Second)
			authenticate := func(ctx *gin.Context) {
				ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), test.principal))
			}
			service := &serviceStub{}
			NewHandler(service, authenticate, tenantMiddleware, logger, time.Second).Register(engine, hosts)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+test.host+"/api/v1/admin/catalog/categories", nil)
			request.Host = test.host
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if test.want == http.StatusOK && service.storeID != storeID {
				t.Fatalf("service store = %s", service.storeID)
			}
		})
	}
}

type hostResolverStub struct{ storeID ids.StoreID }

func (stub hostResolverStub) Execute(_ context.Context, subdomain string) (storesapplication.StoreRecord, error) {
	if subdomain != "shop" {
		return storesapplication.StoreRecord{}, storesapplication.ErrStoreUnavailable
	}
	parsed, _ := storesdomain.ParseSubdomain(subdomain)
	return storesapplication.StoreRecord{StoreID: stub.storeID, Subdomain: parsed, Name: "Shop", Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, nil
}

type tenantRepositoryStub struct {
	storeID ids.StoreID
	userID  ids.UserID
	role    storesdomain.Role
}

func (stub tenantRepositoryStub) ResolveTenant(_ context.Context, storeID ids.StoreID, userID ids.UserID) (storesapplication.TenantRecord, bool, error) {
	if storeID != stub.storeID || userID != stub.userID {
		return storesapplication.TenantRecord{}, false, nil
	}
	return storesapplication.TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Shop", StoreStatus: storesdomain.StoreStatusActive, MembershipStatus: storesdomain.MembershipStatusActive, Role: stub.role, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

type serviceStub struct{ storeID ids.StoreID }

func (*serviceStub) CreateProduct(context.Context, ids.StoreID, domain.ProductDraft) (domain.Product, error) {
	return domain.Product{}, nil
}
func (*serviceStub) ReplaceProduct(context.Context, ids.StoreID, ids.ProductID, int64, domain.ProductDraft) (domain.Product, error) {
	return domain.Product{}, nil
}
func (*serviceStub) GetProduct(context.Context, ids.StoreID, ids.ProductID) (domain.Product, error) {
	return domain.Product{}, nil
}
func (*serviceStub) ListProducts(context.Context, ids.StoreID, int, string) (application.Page, error) {
	return application.Page{}, nil
}
func (*serviceStub) ArchiveProduct(context.Context, ids.StoreID, ids.ProductID, int64) (domain.Product, error) {
	return domain.Product{}, nil
}
func (*serviceStub) CreateCategory(context.Context, ids.StoreID, *ids.CategoryID, string, string) (domain.Category, error) {
	return domain.Category{}, nil
}
func (*serviceStub) ReplaceCategory(context.Context, ids.StoreID, ids.CategoryID, int64, *ids.CategoryID, string, string) (domain.Category, error) {
	return domain.Category{}, nil
}
func (stub *serviceStub) ListCategories(_ context.Context, storeID ids.StoreID) ([]domain.Category, error) {
	stub.storeID = storeID
	return []domain.Category{}, nil
}
func (*serviceStub) ArchiveCategory(context.Context, ids.StoreID, ids.CategoryID, int64) (domain.Category, error) {
	return domain.Category{}, nil
}
