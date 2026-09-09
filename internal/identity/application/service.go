package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrIdentityExists     = errors.New("identity exists")
	ErrInvalidIdentity    = errors.New("invalid identity")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrRefreshReplay      = errors.New("refresh token replay")
	ErrStoreUnavailable   = errors.New("storefront unavailable")
	ErrAuthenticationBusy = errors.New("authentication capacity unavailable")
)

const accessTTL = 15 * time.Minute
const sessionTTL = 30 * 24 * time.Hour

func DefaultSessionSettings() SessionSettings {
	return SessionSettings{AccessTTL: accessTTL, SessionTTL: sessionTTL}
}

type PasswordHasher interface {
	Hash(context.Context, string) (string, error)
	Verify(context.Context, string, string) (bool, bool, error)
}

type Repository interface {
	Register(context.Context, UserRecord, string, SessionRecord) error
	PasswordUser(context.Context, ids.StoreID, string) (UserRecord, string, bool, error)
	UpdatePasswordHash(context.Context, ids.StoreID, ids.UserID, string, string, time.Time) error
	CreateUserSession(context.Context, SessionRecord) (bool, error)
	AuthenticateUser(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (UserRecord, string, bool, error)
	RotateUser(context.Context, ids.StoreID, identitydomain.TokenDigest, TokenRecord, TokenRecord, time.Time) (string, time.Time, bool, error)
	RevokeUserSession(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (bool, error)
	CreateGuest(context.Context, GuestRecord, SessionRecord) error
	AuthenticateGuest(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (ids.GuestID, string, time.Time, time.Time, bool, error)
	RotateGuest(context.Context, ids.StoreID, identitydomain.TokenDigest, TokenRecord, TokenRecord, time.Time) (string, time.Time, bool, error)
	RevokeGuestSession(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (bool, error)
	ResolveSocial(context.Context, ids.StoreID, identitydomain.Provider, string, *identitydomain.Email, ids.UserID, time.Time) (UserRecord, error)
	LinkSocial(context.Context, ids.StoreID, ids.UserID, string, identitydomain.Provider, string, time.Time) error
}

type StoreReader interface {
	IsActive(context.Context, ids.StoreID) (bool, error)
}

type TokenManager interface {
	Generate() (string, identitydomain.TokenDigest, error)
	Parse(string) (identitydomain.TokenDigest, error)
	NewSessionID() (string, error)
}

type SessionSettings struct {
	AccessTTL  time.Duration
	SessionTTL time.Duration
}

type UserRecord struct {
	StoreID   ids.StoreID
	ID        ids.UserID
	Email     *identitydomain.Email
	Status    identitydomain.UserStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type GuestRecord struct {
	StoreID   ids.StoreID
	GuestID   ids.GuestID
	CreatedAt time.Time
	ExpiresAt time.Time
}

type TokenRecord struct {
	Digest    identitydomain.TokenDigest
	ExpiresAt time.Time
	CreatedAt time.Time
}

type SessionRecord struct {
	SessionID string
	UserID    ids.UserID
	StoreID   ids.StoreID
	GuestID   ids.GuestID
	ExpiresAt time.Time
	CreatedAt time.Time
	Access    TokenRecord
	Refresh   TokenRecord
}

type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	SessionExpiresAt time.Time
	SessionID        string
}

type UserSession struct {
	User  UserRecord
	Token TokenPair
}

type PreparedRegistration struct {
	PreparedAccount
	Session SessionRecord
	Result  UserSession
}

type PreparedAccount struct {
	User         UserRecord
	PasswordHash string
}

type GuestSession struct {
	StoreID ids.StoreID
	GuestID ids.GuestID
	Token   TokenPair
}

type Service struct {
	repository Repository
	hasher     PasswordHasher
	stores     StoreReader
	tokens     TokenManager
	settings   SessionSettings
	now        func() time.Time
}

func NewService(repository Repository, hasher PasswordHasher, stores StoreReader, tokens TokenManager, settings SessionSettings, now func() time.Time) Service {
	return Service{repository: repository, hasher: hasher, stores: stores, tokens: tokens, settings: settings, now: now}
}

func (service Service) Register(ctx context.Context, storeID ids.StoreID, emailValue, passwordValue string) (UserSession, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return UserSession{}, err
	}
	prepared, err := service.PrepareRegistration(ctx, storeID, emailValue, passwordValue)
	if err != nil {
		return UserSession{}, err
	}
	if err := service.repository.Register(ctx, prepared.User, prepared.PasswordHash, prepared.Session); err != nil {
		return UserSession{}, fmt.Errorf("register persistence: %w", err)
	}
	return prepared.Result, nil
}

func (service Service) PrepareRegistration(ctx context.Context, storeID ids.StoreID, emailValue, passwordValue string) (PreparedRegistration, error) {
	account, err := service.PrepareAccount(ctx, storeID, emailValue, passwordValue)
	if err != nil {
		return PreparedRegistration{}, err
	}
	session, pair, err := service.newSession(account.User.ID, storeID, "", account.User.CreatedAt)
	if err != nil {
		return PreparedRegistration{}, fmt.Errorf("register: session: %w", err)
	}
	result := UserSession{User: account.User, Token: pair}
	return PreparedRegistration{PreparedAccount: account, Session: session, Result: result}, nil
}

func (service Service) PrepareAccount(ctx context.Context, storeID ids.StoreID, emailValue, passwordValue string) (PreparedAccount, error) {
	email, err := identitydomain.ParseEmail(emailValue)
	if err != nil || identitydomain.ValidatePassword(passwordValue) != nil {
		return PreparedAccount{}, ErrInvalidIdentity
	}
	hash, err := service.hasher.Hash(ctx, passwordValue)
	if err != nil {
		return PreparedAccount{}, fmt.Errorf("register: hash password: %w", err)
	}
	userID, err := ids.NewUserID()
	if err != nil {
		return PreparedAccount{}, fmt.Errorf("register: user ID: %w", err)
	}
	now := service.now().UTC()
	user := UserRecord{StoreID: storeID, ID: userID, Email: &email, Status: identitydomain.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	return PreparedAccount{User: user, PasswordHash: hash}, nil
}

func (service Service) Login(ctx context.Context, storeID ids.StoreID, emailValue, passwordValue string) (UserSession, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return UserSession{}, err
	}
	email, err := identitydomain.ParseEmail(emailValue)
	if err != nil || identitydomain.ValidatePassword(passwordValue) != nil {
		if workErr := service.fakePasswordWork(ctx); workErr != nil {
			return UserSession{}, workErr
		}
		return UserSession{}, ErrUnauthenticated
	}
	user, hash, found, err := service.repository.PasswordUser(ctx, storeID, email.Canonical())
	if err != nil {
		return UserSession{}, fmt.Errorf("login persistence: %w", err)
	}
	if !found {
		if workErr := service.fakePasswordWork(ctx); workErr != nil {
			return UserSession{}, workErr
		}
		return UserSession{}, ErrUnauthenticated
	}
	valid, rehash, err := service.hasher.Verify(ctx, passwordValue, hash)
	if err != nil {
		return UserSession{}, fmt.Errorf("login: verify password: %w", err)
	}
	if !valid || user.Status != identitydomain.UserStatusActive {
		return UserSession{}, ErrUnauthenticated
	}
	now := service.now().UTC()
	if rehash {
		replacement, err := service.hasher.Hash(ctx, passwordValue)
		if err != nil {
			return UserSession{}, fmt.Errorf("login rehash password: %w", err)
		}
		if err := service.repository.UpdatePasswordHash(ctx, storeID, user.ID, hash, replacement, now); err != nil {
			return UserSession{}, fmt.Errorf("login rehash persistence: %w", err)
		}
	}
	session, pair, err := service.newSession(user.ID, storeID, "", now)
	if err != nil {
		return UserSession{}, fmt.Errorf("login session: %w", err)
	}
	created, err := service.repository.CreateUserSession(ctx, session)
	if err != nil {
		return UserSession{}, fmt.Errorf("login persistence: %w", err)
	}
	if !created {
		return UserSession{}, ErrUnauthenticated
	}
	return UserSession{User: user, Token: pair}, nil
}

func (service Service) AuthenticateUser(ctx context.Context, storeID ids.StoreID, accessToken string) (UserRecord, string, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return UserRecord{}, "", err
	}
	digest, err := service.tokens.Parse(accessToken)
	if err != nil {
		return UserRecord{}, "", ErrUnauthenticated
	}
	user, sessionID, found, err := service.repository.AuthenticateUser(ctx, storeID, digest, service.now().UTC())
	if err != nil {
		return UserRecord{}, "", fmt.Errorf("authenticate persistence: %w", err)
	}
	if !found || user.Status != identitydomain.UserStatusActive {
		return UserRecord{}, "", ErrUnauthenticated
	}
	return user, sessionID, nil
}

func (service Service) RefreshUser(ctx context.Context, storeID ids.StoreID, refreshToken string) (TokenPair, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return TokenPair{}, err
	}
	digest, err := service.tokens.Parse(refreshToken)
	if err != nil {
		return TokenPair{}, ErrUnauthenticated
	}
	now := service.now().UTC()
	accessValue, access, refreshValue, refresh, err := service.newTokens(now, now.Add(service.settings.SessionTTL))
	if err != nil {
		return TokenPair{}, err
	}
	sessionID, sessionExpires, replay, err := service.repository.RotateUser(ctx, storeID, digest, access, refresh, now)
	if err != nil {
		return TokenPair{}, fmt.Errorf("refresh persistence: %w", err)
	}
	if replay {
		return TokenPair{}, ErrRefreshReplay
	}
	if sessionID == "" {
		return TokenPair{}, ErrUnauthenticated
	}
	if access.ExpiresAt.After(sessionExpires) {
		access.ExpiresAt = sessionExpires
	}
	return TokenPair{AccessToken: accessValue, RefreshToken: refreshValue, AccessExpiresAt: access.ExpiresAt, SessionExpiresAt: sessionExpires, SessionID: sessionID}, nil
}

func (service Service) LogoutUser(ctx context.Context, storeID ids.StoreID, accessToken string) error {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return err
	}
	digest, err := service.tokens.Parse(accessToken)
	if err != nil {
		return ErrUnauthenticated
	}
	found, err := service.repository.RevokeUserSession(ctx, storeID, digest, service.now().UTC())
	if err != nil {
		return fmt.Errorf("logout persistence: %w", err)
	}
	if !found {
		return ErrUnauthenticated
	}
	return nil
}

