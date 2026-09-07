//go:build integration

package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMiddlewareDeniesCrossStoreThroughPostgres(t *testing.T) {
	pool := tenantIntegrationPool(t)
	pair := storefixture.NewPair(t, pool)
	resolver := application.NewResolveTenant(storepostgres.NewRepository(pool))
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	called := 0
	middleware := NewMiddleware(resolver, logger, 500*time.Millisecond)
	handler := httpserver.Middleware(logger, middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})))

	success := httptest.NewRecorder()
	handler.ServeHTTP(success, authenticatedRequest(pair.A.UserID.String(), pair.A.Store.ID().String()))
	if success.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("same-store response = %d, handler calls = %d", success.Code, called)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE store_memberships SET status = 'inactive', updated_at = now() WHERE store_id = $1::uuid AND user_id = $2::uuid`, pair.B.Store.ID().String(), pair.B.UserID.String()); err != nil {
		t.Fatalf("deactivate fixture membership: %v", err)
	}
	missingStoreID, _ := domain.NewStoreID()
	tests := []struct {
		name    string
		userID  domain.UserID
		storeID domain.StoreID
	}{
		{name: "A user requests B", userID: pair.A.UserID, storeID: pair.B.Store.ID()},
		{name: "B user requests A", userID: pair.B.UserID, storeID: pair.A.Store.ID()},
		{name: "missing store", userID: pair.A.UserID, storeID: missingStoreID},
		{name: "inactive membership", userID: pair.B.UserID, storeID: pair.B.Store.ID()},
	}
	var denialBody string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedRequest(test.userID.String(), test.storeID.String()))
			var body problem.Body
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode denial response: %v", err)
			}
			if response.Code != http.StatusForbidden || body.Code != "tenant_access_denied" || body.Message != "Access to this store is denied." || body.RequestID != "request-123" || len(body.Details) != 0 {
				t.Fatalf("denial response = %d %#v", response.Code, body)
			}
			if denialBody == "" {
				denialBody = response.Body.String()
			} else if response.Body.String() != denialBody {
				t.Fatalf("denial body differs: %q != %q", response.Body.String(), denialBody)
			}
		})
	}
	if called != 1 {
		t.Fatalf("protected handler calls = %d, want only same-store success", called)
	}
}

func TestMiddlewareTimesOutBlockedPostgresResolution(t *testing.T) {
	pool := tenantIntegrationPool(t)
	pair := storefixture.NewPair(t, pool)
	lock, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	if _, err := lock.Exec(context.Background(), `LOCK TABLE store_memberships IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock memberships: %v", err)
	}
	resolver := application.NewResolveTenant(storepostgres.NewRepository(pool))
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	middleware := NewMiddleware(resolver, logger, 25*time.Millisecond)
	handler := httpserver.Middleware(logger, middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler called")
	})))
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, authenticatedRequest(pair.A.UserID.String(), pair.A.Store.ID().String()))
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > time.Second {
		t.Fatalf("blocked response = %d after %s", response.Code, time.Since(started))
	}
	if bytes.Contains(response.Body.Bytes(), []byte("store_memberships")) || bytes.Contains(logs.Bytes(), []byte("store_memberships")) {
		t.Fatalf("database details exposed: body=%q logs=%q", response.Body.String(), logs.String())
	}
}

func tenantIntegrationPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		tb.Fatalf("parse test database config: %v", err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatalf("test database guard: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatalf("create PostgreSQL pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping PostgreSQL: %v", err)
	}
	return pool
}
