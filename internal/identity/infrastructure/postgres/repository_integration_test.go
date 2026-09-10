//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUserSessionRotationReplayAndLogout(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), func() time.Time { return now })
	registered, err := service.Register(context.Background(), pair.A.Store.ID(), "session-test@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), registered.User.ID.String())
	})
	if _, _, err := service.AuthenticateUser(context.Background(), pair.B.Store.ID(), registered.Token.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store access error = %v", err)
	}
	if _, err := service.RefreshUser(context.Background(), pair.B.Store.ID(), registered.Token.RefreshToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store refresh error = %v", err)
	}
	if err := service.LogoutUser(context.Background(), pair.B.Store.ID(), registered.Token.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store logout error = %v", err)
	}
	user, sessionID, err := service.AuthenticateUser(context.Background(), pair.A.Store.ID(), registered.Token.AccessToken)
	if err != nil || user.ID != registered.User.ID || sessionID != registered.Token.SessionID {
		t.Fatalf("AuthenticateUser() = %#v, %q, %v", user, sessionID, err)
	}
	rotated, err := service.RefreshUser(context.Background(), pair.A.Store.ID(), registered.Token.RefreshToken)
	if err != nil || rotated.RefreshToken == registered.Token.RefreshToken {
		t.Fatalf("RefreshUser() = %#v, %v", rotated, err)
	}
	if _, err := service.RefreshUser(context.Background(), pair.A.Store.ID(), registered.Token.RefreshToken); !errors.Is(err, application.ErrRefreshReplay) {
		t.Fatalf("replay error = %v", err)
	}
	if _, _, err := service.AuthenticateUser(context.Background(), pair.A.Store.ID(), rotated.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("access after compromise error = %v", err)
	}
}

func TestConcurrentUserRefreshCreatesOnlyOneSuccessor(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), time.Now)
	registered, err := service.Register(context.Background(), pair.A.Store.ID(), "concurrent-refresh@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), registered.User.ID.String())
	})
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errorsByCall {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = service.RefreshUser(context.Background(), pair.A.Store.ID(), registered.Token.RefreshToken)
		}(index)
	}
	close(start)
	wait.Wait()
	assertOneRefreshSuccessAndReplay(t, errorsByCall)
}

func TestDisabledUserRefreshRevokesFamilyAcrossReactivation(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), time.Now)
	registered, err := service.Register(context.Background(), pair.A.Store.ID(), "disabled-refresh@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), registered.User.ID.String())
	})
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status='disabled',updated_at=now() WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), registered.User.ID.String()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errorsByCall {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = service.RefreshUser(context.Background(), pair.A.Store.ID(), registered.Token.RefreshToken)
		}(index)
	}
	close(start)
	wait.Wait()
	for _, err := range errorsByCall {
		if !errors.Is(err, application.ErrUnauthenticated) {
			t.Fatalf("disabled refresh errors = %v", errorsByCall)
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status='active',updated_at=now() WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), registered.User.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshUser(context.Background(), pair.A.Store.ID(), registered.Token.RefreshToken); err == nil {
		t.Fatal("reactivated refresh unexpectedly succeeded")
	}
	var refreshTokens int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity_refresh_tokens WHERE store_id=$1::uuid AND session_id=$2::uuid`, pair.A.Store.ID().String(), registered.Token.SessionID).Scan(&refreshTokens); err != nil || refreshTokens != 1 {
		t.Fatalf("refresh token count = %d, error %v", refreshTokens, err)
	}
}

func TestSocialExchangeSessionAdmissionUsesCurrentUserAndStoreStatus(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), time.Now)
	staleUser := application.UserRecord{StoreID: pair.A.Store.ID(), ID: pair.A.UserID, Status: identitydomain.UserStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	var sessionsBefore int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity_sessions WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status='disabled',updated_at=now() WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSessionForUser(context.Background(), staleUser); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("disabled user admission error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE users SET status='active',updated_at=now() WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='suspended',updated_at=now() WHERE store_id=$1::uuid`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSessionForUser(context.Background(), staleUser); !errors.Is(err, application.ErrStoreUnavailable) {
		t.Fatalf("suspended store admission error = %v", err)
	}
	var sessionsAfter int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity_sessions WHERE store_id=$1::uuid AND user_id=$2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()).Scan(&sessionsAfter); err != nil || sessionsAfter != sessionsBefore {
		t.Fatalf("session counts = %d before, %d after; error %v", sessionsBefore, sessionsAfter, err)
	}
}

