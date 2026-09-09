package tenant

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type hostResolver struct {
	subdomain string
	err       error
}

func (resolver *hostResolver) Execute(_ context.Context, subdomain string) (application.StoreRecord, error) {
	resolver.subdomain = subdomain
	if resolver.err != nil {
		return application.StoreRecord{}, resolver.err
	}
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	parsed, _ := domain.ParseSubdomain(subdomain)
	return application.StoreRecord{StoreID: storeID, Subdomain: parsed, Status: domain.StoreStatusActive}, nil
}

func TestHostMiddlewareUsesOnlyCanonicalRequestHost(t *testing.T) {
	resolver := &hostResolver{}
	middleware := NewHostMiddleware(resolver, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	called := false
	handler := middleware.Tenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, called = StoreFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/store", nil)
	request.Host = "Shop.Example.Test:443"
	request.Header.Set("X-Forwarded-Host", "evil.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !called || resolver.subdomain != "shop" {
		t.Fatalf("response = %d, called = %v, subdomain = %q", response.Code, called, resolver.subdomain)
	}
}

func TestHostMiddlewareRejectsApexReservedMultiLabelAndUnknownHosts(t *testing.T) {
	for _, host := range []string{"example.test", "www.example.test", "a.b.example.test", "shop.evil.test", "shop.example.test.invalid", "unknown.example.test"} {
		t.Run(host, func(t *testing.T) {
			resolver := &hostResolver{}
			if host == "unknown.example.test" {
				resolver.err = application.ErrStoreUnavailable
			}
			middleware := NewHostMiddleware(resolver, "example.test", "auth.example.test", slog.Default(), time.Second)
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			request.Host = host
			middleware.Tenant(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next called") })).ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestHostMiddlewareSeparatesBaseAndAuthenticationHosts(t *testing.T) {
	middleware := NewHostMiddleware(&hostResolver{}, "example.test", "auth.example.test", slog.Default(), time.Second)
	for _, test := range []struct {
		name string
		wrap func(http.Handler) http.Handler
		host string
		want int
	}{{"base", middleware.Base, "example.test", http.StatusNoContent}, {"base rejects auth", middleware.Base, "auth.example.test", http.StatusNotFound}, {"auth", middleware.Auth, "auth.example.test", http.StatusNoContent}, {"auth rejects tenant", middleware.Auth, "shop.example.test", http.StatusNotFound}} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			request.Host = test.host
			test.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