func (service Service) CreateGuest(ctx context.Context, storeID ids.StoreID) (GuestSession, error) {
	active, err := service.stores.IsActive(ctx, storeID)
	if err != nil {
		return GuestSession{}, fmt.Errorf("resolve guest store: %w", err)
	}
	if !active {
		return GuestSession{}, ErrStoreUnavailable
	}
	guestID, err := ids.NewGuestID()
	if err != nil {
		return GuestSession{}, err
	}
	now := service.now().UTC()
	session, pair, err := service.newSession("", storeID, guestID, now)
	if err != nil {
		return GuestSession{}, err
	}
	guest := GuestRecord{StoreID: storeID, GuestID: guestID, CreatedAt: now, ExpiresAt: pair.SessionExpiresAt}
	if err := service.repository.CreateGuest(ctx, guest, session); err != nil {
		return GuestSession{}, fmt.Errorf("create guest persistence: %w", err)
	}
	return GuestSession{StoreID: storeID, GuestID: guestID, Token: pair}, nil
}

func (service Service) AuthenticateGuest(ctx context.Context, storeID ids.StoreID, accessToken string) (GuestSession, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return GuestSession{}, err
	}
	digest, err := service.tokens.Parse(accessToken)
	if err != nil {
		return GuestSession{}, ErrUnauthenticated
	}
	guestID, sessionID, accessExpires, sessionExpires, found, err := service.repository.AuthenticateGuest(ctx, storeID, digest, service.now().UTC())
	if err != nil {
		return GuestSession{}, fmt.Errorf("authenticate guest persistence: %w", err)
	}
	if !found {
		return GuestSession{}, ErrUnauthenticated
	}
	return GuestSession{StoreID: storeID, GuestID: guestID, Token: TokenPair{SessionID: sessionID, AccessExpiresAt: accessExpires, SessionExpiresAt: sessionExpires}}, nil
}

