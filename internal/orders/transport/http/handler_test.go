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
	command application.CheckoutCommand
	grant   string
}

func (stub *serviceStub) Checkout(_ context.Context, command application.CheckoutCommand) (application.CheckoutResult, error) {
	stub.command = command
	orderID, _ := ids.NewOrderID()
	return application.CheckoutResult{Order: domain.Order{ID: orderID}, Created: true}, nil
}
func (*serviceStub) GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (domain.Order, error) {
	return domain.Order{}, application.ErrNotFound
}
func (stub *serviceStub) GetForGrant(_ context.Context, _ ids.StoreID, orderID ids.OrderID, token string) (domain.Order, error) {
	stub.grant = token
	return domain.Order{ID: orderID}, nil
}

type userAuthenticatorStub struct{}

func (userAuthenticatorStub) AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error) {
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

func testEngine(service *serviceStub, storeID ids.StoreID, authenticate gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	store := storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive, DefaultCurrency: "VND"}
	hosts := tenant.NewHostMiddleware(hostResolverStub{store: store}, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	NewHandler(service, userAuthenticatorStub{}, authenticate, slog.New(slog.DiscardHandler), time.Second).Register(engine, hosts)
	return engine
}
