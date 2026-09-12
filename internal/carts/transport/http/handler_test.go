package carthttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/application"
	"github.com/ah2909/e-commerce/internal/carts/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type cartHostResolver struct{ store storesapplication.StoreRecord }

func (resolver cartHostResolver) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	return resolver.store, nil
}

type cartServiceStub struct {
	owner   domain.Owner
	store   ids.StoreID
	view    application.View
	err     error
	call    string
	created bool
}

func (stub *cartServiceStub) CreateOrGet(_ context.Context, store ids.StoreID, owner domain.Owner) (application.View, bool, error) {
	stub.owner, stub.store = owner, store
	stub.call = "create"
	return stub.view, stub.created, stub.err
}
func (stub *cartServiceStub) Get(context.Context, ids.StoreID, domain.Owner) (application.View, error) {
	stub.call = "get"
	return stub.view, stub.err
}
func (stub *cartServiceStub) SetLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64, int64) (application.View, error) {
	stub.call = "set-line"
	return stub.view, stub.err
}
func (stub *cartServiceStub) RemoveLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64) (application.View, error) {
	stub.call = "remove-line"
	return stub.view, stub.err
}
func (stub *cartServiceStub) Abandon(context.Context, ids.StoreID, domain.Owner, int64) error {
	stub.call = "abandon"
	return stub.err
}

func TestCartRouteAcceptsStoreBoundGuest(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	guestID, _ := ids.NewGuestID()
	cartID, _ := ids.NewCartID()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	logger := slog.New(slog.DiscardHandler)
	hosts := tenant.NewHostMiddleware(cartHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive}}, "example.test", "auth.example.test", logger, time.Second)
	authenticate := func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), authn.NewGuestPrincipal(storeID.String(), guestID.String(), "session")))
		ctx.Next()
	}
	service := &cartServiceStub{created: true, view: application.View{CartID: cartID, Currency: "VND", Status: domain.StatusActive, Version: 1, ExpiresAt: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Lines: []application.LineView{}}}
	engine := gin.New()
	NewHandler(service, authenticate, logger, time.Second).Register(engine, hosts)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/cart", nil)
	request.Host = "shop.example.test"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.store != storeID || service.owner.Kind != domain.OwnerGuest || service.owner.ID != guestID.String() {
		t.Fatalf("response = %d %s, store = %s, owner = %#v", response.Code, response.Body.String(), service.store, service.owner)
	}
}

func TestCartUserRouteSuccesses(t *testing.T) {
	service := &cartServiceStub{created: true, view: cartView(t)}
	engine := cartTestEngine(t, service, authn.KindUser)
	variant, _ := ids.NewVariantID()
	for _, test := range []struct {
		method string
		path   string
		body   string
		call   string
		status int
	}{
		{http.MethodPost, "/api/v1/cart", "", "create", http.StatusCreated},
		{http.MethodGet, "/api/v1/cart", "", "get", http.StatusOK},
		{http.MethodPut, "/api/v1/cart/lines/" + variant.String(), `{"quantity":2,"expected_version":1}`, "set-line", http.StatusOK},
		{http.MethodDelete, "/api/v1/cart/lines/" + variant.String(), `{"expected_version":2}`, "remove-line", http.StatusOK},
		{http.MethodDelete, "/api/v1/cart", `{"expected_version":3}`, "abandon", http.StatusNoContent},
	} {
		response := cartRequest(engine, test.method, test.path, test.body)
		if response.Code != test.status || service.call != test.call {
			t.Fatalf("%s %s = %d %s, call = %s", test.method, test.path, response.Code, response.Body.String(), service.call)
		}
	}
}

func TestCartRoutesMapStrictAndDependencyErrors(t *testing.T) {
	variant, _ := ids.NewVariantID()
	linePath := "/api/v1/cart/lines/" + variant.String()
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		code   string
		err    error
		status int
	}{
		{"unknown field", http.MethodPut, linePath, `{"quantity":1,"expected_version":1,"extra":true}`, "invalid_request", nil, http.StatusBadRequest},
		{"oversized body", http.MethodPut, linePath, `{"quantity":1,"padding":"` + strings.Repeat("a", maxBodyBytes) + `"}`, "invalid_request", nil, http.StatusBadRequest},
		{"invalid input", http.MethodPut, linePath, `{"quantity":0,"expected_version":1}`, "invalid_cart", domain.ErrInvalidCart, http.StatusBadRequest},
		{"conflict", http.MethodPut, linePath, `{"quantity":1,"expected_version":1}`, "cart_conflict", application.ErrConflict, http.StatusConflict},
		{"not found", http.MethodGet, "/api/v1/cart", "", "cart_unavailable", application.ErrNotFound, http.StatusNotFound},
		{"dependency", http.MethodGet, "/api/v1/cart", "", "dependency_unavailable", context.DeadlineExceeded, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &cartServiceStub{err: test.err, view: cartView(t)}
			engine := cartTestEngine(t, service, authn.KindUser)
			response := cartRequest(engine, test.method, test.path, test.body)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || !strings.Contains(response.Body.String(), `"request_id":"cart-request"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func cartTestEngine(t *testing.T, service Service, kind authn.Kind) *gin.Engine {
	t.Helper()
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	guestID, _ := ids.NewGuestID()
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	logger := slog.New(slog.DiscardHandler)
	hosts := tenant.NewHostMiddleware(cartHostResolver{store: storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive}}, "example.test", "auth.example.test", logger, time.Second)
	authenticate := func(ctx *gin.Context) {
		principal := authn.NewUserPrincipal(storeID.String(), userID.String(), "session")
		if kind == authn.KindGuest {
			principal = authn.NewGuestPrincipal(storeID.String(), guestID.String(), "session")
		}
		ctx.Request = ctx.Request.WithContext(authn.WithPrincipal(ctx.Request.Context(), principal))
		ctx.Next()
	}
	engine := httpserver.NewEngine(logger)
	NewHandler(service, authenticate, logger, time.Second).Register(engine, hosts)
	return engine
}

func cartView(t *testing.T) application.View {
	t.Helper()
	id, _ := ids.NewCartID()
	now := time.Now().UTC()
	return application.View{CartID: id, Currency: "VND", Status: domain.StatusActive, Version: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Lines: []application.LineView{}}
}

func cartRequest(engine http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.Host = "shop.example.test"
	request.Header.Set("X-Request-ID", "cart-request")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
