//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
)

func TestHomepageSnapshotsConcurrencyAndTenantAudit(t *testing.T) {
	pool := integrationPool(t, nil)
	pair := storefixture.NewPair(t, pool)
	ctx := context.Background()
	store, actor := pair.A.Store.ID(), pair.A.UserID
	other := pair.B.Store.ID()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM store_homepages WHERE store_id IN ($1,$2)", store.String(), other.String()); err != nil {
			t.Error(err)
		}
	})
	db := gormtest.FromPool(t, pool)
	service := application.NewHomepageService(storepostgres.NewRepository(db, auditpostgres.NewWriter()))
	defaultLayout := domain.DefaultHomepage()
	layout := domain.Homepage{SchemaVersion: 1, Mode: "custom", Sections: []domain.HomepageSection{{ID: "hero", Type: "hero", Layout: new("stacked")}}}
	nextLayout := domain.Homepage{SchemaVersion: 1, Mode: "custom", Sections: []domain.HomepageSection{{ID: "hero", Type: "hero", Layout: new("split")}}}

	assertPublic := func(want domain.Homepage) {
		t.Helper()
		got, err := service.Published(ctx, store)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("public layout: %+v, %v", got, err)
		}
	}
	state, err := service.Get(ctx, store)
	if err != nil || !reflect.DeepEqual(state, domain.HomepageState{Draft: defaultLayout, Published: defaultLayout}) {
		t.Fatalf("initial state: %+v, %v", state, err)
	}
	if _, err := service.Publish(ctx, store, actor, 0, "missing-draft"); !errors.Is(err, application.ErrHomepageConflict) {
		t.Fatalf("publish without saved draft: %v", err)
	}
	if _, err := service.SaveDraft(ctx, other, actor, layout, 0, "cross-store-save"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("cross-store draft: %v", err)
	}
	if _, err := service.Publish(ctx, other, actor, 0, "cross-store-publish"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("cross-store publish: %v", err)
	}

	firstDraft := func() error {
		_, err := service.SaveDraft(ctx, store, actor, layout, 0, "first-draft")
		return err
	}
	homepageRace(t, firstDraft, firstDraft)
	assertPublic(defaultLayout)
	state, err = service.Get(ctx, store)
	if err != nil || state.Version != 1 || !reflect.DeepEqual(state.Draft, layout) {
		t.Fatalf("saved draft: %+v, %v", state, err)
	}
	broken := application.NewHomepageService(storepostgres.NewRepository(db, nil))
	if _, err := broken.Publish(ctx, store, actor, 1, "unavailable-audit"); err == nil {
		t.Fatal("publish succeeded without audit")
	}
	assertPublic(defaultLayout)
	if _, err := pool.Exec(ctx, "UPDATE store_memberships SET role='staff' WHERE store_id=$1 AND user_id=$2", store.String(), actor.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, store, actor, 1, "staff-publish"); !errors.Is(err, application.ErrTenantAccessDenied) {
		t.Fatalf("permission recheck: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE store_memberships SET role='admin' WHERE store_id=$1 AND user_id=$2", store.String(), actor.String()); err != nil {
		t.Fatal(err)
	}
	published := homepageRace(t, func() error {
		_, err := service.Publish(ctx, store, actor, 1, "racing-publish")
		return err
	}, func() error {
		_, err := service.SaveDraft(ctx, store, actor, nextLayout, 1, "racing-draft")
		return err
	})
	if published {
		assertPublic(layout)
	} else {
		assertPublic(defaultLayout)
		if _, err := service.Publish(ctx, store, actor, 2, "newer-draft"); err != nil {
			t.Fatal(err)
		}
		assertPublic(nextLayout)
	}
	if otherPublic, err := service.Published(ctx, other); err != nil || !reflect.DeepEqual(otherPublic, defaultLayout) {
		t.Fatalf("other tenant: %+v, %v", otherPublic, err)
	}
	current, err := service.Get(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveDraft(ctx, store, actor, defaultLayout, current.Version, "restore-default-draft")
	if err != nil {
		t.Fatal(err)
	}
	assertPublic(current.Published)
	if _, err := service.Publish(ctx, store, actor, saved.Version, "restore-default-publish"); err != nil {
		t.Fatal(err)
	}
	assertPublic(defaultLayout)
	var total, valid int
	err = pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE actor_id=$2 AND subject_id=$1)
		FROM audit_records WHERE store_id=$1 AND action IN ($3,$4)`, store.String(), actor.String(), auditdomain.StoreHomepageDraftSaved, auditdomain.StoreHomepagePublished).Scan(&total, &valid)
	if err != nil || total != valid || int64(total) != saved.Version+1 {
		t.Fatalf("audit entries: %d total, %d matching actor and subject, version %d, %v", total, valid, saved.Version+1, err)
	}
}

func homepageRace(t *testing.T, first, second func() error) bool {
	t.Helper()
	var results [2]error
	var ready, done sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for i, run := range []func() error{first, second} {
		done.Go(func() {
			ready.Done()
			<-start
			results[i] = run()
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	if results[0] == nil && errors.Is(results[1], application.ErrHomepageConflict) {
		return true
	}
	if results[1] == nil && errors.Is(results[0], application.ErrHomepageConflict) {
		return false
	}
	t.Fatalf("expected one success and one conflict, got %v", results)
	return false
}