func (service Service) RefreshGuest(ctx context.Context, storeID ids.StoreID, refreshToken string) (TokenPair, error) {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return TokenPair{}, err
	}
	digest, err := service.tokens.Parse(refreshToken)
	if err != nil {
		return TokenPair{}, ErrUnauthenticated
	}
	now := service.now().UTC()
	accessValue, access, refreshValue, refresh, err := service.newTokens(now, now.Add(service.settings.SessionTTL))
	if err != nil {
		return TokenPair{}, err
	}
	sessionID, sessionExpires, replay, err := service.repository.RotateGuest(ctx, storeID, digest, access, refresh, now)
	if err != nil {
		return TokenPair{}, fmt.Errorf("refresh guest persistence: %w", err)
	}
	if replay {
		return TokenPair{}, ErrRefreshReplay
	}
	if sessionID == "" {
		return TokenPair{}, ErrUnauthenticated
	}
	if access.ExpiresAt.After(sessionExpires) {
		access.ExpiresAt = sessionExpires
	}
	return TokenPair{AccessToken: accessValue, RefreshToken: refreshValue, AccessExpiresAt: access.ExpiresAt, SessionExpiresAt: sessionExpires, SessionID: sessionID}, nil
}

func (service Service) LogoutGuest(ctx context.Context, storeID ids.StoreID, accessToken string) error {
	if err := service.requireActiveStore(ctx, storeID); err != nil {
		return err
	}
	digest, err := service.tokens.Parse(accessToken)
	if err != nil {
		return ErrUnauthenticated
	}
	found, err := service.repository.RevokeGuestSession(ctx, storeID, digest, service.now().UTC())
	if err != nil {
		return fmt.Errorf("logout guest persistence: %w", err)
	}
	if !found {
		return ErrUnauthenticated
	}
	return nil
}

