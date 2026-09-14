package paymenthttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type paymentServiceStub struct {
	instruction  application.InstructionCommand
	confirmation application.ConfirmationCommand
}

func (*paymentServiceStub) GetConfig(context.Context, ids.StoreID) (domain.VietQRConfig, error) {
	return domain.VietQRConfig{}, application.ErrNotFound
}
func (*paymentServiceStub) ReplaceConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.VietQRConfigDraft) (domain.VietQRConfig, error) {
	return domain.VietQRConfig{}, nil
}
func (stub *paymentServiceStub) CreateInstruction(_ context.Context, command application.InstructionCommand) (domain.Instruction, error) {
	stub.instruction = command
	return domain.Instruction{AttemptID: ids.PaymentAttemptID("128f0f7d-7b3d-4d2e-8f4f-3b86f31f2743"), Created: true}, nil
}
func (stub *paymentServiceStub) Confirm(_ context.Context, command application.ConfirmationCommand) (domain.Confirmation, error) {
	stub.confirmation = command
	return domain.Confirmation{PaymentID: command.PaymentID, Outcome: "paid", Created: true}, nil
}
func (*paymentServiceStub) ListReconciliation(context.Context, ids.StoreID, int, time.Time, ids.ReconciliationID) ([]domain.Reconciliation, error) {
	return nil, nil
}

type paymentOrderStub struct{ order ordersdomain.Order }

func (stub paymentOrderStub) GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (ordersdomain.Order, error) {
	return stub.order, nil
}
func (stub paymentOrderStub) GetForGrant(context.Context, ids.StoreID, ids.OrderID, string) (ordersdomain.Order, error) {
	return stub.order, nil
}

type paymentAuthenticatorStub struct{}

func (paymentAuthenticatorStub) AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error) {
	return identityapplication.UserRecord{}, "", errors.New("not a user token")
}

type paymentHostResolver struct{ store storesapplication.StoreRecord }

func (resolver paymentHostResolver) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	return resolver.store, nil
}

type permissionStub struct{ allowed bool }

func (stub permissionStub) HasPermission(context.Context, ids.StoreID, ids.UserID, storesdomain.Permission) (bool, error) {
	return stub.allowed, nil
}

type paymentTenantRepository struct {
	storeID ids.StoreID
	userID  ids.UserID
	role    storesdomain.Role
}

func (repository paymentTenantRepository) ResolveTenant(context.Context, ids.StoreID, ids.UserID) (storesapplication.TenantRecord, bool, error) {
	return storesapplication.TenantRecord{StoreID: repository.storeID, UserID: repository.userID, StoreName: "Store", StoreStatus: storesdomain.StoreStatusActive, MembershipStatus: storesdomain.MembershipStatusActive, Role: repository.role, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, true, nil
}

func TestInstructionDerivesPaymentFromOwnedOrderAndRequiresIdempotency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storeID, _ := ids.NewStoreID()
	orderID, _ := ids.NewOrderID()
	paymentID, _ := ids.NewPaymentID()
	service := &paymentServiceStub{}
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	hosts := tenant.NewHostMiddleware(paymentHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	handler := NewHandler(service, paymentOrderStub{order: ordersdomain.Order{ID: orderID, Payment: ordersdomain.Payment{ID: paymentID, Provider: domain.ProviderVietQR}}}, paymentAuthenticatorStub{}, func(ctx *gin.Context) { ctx.Next() }, tenant.Middleware{}, permissionStub{}, slog.New(slog.DiscardHandler), time.Second)
	handler.Register(engine, hosts)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/orders/"+orderID.String()+"/payments/vietqr/instructions", nil)
	request.Host = "shop.example.test"
	request.Header.Set("Authorization", "Bearer order-grant")
	request.Header.Set("Idempotency-Key", "instruction-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.instruction.StoreID != storeID || service.instruction.OrderID != orderID || service.instruction.PaymentID != paymentID || service.instruction.IdempotencyKey != "instruction-key" {
		t.Fatalf("response = %d, command = %#v", response.Code, service.instruction)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("instruction response is cacheable")
	}
}

func TestPrivilegedPaymentRoutesEnforceWorkforceScopeAndExplicitPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storeID, _ := ids.NewStoreID()
	foreignStoreID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	paymentID, _ := ids.NewPaymentID()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	hosts := tenant.NewHostMiddleware(paymentHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)

	for _, test := range []struct {
		name           string
		path           string
		method         string
		body           string
		role           storesdomain.Role
		allowed        bool
		principalStore ids.StoreID
		guest          bool
		want           int
	}{{"staff confirm denied", "/api/v1/admin/payments/" + paymentID.String() + "/vietqr-confirmations", http.MethodPost, `{"transfer_reference":"VQREFERENCE","amount_minor":1000,"received_at":"2026-09-13T12:00:00Z"}`, storesdomain.RoleStaff, false, storeID, false, http.StatusForbidden}, {"staff confirm granted", "/api/v1/admin/payments/" + paymentID.String() + "/vietqr-confirmations", http.MethodPost, `{"transfer_reference":"VQREFERENCE","amount_minor":1000,"received_at":"2026-09-13T12:00:00Z"}`, storesdomain.RoleStaff, true, storeID, false, http.StatusCreated}, {"staff reconciliation denied", "/api/v1/admin/payment-reconciliation-items", http.MethodGet, "", storesdomain.RoleStaff, false, storeID, false, http.StatusForbidden}, {"guest config denied", "/api/v1/admin/payment-providers/vietqr", http.MethodGet, "", storesdomain.RoleOwner, false, storeID, true, http.StatusUnauthorized}, {"foreign store principal denied", "/api/v1/admin/payment-reconciliation-items", http.MethodGet, "", storesdomain.RoleOwner, false, foreignStoreID, false, http.StatusUnauthorized}} {
		t.Run(test.name, func(t *testing.T) {
			service := &paymentServiceStub{}
			engine := gin.New()
			authenticate := func(ctx *gin.Context) {
				principal := authn.NewUserPrincipal(test.principalStore.String(), userID.String(), "session")
				if test.guest {
					principal = authn.NewGuestPrincipal(storeID.String(), "cba46f45-6c75-4f61-b87d-28ed84dd35a0", "session")
				}
				ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), principal))
				ctx.Next()
			}
			tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(paymentTenantRepository{storeID: storeID, userID: userID, role: test.role}), slog.New(slog.DiscardHandler), time.Second)
			NewHandler(service, paymentOrderStub{}, paymentAuthenticatorStub{}, authenticate, tenantMiddleware, permissionStub{allowed: test.allowed}, slog.New(slog.DiscardHandler), time.Second).Register(engine, hosts)
			request := httptest.NewRequestWithContext(context.Background(), test.method, "http://shop.example.test"+test.path, strings.NewReader(test.body))
			request.Host = "shop.example.test"
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("Idempotency-Key", "confirmation-key")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestPaymentErrorClassifications(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := Handler{logger: slog.New(slog.DiscardHandler)}
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{{"dependency", application.ErrDependency, http.StatusServiceUnavailable}, {"forbidden", application.ErrForbidden, http.StatusForbidden}, {"business unavailable", application.ErrUnavailable, http.StatusUnprocessableEntity}} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			handler.writeError(ctx, test.err)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}
