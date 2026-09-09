//go:build integration

package identityhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	identityhttp "github.com/ah2909/e-commerce/internal/identity/transport/http"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	onboardingpostgres "github.com/ah2909/e-commerce/internal/onboarding/infrastructure/postgres"
	onboardinghttp "github.com/ah2909/e-commerce/internal/onboarding/transport/http"
	"github.com/ah2909/e-commerce/internal/platform/adminauth"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/ratelimit"
	"github.com/ah2909/e-commerce/internal/platform/redisstore"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	storeshttp "github.com/ah2909/e-commerce/internal/stores/transport/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestTenantHostIdentityStoreAndGuestHTTPFlow(t *testing.T) {
	pool := integrationPool(t)
	redisClient := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS")})
	t.Cleanup(func() { _ = redisClient.Close() })
	logger := slog.New(slog.DiscardHandler)
	stores := storespostgres.NewRepository(pool, auditpostgres.NewWriter())
	identities := identitypostgres.NewRepository(pool)
	hasher, _ := password.NewArgon2ID(1)
	identityService := identityapplication.NewService(identities, hasher, stores, token.Generator{}, identityapplication.DefaultSessionSettings(), time.Now)
	social := identityapplication.NewSocialService(identities, identityService, redisstore.NewSocial(redisClient, time.Second), map[identitydomain.Provider]identityapplication.SocialProvider{}, []string{"/auth/complete"}, identityapplication.DefaultSocialSettings(), time.Now)
	settings := identityhttp.DefaultSettings()
	settings.TenantBaseDomain = "example.test"
	settings.AuthHost = "auth.example.test"
	identityHandler := identityhttp.NewHandler(identityService, social, ratelimit.NewRedis(redisClient, time.Second), logger, settings)
	hosts := tenant.NewHostMiddleware(storesapplication.NewResolveStore(stores), "example.test", "auth.example.test", logger, time.Second)
	mux := http.NewServeMux()
	identityHandler.Register(mux, hosts)
	onboarding := onboardingapplication.NewService(onboardingpostgres.NewRepository(pool, stores, identities), identityService, ids.NewStoreID, time.Now)
	var platformToken [32]byte
	copy(platformToken[:], []byte("dev-platform-admin-token-0000000"))
	onboardinghttp.NewHandler(onboarding, ratelimit.NewRedis(redisClient, time.Second), logger, time.Second, 20, time.Minute).Register(mux, hosts, adminauth.New(platformToken).Authenticate)
	memberships := storesapplication.NewMembershipManager(stores, identities, time.Now)
	tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(stores), logger, time.Second)
	storeshttp.NewHandler(memberships, identityHandler.AuthenticateUser, tenantMiddleware, logger).Register(mux, hosts)
	server := httpserver.Middleware(logger, mux)

	suffix := time.Now().UnixNano()
	email := fmt.Sprintf("http-%d@example.com", suffix)
	storeA := onboard(t, server, fmt.Sprintf("shop-a-%d", suffix), email)
	storeB := onboard(t, server, fmt.Sprintf("shop-b-%d", suffix), email)
	t.Cleanup(func() {
		for _, storeID := range []string{storeA.StoreID, storeB.StoreID} {
			_, _ = pool.Exec(context.Background(), `DELETE FROM identity_guests WHERE store_id=$1::uuid`, storeID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM audit_records WHERE store_id=$1::uuid`, storeID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM store_memberships WHERE store_id=$1::uuid`, storeID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id=$1::uuid`, storeID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM stores WHERE store_id=$1::uuid`, storeID)
		}
	})

	viewA := request(t, server, http.MethodGet, "/api/v1/store", nil, storeA.AccessToken, storeA.Subdomain+".example.test")
	crossUser := request(t, server, http.MethodGet, "/api/v1/store", nil, storeA.AccessToken, storeB.Subdomain+".example.test")
	if viewA.Code != http.StatusOK || crossUser.Code != http.StatusUnauthorized {
		t.Fatalf("user host scope statuses = same %d, cross %d", viewA.Code, crossUser.Code)
	}

	guest := request(t, server, http.MethodPost, "/api/v1/guest-sessions", nil, "", storeA.Subdomain+".example.test")
	if guest.Code != http.StatusCreated {
		t.Fatalf("guest creation = %d %s", guest.Code, guest.Body.String())
	}
	var guestSession struct {
		AccessToken string `json:"access_token"`
	}
	decode(t, guest, &guestSession)
	sameGuest := request(t, server, http.MethodGet, "/api/v1/guest-sessions/current", nil, guestSession.AccessToken, storeA.Subdomain+".example.test")
	crossGuest := request(t, server, http.MethodGet, "/api/v1/guest-sessions/current", nil, guestSession.AccessToken, storeB.Subdomain+".example.test")
	if sameGuest.Code != http.StatusOK || crossGuest.Code != http.StatusUnauthorized {
		t.Fatalf("guest host scope statuses = same %d, cross %d", sameGuest.Code, crossGuest.Code)
	}
}

type onboardingResponse struct {
	StoreID     string `json:"store_id"`
	Subdomain   string `json:"subdomain"`
	OwnerUserID string `json:"owner_user_id"`
	AccessToken string `json:"access_token"`
}

func onboard(t *testing.T, handler http.Handler, subdomain, email string) onboardingResponse {
	t.Helper()
	response := request(t, handler, http.MethodPost, "/api/v1/onboarding/stores", map[string]string{"subdomain": subdomain, "store_name": "Customer Store", "email": email, "password": "correct-horse-battery-staple"}, "ZGV2LXBsYXRmb3JtLWFkbWluLXRva2VuLTAwMDAwMDA", "example.test")
	if response.Code != http.StatusCreated {
		t.Fatalf("onboarding %s = %d %s", subdomain, response.Code, response.Body.String())
	}
	var result onboardingResponse
	decode(t, response, &result)
	login := request(t, handler, http.MethodPost, "/api/v1/auth/sessions", map[string]string{"email": email, "password": "correct-horse-battery-staple"}, "", result.Subdomain+".example.test")
	if login.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", subdomain, login.Code, login.Body.String())
	}
	decode(t, login, &result)
	result.StoreID = responseField(t, response.Body.Bytes(), "store_id")
	result.Subdomain = subdomain
	return result
}

func responseField(t *testing.T, encoded []byte, field string) string {
	t.Helper()
	var value map[string]string
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return value[field]
}

func request(t *testing.T, handler http.Handler, method, path string, body any, accessToken, host string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded io.Reader
	if body != nil {
		contents, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		encoded = bytes.NewReader(contents)
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, encoded)
	request.Host = host
	request.RemoteAddr = "127.0.0.1:12345"
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body.String())
	}
}

func integrationPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		tb.Fatalf("parse test database URL: %v", err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatalf("test database guard: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatalf("create PostgreSQL pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}
