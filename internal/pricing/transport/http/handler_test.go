package pricinghttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/pricing/application"
	"github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

func TestPricingRoutesEnforceTenantPrincipalAndRole(t *testing.T) {
	storeID, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	otherStoreID, _ := ids.ParseStoreID("22222222-2222-4222-8222-222222222222")
	userID, _ := ids.ParseUserID("33333333-3333-4333-8333-333333333333")
	for _, test := range []struct {
		name      string
		principal authn.Principal
		role      storesdomain.Role
		want      int
	}{
		{name: "owner", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleOwner, want: http.StatusOK},
		{name: "admin", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleAdmin, want: http.StatusOK},
		{name: "staff", principal: authn.NewUserPrincipal(storeID.String(), userID.String(), "session"), role: storesdomain.RoleStaff, want: http.StatusForbidden},
		{name: "cross store", principal: authn.NewUserPrincipal(otherStoreID.String(), userID.String(), "session"), role: storesdomain.RoleOwner, want: http.StatusUnauthorized},
		{name: "guest", principal: authn.NewGuestPrincipal(storeID.String(), "44444444-4444-4444-8444-444444444444", "session"), role: storesdomain.RoleOwner, want: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			engine := httpserver.NewEngine(logger)
			hosts := tenant.NewHostMiddleware(pricingHostResolver{storeID: storeID}, "example.test", "auth.example.test", logger, time.Second)
			resolver := storesapplication.NewResolveTenant(pricingTenantRepository{storeID: storeID, userID: userID, role: test.role})
			tenantMiddleware := tenant.NewMiddleware(resolver, logger, time.Second)
			authenticate := func(ctx *gin.Context) {
				ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), test.principal))
			}
			service := &pricingServiceStub{}
			NewHandler(service, authenticate, tenantMiddleware, logger, time.Second).Register(engine, hosts)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://shop.example.test/api/v1/admin/pricing/price-lists", nil)
			request.Host = "shop.example.test"
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

type pricingHostResolver struct{ storeID ids.StoreID }

func (stub pricingHostResolver) Execute(_ context.Context, subdomain string) (storesapplication.StoreRecord, error) {
	if subdomain != "shop" {
		return storesapplication.StoreRecord{}, storesapplication.ErrStoreUnavailable
	}
	parsed, _ := storesdomain.ParseSubdomain(subdomain)
	return storesapplication.StoreRecord{StoreID: stub.storeID, Subdomain: parsed, Name: "Shop", Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, nil
}

type pricingTenantRepository struct {
	storeID ids.StoreID
	userID  ids.UserID
	role    storesdomain.Role
}

func (stub pricingTenantRepository) ResolveTenant(_ context.Context, storeID ids.StoreID, userID ids.UserID) (storesapplication.TenantRecord, bool, error) {
	if storeID != stub.storeID || userID != stub.userID {
		return storesapplication.TenantRecord{}, false, nil
	}
	return storesapplication.TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Shop", StoreStatus: storesdomain.StoreStatusActive, MembershipStatus: storesdomain.MembershipStatusActive, Role: stub.role, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

type pricingServiceStub struct{ storeID ids.StoreID }

func (*pricingServiceStub) Create(context.Context, ids.StoreID, domain.PriceListDraft) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (*pricingServiceStub) Replace(context.Context, ids.StoreID, ids.PriceListID, int64, domain.PriceListDraft) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (*pricingServiceStub) Get(context.Context, ids.StoreID, ids.PriceListID) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (stub *pricingServiceStub) List(_ context.Context, storeID ids.StoreID, _ int, _ string) (application.Page, error) {
	stub.storeID = storeID
	return application.Page{}, nil
}
func (*pricingServiceStub) Archive(context.Context, ids.StoreID, ids.PriceListID, int64) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
