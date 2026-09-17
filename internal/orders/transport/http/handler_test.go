package orderhttp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type serviceStub struct {
	command      application.CheckoutCommand
	cancellation application.CancellationCommand
	grant        string
	userOrder    domain.Order
	grantOrder   domain.Order
	userErr      error
	grantErr     error
}

func (stub *serviceStub) Checkout(_ context.Context, command application.CheckoutCommand) (application.CheckoutResult, error) {
	stub.command = command
	orderID, _ := ids.NewOrderID()
	return application.CheckoutResult{Order: domain.Order{ID: orderID}, Created: true}, nil
}
func (stub *serviceStub) GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (domain.Order, error) {
	return stub.userOrder, stub.userErr
}
func (stub *serviceStub) GetForGrant(_ context.Context, _ ids.StoreID, orderID ids.OrderID, token string) (domain.Order, error) {
	stub.grant = token
	if stub.grantOrder.ID == "" {
		stub.grantOrder.ID = orderID
	}
	return stub.grantOrder, stub.grantErr
}
func (stub *serviceStub) Cancel(_ context.Context, command application.CancellationCommand) (application.CancellationResult, error) {
	stub.cancellation = command
	return application.CancellationResult{OrderID: command.OrderID, State: domain.StateCancelled, Created: true}, nil
}

type userAuthenticatorStub struct {
	user *identityapplication.UserRecord
}

func (stub userAuthenticatorStub) AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error) {
	if stub.user != nil {
		return *stub.user, "session", nil
	}
	return identityapplication.UserRecord{}, "", errors.New("not a user token")
}

type hostResolverStub struct{ store storesapplication.StoreRecord }

func (stub hostResolverStub) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	return stub.store, nil
}

func TestCheckoutDerivesUserOwnerAndRequiresIdempotency(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	addressID, _ := ids.NewAddressID()
	service := &serviceStub{}
	engine := testEngine(service, storeID, func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewUserPrincipal(storeID.String(), userID.String(), "session")))
	})
	body := `{"cart_version":2,"payment_provider":"vietqr","address_id":"` + addressID.String() + `"}`
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/checkout", bytes.NewBufferString(body))
	request.Host = "shop.example.test"
	request.Header.Set("Authorization", "Bearer shopper")
	request.Header.Set("Idempotency-Key", "checkout-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.command.StoreID != storeID || service.command.Owner.UserID != userID || service.command.AddressID != addressID || service.command.IdempotencyKey != "checkout-key" {
		t.Fatalf("response = %d, command = %#v", response.Code, service.command)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("checkout response is cacheable")
	}
}

func TestOrderReadRequiresCredentialAndAcceptsGrant(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	orderID, _ := ids.NewOrderID()
	service := &serviceStub{}
	engine := testEngine(service, storeID, func(ctx *gin.Context) { ctx.Next() })
	path := "http://shop.example.test/api/v1/orders/" + orderID.String()
	unauthenticated := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	unauthenticated.Host = "shop.example.test"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, unauthenticated)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing credential response = %d", response.Code)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Host = "shop.example.test"
	request.Header.Set("Authorization", "Bearer order-grant")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || service.grant != "order-grant" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("grant response = %d, grant = %q", response.Code, service.grant)
	}
}

func TestCancellationRequiresCredential(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	orderID, _ := ids.NewOrderID()
	engine := testEngine(&serviceStub{}, storeID, func(ctx *gin.Context) { ctx.Next() })
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/orders/"+orderID.String()+"/cancellations", bytes.NewBufferString(`{"reason":"Changed my mind"}`))
	request.Host = "shop.example.test"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "cancel")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing credential response = %d", response.Code)
	}
}

func TestCancellationBindsUserOrGuestAndDeniesForeignGrant(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	orderID, _ := ids.NewOrderID()
	userID, _ := ids.NewUserID()
	guestID, _ := ids.NewGuestID()
	for _, test := range []struct {
		name      string
		service   *serviceStub
		auth      UserAuthenticator
		want      int
		actorType application.CancellationActor
		actorID   string
	}{{name: "user", service: &serviceStub{userOrder: domain.Order{ID: orderID}}, auth: userAuthenticatorStub{user: &identityapplication.UserRecord{ID: userID}}, want: http.StatusCreated, actorType: application.CancellationUser, actorID: userID.String()}, {name: "guest grant", service: &serviceStub{grantOrder: domain.Order{ID: orderID, Owner: domain.Owner{GuestID: guestID}}}, auth: userAuthenticatorStub{}, want: http.StatusCreated, actorType: application.CancellationGuest, actorID: guestID.String()}, {name: "foreign grant", service: &serviceStub{grantErr: application.ErrNotFound}, auth: userAuthenticatorStub{}, want: http.StatusNotFound}} {
		t.Run(test.name, func(t *testing.T) {
			engine := cancellationEngine(test.service, test.auth, storeID)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://shop.example.test/api/v1/orders/"+orderID.String()+"/cancellations", bytes.NewBufferString(`{"reason":"Changed my mind"}`))
			request.Host = "shop.example.test"
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "cancel-key")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusCreated && (test.service.cancellation.StoreID != storeID || test.service.cancellation.OrderID != orderID || test.service.cancellation.ActorType != test.actorType || test.service.cancellation.ActorID != test.actorID || test.service.cancellation.IdempotencyKey != "cancel-key" || response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("command = %#v, cache = %q", test.service.cancellation, response.Header().Get("Cache-Control"))
			}
		})
	}
}

func cancellationEngine(service *serviceStub, authenticator UserAuthenticator, storeID ids.StoreID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	store := storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}
	hosts := tenant.NewHostMiddleware(hostResolverStub{store: store}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	NewHandler(service, authenticator, func(ctx *gin.Context) { ctx.Next() }, slog.New(slog.DiscardHandler), time.Second).WithCancellation().Register(engine, hosts)
	return engine
}

func testEngine(service *serviceStub, storeID ids.StoreID, authenticate gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	store := storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}
	hosts := tenant.NewHostMiddleware(hostResolverStub{store: store}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	NewHandler(service, userAuthenticatorStub{}, authenticate, slog.New(slog.DiscardHandler), time.Second).WithCancellation().Register(engine, hosts)
	return engine
}
