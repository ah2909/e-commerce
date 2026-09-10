//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitypassword "github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	onboardingpostgres "github.com/ah2909/e-commerce/internal/onboarding/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

func TestOnboardingAtomicallyCreatesTenantOwnerWithoutSession(t *testing.T) {
	pool := integrationPool(t)
	service := onboardingService(t, pool, auditpostgres.NewWriter())
	subdomain := uniqueSubdomain(t, "atomic")
	result, err := service.Execute(context.Background(), onboardingapplication.Command{Subdomain: subdomain, StoreName: "Reusable Store", Email: "owner@example.com", Password: "correct-horse-battery-staple", PlatformActorID: "deployment", RequestID: "onboarding-atomic"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupStore(t, pool, result.Store.ID().String()) })
	var users, owners, sessions, audits int
	err = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM users WHERE store_id=$1::uuid),(SELECT count(*) FROM store_memberships WHERE store_id=$1::uuid AND role='owner' AND status='active'),(SELECT count(*) FROM identity_sessions WHERE store_id=$1::uuid),(SELECT count(*) FROM audit_records WHERE store_id=$1::uuid AND action='store.created' AND actor_type='platform_admin' AND actor_id='deployment')`, result.Store.ID().String()).Scan(&users, &owners, &sessions, &audits)
	if err != nil || users != 1 || owners != 1 || sessions != 0 || audits != 1 || result.OwnerUserID == "" {
		t.Fatalf("counts = %d/%d/%d/%d, owner=%v, error=%v", users, owners, sessions, audits, result.OwnerUserID, err)
	}
}

func TestIdentityMigrationHasExplicitRolesAndTypedAuditActors(t *testing.T) {
	pool := integrationPool(t)
	var roleDefault *string
	if err := pool.QueryRow(context.Background(), `SELECT column_default FROM information_schema.columns WHERE table_schema='public' AND table_name='store_memberships' AND column_name='role'`).Scan(&roleDefault); err != nil {
		t.Fatal(err)
	}
	if roleDefault != nil {
		t.Fatalf("store membership role default = %q", *roleDefault)
	}
	for _, column := range []string{"actor_type", "actor_id"} {
		var exists bool
		if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='audit_records' AND column_name=$1)`, column).Scan(&exists); err != nil || !exists {
			t.Fatalf("audit column %s exists = %v, error = %v", column, exists, err)
		}
	}
}

func TestIdentityMigrationUpgradesVersionOneMemberships(t *testing.T) {
	pool := integrationPool(t)
	suffix, _ := ids.NewStoreID()
	schema := "migration_upgrade_" + strings.ReplaceAll(suffix.String(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })

	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	connection := config.ConnConfig.Copy()
	connection.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*connection)
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := goose.UpToContext(ctx, database, migrationDir, 1); err != nil {
		t.Fatal(err)
	}
	storeID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	userID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if _, err := database.ExecContext(ctx, `INSERT INTO stores (store_id,subdomain,name,status,default_currency,locale,timezone,created_at,updated_at) VALUES ($1::uuid,'upgrade-store','Store','active','VND','vi-VN','Asia/Ho_Chi_Minh',now(),now())`, storeID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO store_memberships (store_id,user_id,status,created_at,updated_at) VALUES ($1::uuid,$2::uuid,'active',now(),now())`, storeID, userID); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, database, migrationDir, 2); err != nil {
		t.Fatal(err)
	}
	var role string
	var userCount int
	if err := database.QueryRowContext(ctx, `SELECT m.role,(SELECT count(*) FROM users u WHERE u.store_id=m.store_id AND u.user_id=m.user_id) FROM store_memberships m WHERE m.store_id=$1::uuid AND m.user_id=$2::uuid`, storeID, userID).Scan(&role, &userCount); err != nil || role != "owner" || userCount != 1 {
		t.Fatalf("upgraded membership = role %q, users %d, error %v", role, userCount, err)
	}
}

func TestConcurrentOnboardingReservesSubdomainOnce(t *testing.T) {
	pool := integrationPool(t)
	service := onboardingService(t, pool, auditpostgres.NewWriter())
	subdomain := uniqueSubdomain(t, "concurrent")
	start := make(chan struct{})
	results := make([]onboardingapplication.Result, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] = service.Execute(context.Background(), onboardingapplication.Command{Subdomain: subdomain, StoreName: "Concurrent Store", Email: fmt.Sprintf("owner-%d@example.com", index), Password: "correct-horse-battery-staple", PlatformActorID: "deployment", RequestID: fmt.Sprintf("concurrent-%d", index)})
		}(index)
	}
	close(start)
	wait.Wait()
	var successes, conflicts int
	for index, err := range errs {
		if err == nil {
			successes++
			t.Cleanup(func() { cleanupStore(t, pool, results[index].Store.ID().String()) })
		} else if errors.Is(err, onboardingapplication.ErrOnboardingConflict) {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("onboarding outcomes = %v", errs)
	}
}

func TestOnboardingRollsBackEveryModuleWhenAuditFails(t *testing.T) {
	pool := integrationPool(t)
	service := onboardingService(t, pool, failingAudit{})
	subdomain := uniqueSubdomain(t, "rollback")
	if _, err := service.Execute(context.Background(), onboardingapplication.Command{Subdomain: subdomain, StoreName: "Rollback Store", Email: "rollback@example.com", Password: "correct-horse-battery-staple", PlatformActorID: "deployment", RequestID: "rollback"}); err == nil {
		t.Fatal("onboarding succeeded")
	}
	var stores, users int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM stores WHERE subdomain=$1),(SELECT count(*) FROM users u JOIN stores s ON s.store_id=u.store_id WHERE s.subdomain=$1)`, subdomain).Scan(&stores, &users); err != nil || stores != 0 || users != 0 {
		t.Fatalf("rollback counts = %d/%d, error=%v", stores, users, err)
	}
}

type failingAudit struct{}

func (failingAudit) Insert(context.Context, *gorm.DB, auditdomain.Record) error {
	return errors.New("forced audit failure")
}

func onboardingService(tb testing.TB, pool *pgxpool.Pool, audit storespostgres.AuditWriter) onboardingapplication.Service {
	tb.Helper()
	db := gormtest.FromPool(tb, pool)
	stores := storespostgres.NewRepository(db, audit)
	identity := identitypostgres.NewRepository(db)
	hasher, err := identitypassword.NewArgon2ID(4)
	if err != nil {
		tb.Fatal(err)
	}
	identityService := identityapplication.NewService(identity, hasher, stores, token.Generator{}, identityapplication.DefaultSessionSettings(), time.Now)
	return onboardingapplication.NewService(onboardingpostgres.NewRepository(db, stores, identity), identityService, ids.NewStoreID, time.Now)
}

func uniqueSubdomain(tb testing.TB, prefix string) string {
	tb.Helper()
	id, err := ids.NewStoreID()
	if err != nil {
		tb.Fatal(err)
	}
	return prefix + "-" + id.String()
}

func cleanupStore(tb testing.TB, pool *pgxpool.Pool, storeID string) {
	tb.Helper()
	for _, statement := range []string{`DELETE FROM audit_records WHERE store_id=$1::uuid`, `DELETE FROM store_memberships WHERE store_id=$1::uuid`, `DELETE FROM users WHERE store_id=$1::uuid`, `DELETE FROM stores WHERE store_id=$1::uuid`} {
		if _, err := pool.Exec(context.Background(), statement, storeID); err != nil {
			tb.Errorf("cleanup onboarding store: %v", err)
		}
	}
}

func integrationPool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		tb.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		tb.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	return pool
}
