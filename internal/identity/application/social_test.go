package application

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type socialStoreStub struct {
	transactions      map[[32]byte]SocialTransaction
	continuations     map[[32]byte]SocialContinuation
	exchanges         map[[32]byte]SocialExchange
	transactionTTL    time.Duration
	exchangeTTL       time.Duration
	continuationReads int
}

func newSocialStoreStub() *socialStoreStub {
	return &socialStoreStub{transactions: map[[32]byte]SocialTransaction{}, continuations: map[[32]byte]SocialContinuation{}, exchanges: map[[32]byte]SocialExchange{}}
}

func (stub *socialStoreStub) PutTransaction(_ context.Context, digest [32]byte, transaction SocialTransaction, ttl time.Duration) error {
	stub.transactions[digest] = transaction
	stub.transactionTTL = ttl
	return nil
}

func (stub *socialStoreStub) TakeTransaction(_ context.Context, digest [32]byte, provider identitydomain.Provider, browserDigest [32]byte) (SocialTransaction, bool, error) {
	value, ok := stub.transactions[digest]
	if !ok || value.Provider != provider || value.BrowserDigest != browserDigest {
		return SocialTransaction{}, false, nil
	}
	delete(stub.transactions, digest)
	return value, ok, nil
}

func (stub *socialStoreStub) PutContinuation(_ context.Context, digest [32]byte, continuation SocialContinuation, _ time.Duration) error {
	stub.continuations[digest] = continuation
	return nil
}

func (stub *socialStoreStub) TakeContinuation(_ context.Context, digest [32]byte) (SocialContinuation, bool, error) {
	stub.continuationReads++
	value, ok := stub.continuations[digest]
	delete(stub.continuations, digest)
	return value, ok, nil
}

func (stub *socialStoreStub) PutExchange(_ context.Context, digest [32]byte, exchange SocialExchange, ttl time.Duration) error {
	stub.exchanges[digest] = exchange
	stub.exchangeTTL = ttl
	return nil
}

func (stub *socialStoreStub) TakeExchange(_ context.Context, digest [32]byte, storeID ids.StoreID) (SocialExchange, bool, error) {
	value, ok := stub.exchanges[digest]
	if !ok || value.StoreID != storeID {
		return SocialExchange{}, false, nil
	}
	delete(stub.exchanges, digest)
	return value, ok, nil
}

type socialProviderStub struct {
	calls int
	err   error
}

func (stub *socialProviderStub) AuthorizationURL(state, nonce, challenge string) string {
	return "https://provider.example/auth?state=" + state + "&nonce=" + nonce + "&challenge=" + challenge
}

func (stub *socialProviderStub) Exchange(context.Context, string, string, string) (SocialProfile, error) {
	stub.calls++
	return SocialProfile{Subject: "provider-user"}, stub.err
}

func continueStart(t *testing.T, service SocialService, started SocialStart) (string, string) {
	t.Helper()
	continuation, err := service.Continue(context.Background(), started.ContinuationCode)
	if err != nil {
		t.Fatal(err)
	}
	providerURL, err := url.Parse(continuation.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	return providerURL.Query().Get("state"), continuation.BrowserToken
}

func newSocialTestService(repository *repositoryStub, store *socialStoreStub, provider identitydomain.Provider, adapter SocialProvider, paths []string, settings SocialSettings) SocialService {
	sessions := NewService(repository, &hasherStub{}, storesStub{active: true}, token.Generator{}, DefaultSessionSettings(), time.Now)
	return NewSocialService(repository, sessions, store, map[identitydomain.Provider]SocialProvider{provider: adapter}, paths, settings, time.Now)
}

func TestSocialSettingsControlTransactionAndExchangeLifetimes(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	userID, _ := ids.ParseUserID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	repository := &repositoryStub{socialUser: UserRecord{ID: userID, Status: identitydomain.UserStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	store := newSocialStoreStub()
	settings := SocialSettings{TransactionTTL: 4 * time.Minute, ExchangeTTL: 30 * time.Second}
	service := newSocialTestService(repository, store, identitydomain.ProviderFacebook, &socialProviderStub{}, []string{"/auth/complete"}, settings)
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderFacebook, "login", "", "", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := continueStart(t, service, started)
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderFacebook, state, "provider-code", browser); err != nil {
		t.Fatal(err)
	}
	if store.transactionTTL != settings.TransactionTTL || store.exchangeTTL != settings.ExchangeTTL {
		t.Fatalf("social TTLs = %s/%s", store.transactionTTL, store.exchangeTTL)
	}
}

func TestSocialStartRestrictsReturnPathAndBindsStoreAndLinkSession(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	repository := &repositoryStub{}
	store := newSocialStoreStub()
	service := newSocialTestService(repository, store, identitydomain.ProviderFacebook, &socialProviderStub{}, []string{"/auth/complete"}, DefaultSocialSettings())
	userID, _ := ids.ParseUserID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if _, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderFacebook, "link", userID, "session-id", "https://evil.example"); !errors.Is(err, ErrSocialDenied) {
		t.Fatalf("external return path error = %v", err)
	}
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderFacebook, "link", userID, "session-id", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := continueStart(t, service, started)
	digest, _ := secretDigest(state)
	transaction := store.transactions[digest]
	if transaction.StoreID != storeID || transaction.Subdomain != "shop" || transaction.UserID != userID || transaction.SessionID != "session-id" || transaction.Purpose != "link" {
		t.Fatalf("link transaction not store/session bound: %#v", transaction)
	}
}

func TestSocialCallbackPreservesStateForWrongBrowser(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	repository := &repositoryStub{}
	store := newSocialStoreStub()
	provider := &socialProviderStub{}
	service := newSocialTestService(repository, store, identitydomain.ProviderGoogle, provider, []string{"/auth/complete"}, DefaultSocialSettings())
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderGoogle, "login", "", "", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := continueStart(t, service, started)
	wrongBrowser, _, _ := randomSecret()
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderGoogle, state, "provider-code", wrongBrowser); !errors.Is(err, ErrSocialState) {
		t.Fatalf("wrong browser error = %v", err)
	}
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderGoogle, state, "provider-code", browser); err != nil {
		t.Fatalf("valid callback error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider exchange called %d times", provider.calls)
	}
}

