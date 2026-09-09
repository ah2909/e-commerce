package application

import (
	"context"
	"errors"
	"testing"
	"time"

	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type repositoryStub struct {
	passwordUser       UserRecord
	passwordHash       string
	passwordFound      bool
	registered         *SessionRecord
	createdUserSession *SessionRecord
	rejectSession      bool
	guest              *GuestRecord
	rotateReplay       bool
	rotateSession      string
	rotateExpires      time.Time
	socialUser         UserRecord
	linkedSessionID    string
}

func (stub *repositoryStub) Register(_ context.Context, _ UserRecord, _ string, session SessionRecord) error {
	stub.registered = &session
	return nil
}
func (stub *repositoryStub) PasswordUser(context.Context, ids.StoreID, string) (UserRecord, string, bool, error) {
	return stub.passwordUser, stub.passwordHash, stub.passwordFound, nil
}
func (stub *repositoryStub) UpdatePasswordHash(context.Context, ids.StoreID, ids.UserID, string, string, time.Time) error {
	return nil
}
func (stub *repositoryStub) CreateUserSession(_ context.Context, session SessionRecord) (bool, error) {
	stub.createdUserSession = &session
	return !stub.rejectSession, nil
}
func (stub *repositoryStub) AuthenticateUser(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (UserRecord, string, bool, error) {
	return UserRecord{}, "", false, nil
}
func (stub *repositoryStub) RotateUser(context.Context, ids.StoreID, identitydomain.TokenDigest, TokenRecord, TokenRecord, time.Time) (string, time.Time, bool, error) {
	return stub.rotateSession, stub.rotateExpires, stub.rotateReplay, nil
}
func (stub *repositoryStub) RevokeUserSession(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (bool, error) {
	return true, nil
}
func (stub *repositoryStub) CreateGuest(_ context.Context, guest GuestRecord, _ SessionRecord) error {
	stub.guest = &guest
	return nil
}
func (stub *repositoryStub) AuthenticateGuest(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (ids.GuestID, string, time.Time, time.Time, bool, error) {
	return "", "", time.Time{}, time.Time{}, false, nil
}
func (stub *repositoryStub) RotateGuest(context.Context, ids.StoreID, identitydomain.TokenDigest, TokenRecord, TokenRecord, time.Time) (string, time.Time, bool, error) {
	return stub.rotateSession, stub.rotateExpires, stub.rotateReplay, nil
}
func (stub *repositoryStub) RevokeGuestSession(context.Context, ids.StoreID, identitydomain.TokenDigest, time.Time) (bool, error) {
	return true, nil
}
func (stub *repositoryStub) ResolveSocial(_ context.Context, storeID ids.StoreID, _ identitydomain.Provider, _ string, _ *identitydomain.Email, _ ids.UserID, _ time.Time) (UserRecord, error) {
	stub.socialUser.StoreID = storeID
	return stub.socialUser, nil
}
func (stub *repositoryStub) LinkSocial(_ context.Context, _ ids.StoreID, _ ids.UserID, sessionID string, _ identitydomain.Provider, _ string, _ time.Time) error {
	stub.linkedSessionID = sessionID
	return nil
}

type hasherStub struct {
	valid     bool
	hashes    int
	verifyErr error
}

func (stub *hasherStub) Hash(context.Context, string) (string, error) {
	stub.hashes++
	return "hash", nil
}
func (stub *hasherStub) Verify(context.Context, string, string) (bool, bool, error) {
	return stub.valid, false, stub.verifyErr
}

func TestLoginPreservesHashAdmissionFailure(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	email, _ := identitydomain.ParseEmail("member@example.com")
	userID, _ := ids.ParseUserID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	repository := &repositoryStub{passwordUser: UserRecord{ID: userID, Email: &email, Status: identitydomain.UserStatusActive}, passwordHash: "hash", passwordFound: true}
	repository.passwordUser.StoreID = storeID
	service := NewService(repository, &hasherStub{verifyErr: ErrAuthenticationBusy}, storesStub{active: true}, token.Generator{}, DefaultSessionSettings(), time.Now)
	if _, err := service.Login(context.Background(), storeID, "member@example.com", "valid-password-value"); !errors.Is(err, ErrAuthenticationBusy) {
		t.Fatalf("Login() error = %v", err)
	}
}

type storesStub struct{ active bool }

func (stub storesStub) IsActive(context.Context, ids.StoreID) (bool, error) { return stub.active, nil }

func TestLoginUsesGenericFailureAndPasswordWorkForMissingUser(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	hasher := &hasherStub{}
	service := NewService(&repositoryStub{}, hasher, storesStub{active: true}, token.Generator{}, DefaultSessionSettings(), time.Now)
	_, err := service.Login(context.Background(), storeID, "missing@example.com", "valid-password-value")
	if !errors.Is(err, ErrUnauthenticated) || hasher.hashes != 1 {
		t.Fatalf("Login() error = %v, fake hashes = %d", err, hasher.hashes)
	}
}

func TestRegisterCreatesOpaqueBoundedSession(t *testing.T) {
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	now := time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC)
	repository := &repositoryStub{}
	service := NewService(repository, &hasherStub{}, storesStub{active: true}, token.Generator{}, DefaultSessionSettings(), func() time.Time { return now })
	result, err := service.Register(context.Background(), storeID, "User@Example.com", "valid-password-value")
	if err != nil {
		t.Fatal(err)
	}
	if repository.registered == nil || result.Token.AccessToken == result.Token.RefreshToken || result.Token.AccessExpiresAt != now.Add(15*time.Minute) || result.Token.SessionExpiresAt != now.Add(30*24*time.Hour) {
		t.Fatalf("unexpected registered session: %#v", result.Token)
	}
	if result.User.Email == nil || result.User.Email.Canonical() != "user@example.com" {
		t.Fatalf("unexpected registered user: %#v", result.User)
	}
}

func TestGuestCreationRequiresActiveStore(t *testing.T) {
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	service := NewService(&repositoryStub{}, &hasherStub{}, storesStub{active: false}, token.Generator{}, DefaultSessionSettings(), time.Now)
	if _, err := service.CreateGuest(context.Background(), storeID); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("CreateGuest() error = %v", err)
	}
}

func TestRefreshReplayIsDistinguishedWithoutReturningTokens(t *testing.T) {
	value, _, _ := token.Generate()
	storeID, _ := ids.ParseStoreID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	service := NewService(&repositoryStub{rotateReplay: true}, &hasherStub{}, storesStub{active: true}, token.Generator{}, DefaultSessionSettings(), time.Now)
	result, err := service.RefreshUser(context.Background(), storeID, value)
	if !errors.Is(err, ErrRefreshReplay) || result.AccessToken != "" || result.RefreshToken != "" {
		t.Fatalf("RefreshUser() = %#v, %v", result, err)
	}
}
