package redisstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/redis/go-redis/v9"
)

type Social struct {
	client  *redis.Client
	timeout time.Duration
}

func NewSocial(client *redis.Client, timeout time.Duration) Social {
	return Social{client: client, timeout: timeout}
}

func (store Social) PutTransaction(ctx context.Context, digest [32]byte, transaction application.SocialTransaction, ttl time.Duration) error {
	return store.putBound(ctx, "social:transaction:"+hex.EncodeToString(digest[:]), transaction, string(transaction.Provider), hex.EncodeToString(transaction.BrowserDigest[:]), ttl)
}

func (store Social) TakeTransaction(ctx context.Context, digest [32]byte, provider identitydomain.Provider, browserDigest [32]byte) (application.SocialTransaction, bool, error) {
	var transaction application.SocialTransaction
	found, err := store.takeBound(ctx, "social:transaction:"+hex.EncodeToString(digest[:]), string(provider), hex.EncodeToString(browserDigest[:]), &transaction)
	return transaction, found, err
}

func (store Social) PutContinuation(ctx context.Context, digest [32]byte, continuation application.SocialContinuation, ttl time.Duration) error {
	return store.put(ctx, "social:continuation:"+hex.EncodeToString(digest[:]), continuation, ttl)
}

func (store Social) TakeContinuation(ctx context.Context, digest [32]byte) (application.SocialContinuation, bool, error) {
	var continuation application.SocialContinuation
	found, err := store.take(ctx, "social:continuation:"+hex.EncodeToString(digest[:]), &continuation)
	return continuation, found, err
}

func (store Social) PutExchange(ctx context.Context, digest [32]byte, exchange application.SocialExchange, ttl time.Duration) error {
	return store.putBound(ctx, "social:exchange:"+hex.EncodeToString(digest[:]), exchange, exchange.StoreID.String(), "", ttl)
}

func (store Social) TakeExchange(ctx context.Context, digest [32]byte, storeID ids.StoreID) (application.SocialExchange, bool, error) {
	var exchange application.SocialExchange
	found, err := store.takeBound(ctx, "social:exchange:"+hex.EncodeToString(digest[:]), storeID.String(), "", &exchange)
	return exchange, found, err
}

func (store Social) put(ctx context.Context, key string, value any, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	ok, err := store.client.SetNX(bounded, key, encoded, ttl).Result()
	if err != nil {
		return fmt.Errorf("store Redis value: %w", err)
	}
	if !ok {
		return errors.New("redis key collision")
	}
	return nil
}

func (store Social) take(ctx context.Context, key string, target any) (bool, error) {
	bounded, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	encoded, err := store.client.GetDel(bounded, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("take Redis value: %w", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return false, fmt.Errorf("decode Redis value: %w", err)
	}
	return true, nil
}

const putBoundScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], 'payload', ARGV[1], 'binding1', ARGV[2], 'binding2', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1`

const takeBoundScript = `
if redis.call('HGET', KEYS[1], 'binding1') ~= ARGV[1] then return nil end
if redis.call('HGET', KEYS[1], 'binding2') ~= ARGV[2] then return nil end
local payload = redis.call('HGET', KEYS[1], 'payload')
if payload then redis.call('DEL', KEYS[1]) end
return payload`

func (store Social) putBound(ctx context.Context, key string, value any, binding1, binding2 string, ttl time.Duration) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	inserted, err := store.client.Eval(bounded, putBoundScript, []string{key}, encoded, binding1, binding2, ttl.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("store bound Redis value: %w", err)
	}
	if inserted != 1 {
		return errors.New("redis key collision")
	}
	return nil
}

func (store Social) takeBound(ctx context.Context, key, binding1, binding2 string, target any) (bool, error) {
	bounded, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	encoded, err := store.client.Eval(bounded, takeBoundScript, []string{key}, binding1, binding2).Text()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("take bound Redis value: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), target); err != nil {
		return false, fmt.Errorf("decode bound Redis value: %w", err)
	}
	return true, nil
}