func TestSocialCallbackPreservesStateForWrongProvider(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	store := newSocialStoreStub()
	provider := &socialProviderStub{}
	service := newSocialTestService(&repositoryStub{}, store, identitydomain.ProviderGoogle, provider, []string{"/auth/complete"}, DefaultSocialSettings())
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderGoogle, "login", "", "", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := continueStart(t, service, started)
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderFacebook, state, "provider-code", browser); !errors.Is(err, ErrSocialState) {
		t.Fatalf("wrong provider error = %v", err)
	}
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderGoogle, state, "provider-code", browser); err != nil {
		t.Fatalf("valid callback error = %v", err)
	}
}

func TestSocialCallbackClassifiesAndConsumesProviderFailure(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	store := newSocialStoreStub()
	provider := &socialProviderStub{err: ErrSocialProviderRejected}
	service := newSocialTestService(&repositoryStub{}, store, identitydomain.ProviderGoogle, provider, []string{"/auth/complete"}, DefaultSocialSettings())
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderGoogle, "login", "", "", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := continueStart(t, service, started)
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderGoogle, state, "bad-code", browser); !errors.Is(err, ErrSocialProviderRejected) {
		t.Fatalf("provider rejection error = %v", err)
	}
	if _, _, _, err := service.Callback(context.Background(), identitydomain.ProviderGoogle, state, "bad-code", browser); !errors.Is(err, ErrSocialState) {
		t.Fatalf("replayed rejected callback error = %v", err)
	}
}

func TestSocialExchangePreservesCodeForWrongStore(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	otherStoreID, _ := ids.ParseStoreID("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	userID, _ := ids.ParseUserID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	store := newSocialStoreStub()
	service := newSocialTestService(&repositoryStub{}, store, identitydomain.ProviderGoogle, &socialProviderStub{}, []string{"/auth/complete"}, DefaultSocialSettings())
	code, digest, _ := randomSecret()
	store.exchanges[digest] = SocialExchange{StoreID: storeID, UserID: userID, Status: identitydomain.UserStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, err := service.Exchange(context.Background(), otherStoreID, code); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong-store exchange error = %v", err)
	}
	if _, err := service.Exchange(context.Background(), storeID, code); err != nil {
		t.Fatalf("valid exchange error = %v", err)
	}
}

func TestSocialLinkUsesBoundStoreSessionWithoutIssuingNewSession(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	repository := &repositoryStub{}
	store := newSocialStoreStub()
	service := newSocialTestService(repository, store, identitydomain.ProviderFacebook, &socialProviderStub{}, []string{"/settings/security"}, DefaultSocialSettings())
	userID, _ := ids.ParseUserID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderFacebook, "link", userID, "active-session", "/settings/security")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := continueStart(t, service, started)
	exchange, returnPath, subdomain, err := service.Callback(context.Background(), identitydomain.ProviderFacebook, state, "provider-code", browser)
	if err != nil || exchange != "" || returnPath != "/settings/security" || subdomain != "shop" || repository.linkedSessionID != "active-session" || len(store.exchanges) != 0 {
		t.Fatalf("Callback() = %q, %q, %q, %v; linked session %q", exchange, returnPath, subdomain, err, repository.linkedSessionID)
	}
}

func TestSocialContinuationIsSingleUse(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	store := newSocialStoreStub()
	service := newSocialTestService(&repositoryStub{}, store, identitydomain.ProviderFacebook, &socialProviderStub{}, []string{"/auth/complete"}, DefaultSocialSettings())
	started, err := service.Start(context.Background(), storeID, "shop", identitydomain.ProviderFacebook, "login", "", "", "/auth/complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Continue(context.Background(), started.ContinuationCode); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Continue(context.Background(), started.ContinuationCode); !errors.Is(err, ErrSocialState) {
		t.Fatalf("replayed continuation error = %v", err)
	}
}

func TestSocialContinuationRejectsOversizedCodeBeforeStoreRead(t *testing.T) {
	store := newSocialStoreStub()
	service := newSocialTestService(&repositoryStub{}, store, identitydomain.ProviderFacebook, &socialProviderStub{}, []string{"/auth/complete"}, DefaultSocialSettings())
	if _, err := service.Continue(context.Background(), strings.Repeat("A", 4096)); !errors.Is(err, ErrSocialState) {
		t.Fatalf("Continue() error = %v", err)
	}
	if store.continuationReads != 0 {
		t.Fatalf("continuation store reads = %d", store.continuationReads)
	}
}
