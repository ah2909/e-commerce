package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

const (
	testStoreID = "018f0f7d-7b3d-7d2e-8f4f-3b86f31f2741"
	testUserID  = "028f0f7d-7b3d-7d2e-8f4f-3b86f31f2742"
)

type resolverStub struct {
	resolved application.TenantContext
	err      error
	wait     bool
	calls    int
	storeID  domain.StoreID
	userID   domain.UserID
}

func (resolver *resolverStub) Execute(ctx context.Context, storeID domain.StoreID, userID domain.UserID) (application.TenantContext, error) {
	resolver.calls++
	resolver.storeID = storeID
	resolver.userID = userID
	if resolver.wait {
		<-ctx.Done()
		return application.TenantContext{}, ctx.Err()
	}
	return resolver.resolved, resolver.err
}

func TestMiddlewareInjectsResolvedTenant(t *testing.T) {
	resolved := resolvedContext(t)
	resolver := &resolverStub{resolved: resolved}
	var logs bytes.Buffer
	handler := middlewareHandler(resolver, &logs, time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := FromContext(r.Context())
		if !ok || got.StoreID() != resolved.StoreID() || got.UserID() != resolved.UserID() {
			t.Fatalf("tenant context = %#v, %v", got, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := authenticatedRequest(testUserID, testStoreID)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	storeID, _ := domain.ParseStoreID(testStoreID)
	userID, _ := domain.ParseUserID(testUserID)
	if response.Code != http.StatusNoContent || resolver.calls != 1 || resolver.storeID != storeID || resolver.userID != userID {
		t.Fatalf("response = %d, calls = %d, scope = %s/%s", response.Code, resolver.calls, resolver.storeID, resolver.userID)
	}
}

func TestMiddlewareFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		request     *http.Request
		resolverErr error
		wantStatus  int
		wantCode    string
	}{
		{name: "missing principal", request: httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil), wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated"},
		{name: "malformed principal", request: authenticatedRequest("not-a-uuid", testStoreID), wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated"},
		{name: "denied", request: authenticatedRequest(testUserID, testStoreID), resolverErr: application.ErrTenantAccessDenied, wantStatus: http.StatusForbidden, wantCode: "tenant_access_denied"},
		{name: "internal", request: authenticatedRequest(testUserID, testStoreID), resolverErr: errors.New("password=secret database failure"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &resolverStub{err: test.resolverErr}
			var logs bytes.Buffer
			handler := middlewareHandler(resolver, &logs, time.Second, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler called")
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, test.request)
			var body problem.Body
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Code != test.wantStatus || body.Code != test.wantCode || body.RequestID == "" || body.Details == nil {
				t.Fatalf("response = %d %#v", response.Code, body)
			}
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), testUserID) {
				t.Fatalf("sensitive detail exposed: body=%q logs=%q", response.Body.String(), logs.String())
			}
		})
	}
}

func TestMiddlewareBoundsResolution(t *testing.T) {
	resolver := &resolverStub{wait: true}
	var logs bytes.Buffer
	handler := middlewareHandler(resolver, &logs, 5*time.Millisecond, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler called")
	}))
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, authenticatedRequest(testUserID, testStoreID))
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("response = %d after %s", response.Code, time.Since(started))
	}
}

func BenchmarkMiddlewareResolved(b *testing.B) {
	resolver := &resolverStub{resolved: resolvedContext(b)}
	handler := middlewareHandler(resolver, &bytes.Buffer{}, time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := authenticatedRequest(testUserID, testStoreID)
	b.ReportAllocs()
	for range b.N {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
}

func middlewareHandler(resolver Resolver, logs *bytes.Buffer, timeout time.Duration, next http.Handler) http.Handler {
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	middleware := NewMiddleware(resolver, logger, timeout)
	return httpserver.Middleware(logger, middleware.Wrap(next))
}

func authenticatedRequest(userID, storeID string) *http.Request {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/probe", nil)
	request.Header.Set("X-Request-ID", "request-123")
	return request.WithContext(authn.WithPrincipal(request.Context(), authn.NewPrincipal(userID, storeID)))
}

func resolvedContext(tb testing.TB) application.TenantContext {
	tb.Helper()
	storeID, _ := domain.ParseStoreID(testStoreID)
	userID, _ := domain.ParseUserID(testUserID)
	now := time.Now()
	store, _ := domain.NewStore(storeID, "Cửa hàng", "VND", "vi-VN", "Asia/Ho_Chi_Minh", now)
	membership, _ := domain.NewMembership(storeID, userID, now)
	repository := &resolvedRepository{store: store, membership: membership}
	resolved, err := application.NewResolveTenant(repository).Execute(context.Background(), storeID, userID)
	if err != nil {
		tb.Fatal(err)
	}
	return resolved
}

type resolvedRepository struct {
	store      domain.Store
	membership domain.Membership
}

func (*resolvedRepository) CreateWithMembership(context.Context, domain.Store, domain.Membership) error {
	return nil
}

func (repository *resolvedRepository) ResolveTenant(context.Context, domain.StoreID, domain.UserID) (application.TenantRecord, bool, error) {
	return application.TenantRecord{
		StoreID:          repository.store.ID(),
		UserID:           repository.membership.UserID(),
		StoreName:        repository.store.Name(),
		StoreStatus:      repository.store.Status(),
		MembershipStatus: repository.membership.Status(),
		DefaultCurrency:  repository.store.DefaultCurrency(),
		Locale:           repository.store.Locale(),
		Timezone:         repository.store.Timezone(),
		SettingsVersion:  repository.store.SettingsVersion(),
	}, true, nil
}