func TestGuestTokensCannotCrossStoreScope(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), time.Now)
	guest, err := service.CreateGuest(context.Background(), pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity_guests WHERE store_id=$1::uuid`, guest.StoreID.String())
	})
	if _, err := service.AuthenticateGuest(context.Background(), pair.B.Store.ID(), guest.Token.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store access error = %v", err)
	}
	if _, err := service.RefreshGuest(context.Background(), pair.B.Store.ID(), guest.Token.RefreshToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store refresh error = %v", err)
	}
	if err := service.LogoutGuest(context.Background(), pair.B.Store.ID(), guest.Token.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("cross-store logout error = %v", err)
	}
	authenticated, err := service.AuthenticateGuest(context.Background(), pair.A.Store.ID(), guest.Token.AccessToken)
	if err != nil || authenticated.GuestID != guest.GuestID || authenticated.StoreID != pair.A.Store.ID() {
		t.Fatalf("same-store AuthenticateGuest() = %#v, %v", authenticated, err)
	}
	rotated, err := service.RefreshGuest(context.Background(), pair.A.Store.ID(), guest.Token.RefreshToken)
	if err != nil {
		t.Fatalf("same-store RefreshGuest() error = %v", err)
	}
	if _, err := service.RefreshGuest(context.Background(), pair.A.Store.ID(), guest.Token.RefreshToken); !errors.Is(err, application.ErrRefreshReplay) {
		t.Fatalf("guest replay error = %v", err)
	}
	if _, err := service.AuthenticateGuest(context.Background(), pair.A.Store.ID(), rotated.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("guest access after replay error = %v", err)
	}
	logoutGuest, err := service.CreateGuest(context.Background(), pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.LogoutGuest(context.Background(), pair.A.Store.ID(), logoutGuest.Token.AccessToken); err != nil {
		t.Fatalf("LogoutGuest() error = %v", err)
	}
	if _, err := service.AuthenticateGuest(context.Background(), pair.A.Store.ID(), logoutGuest.Token.AccessToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("guest access after logout error = %v", err)
	}
	if _, err := service.RefreshGuest(context.Background(), pair.A.Store.ID(), logoutGuest.Token.RefreshToken); !errors.Is(err, application.ErrUnauthenticated) {
		t.Fatalf("guest refresh after logout error = %v", err)
	}
	suspendedGuest, err := service.CreateGuest(context.Background(), pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='suspended',updated_at=now() WHERE store_id=$1::uuid`, pair.A.Store.ID().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateGuest(context.Background(), pair.A.Store.ID(), suspendedGuest.Token.AccessToken); !errors.Is(err, application.ErrStoreUnavailable) {
		t.Fatalf("suspended-store authenticate error = %v", err)
	}
	if _, err := service.RefreshGuest(context.Background(), pair.A.Store.ID(), suspendedGuest.Token.RefreshToken); !errors.Is(err, application.ErrStoreUnavailable) {
		t.Fatalf("suspended-store refresh error = %v", err)
	}
	if err := service.LogoutGuest(context.Background(), pair.A.Store.ID(), suspendedGuest.Token.AccessToken); !errors.Is(err, application.ErrStoreUnavailable) {
		t.Fatalf("suspended-store logout error = %v", err)
	}
}

func TestConcurrentGuestRefreshCreatesOnlyOneSuccessor(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	repository := identitypostgres.NewRepository(db)
	hasher, _ := password.NewArgon2ID(1)
	service := application.NewService(repository, hasher, storepostgres.NewRepository(db, auditpostgres.NewWriter()), token.Generator{}, application.DefaultSessionSettings(), time.Now)
	guest, err := service.CreateGuest(context.Background(), pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM identity_guests WHERE store_id=$1::uuid`, pair.A.Store.ID().String())
	})
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errorsByCall {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = service.RefreshGuest(context.Background(), pair.A.Store.ID(), guest.Token.RefreshToken)
		}(index)
	}
	close(start)
	wait.Wait()
	assertOneRefreshSuccessAndReplay(t, errorsByCall)
}

func assertOneRefreshSuccessAndReplay(t *testing.T, errorsByCall []error) {
	t.Helper()
	var successes, replays int
	for _, err := range errorsByCall {
		if err == nil {
			successes++
		}
		if errors.Is(err, application.ErrRefreshReplay) {
			replays++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("refresh outcomes = %v, want one success and one replay", errorsByCall)
	}
}

func TestConcurrentFirstSocialLoginResolvesOneUser(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := identitypostgres.NewRepository(gormtest.FromPool(t, pool))
	newIDs := make([]ids.UserID, 2)
	for index := range newIDs {
		newIDs[index], _ = ids.NewUserID()
	}
	subject := "concurrent-facebook-subject"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id=$1::uuid AND user_id = ANY($2::uuid[])`, pair.A.Store.ID().String(), []string{newIDs[0].String(), newIDs[1].String()})
	})
	start := make(chan struct{})
	results := make([]application.UserRecord, 2)
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errorsByCall[index] = repository.ResolveSocial(context.Background(), pair.A.Store.ID(), identitydomain.ProviderFacebook, subject, nil, newIDs[index], time.Now().UTC())
		}(index)
	}
	close(start)
	wait.Wait()
	for index, err := range errorsByCall {
		if err != nil {
			t.Fatalf("ResolveSocial(%d) error = %v", index, err)
		}
	}
	if results[0].ID != results[1].ID {
		t.Fatalf("resolved users = %s and %s", results[0].ID, results[1].ID)
	}
	var links int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity_social_identities WHERE store_id=$1::uuid AND provider='facebook' AND provider_subject=$2`, pair.A.Store.ID().String(), subject).Scan(&links); err != nil || links != 1 {
		t.Fatalf("social link count = %d, error %v", links, err)
	}
}

func TestSameSocialSubjectResolvesIndependentUsersAcrossStores(t *testing.T) {
	pool := integrationPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := identitypostgres.NewRepository(gormtest.FromPool(t, pool))
	subject := "shared-google-subject"
	firstID, _ := ids.NewUserID()
	secondID, _ := ids.NewUserID()
	first, err := repository.ResolveSocial(context.Background(), pair.A.Store.ID(), identitydomain.ProviderGoogle, subject, nil, firstID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ResolveSocial(context.Background(), pair.B.Store.ID(), identitydomain.ProviderGoogle, subject, nil, secondID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if first.StoreID != pair.A.Store.ID() || second.StoreID != pair.B.Store.ID() || first.ID == second.ID {
		t.Fatalf("social users = %#v / %#v", first, second)
	}
	resolved, err := repository.ResolveSocial(context.Background(), pair.A.Store.ID(), identitydomain.ProviderGoogle, subject, nil, secondID, time.Now().UTC())
	if err != nil || resolved.ID != first.ID {
		t.Fatalf("store A re-resolution = %#v, %v", resolved, err)
	}
}

func integrationPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		tb.Fatalf("parse test database URL: %v", err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatalf("test database guard: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatalf("create PostgreSQL pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}
