package inventoryhttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type serviceStub struct {
	called  bool
	storeID ids.StoreID
}

func (*serviceStub) Get(context.Context, ids.StoreID, ids.VariantID) (domain.StockItem, error) {
	return domain.StockItem{}, nil
}
func (stub *serviceStub) List(_ context.Context, storeID ids.StoreID, _ int, _ string) (application.Page, error) {
	stub.storeID = storeID
	return application.Page{}, nil
}
func (stub *serviceStub) Adjust(_ context.Context, storeID ids.StoreID, _ domain.Adjustment) (domain.StockItem, error) {
	stub.called = true
	stub.storeID = storeID
	return domain.StockItem{}, nil
}

func TestInventoryRoutesEnforceTenantPrincipalAndRole(t *testing.T) {
	storeID, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	otherStoreID, _ := ids.ParseStoreID("22222222-2222-4222-8222-222222222222")
	userID, _ := ids.ParseUserID("33333333-3333-4333-8333-333333333333")
	for _, test := range []struct {
		name             string
		principal        authn.Principal
		role             storesdomain.Role
		membershipStatus storesdomain.MembershipStatus
		hostUnavailable  bool
		want             int
	}{
		{"owner", authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), storesdomain.RoleOwner, storesdomain.MembershipStatusActive, false, http.StatusOK},
		{"admin", authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), storesdomain.RoleAdmin, storesdomain.MembershipStatusActive, false, http.StatusOK},
		{"staff", authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), storesdomain.RoleStaff, storesdomain.MembershipStatusActive, false, http.StatusForbidden},
		{"inactive", authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), storesdomain.RoleOwner, storesdomain.MembershipStatusInactive, false, http.StatusForbidden},
		{"suspended", authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), storesdomain.RoleOwner, storesdomain.MembershipStatusActive, true, http.StatusNotFound},
		{"cross store", authn.NewUserPrincipal(otherStoreID.String(), userID.String(), "session"), storesdomain.RoleOwner, storesdomain.MembershipStatusActive, false, http.StatusUnauthorized},
		{"guest", authn.NewGuestPrincipal(storeID.String(), "44444444-4444-4444-8444-444444444444", "session"), storesdomain.RoleOwner, storesdomain.MembershipStatusActive, false, http.StatusUnauthorized},
		{"unauthenticated", authn.Principal{}, storesdomain.RoleOwner, storesdomain.MembershipStatusActive, false, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, route := range []struct {
				method string
				path   string
				body   string
			}{
				{http.MethodGet, "/api/v1/admin/inventory/stock-items", ""},
				{http.MethodPost, "/api/v1/admin/inventory/stock-items/11111111-1111-4111-8111-111111111111/adjustments", `{"quantity_delta":1,"reason":"receipt","expected_version":0}`},
			} {
				logger := slog.New(slog.DiscardHandler)
				engine := httpserver.NewEngine(logger)
				hosts := tenant.NewHostMiddleware(inventoryHostResolver{storeID: storeID, unavailable: test.hostUnavailable}, "example.test", "auth.example.test", logger, time.Second)
				resolver := storesapplication.NewResolveTenant(inventoryTenantRepository{storeID: storeID, userID: userID, role: test.role, membershipStatus: test.membershipStatus})
				tenantMiddleware := tenant.NewMiddleware(resolver, logger, time.Second)
				authenticate := func(ctx *gin.Context) {
					if test.principal.Kind() != "" {
						ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), test.principal))
					}
				}
				service := &serviceStub{}
				NewHandler(service, authenticate, tenantMiddleware, logger, time.Second).Register(engine, hosts)
				request := httptest.NewRequestWithContext(context.Background(), route.method, "http://shop.example.test"+route.path, strings.NewReader(route.body))
				request.Host = "shop.example.test"
				if route.method == http.MethodPost {
					request.Header.Set("Idempotency-Key", "authorization-test")
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != test.want {
					t.Fatalf("%s status = %d, body = %s", route.method, response.Code, response.Body.String())
				}
				if test.want == http.StatusOK && service.storeID != storeID {
					t.Fatalf("%s service store = %s", route.method, service.storeID)
				}
			}
		})
	}
}

type inventoryHostResolver struct {
	storeID     ids.StoreID
	unavailable bool
}

func (stub inventoryHostResolver) Execute(_ context.Context, subdomain string) (storesapplication.StoreRecord, error) {
	if subdomain != "shop" || stub.unavailable {
		return storesapplication.StoreRecord{}, storesapplication.ErrStoreUnavailable
	}
	parsed, _ := storesdomain.ParseSubdomain(subdomain)
	return storesapplication.StoreRecord{StoreID: stub.storeID, Subdomain: parsed, Name: "Shop", Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, nil
}

type inventoryTenantRepository struct {
	storeID          ids.StoreID
	userID           ids.UserID
	role             storesdomain.Role
	membershipStatus storesdomain.MembershipStatus
}

func (stub inventoryTenantRepository) ResolveTenant(_ context.Context, storeID ids.StoreID, userID ids.UserID) (storesapplication.TenantRecord, bool, error) {
	if storeID != stub.storeID || userID != stub.userID {
		return storesapplication.TenantRecord{}, false, nil
	}
	return storesapplication.TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Shop", StoreStatus: storesdomain.StoreStatusActive, MembershipStatus: stub.membershipStatus, Role: stub.role, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

func TestAdjustmentRejectsInvalidIdempotencyKeyBeforeService(t *testing.T) {
	service := &serviceStub{}
	handler := Handler{service: service, logger: slog.New(slog.DiscardHandler), timeout: time.Second}
	engine := httpserver.NewEngine(slog.New(slog.DiscardHandler))
	engine.POST("/api/v1/admin/inventory/stock-items/:variant_id/adjustments", handler.adjust)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/inventory/stock-items/11111111-1111-4111-8111-111111111111/adjustments", strings.NewReader(`{"quantity_delta":1,"reason":"receipt","expected_version":0}`))
	request.Header.Set("Idempotency-Key", "contains space")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.called {
		t.Fatalf("status = %d, called = %v, body = %s", response.Code, service.called, response.Body.String())
	}
}
