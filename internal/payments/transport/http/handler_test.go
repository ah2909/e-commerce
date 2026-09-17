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
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
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

type momoServiceStub struct {
	start         application.InstructionCommand
	callbackToken string
	raw           []byte
	err           error
	refundStore   ids.StoreID
	refundID      ids.RefundID
	refundActor   ids.UserID
	refundKey     string
	settleRef     string
	settledAt     time.Time
	refundErr     error
}

func (*momoServiceStub) GetMoMoConfig(context.Context, ids.StoreID) (domain.MoMoConfig, error) {
	return domain.MoMoConfig{}, application.ErrNotFound
}

func (*momoServiceStub) ReplaceMoMoConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.MoMoConfigDraft) (domain.MoMoConfig, error) {
	return domain.MoMoConfig{}, nil
}

func (stub *momoServiceStub) StartMoMo(_ context.Context, command application.InstructionCommand) (domain.MoMoAttempt, error) {
	stub.start = command
	return domain.MoMoAttempt{AttemptID: ids.PaymentAttemptID("128f0f7d-7b3d-4d2e-8f4f-3b86f31f2743"), Created: true}, stub.err
}

func (stub *momoServiceStub) HandleMoMoIPN(_ context.Context, callbackToken string, raw []byte, _ string) (domain.WebhookResult, error) {
	stub.callbackToken = callbackToken
	stub.raw = append([]byte(nil), raw...)
	return domain.WebhookResult{Outcome: "paid", Created: true}, stub.err
}

func (stub *momoServiceStub) GetRefund(_ context.Context, storeID ids.StoreID, refundID ids.RefundID) (domain.Refund, error) {
	stub.refundStore, stub.refundID = storeID, refundID
	return domain.Refund{ID: refundID, State: "pending"}, stub.refundErr
}

func (stub *momoServiceStub) ExecuteMoMoRefund(_ context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key string) (domain.Refund, error) {
	stub.refundStore, stub.refundID, stub.refundActor, stub.refundKey = storeID, refundID, actorID, key
	return domain.Refund{ID: refundID, State: "succeeded"}, stub.refundErr
}

func (stub *momoServiceStub) SettleVietQRRefund(_ context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key, reference string, settledAt time.Time, _ string) (domain.Refund, error) {
	stub.refundStore, stub.refundID, stub.refundActor, stub.refundKey, stub.settleRef, stub.settledAt = storeID, refundID, actorID, key, reference, settledAt
	return domain.Refund{ID: refundID, State: "succeeded", ExternalReference: reference}, stub.refundErr
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

type paymentOrderStub struct {
	order     ordersdomain.Order
	cancelled *ordersapplication.CancellationCommand
	cancelErr error
}

func (stub paymentOrderStub) GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (ordersdomain.Order, error) {
	return stub.order, nil
}
func (stub paymentOrderStub) GetForGrant(context.Context, ids.StoreID, ids.OrderID, string) (ordersdomain.Order, error) {
	return stub.order, nil
}
func (stub paymentOrderStub) Cancel(_ context.Context, command ordersapplication.CancellationCommand) (ordersapplication.CancellationResult, error) {
	if stub.cancelled != nil {
		*stub.cancelled = command
	}
	return ordersapplication.CancellationResult{OrderID: command.OrderID, State: ordersdomain.StateRefundPending, RefundID: ids.RefundID("55555555-5555-4555-8555-555555555555"), Created: true}, stub.cancelErr
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

func TestMoMoAttemptDerivesPaymentFromOwnedOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storeID, _ := ids.NewStoreID()
	orderID, _ := ids.NewOrderID()
	paymentID, _ := ids.NewPaymentID()
	service := &paymentServiceStub{}
	momo := &momoServiceStub{}
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	hosts := tenant.NewHostMiddleware(paymentHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	handler := NewHandler(service, paymentOrderStub{order: ordersdomain.Order{ID: orderID, Payment: ordersdomain.Payment{ID: paymentID, Provider: domain.ProviderMoMo}}}, paymentAuthenticatorStub{}, func(ctx *gin.Context) { ctx.Next() }, tenant.Middleware{}, permissionStub{}, slog.New(slog.DiscardHandler), time.Second).WithMoMo(momo, 35*time.Second)
	handler.Register(engine, hosts)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/orders/"+orderID.String()+"/payments/momo/attempts", nil)
	request.Host = "shop.example.test"
	request.Header.Set("Authorization", "Bearer order-grant")
	request.Header.Set("Idempotency-Key", "momo-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || momo.start.StoreID != storeID || momo.start.OrderID != orderID || momo.start.PaymentID != paymentID || momo.start.IdempotencyKey != "momo-key" {
		t.Fatalf("response = %d, command = %#v", response.Code, momo.start)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("MoMo response is cacheable")
	}
}

func TestMoMoWebhookPreservesBoundedRawBodyAndAcknowledgesWithNoContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &paymentServiceStub{}
	momo := &momoServiceStub{}
	engine := gin.New()
	handler := NewHandler(service, paymentOrderStub{}, paymentAuthenticatorStub{}, func(ctx *gin.Context) { ctx.Next() }, tenant.Middleware{}, permissionStub{}, slog.New(slog.DiscardHandler), time.Second).WithMoMo(momo, 35*time.Second)
	handler.Register(engine, tenant.HostMiddleware{})
	raw := `{"amount":1000,"signature":"opaque"}`
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/payment-webhooks/momo/abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", strings.NewReader(raw))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || string(momo.raw) != raw || momo.callbackToken == "" {
		t.Fatalf("response = %d, raw = %q, token = %q", response.Code, momo.raw, momo.callbackToken)
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

func TestCancellationAndRefundRoutesBindTenantActorAndRemainNoStore(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	orderID, _ := ids.NewOrderID()
	refundID, _ := ids.NewRefundID()
	var cancellation ordersapplication.CancellationCommand
	momo := &momoServiceStub{}
	engine := refundEngine(storeID, storeID, userID, storesdomain.RoleOwner, true, paymentOrderStub{cancelled: &cancellation}, momo)
	settledAt := "2026-09-17T10:00:00Z"
	for _, test := range []struct {
		name, method, path, body string
		want                     int
	}{{"cancel", http.MethodPost, "/api/v1/admin/orders/" + orderID.String() + "/cancellations", `{"reason":"Customer request"}`, http.StatusCreated}, {"get refund", http.MethodGet, "/api/v1/admin/refunds/" + refundID.String(), "", http.StatusOK}, {"execute refund", http.MethodPost, "/api/v1/admin/refunds/" + refundID.String() + "/momo-executions", "", http.StatusOK}, {"settle refund", http.MethodPost, "/api/v1/admin/refunds/" + refundID.String() + "/vietqr-settlements", `{"external_reference":"BANK-123","settled_at":"` + settledAt + `"}`, http.StatusOK}} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), test.method, "http://shop.example.test"+test.path, strings.NewReader(test.body))
			request.Host = "shop.example.test"
			request.Header.Set("Authorization", "Bearer workforce")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "operation-key")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status/cache = %d/%q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
			}
		})
	}
	if cancellation.StoreID != storeID || cancellation.OrderID != orderID || cancellation.ActorID != userID.String() || cancellation.IdempotencyKey != "operation-key" || momo.refundStore != storeID || momo.refundID != refundID || momo.refundActor != userID || momo.refundKey != "operation-key" || momo.settleRef != "BANK-123" || momo.settledAt.Format(time.RFC3339) != settledAt {
		t.Fatalf("cancellation=%#v refund=%s/%s/%s/%q settlement=%q/%s", cancellation, momo.refundStore, momo.refundID, momo.refundActor, momo.refundKey, momo.settleRef, momo.settledAt)
	}
}

