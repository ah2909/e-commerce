package authz_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type repositoryStub struct{ role domain.Role }

func (stub repositoryStub) CreateWithMembershipAndAudit(context.Context, domain.Store, domain.Membership, string) error {
	return nil
}

type storeResolverStub struct{ storeID ids.StoreID }

func (stub storeResolverStub) Execute(context.Context, string) (application.StoreRecord, error) {
	subdomain, _ := domain.ParseSubdomain("shop")
	return application.StoreRecord{StoreID: stub.storeID, Subdomain: subdomain, Status: domain.StoreStatusActive}, nil
}
func (stub repositoryStub) ResolveTenant(_ context.Context, storeID ids.StoreID, userID ids.UserID) (application.TenantRecord, bool, error) {
	return application.TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Store", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusActive, Role: stub.role, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

func TestPermissionMiddlewareUsesDenyByDefaultMatrix(t *testing.T) {
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID, _ := ids.ParseUserID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	logger := slog.New(slog.DiscardHandler)
	for _, test := range []struct {
		role       domain.Role
		permission domain.Permission
		want       int
	}{
		{domain.RoleOwner, domain.PermissionMembershipsManage, http.StatusNoContent},
		{domain.RoleAdmin, domain.PermissionMembershipsManage, http.StatusForbidden},
		{domain.RoleStaff, domain.PermissionStoreView, http.StatusNoContent},
		{domain.RoleStaff, "unknown", http.StatusForbidden},
	} {
		resolver := application.NewResolveTenant(repositoryStub{role: test.role})
		tenantMiddleware := tenant.NewMiddleware(resolver, logger, time.Second)
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		hosts := tenant.NewHostMiddleware(storeResolverStub{storeID: storeID}, "example.test", "auth.example.test", logger, time.Second)
		handler := hosts.Tenant(tenantMiddleware.Wrap(authz.Require(test.permission, logger, next)))
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/store", nil)
		request.Host = "shop.example.test"
		request = request.WithContext(authn.WithPrincipal(request.Context(), authn.NewUserPrincipal(storeID.String(), userID.String(), "session")))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("role %q permission %q status = %d", test.role, test.permission, response.Code)
		}
	}
}
