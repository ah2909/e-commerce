//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitypassword "github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	onboardingpostgres "github.com/ah2909/e-commerce/internal/onboarding/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"
)

func TestRepositoryTenantIsolationAndVietnameseRoundTrip(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	repository := storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	record, found, err := repository.ResolveTenant(ctx, pair.A.Store.ID(), pair.A.UserID)
	if err != nil || !found {
		t.Fatalf("ResolveTenant(A) = found %v, error %v", found, err)
	}
	if record.StoreName != "Cửa hàng Ánh Dương" || record.DefaultCurrency != "VND" || record.Locale != "vi-VN" || record.Timezone != "Asia/Ho_Chi_Minh" {
		t.Fatalf("Vietnamese store = %q %s %s %s", record.StoreName, record.DefaultCurrency, record.Locale, record.Timezone)
	}
	if record.StoreID != pair.A.Store.ID() || record.UserID != pair.A.UserID {
		t.Fatalf("membership scope = %s/%s", record.StoreID, record.UserID)
	}
	for _, attempt := range []struct {
		storeID ids.StoreID
		userID  ids.UserID
	}{{pair.B.Store.ID(), pair.A.UserID}, {pair.A.Store.ID(), pair.B.UserID}} {
		_, found, err := repository.ResolveTenant(ctx, attempt.storeID, attempt.userID)
		if err != nil || found {
			t.Fatalf("cross-store ResolveTenant(%s, %s) = found %v, error %v", attempt.storeID, attempt.userID, found, err)
		}
	}
}

func TestRepositoryRejectsCrossStoreUserMembership(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	now := time.Now().UTC()
	membership, err := domain.NewMembership(pair.B.Store.ID(), pair.A.UserID, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
		membership.StoreID().String(), membership.UserID().String(), membership.Status(), membership.Role(), membership.CreatedAt(), membership.UpdatedAt())
	if err == nil {
		t.Fatal("cross-store membership insert succeeded")
	}
}

func TestMembershipMutationsAreScopedAuditedAndKeepAnOwner(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	repository := storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter())
	ctx := context.Background()
	now := time.Now().UTC()
	secondOwnerID, _ := ids.NewUserID()
	insertTenantUser(t, pool, pair.A.Store.ID(), secondOwnerID)
	secondOwner, _ := domain.NewMembershipWithRole(pair.A.Store.ID(), secondOwnerID, domain.RoleOwner, now)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_records WHERE store_id=$1::uuid`, pair.A.Store.ID().String())
	})
	if err := repository.CreateMembership(ctx, secondOwner, pair.A.UserID, "create-member-request"); err != nil {
		t.Fatalf("CreateMembership() error = %v", err)
	}
	if _, err := repository.UpdateMembership(ctx, pair.A.Store.ID(), pair.A.UserID, domain.RoleStaff, domain.MembershipStatusInactive, pair.A.UserID, "demote-first-request", now.Add(time.Second)); err != nil {
		t.Fatalf("UpdateMembership(first owner) error = %v", err)
	}
	if _, err := repository.UpdateMembership(ctx, pair.A.Store.ID(), secondOwnerID, domain.RoleStaff, domain.MembershipStatusInactive, secondOwnerID, "demote-last-request", now.Add(2*time.Second)); !errors.Is(err, application.ErrLastOwner) {
		t.Fatalf("last-owner UpdateMembership() error = %v", err)
	}
	if err := repository.CreateMembership(ctx, secondOwner, pair.B.UserID, "cross-store-request"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("cross-store actor error = %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_records WHERE store_id=$1::uuid AND action LIKE 'membership.%' AND actor_type='user' AND actor_id=$2`, pair.A.Store.ID().String(), pair.A.UserID.String()).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit count = %d, error %v", auditCount, err)
	}
}