func (service Service) requireActiveStore(ctx context.Context, storeID ids.StoreID) error {
	active, err := service.stores.IsActive(ctx, storeID)
	if err != nil {
		return fmt.Errorf("resolve guest store: %w", err)
	}
	if !active {
		return ErrStoreUnavailable
	}
	return nil
}

func (service Service) CreateSessionForUser(ctx context.Context, user UserRecord) (UserSession, error) {
	if user.Status != identitydomain.UserStatusActive {
		return UserSession{}, ErrUnauthenticated
	}
	if err := service.requireActiveStore(ctx, user.StoreID); err != nil {
		return UserSession{}, err
	}
	now := service.now().UTC()
	session, pair, err := service.newSession(user.ID, user.StoreID, "", now)
	if err != nil {
		return UserSession{}, err
	}
	created, err := service.repository.CreateUserSession(ctx, session)
	if err != nil {
		return UserSession{}, fmt.Errorf("create social session: %w", err)
	}
	if !created {
		return UserSession{}, ErrUnauthenticated
	}
	return UserSession{User: user, Token: pair}, nil
}

func (service Service) fakePasswordWork(ctx context.Context) error {
	_, err := service.hasher.Hash(ctx, "invalid-password-value")
	return err
}

func (service Service) newSession(userID ids.UserID, storeID ids.StoreID, guestID ids.GuestID, now time.Time) (SessionRecord, TokenPair, error) {
	sessionID, err := service.tokens.NewSessionID()
	if err != nil {
		return SessionRecord{}, TokenPair{}, err
	}
	sessionExpires := now.Add(service.settings.SessionTTL)
	accessValue, access, refreshValue, refresh, err := service.newTokens(now, sessionExpires)
	if err != nil {
		return SessionRecord{}, TokenPair{}, err
	}
	record := SessionRecord{SessionID: sessionID, UserID: userID, StoreID: storeID, GuestID: guestID, ExpiresAt: sessionExpires, CreatedAt: now, Access: access, Refresh: refresh}
	pair := TokenPair{AccessToken: accessValue, RefreshToken: refreshValue, AccessExpiresAt: access.ExpiresAt, SessionExpiresAt: sessionExpires, SessionID: sessionID}
	return record, pair, nil
}

func (service Service) newTokens(now, sessionExpires time.Time) (string, TokenRecord, string, TokenRecord, error) {
	accessValue, accessDigest, err := service.tokens.Generate()
	if err != nil {
		return "", TokenRecord{}, "", TokenRecord{}, err
	}
	refreshValue, refreshDigest, err := service.tokens.Generate()
	if err != nil {
		return "", TokenRecord{}, "", TokenRecord{}, err
	}
	accessExpires := now.Add(service.settings.AccessTTL)
	if accessExpires.After(sessionExpires) {
		accessExpires = sessionExpires
	}
	return accessValue, TokenRecord{Digest: accessDigest, CreatedAt: now, ExpiresAt: accessExpires}, refreshValue, TokenRecord{Digest: refreshDigest, CreatedAt: now, ExpiresAt: sessionExpires}, nil
}
