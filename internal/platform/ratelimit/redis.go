package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrUnavailable = errors.New("rate limiter unavailable")

var windowScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
return {current, ttl}
`)

type Redis struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedis(client *redis.Client, timeout time.Duration) Redis {
	return Redis{client: client, timeout: timeout}
}

func (limiter Redis) Allow(ctx context.Context, policy, subject string, maximum int64, window time.Duration) (bool, time.Duration, error) {
	if maximum < 1 || window <= 0 {
		return false, 0, ErrUnavailable
	}
	digest := sha256.Sum256([]byte(subject))
	key := "rate:" + policy + ":" + hex.EncodeToString(digest[:])
	bounded, cancel := context.WithTimeout(ctx, limiter.timeout)
	defer cancel()
	result, err := windowScript.Run(bounded, limiter.client, []string{key}, window.Milliseconds()).Slice()
	if err != nil || len(result) != 2 {
		return false, 0, ErrUnavailable
	}
	count, okCount := result[0].(int64)
	ttl, okTTL := result[1].(int64)
	if !okCount || !okTTL || ttl < 0 {
		return false, 0, fmt.Errorf("%w: invalid Redis response", ErrUnavailable)
	}
	return count <= maximum, time.Duration(ttl) * time.Millisecond, nil
}
