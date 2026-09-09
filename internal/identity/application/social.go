package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrSocialDenied              = errors.New("social login denied")
	ErrSocialState               = errors.New("invalid social state")
	ErrSocialAccountLink         = errors.New("social account linking required")
	ErrSocialProviderRejected    = errors.New("social provider rejected request")
	ErrSocialProviderUnavailable = errors.New("social provider unavailable")
)

type SocialProfile struct {
	Subject       string
	Email         string
	EmailVerified bool
}

type SocialProvider interface {
	AuthorizationURL(state, nonce, codeChallenge string) string
	Exchange(context.Context, string, string, string) (SocialProfile, error)
}

type SocialTransaction struct {
	StoreID       ids.StoreID             `json:"store_id"`
	Subdomain     string                  `json:"subdomain"`
	Provider      identitydomain.Provider `json:"provider"`
	Purpose       string                  `json:"purpose"`
	UserID        ids.UserID              `json:"user_id,omitempty"`
	SessionID     string                  `json:"session_id,omitempty"`
	Nonce         string                  `json:"nonce"`
	Verifier      string                  `json:"verifier"`
	BrowserDigest [32]byte                `json:"browser_digest"`
	ReturnPath    string                  `json:"return_path"`
}

type SocialExchange struct {
	StoreID   ids.StoreID               `json:"store_id"`
	UserID    ids.UserID                `json:"user_id"`
	Email     string                    `json:"email,omitempty"`
	Status    identitydomain.UserStatus `json:"status"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type SocialContinuation struct {
	AuthorizationURL string `json:"authorization_url"`
	BrowserToken     string `json:"browser_token"`
}

type SocialStore interface {
	PutTransaction(context.Context, [32]byte, SocialTransaction, time.Duration) error
	TakeTransaction(context.Context, [32]byte, identitydomain.Provider, [32]byte) (SocialTransaction, bool, error)
	PutContinuation(context.Context, [32]byte, SocialContinuation, time.Duration) error
	TakeContinuation(context.Context, [32]byte) (SocialContinuation, bool, error)
	PutExchange(context.Context, [32]byte, SocialExchange, time.Duration) error
	TakeExchange(context.Context, [32]byte, ids.StoreID) (SocialExchange, bool, error)
}

type SocialService struct {
	repository Repository
	sessions   Service
	store      SocialStore
	providers  map[identitydomain.Provider]SocialProvider
	returns    map[string]struct{}
	settings   SocialSettings
	now        func() time.Time
}

type SocialSettings struct {
	TransactionTTL time.Duration
	ExchangeTTL    time.Duration
}

func DefaultSocialSettings() SocialSettings {
	return SocialSettings{TransactionTTL: 10 * time.Minute, ExchangeTTL: time.Minute}
}

type SocialStart struct {
	ContinuationCode string
}

func NewSocialService(repository Repository, sessions Service, store SocialStore, providers map[identitydomain.Provider]SocialProvider, returnPaths []string, settings SocialSettings, now func() time.Time) SocialService {
	allowed := make(map[string]struct{}, len(returnPaths))
	for _, path := range returnPaths {
		allowed[path] = struct{}{}
	}
	return SocialService{repository: repository, sessions: sessions, store: store, providers: providers, returns: allowed, settings: settings, now: now}
}

func (service SocialService) Start(ctx context.Context, storeID ids.StoreID, subdomain string, provider identitydomain.Provider, purpose string, userID ids.UserID, sessionID, returnPath string) (SocialStart, error) {
	adapter, ok := service.providers[provider]
	if !ok || (purpose != "login" && purpose != "link") {
		return SocialStart{}, ErrSocialDenied
	}
	if _, ok := service.returns[returnPath]; !ok {
		return SocialStart{}, ErrSocialDenied
	}
	if purpose == "link" && (userID == "" || sessionID == "") {
		return SocialStart{}, ErrUnauthenticated
	}
	state, stateDigest, err := randomSecret()
	if err != nil {
		return SocialStart{}, err
	}
	nonce, _, err := randomSecret()
	if err != nil {
		return SocialStart{}, err
	}
	verifier, _, err := randomSecret()
	if err != nil {
		return SocialStart{}, err
	}
	browser, browserDigest, err := randomSecret()
	if err != nil {
		return SocialStart{}, err
	}
	challengeRaw := sha256.Sum256([]byte(verifier))
	transaction := SocialTransaction{StoreID: storeID, Subdomain: subdomain, Provider: provider, Purpose: purpose, UserID: userID, SessionID: sessionID, Nonce: nonce, Verifier: verifier, BrowserDigest: browserDigest, ReturnPath: returnPath}
	if err := service.store.PutTransaction(ctx, stateDigest, transaction, service.settings.TransactionTTL); err != nil {
		return SocialStart{}, fmt.Errorf("store social transaction: %w", err)
	}
	continuation, continuationDigest, err := randomSecret()
	if err != nil {
		return SocialStart{}, err
	}
	record := SocialContinuation{AuthorizationURL: adapter.AuthorizationURL(state, nonce, base64.RawURLEncoding.EncodeToString(challengeRaw[:])), BrowserToken: browser}
	if err := service.store.PutContinuation(ctx, continuationDigest, record, service.settings.TransactionTTL); err != nil {
		return SocialStart{}, fmt.Errorf("store social continuation: %w", err)
	}
	return SocialStart{ContinuationCode: continuation}, nil
}

func (service SocialService) Continue(ctx context.Context, code string) (SocialContinuation, error) {
	digest, err := secretDigest(code)
	if err != nil {
		return SocialContinuation{}, ErrSocialState
	}
	continuation, found, err := service.store.TakeContinuation(ctx, digest)
	if err != nil {
		return SocialContinuation{}, fmt.Errorf("take social continuation: %w", err)
	}
	if !found || continuation.AuthorizationURL == "" || continuation.BrowserToken == "" {
		return SocialContinuation{}, ErrSocialState
	}
	return continuation, nil
}

func (service SocialService) Callback(ctx context.Context, provider identitydomain.Provider, state, code, browser string) (string, string, string, error) {
	stateDigest, err := secretDigest(state)
	if err != nil {
		return "", "", "", ErrSocialState
	}
	browserDigest, browserErr := secretDigest(browser)
	if browserErr != nil {
		return "", "", "", ErrSocialState
	}
	transaction, found, err := service.store.TakeTransaction(ctx, stateDigest, provider, browserDigest)
	if err != nil {
		return "", "", "", fmt.Errorf("take social transaction: %w", err)
	}
	if !found {
		return "", "", "", ErrSocialState
	}
	if code == "" {
		return "", "", "", ErrSocialDenied
	}
	if err := service.sessions.requireActiveStore(ctx, transaction.StoreID); err != nil {
		return "", "", "", err
	}
	adapter, ok := service.providers[provider]
	if !ok {
		return "", "", "", ErrSocialDenied
	}
	profile, err := adapter.Exchange(ctx, code, transaction.Verifier, transaction.Nonce)
	if err != nil {
		if errors.Is(err, ErrSocialProviderRejected) {
			return "", "", "", fmt.Errorf("exchange provider code: %w", ErrSocialProviderRejected)
		}
		return "", "", "", fmt.Errorf("exchange provider code: %w", ErrSocialProviderUnavailable)
	}
	if profile.Subject == "" || len(profile.Subject) > 255 {
		return "", "", "", ErrSocialDenied
	}
	now := service.now().UTC()
	var user UserRecord
	if transaction.Purpose == "link" {
		if err := service.repository.LinkSocial(ctx, transaction.StoreID, transaction.UserID, transaction.SessionID, provider, profile.Subject, now); err != nil {
			return "", "", "", err
		}
		return "", transaction.ReturnPath, transaction.Subdomain, nil
	} else {
		var email *identitydomain.Email
		if provider == identitydomain.ProviderGoogle && profile.EmailVerified {
			parsed, parseErr := identitydomain.ParseEmail(profile.Email)
			if parseErr == nil {
				email = &parsed
			}
		}
		newID, err := ids.NewUserID()
		if err != nil {
			return "", "", "", err
		}
		user, err = service.repository.ResolveSocial(ctx, transaction.StoreID, provider, profile.Subject, email, newID, now)
		if errors.Is(err, ErrIdentityExists) {
			return "", "", "", ErrSocialAccountLink
		}
		if err != nil {
			return "", "", "", err
		}
	}
	exchange, exchangeDigest, err := randomSecret()
	if err != nil {
		return "", "", "", err
	}
	pending := SocialExchange{StoreID: transaction.StoreID, UserID: user.ID, Status: user.Status, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
	if user.Email != nil {
		pending.Email = user.Email.Presented()
	}
	if err := service.store.PutExchange(ctx, exchangeDigest, pending, service.settings.ExchangeTTL); err != nil {
		return "", "", "", err
	}
	return exchange, transaction.ReturnPath, transaction.Subdomain, nil
}

func (service SocialService) Exchange(ctx context.Context, storeID ids.StoreID, code string) (UserSession, error) {
	digest, err := secretDigest(code)
	if err != nil {
		return UserSession{}, ErrUnauthenticated
	}
	exchange, found, err := service.store.TakeExchange(ctx, digest, storeID)
	if err != nil {
		return UserSession{}, fmt.Errorf("take social exchange: %w", err)
	}
	if !found {
		return UserSession{}, ErrUnauthenticated
	}
	user := UserRecord{StoreID: storeID, ID: exchange.UserID, Status: exchange.Status, CreatedAt: exchange.CreatedAt, UpdatedAt: exchange.UpdatedAt}
	if exchange.Email != "" {
		email, err := identitydomain.ParseEmail(exchange.Email)
		if err != nil {
			return UserSession{}, ErrUnauthenticated
		}
		user.Email = &email
	}
	return service.sessions.CreateSessionForUser(ctx, user)
}

func randomSecret() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(raw[:])
	return value, sha256.Sum256([]byte(value)), nil
}

func secretDigest(value string) ([32]byte, error) {
	if len(value) != 43 {
		return [32]byte{}, ErrSocialState
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, ErrSocialState
	}
	return sha256.Sum256([]byte(value)), nil
}
