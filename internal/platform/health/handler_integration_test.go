//go:build integration

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestReadinessWithRealDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, requiredEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: requiredEnv(t, "REDIS_ADDRESS")})
	t.Cleanup(func() { _ = redisClient.Close() })

	handler := New([]Dependency{
		{Name: "postgresql", Check: pool.Ping},
		{Name: "redis", Check: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	}, 5*time.Second)
	mux := http.NewServeMux()
	handler.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("readiness = %d %s", response.Code, response.Body.String())
	}
}

func TestReadinessWithClosedRedis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, requiredEnv(t, "DATABASE_URL"))
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()
	redisClient := redis.NewClient(&redis.Options{Addr: requiredEnv(t, "REDIS_ADDRESS")})
	if err := redisClient.Close(); err != nil {
		t.Fatalf("redis.Close() error = %v", err)
	}

	handler := New([]Dependency{
		{Name: "postgresql", Check: pool.Ping},
		{Name: "redis", Check: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	}, 5*time.Second)
	mux := http.NewServeMux()
	handler.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "client is closed") {
		t.Fatalf("readiness = %d %s", response.Code, response.Body.String())
	}
}

func requiredEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("%s is required for integration tests", key)
	}
	return value
}
