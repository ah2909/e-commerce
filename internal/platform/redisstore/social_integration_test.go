//go:build integration

package redisstore

import (
	"context"
	"crypto/sha256"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/redis/go-redis/v9"
)

func TestSocialStateIsConsumedOnce(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS")})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSocial(client, time.Second)
	digest := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	browser := sha256.Sum256([]byte("browser"))
	want := application.SocialTransaction{Provider: identitydomain.ProviderGoogle, BrowserDigest: browser, Purpose: "login", ReturnPath: "/auth/complete"}
	if err := store.PutTransaction(context.Background(), digest, want, time.Minute); err != nil {
		t.Fatal(err)
	}
	wrongBrowser := sha256.Sum256([]byte("wrong-browser"))
	if _, found, err := store.TakeTransaction(context.Background(), digest, identitydomain.ProviderFacebook, browser); err != nil || found {
		t.Fatalf("wrong-provider TakeTransaction() = found %v, error %v", found, err)
	}
	if _, found, err := store.TakeTransaction(context.Background(), digest, identitydomain.ProviderGoogle, wrongBrowser); err != nil || found {
		t.Fatalf("wrong-browser TakeTransaction() = found %v, error %v", found, err)
	}
	got, found, err := store.TakeTransaction(context.Background(), digest, identitydomain.ProviderGoogle, browser)
	if err != nil || !found || got.Provider != want.Provider || got.ReturnPath != want.ReturnPath {
		t.Fatalf("TakeTransaction() = %#v, %v, %v", got, found, err)
	}
	if _, found, err := store.TakeTransaction(context.Background(), digest, identitydomain.ProviderGoogle, browser); err != nil || found {
		t.Fatalf("second TakeTransaction() = found %v, error %v", found, err)
	}
}

func TestSocialStateConcurrentTakeHasOneWinner(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS")})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSocial(client, time.Second)
	digest := sha256.Sum256([]byte("concurrent-" + time.Now().UTC().Format(time.RFC3339Nano)))
	browser := sha256.Sum256([]byte("browser"))
	transaction := application.SocialTransaction{Provider: identitydomain.ProviderGoogle, BrowserDigest: browser, Purpose: "login", ReturnPath: "/auth/complete"}
	if err := store.PutTransaction(context.Background(), digest, transaction, time.Minute); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	found := make([]bool, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range found {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, found[index], errs[index] = store.TakeTransaction(context.Background(), digest, identitydomain.ProviderGoogle, browser)
		}(index)
	}
	close(start)
	wait.Wait()
	winners := 0
	for index := range found {
		if errs[index] != nil {
			t.Fatal(errs[index])
		}
		if found[index] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners = %d", winners)
	}
}

func TestSocialExchangeWrongStoreDoesNotConsumeCode(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDRESS")})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSocial(client, time.Second)
	digest := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	otherStoreID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	want := application.SocialExchange{StoreID: storeID}
	if err := store.PutExchange(context.Background(), digest, want, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.TakeExchange(context.Background(), digest, otherStoreID); err != nil || found {
		t.Fatalf("wrong-store TakeExchange() = found %v, error %v", found, err)
	}
	if _, found, err := store.TakeExchange(context.Background(), digest, storeID); err != nil || !found {
		t.Fatalf("valid TakeExchange() = found %v, error %v", found, err)
	}
}
