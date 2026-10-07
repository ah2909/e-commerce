//go:build integration

package postgres_test

import (
	"context"
	"errors"
	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/stores/application"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"sync"
	"testing"
)

func TestAppearanceIsolationConcurrencyAndAudit(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM store_appearances WHERE store_id IN ($1,$2)", pair.A.Store.ID().String(), pair.B.Store.ID().String()); err != nil {
			t.Error(err)
		}
	})
	repo := storepostgres.NewRepository(gormtest.FromPool(t, pool), auditpostgres.NewWriter())
	service := application.NewAppearanceService(repo)
	a, err := service.Get(ctx, pair.A.Store.ID())
	if err != nil || a.Version != 0 || a.DisplayName != pair.A.Store.Name() {
		t.Fatalf("defaults: %+v %v", a, err)
	}
	a.DisplayName = "Independent shop"
	if _, err = service.Save(ctx, pair.B.Store.ID(), pair.A.UserID, a, "cross-store"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("cross-store write: %v", err)
	}
	saved, err := service.Save(ctx, pair.A.Store.ID(), pair.A.UserID, a, "appearance-first")
	if err != nil || saved.Version != 1 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	b, err := service.Get(ctx, pair.B.Store.ID())
	if err != nil || b.Version != 0 || b.DisplayName == saved.DisplayName {
		t.Fatalf("cross-store read: %+v %v", b, err)
	}
	if _, err = service.Save(ctx, pair.A.Store.ID(), pair.A.UserID, a, "stale"); !errors.Is(err, application.ErrAppearanceConflict) {
		t.Fatalf("stale write: %v", err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := service.Save(ctx, pair.A.Store.ID(), pair.A.UserID, saved, "concurrent")
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, application.ErrAppearanceConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes %d conflicts %d", successes, conflicts)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_records WHERE store_id=$1 AND action='store.appearance_updated'", pair.A.Store.ID().String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("audit count %d", count)
	}
	current, err := service.Get(ctx, pair.A.Store.ID())
	if err != nil {
		t.Fatal(err)
	}
	broken := application.NewAppearanceService(storepostgres.NewRepository(gormtest.FromPool(t, pool), nil))
	if _, err := broken.Save(ctx, pair.A.Store.ID(), pair.A.UserID, current, "audit-failure"); err == nil {
		t.Fatal("missing audit accepted")
	}
	after, err := service.Get(ctx, pair.A.Store.ID())
	if err != nil || after.Version != current.Version {
		t.Fatalf("audit rollback: %+v %v", after, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE store_memberships SET role='staff' WHERE store_id=$1 AND user_id=$2", pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, pair.A.Store.ID(), pair.A.UserID, current, "staff-write"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("staff write: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE store_memberships SET role='admin' WHERE store_id=$1 AND user_id=$2", pair.A.Store.ID().String(), pair.A.UserID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(ctx, pair.A.Store.ID(), pair.A.UserID, current, "admin-write"); err != nil {
		t.Fatalf("admin write: %v", err)
	}
}
