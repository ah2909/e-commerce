//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"
)

func TestRepositoryTenantIsolationAndVietnameseRoundTrip(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	repository := storepostgres.NewRepository(pool)
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
		storeID domain.StoreID
		userID  domain.UserID
	}{{pair.B.Store.ID(), pair.A.UserID}, {pair.A.Store.ID(), pair.B.UserID}} {
		_, found, err := repository.ResolveTenant(ctx, attempt.storeID, attempt.userID)
		if err != nil || found {
			t.Fatalf("cross-store ResolveTenant(%s, %s) = found %v, error %v", attempt.storeID, attempt.userID, found, err)
		}
	}
}

func TestRepositoryAllowsOneUserInMultipleStores(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	now := time.Now().UTC()
	membership, err := domain.NewMembership(pair.B.Store.ID(), pair.A.UserID, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
		membership.StoreID().String(), membership.UserID().String(), membership.Status(), membership.CreatedAt(), membership.UpdatedAt())
	if err != nil {
		t.Fatalf("insert second membership: %v", err)
	}
	_, found, err := storepostgres.NewRepository(pool).ResolveTenant(context.Background(), pair.B.Store.ID(), pair.A.UserID)
	if err != nil || !found {
		t.Fatalf("ResolveTenant(second membership) = found %v, error %v", found, err)
	}
}

func TestRepositoryCreationRollsBackWhenMembershipInsertFails(t *testing.T) {
	pool := integrationPool(t, nil)
	storeID, _ := domain.NewStoreID()
	userID, _ := domain.ParseUserID("138f0f7d-7b3d-4d2e-8f4f-3b86f31f2741")
	now := time.Now().UTC()
	store, _ := domain.NewStore(storeID, "Cửa hàng rollback", "VND", "vi-VN", "Asia/Ho_Chi_Minh", now)
	membership, _ := domain.NewMembership(storeID, userID, now)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS store_membership_rollback_test_trigger ON store_memberships`)
	_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS store_membership_rollback_test()`)
	_, err := pool.Exec(ctx, `
		CREATE FUNCTION store_membership_rollback_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.user_id = '138f0f7d-7b3d-4d2e-8f4f-3b86f31f2741'::uuid THEN RAISE EXCEPTION 'fixture rejection'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER store_membership_rollback_test_trigger BEFORE INSERT ON store_memberships FOR EACH ROW EXECUTE FUNCTION store_membership_rollback_test()`)
	if err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS store_membership_rollback_test_trigger ON store_memberships`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS store_membership_rollback_test()`)
	})

	err = storepostgres.NewRepository(pool).CreateWithMembership(ctx, store, membership)
	if err == nil {
		t.Fatal("CreateWithMembership() error = nil")
	}
	var count int
	if queryErr := pool.QueryRow(ctx, `SELECT count(*) FROM stores WHERE store_id = $1::uuid`, storeID.String()).Scan(&count); queryErr != nil || count != 0 {
		t.Fatalf("store count after rollback = %d, error %v", count, queryErr)
	}
}

func TestRepositoryDatabaseConstraints(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'owner', now(), now())`, pair.A.Store.ID().String(), pair.B.UserID.String())
	missingStoreID, _ := domain.NewStoreID()
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'active', now(), now())`, missingStoreID.String(), pair.B.UserID.String())
	assertExecRejected(t, pool, `
		INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, 'active', now(), now())`, pair.A.Store.ID().String(), pair.A.UserID.String())
	assertExecRejected(t, pool, `UPDATE stores SET status = 'deleted' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET name = ' ' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET settings_version = 0 WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE stores SET updated_at = created_at - interval '1 second' WHERE store_id = $1::uuid`, pair.A.Store.ID().String())
	assertExecRejected(t, pool, `UPDATE store_memberships SET updated_at = created_at - interval '1 second' WHERE store_id = $1::uuid AND user_id = $2::uuid`, pair.A.Store.ID().String(), pair.A.UserID.String())
}

func TestRepositoryRoundTripsInternationalSettingsUnicodeAndUTC(t *testing.T) {
	pool := integrationPool(t, nil)
	repository := storepostgres.NewRepository(pool)
	storeID, _ := domain.NewStoreID()
	userID, _ := domain.NewUserID()
	now := time.Date(2026, 9, 7, 20, 0, 0, 123456000, time.FixedZone("ICT", 7*60*60))
	decomposedName := "Cửa hàng A\u0301nh Dương"
	useCase := application.NewCreateStore(repository, func() (domain.StoreID, error) { return storeID, nil }, func() time.Time { return now })
	store, err := useCase.Execute(context.Background(), application.CreateStoreCommand{
		CreatorID: userID, Name: decomposedName, DefaultCurrency: "USD", Locale: "en-US", Timezone: "America/New_York",
	})
	if err != nil {
		t.Fatalf("create international store: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM store_memberships WHERE store_id = $1::uuid`, storeID.String())
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
		INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
		SELECT $1::uuid, (substr(md5(value::text), 1, 8) || '-' || substr(md5(value::text), 9, 4) || '-4' || substr(md5(value::text), 14, 3) || '-8' || substr(md5(value::text), 18, 3) || '-' || substr(md5(value::text), 21, 12))::uuid, 'active', now(), now()
		FROM generate_series(1, 10000) AS value
		ON CONFLICT DO NOTHING`, pair.A.Store.ID().String())
	if err != nil {
		t.Fatalf("seed representative memberships: %v", err)
	}
	trace.count.Store(0)
	_, found, err := storepostgres.NewRepository(pool).ResolveTenant(context.Background(), pair.A.Store.ID(), pair.A.UserID)
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
	_, _, err = storepostgres.NewRepository(pool).ResolveTenant(ctx, pair.A.Store.ID(), pair.A.UserID)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("ResolveTenant() error = %v after %s", err, time.Since(started))
	}
}

func BenchmarkResolveTenantPostgres(b *testing.B) {
	pool := integrationPool(b, nil)
	pair := storefixture.NewPair(b, pool)
	repository := storepostgres.NewRepository(pool)
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

var _ application.Repository = (*storepostgres.Repository)(nil)
