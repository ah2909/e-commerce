package identityhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
)

type hostResolverStub struct{}

func (hostResolverStub) Execute(context.Context, string) (storesapplication.StoreRecord, error) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	subdomain, _ := storesdomain.ParseSubdomain("shop")
	return storesapplication.StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: storesdomain.StoreStatusActive}, nil
}

func testHosts(logger *slog.Logger) tenant.HostMiddleware {
	return tenant.NewHostMiddleware(hostResolverStub{}, "example.test", "auth.example.test", logger, time.Second)
}

type limiterStub struct{ err error }

func (stub limiterStub) Allow(context.Context, string, string, int64, time.Duration) (bool, time.Duration, error) {
	return false, 0, stub.err
}

type limitCall struct {
	policy  string
	subject string
	maximum int64
	window  time.Duration
}

type recordingLimiter struct{ calls []limitCall }

func (limiter *recordingLimiter) Allow(_ context.Context, policy, subject string, maximum int64, window time.Duration) (bool, time.Duration, error) {
	limiter.calls = append(limiter.calls, limitCall{policy: policy, subject: subject, maximum: maximum, window: window})
	return len(limiter.calls) == 1, time.Minute, nil
}

type peerAllowingLimiter struct{ calls []limitCall }

func (limiter *peerAllowingLimiter) Allow(_ context.Context, policy, subject string, maximum int64, window time.Duration) (bool, time.Duration, error) {
	limiter.calls = append(limiter.calls, limitCall{policy: policy, subject: subject, maximum: maximum, window: window})
	return strings.HasSuffix(policy, "-peer"), time.Minute, nil
}

func TestBearerParsingIsStrict(t *testing.T) {
	for _, header := range []string{"", "Basic abc", "bearer abc", "Bearer", "Bearer abc extra"} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		if _, ok := bearer(request); ok {
			t.Errorf("bearer accepted %q", header)
		}
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer opaque")
	if value, ok := bearer(request); !ok || value != "opaque" {
		t.Fatalf("bearer() = %q, %v", value, ok)
	}
}

func TestCredentialEndpointFailsClosedWhenLimiterIsUnavailable(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := NewHandler(application.Service{}, application.SocialService{}, limiterStub{err: errors.New("redis unavailable")}, logger, DefaultSettings())
	mux := http.NewServeMux()
	handler.Register(mux, testHosts(logger))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/registrations", strings.NewReader(`{"email":"private@example.com","password":"valid-password-value"}`))
	request.Host = "shop.example.test"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"rate_limit_unavailable"`) || strings.Contains(response.Body.String(), "private@example.com") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), "private@example.com") || strings.Contains(logs.String(), "valid-password-value") {
		t.Fatalf("sensitive data logged: %s", logs.String())
	}
}

func TestCredentialLimitsUseIndependentPeerAndIdentityPolicies(t *testing.T) {
	limiter := &recordingLimiter{}
	settings := DefaultSettings()
	settings.Login = CredentialRatePolicy{PeerMaximum: 11, IdentityMaximum: 3, Window: 7 * time.Minute}
	handler := NewHandler(application.Service{}, application.SocialService{}, limiter, slog.Default(), settings)
	mux := http.NewServeMux()
	handler.Register(mux, testHosts(slog.Default()))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/sessions", strings.NewReader(`{"email":"Person@Example.com","password":"valid-password-value"}`))
	request.Host = "shop.example.test"
	request.RemoteAddr = "192.0.2.10:4567"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || len(limiter.calls) != 2 {
		t.Fatalf("response = %d, calls = %#v", response.Code, limiter.calls)
	}
	if limiter.calls[0] != (limitCall{policy: "login-peer", subject: "192.0.2.10", maximum: 11, window: 7 * time.Minute}) {
		t.Fatalf("peer limit = %#v", limiter.calls[0])
	}
	if limiter.calls[1] != (limitCall{policy: "login-identity", subject: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb|person@example.com", maximum: 3, window: 7 * time.Minute}) {
		t.Fatalf("identity limit = %#v", limiter.calls[1])
	}
}

func TestTokenEndpointsAlwaysApplyPeerBeforeDigestLimit(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		body   func(string) string
		policy string
	}{
		{name: "refresh", path: "/api/v1/auth/sessions/refresh", body: func(token string) string { return `{"refresh_token":"` + token + `"}` }, policy: "refresh"},
		{name: "social exchange", path: "/api/v1/auth/social/exchanges", body: func(token string) string { return `{"exchange_code":"` + token + `"}` }, policy: "social-exchange"},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := &peerAllowingLimiter{}
			handler := NewHandler(application.Service{}, application.SocialService{}, limiter, slog.Default(), DefaultSettings())
			mux := http.NewServeMux()
			handler.Register(mux, testHosts(slog.Default()))
			for _, token := range []string{"first-secret", "second-secret"} {
				request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, test.path, strings.NewReader(test.body(token)))
				request.Host = "shop.example.test"
				request.RemoteAddr = "192.0.2.10:4567"
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if response.Code != http.StatusTooManyRequests {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
			}
			if len(limiter.calls) != 4 || limiter.calls[0].policy != test.policy+"-peer" || limiter.calls[2].policy != test.policy+"-peer" || limiter.calls[0].subject != "192.0.2.10" || limiter.calls[2].subject != "192.0.2.10" || limiter.calls[1].subject == limiter.calls[3].subject {
				t.Fatalf("rate calls = %#v", limiter.calls)
			}
			for _, call := range limiter.calls {
				if strings.Contains(call.subject, "first-secret") || strings.Contains(call.subject, "second-secret") {
					t.Fatalf("raw secret used in rate key: %#v", call)
				}
			}
		})
	}
}

func TestDependencyDeadlineMapsToRetryableServiceUnavailable(t *testing.T) {
	handler := Handler{logger: slog.Default()}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/sessions", nil)
	response := httptest.NewRecorder()
	handler.internalError(response, request, "authentication failed", fmt.Errorf("query identity: %w", context.DeadlineExceeded))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"dependency_unavailable"`) || !strings.Contains(response.Body.String(), `"retryable":true`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestSocialRedirectTargetUsesOnlyValidatedServerComponents(t *testing.T) {
	target, err := socialRedirectTarget("shop", "example.test", "/auth/social/complete?from=login", "one-time-code")
	if err != nil || target != "https://shop.example.test/auth/social/complete?exchange_code=one-time-code&from=login" {
		t.Fatalf("socialRedirectTarget() = %q, %v", target, err)
	}
	for _, test := range []struct {
		subdomain string
		base      string
		path      string
	}{
		{subdomain: "shop.evil", base: "example.test", path: "/auth/social/complete"},
		{subdomain: "shop", base: "example.test:8443", path: "/auth/social/complete"},
		{subdomain: "shop", base: "example.test", path: "//evil.example/steal"},
		{subdomain: "shop", base: "example.test", path: "https://evil.example/steal"},
		{subdomain: "shop", base: "example.test", path: "/auth/social/complete#fragment"},
	} {
		if target, err := socialRedirectTarget(test.subdomain, test.base, test.path, "code"); err == nil {
			t.Errorf("unsafe target accepted: %q", target)
		}
	}
}
