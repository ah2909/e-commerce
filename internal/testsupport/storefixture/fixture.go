//go:build integration

package storefixture

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	storepostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Store  domain.Store
	UserID domain.UserID
}

type Pair struct {
	A Store
	B Store
}

func NewPair(tb testing.TB, pool *pgxpool.Pool) Pair {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	tb.Cleanup(cancel)
	repository := storepostgres.NewRepository(pool)
	now := time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC)
	var created []Store
	tb.Cleanup(func() {
		cleanupStores(tb, pool, created)
	})
	pair, fixtures, err := buildPair(func() (Store, error) {
		return createStore(ctx, repository, "Cửa hàng Ánh Dương", now)
	})
	created = fixtures
	if err != nil {
		tb.Fatalf("create store pair: %v", err)
	}
	return pair
}

func buildPair(create func() (Store, error)) (Pair, []Store, error) {
	created := make([]Store, 0, 2)
	a, err := create()
	if err != nil {
		return Pair{}, created, err
	}
	created = append(created, a)
	b, err := create()
	if err != nil {
		return Pair{A: a}, created, err
	}
	created = append(created, b)
	return Pair{A: a, B: b}, created, nil
}

func createStore(ctx context.Context, repository application.Repository, name string, now time.Time) (Store, error) {
	storeID, err := domain.NewStoreID()
	if err != nil {
		return Store{}, fmt.Errorf("generate store ID: %w", err)
	}
	userID, err := domain.NewUserID()
	if err != nil {
		return Store{}, fmt.Errorf("generate user ID: %w", err)
	}
	useCase := application.NewCreateStore(repository, func() (domain.StoreID, error) { return storeID, nil }, func() time.Time { return now })
	store, err := useCase.Execute(ctx, application.CreateStoreCommand{CreatorID: userID, Name: name})
	if err != nil {
		return Store{}, fmt.Errorf("create fixture store: %w", err)
	}
	return Store{Store: store, UserID: userID}, nil
}

func cleanupStores(tb testing.TB, pool *pgxpool.Pool, created []Store) {
	tb.Helper()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()
	for _, fixture := range created {
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM store_memberships WHERE store_id = $1::uuid`, fixture.Store.ID().String()); err != nil {
			tb.Errorf("delete fixture memberships: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM stores WHERE store_id = $1::uuid`, fixture.Store.ID().String()); err != nil {
			tb.Errorf("delete fixture store: %v", err)
		}
	}
}