func TestConcurrentOwnerDemotionsPreserveAnActiveOwner(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	repository := storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter())
	now := time.Now().UTC()
	secondOwnerID, _ := ids.NewUserID()
	insertTenantUser(t, pool, pair.A.Store.ID(), secondOwnerID)
	secondOwner, _ := domain.NewMembershipWithRole(pair.A.Store.ID(), secondOwnerID, domain.RoleOwner, now)
	if err := repository.CreateMembership(context.Background(), secondOwner, pair.A.UserID, "concurrent-owner-create"); err != nil {
		t.Fatalf("CreateMembership() error = %v", err)
	}
	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	targets := []ids.UserID{pair.A.UserID, secondOwnerID}
	for index := range targets {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = repository.UpdateMembership(context.Background(), pair.A.Store.ID(), targets[index], domain.RoleStaff, domain.MembershipStatusInactive, targets[index], fmt.Sprintf("concurrent-owner-%d", index), now.Add(time.Second))
		}(index)
	}
	close(start)
	wait.Wait()
	var successes int
	for _, err := range errorsByCall {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful demotions = %d, errors = %v", successes, errorsByCall)
	}
	var activeOwners int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM store_memberships WHERE store_id=$1::uuid AND status='active' AND role='owner'`, pair.A.Store.ID().String()).Scan(&activeOwners); err != nil || activeOwners != 1 {
		t.Fatalf("active owners = %d, error %v", activeOwners, err)
	}
}

func TestRepositoryDatabaseConstraints(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'owner', 'staff', now(), now())`, pair.A.Store.ID().String(), pair.B.UserID.String())
	missingStoreID, _ := ids.NewStoreID()
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'active', 'staff', now(), now())`, missingStoreID.String(), pair.B.UserID.String())
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'active', 'owner', now(), now())`, pair.A.Store.ID().String(), pair.A.UserID.String())
	assertExecRejected(t, pool, `UPDATE stores SET status = 'deleted' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET name = ' ' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET subdomain = 'renamed-store' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET settings_version = 0 WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET updated_at = created_at - interval '1 second' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE store_memberships SET updated_at = created_at - interval '1 second' WHERE store_id = $1::uuid AND user_id = $2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String())
}

func TestRepositoryRoundTripsInternationalSettingsUnicodeAndUTC(t *testing.T) {
	pool := integrationPool(t, nil)
	db := gormtest.FromPool(t, pool)
	repository := storepostgres.NewRepository(db, auditpostgres.NewWriter())
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	now := time.Date(2026, 9, 7, 20, 0, 0, 123456000, time.FixedZone("ICT", 7*60*60))
	decomposedName := "Cửa hàng A\u0301nh Dương"
	identityRepository := identitypostgres.NewRepository(db)
	hasher, _ := identitypassword.NewArgon2ID(1)
	identityService := identityapplication.NewService(identityRepository, hasher, repository, token.Generator{}, identityapplication.DefaultSessionSettings(), func() time.Time { return now })
	onboarding := onboardingapplication.NewService(onboardingpostgres.NewRepository(db, repository, identityRepository), identityService, func() (ids.StoreID, error) { return storeID, nil }, func() time.Time { return now })
	result, err := onboarding.Execute(context.Background(), onboardingapplication.Command{Subdomain: "international-store", StoreName: decomposedName, DefaultCurrency: "USD", Locale: "en-US", Timezone: "America/New_York", Email: "international@example.com", Password: "international-password", PlatformActorID: "deployment", RequestID: "international-store-test"})
	if err != nil {
		t.Fatalf("create international store: %v", err)
	}
	store := result.Store
	userID = result.OwnerUserID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_records WHERE store_id = $1::uuid`, storeID.String())
		_, _ = pool.Exec(context.Background(), `DELETE FROM store_memberships WHERE store_id = $1::uuid`, storeID.String())
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE store_id = $1::uuid`, storeID.String())
		_, _ = pool.Exec(context.Background(), `DELETE FROM stores WHERE store_id = $1::uuid`, storeID.String())
	})
	var persistedName, currencyCode, locale, timezone string
	var createdAt time.Time
	if err := pool.QueryRow(context.Background(), `SELECT name, default_currency, locale, timezone, created_at FROM stores WHERE store_id = $1::uuid`, storeID.String()).Scan(&persistedName, &currencyCode, &locale, &timezone, &createdAt); err != nil {
		t.Fatalf("query persisted store: %v", err)
	}
	wantName := norm.NFC.String(decomposedName)
	if persistedName != wantName || !norm.NFC.IsNormalString(persistedName) || currencyCode != "USD" || locale != "en-US" || timezone != "America/New_York" || !createdAt.Equal(now.UTC()) {
		t.Fatalf("persisted store = %q %s %s %s %s", persistedName, currencyCode, locale, timezone, createdAt)
	}
	record, found, err := repository.ResolveTenant(context.Background(), storeID, userID)
	if err != nil || !found || record.StoreName != wantName || record.DefaultCurrency != "USD" || record.Locale != "en-US" || record.Timezone != "America/New_York" {
		t.Fatalf("resolved international context = %#v, found %v, error %v", record, found, err)
	}
	if store.Name() != wantName || !store.CreatedAt().Equal(now.UTC()) {
		t.Fatalf("domain store = %q %s", store.Name(), store.CreatedAt())
	}
}

func TestResolveTenantUsesOneIndexedQuery(t *testing.T) {
	var trace queryCounter
	pool := integrationPool(t, &trace)
	pair := storefixture.NewPair(t, pool)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (store_id,user_id,status,created_at,updated_at)
		SELECT $1::uuid, (substr(md5($1::text || value::text), 1, 8) || '-' || substr(md5($1::text || value::text), 9, 4) || '-4' || substr(md5($1::text || value::text), 14, 3) || '-8' || substr(md5($1::text || value::text), 18, 3) || '-' || substr(md5($1::text || value::text), 21, 12))::uuid, 'active', now(), now()
		FROM generate_series(1, 10000) AS value
		ON CONFLICT DO NOTHING`, pair.A.Store.ID().String())
	if err != nil {
		t.Fatalf("seed representative users: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		SELECT $1::uuid, (substr(md5($1::text || value::text), 1, 8) || '-' || substr(md5($1::text || value::text), 9, 4) || '-4' || substr(md5($1::text || value::text), 14, 3) || '-8' || substr(md5($1::text || value::text), 18, 3) || '-' || substr(md5($1::text || value::text), 21, 12))::uuid, 'active', 'staff', now(), now()
		FROM generate_series(1, 10000) AS value
		ON CONFLICT DO NOTHING`, pair.A.Store.ID().String())
	if err != nil {
		t.Fatalf("seed representative memberships: %v", err)
	}
	trace.count.Store(0)
	_, found, err := storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter()).ResolveTenant(context.Background(), pair.A.Store.ID(), pair.A.UserID)
	if err != nil || !found || trace.count.Load() != 1 {
		t.Fatalf("ResolveTenant() = found %v, error %v, queries %d", found, err, trace.count.Load())
	}
	var planJSON []byte
	err = pool.QueryRow(context.Background(), `
		EXPLAIN (FORMAT JSON)
		SELECT s.store_id::text, m.user_id::text, s.name, s.status, m.status, s.default_currency, s.locale, s.timezone, s.settings_version
		FROM store_memberships AS m
		JOIN stores AS s ON s.store_id = m.store_id
		WHERE m.store_id = $1::uuid AND m.user_id = $2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String()).Scan(&planJSON)
	if err != nil {
		t.Fatalf("explain tenant resolution: %v", err)
	}
	var plan any
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	planText := string(planJSON)
	usesMembershipIndex := strings.Contains(planText, "store_memberships_pkey") || strings.Contains(planText, "store_memberships_user_store_idx")
	if !usesMembershipIndex || !strings.Contains(planText, "Index") {
		t.Fatalf("tenant plan does not use a composite membership index: %s", planText)
	}
}