func TestCancellationAndRefundRoutesDenyPermissionAndForeignPrincipal(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	foreignStoreID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	refundID, _ := ids.NewRefundID()
	for _, test := range []struct {
		name           string
		principalStore ids.StoreID
		allowed        bool
		want           int
	}{{"permission denied", storeID, false, http.StatusForbidden}, {"foreign principal", foreignStoreID, true, http.StatusUnauthorized}} {
		t.Run(test.name, func(t *testing.T) {
			momo := &momoServiceStub{}
			engine := refundEngine(storeID, test.principalStore, userID, storesdomain.RoleStaff, test.allowed, paymentOrderStub{}, momo)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/admin/refunds/"+refundID.String()+"/momo-executions", nil)
			request.Host = "shop.example.test"
			request.Header.Set("Authorization", "Bearer workforce")
			request.Header.Set("Idempotency-Key", "operation-key")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want || momo.refundID != "" {
				t.Fatalf("status = %d, want %d, service refund = %s", response.Code, test.want, momo.refundID)
			}
		})
	}
}

func TestRefundRouteMapsConflict(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	refundID, _ := ids.NewRefundID()
	momo := &momoServiceStub{refundErr: application.ErrConflict}
	engine := refundEngine(storeID, storeID, userID, storesdomain.RoleOwner, true, paymentOrderStub{}, momo)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/admin/refunds/"+refundID.String()+"/momo-executions", nil)
	request.Host = "shop.example.test"
	request.Header.Set("Authorization", "Bearer workforce")
	request.Header.Set("Idempotency-Key", "operation-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func refundEngine(storeID, principalStore ids.StoreID, userID ids.UserID, role storesdomain.Role, allowed bool, orders paymentOrderStub, momo *momoServiceStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	hosts := tenant.NewHostMiddleware(paymentHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	authenticate := func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewUserPrincipal(principalStore.String(), userID.String(), "session")))
		ctx.Next()
	}
	tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(paymentTenantRepository{storeID: storeID, userID: userID, role: role}), slog.New(slog.DiscardHandler), time.Second)
	NewHandler(&paymentServiceStub{}, orders, paymentAuthenticatorStub{}, authenticate, tenantMiddleware, permissionStub{allowed: allowed}, slog.New(slog.DiscardHandler), time.Second).WithMoMo(momo, 35*time.Second).WithRefunds().Register(engine, hosts)
	return engine
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
