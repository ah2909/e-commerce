package customerhttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/customers/application"
	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type customerHostResolver struct{ store storesapplication.StoreRecord }

func (resolver customerHostResolver) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	return resolver.store, nil
}

type customerServiceStub struct {
	err  error
	call string
}

func (stub *customerServiceStub) GetProfile(context.Context, ids.StoreID, ids.UserID) (domain.Profile, error) {
	stub.call = "get-profile"
	return domain.Profile{}, stub.err
}
func (stub *customerServiceStub) PutProfile(context.Context, ids.StoreID, ids.UserID, int64, domain.ProfileDraft) (domain.Profile, error) {
	stub.call = "put-profile"
	return domain.Profile{}, stub.err
}
func (stub *customerServiceStub) CreateAddress(context.Context, ids.StoreID, ids.UserID, domain.AddressDraft) (domain.Address, error) {
	stub.call = "create-address"
	return domain.Address{}, stub.err
}
func (stub *customerServiceStub) GetAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID) (domain.Address, error) {
	stub.call = "get-address"
	return domain.Address{}, stub.err
}
func (stub *customerServiceStub) ListAddresses(context.Context, ids.StoreID, ids.UserID, int, string) (application.AddressPage, error) {
	stub.call = "list-addresses"
	return application.AddressPage{Addresses: []domain.Address{}}, stub.err
}
func (stub *customerServiceStub) ReplaceAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64, domain.AddressDraft) (domain.Address, error) {
	stub.call = "replace-address"
	return domain.Address{}, stub.err
}
func (stub *customerServiceStub) ArchiveAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64) error {
	stub.call = "archive-address"
	return stub.err
}

func TestCustomerRoutesRejectGuestPrincipal(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	guestID, _ := ids.NewGuestID()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	logger := slog.New(slog.DiscardHandler)
	hosts := tenant.NewHostMiddleware(customerHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive}}, "example.test", "auth.example.test", logger, time.Second)
	authenticate := func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewGuestPrincipal(storeID.String(), guestID.String(), "session")))
		ctx.Next()
	}
	engine := gin.New()
	NewHandler(nil, authenticate, logger, time.Second).Register(engine, hosts)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/customer/profile", nil)
	request.Host = "shop.example.test"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestCustomerUserRouteSuccesses(t *testing.T) {
	service := &customerServiceStub{}
	engine, storeID, userID := customerTestEngine(t, service, authn.KindUser)
	addressID, _ := ids.NewAddressID()
	addressBody := `{"expected_version":1,"label":"Home","recipient_name":"A","phone":"0901234567","line1":"1 Main","ward":"Ward","district":"District","province":"Province","country_code":"VN"}`
	for _, test := range []struct {
		method, path, body, call string
		status                   int
	}{
		{http.MethodPut, "/api/v1/customer/profile", `{"expected_version":0,"full_name":"A","email":"a@example.com","phone":"0901234567"}`, "put-profile", http.StatusOK},
		{http.MethodPost, "/api/v1/customer/addresses", strings.Replace(addressBody, `"expected_version":1,`, "", 1), "create-address", http.StatusCreated},
		{http.MethodGet, "/api/v1/customer/addresses", "", "list-addresses", http.StatusOK},
		{http.MethodPut, "/api/v1/customer/addresses/" + addressID.String(), addressBody, "replace-address", http.StatusOK},
		{http.MethodDelete, "/api/v1/customer/addresses/" + addressID.String(), `{"expected_version":1}`, "archive-address", http.StatusNoContent},
	} {
		response := customerRequest(engine, test.method, test.path, test.body)
		if response.Code != test.status || service.call != test.call {
			t.Fatalf("%s %s = %d %s, call = %s", test.method, test.path, response.Code, response.Body.String(), service.call)
		}
	}
	if storeID == "" || userID == "" {
		t.Fatal("invalid test principal")
	}
}

func TestCustomerRoutesMapStrictAndDependencyErrors(t *testing.T) {
	for _, test := range []struct {
		name, method, path, body, code string
		err                            error
		status                         int
	}{
		{"unknown field", http.MethodPut, "/api/v1/customer/profile", `{"expected_version":0,"full_name":"A","email":"a@example.com","phone":"0901234567","extra":true}`, "invalid_request", nil, http.StatusBadRequest},
		{"oversized body", http.MethodPut, "/api/v1/customer/profile", `{"full_name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, "invalid_request", nil, http.StatusBadRequest},
		{"invalid input", http.MethodPut, "/api/v1/customer/profile", `{"expected_version":0,"full_name":"A","email":"@xx","phone":"+012345678"}`, "invalid_customer", domain.ErrInvalidCustomer, http.StatusBadRequest},
		{"conflict", http.MethodPut, "/api/v1/customer/profile", `{"expected_version":1,"full_name":"A","email":"a@example.com","phone":"0901234567"}`, "customer_conflict", application.ErrConflict, http.StatusConflict},
		{"not found", http.MethodGet, "/api/v1/customer/profile", "", "customer_unavailable", application.ErrNotFound, http.StatusNotFound},
		{"dependency", http.MethodGet, "/api/v1/customer/profile", "", "dependency_unavailable", context.DeadlineExceeded, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &customerServiceStub{err: test.err}
			engine, _, _ := customerTestEngine(t, service, authn.KindUser)
			response := customerRequest(engine, test.method, test.path, test.body)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || !strings.Contains(response.Body.String(), `"request_id":"customer-request"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func customerTestEngine(t *testing.T, service Service, kind authn.Kind) (*gin.Engine, ids.StoreID, ids.UserID) {
	t.Helper()
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	logger := slog.New(slog.DiscardHandler)
	hosts := tenant.NewHostMiddleware(customerHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive}}, "example.test", "auth.example.test", logger, time.Second)
	authenticate := func(ctx *gin.Context) {
		if kind == authn.KindUser {
			ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewUserPrincipal(storeID.String(), userID.String(), "session")))
		}
		ctx.Next()
	}
	engine := httpserver.NewEngine(logger)
	NewHandler(service, authenticate, logger, time.Second).Register(engine, hosts)
	return engine, storeID, userID
}

func customerRequest(engine http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.Host = "shop.example.test"
	request.Header.Set("X-Request-ID", "customer-request")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
