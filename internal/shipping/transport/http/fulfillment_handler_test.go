package shippinghttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type fulfillmentServiceStub struct{ command application.Command }

func (stub *fulfillmentServiceStub) Mutate(_ context.Context, command application.Command) (domain.Shipment, bool, error) {
	stub.command = command
	return domain.Shipment{ID: ids.ShipmentID("33333333-3333-4333-8333-333333333333"), OrderID: command.OrderID, State: domain.ShipmentProcessing, Carrier: command.Details.Carrier, TrackingNumber: command.Details.TrackingNumber, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, true, nil
}
func (*fulfillmentServiceStub) GetOrder(context.Context, ids.StoreID, ids.OrderID) (ordersdomain.Order, error) {
	return ordersdomain.Order{}, nil
}
func (*fulfillmentServiceStub) ListOrders(context.Context, ids.StoreID, string, int, string) ([]ordersdomain.Order, string, error) {
	return nil, "", nil
}

type fulfillmentPermissions struct{ allowed bool }

func (stub fulfillmentPermissions) HasPermission(context.Context, ids.StoreID, ids.UserID, storesdomain.Permission) (bool, error) {
	return stub.allowed, nil
}

type fulfillmentHostResolver struct{ store storesapplication.StoreRecord }

func (resolver fulfillmentHostResolver) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	return resolver.store, nil
}

type fulfillmentTenantRepository struct {
	storeID ids.StoreID
	userID  ids.UserID
}

func (repository fulfillmentTenantRepository) ResolveTenant(context.Context, ids.StoreID, ids.UserID) (storesapplication.TenantRecord, bool, error) {
	return storesapplication.TenantRecord{StoreID: repository.storeID, UserID: repository.userID, StoreName: "Store", StoreStatus: storesdomain.StoreStatusActive, MembershipStatus: storesdomain.MembershipStatusActive, Role: storesdomain.RoleStaff, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

func TestFulfillmentRouteUsesTenantActorAndExplicitStaffPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	orderID, _ := ids.NewOrderID()
	for _, test := range []struct {
		name    string
		allowed bool
		want    int
	}{{"denied", false, http.StatusForbidden}, {"granted", true, http.StatusCreated}} {
		t.Run(test.name, func(t *testing.T) {
			service := &fulfillmentServiceStub{}
			engine := gin.New()
			subdomain, _ := storesdomain.ParseSubdomain("shop")
			hosts := tenant.NewHostMiddleware(fulfillmentHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
			authenticate := func(ctx *gin.Context) {
				ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewUserPrincipal(storeID.String(), userID.String(), "session")))
				ctx.Next()
			}
			tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(fulfillmentTenantRepository{storeID: storeID, userID: userID}), slog.New(slog.DiscardHandler), time.Second)
			NewFulfillmentHandler(service, authenticate, tenantMiddleware, fulfillmentPermissions{allowed: test.allowed}, slog.New(slog.DiscardHandler), time.Second).Register(engine, hosts)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/admin/orders/"+orderID.String()+"/shipments", strings.NewReader(`{"carrier":"VNPost","tracking_number":"VN123"}`))
			request.Host = "shop.example.test"
			request.Header.Set("Authorization", "Bearer workforce")
			request.Header.Set("Idempotency-Key", "prepare")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if test.allowed && (service.command.StoreID != storeID || service.command.ActorID != userID || service.command.OrderID != orderID || response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("command/header = %#v / %q", service.command, response.Header().Get("Cache-Control"))
			}
			if !test.allowed && service.command.StoreID != "" {
				t.Fatalf("denied request reached service: %#v", service.command)
			}
		})
	}
}
