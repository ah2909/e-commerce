//go:build integration

package ratelimit

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisLimiterAppliesAtomicWindow(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS")})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRedis(client, time.Second)
	subject := "integration-" + time.Now().UTC().Format(time.RFC3339Nano)
	for attempt := 1; attempt <= 3; attempt++ {
		allowed, retry, err := limiter.Allow(context.Background(), "login", subject, 2, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if allowed != (attempt <= 2) || retry <= 0 {
			t.Fatalf("attempt %d = allowed %v, retry %s", attempt, allowed, retry)
		}
	}
}

func TestRedisLimiterFailsClosedWhenUnavailable(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	_, _, err := NewRedis(client, 20*time.Millisecond).Allow(context.Background(), "login", "subject", 1, time.Minute)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Allow() error = %v", err)
	}
}