func TestResolveTenantHonorsCancellation(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	lock, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin lock transaction: %v", err)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	if _, err := lock.Exec(context.Background(), `LOCK TABLE store_memberships IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock memberships: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err = storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter()).ResolveTenant(ctx, pair.A.Store.ID(), pair.A.UserID)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("ResolveTenant() error = %v after %s", err, time.Since(started))
	}
}

func BenchmarkResolveTenantPostgres(b *testing.B) {
	pool := integrationPool(b, nil)
	pair := storefixture.NewPair(b, pool)
	repository := storepostgres.NewRepository(gormtest.FromPool(b, pool), auditpostgres.NewWriter())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, found, err := repository.ResolveTenant(context.Background(), pair.A.Store.ID(), pair.A.UserID)
		if err != nil || !found {
			b.Fatalf("ResolveTenant() = found %v, error %v", found, err)
		}
	}
}

type queryCounter struct {
	count atomic.Int64
}

func (counter *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func integrationPool(tb testing.TB, tracer pgx.QueryTracer) *pgxpool.Pool {
	tb.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		tb.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		tb.Fatalf("parse database URL: %v", err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatalf("test database guard: %v", err)
	}
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatalf("create PostgreSQL pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping PostgreSQL: %v", err)
	}
	return pool
}

func assertExecRejected(tb testing.TB, pool *pgxpool.Pool, statement string, arguments ...any) {
	tb.Helper()
	if _, err := pool.Exec(context.Background(), statement, arguments...); err == nil {
		tb.Fatalf("database accepted invalid statement: %s", statement)
	}
}

func insertTenantUser(tb testing.TB, pool *pgxpool.Pool, storeID ids.StoreID, userID ids.UserID) {
	tb.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (store_id,user_id,status,created_at,updated_at) VALUES ($1::uuid,$2::uuid,'active',now(),now())`, storeID.String(), userID.String()); err != nil {
		tb.Fatalf("insert tenant user: %v", err)
	}
}

var _ application.StoreResolverRepository = (*storepostgres.Repository)(nil)
var _ application.TenantRepository = (*storepostgres.Repository)(nil)
