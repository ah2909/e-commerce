package clientaddr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestResolverIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := New([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}).resolve(request); got.String() != "198.51.100.10" {
		t.Fatalf("resolve() = %s", got)
	}
}

func TestResolverUsesFirstUntrustedAddressFromTrustedChain(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.4:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.3")
	resolver := New([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got := resolver.resolve(request); got.String() != "203.0.113.7" {
		t.Fatalf("resolve() = %s", got)
	}
}

func TestResolverRejectsMalformedOrUnboundedChains(t *testing.T) {
	resolver := New([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	for _, value := range []string{"invalid", strings.Repeat("1.1.1.1,", maxForwardedHops) + "1.1.1.1", strings.Repeat("1", maxForwardedBytes+1)} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		request.RemoteAddr = "10.0.0.4:1234"
		request.Header.Set("X-Forwarded-For", value)
		if got := resolver.resolve(request); got.String() != "10.0.0.4" {
			t.Fatalf("resolve(%q) = %s", value, got)
		}
	}
}

func TestMiddlewareStoresResolvedAddress(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.RemoteAddr = "[2001:db8::4]:1234"
	response := httptest.NewRecorder()
	New(nil).Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := FromRequest(r); got != "2001:db8::4" {
			t.Fatalf("FromRequest() = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}
