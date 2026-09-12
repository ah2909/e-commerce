//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/application"
	"github.com/ah2909/e-commerce/internal/carts/domain"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCartCreateMutationAndTenantIsolation(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	variant := insertCartVariant(t, pool, pair.A.Store.ID())
	firstID, _ := ids.NewCartID()
	cart, created, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, firstID)
	if err != nil || !created || cart.Version != 1 || len(cart.Lines) != 0 {
		t.Fatalf("cart = %#v, created = %v, error = %v", cart, created, err)
	}
	retryID, _ := ids.NewCartID()
	retry, created, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, retryID)
	if err != nil || created || retry.ID != cart.ID {
		t.Fatalf("retry = %#v, created = %v, error = %v", retry, created, err)
	}
	updated, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, 2, 1)
	if err != nil || updated.Version != 2 || len(updated.Lines) != 1 {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, 3, 1); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale mutation error = %v", err)
	}
	if _, err := repository.Get(context.Background(), pair.B.Store.ID(), owner); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store cart error = %v", err)
	}
	if _, err := repository.Get(context.Background(), pair.A.Store.ID(), application.OwnerFromUser(pair.B.UserID)); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user cart error = %v", err)
	}
	foreignOwner := application.OwnerFromUser(pair.B.UserID)
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), foreignOwner, variant, 1, 2); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user line mutation error = %v", err)
	}
	if _, err := repository.RemoveLine(context.Background(), pair.A.Store.ID(), foreignOwner, variant, 2); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user line removal error = %v", err)
	}
	if err := repository.Abandon(context.Background(), pair.A.Store.ID(), foreignOwner, 2); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user abandonment error = %v", err)
	}
	foreignVariant := insertCartVariant(t, pool, pair.B.Store.ID())
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, foreignVariant, 1, 2); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("foreign variant mutation error = %v", err)
	}
	if err := repository.Abandon(context.Background(), pair.A.Store.ID(), owner, 2); err != nil {
		t.Fatal(err)
	}
	newID, _ := ids.NewCartID()
	next, created, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, newID)
	if err != nil || !created || next.ID == cart.ID {
		t.Fatalf("next = %#v, created = %v, error = %v", next, created, err)
	}
}

func TestConcurrentCartCreationConverges(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	start := make(chan struct{})
	carts := make([]domain.Cart, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wait.Done()
			id, _ := ids.NewCartID()
			<-start
			carts[i], _, errs[i] = repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
		}(i)
	}
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || carts[0].ID != carts[1].ID {
		t.Fatalf("carts = %#v, errors = %#v", carts, errs)
	}
}

func TestConcurrentCartMutationsSerializeAndRefreshExpiry(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	firstVariant := insertCartVariant(t, pool, pair.A.Store.ID())
	secondVariant := insertCartVariant(t, pool, pair.A.Store.ID())
	id, _ := ids.NewCartID()
	cart, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make([]domain.Cart, 2)
	errs := make([]error, 2)
	variants := []ids.VariantID{firstVariant, secondVariant}
	var wait sync.WaitGroup
	wait.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wait.Done()
			<-start
			results[i], errs[i] = repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variants[i], 1, 1)
		}(i)
	}
	close(start)
	wait.Wait()
	succeeded, conflicted := 0, 0
	for i := range errs {
		if errs[i] == nil {
			succeeded++
			if results[i].Version != 2 || !results[i].ExpiresAt.After(cart.ExpiresAt) {
				t.Fatalf("result = %#v", results[i])
			}
		} else if errors.Is(errs[i], application.ErrConflict) {
			conflicted++
		} else {
			t.Fatalf("mutation error = %v", errs[i])
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded = %d, conflicted = %d", succeeded, conflicted)
	}
}

func TestCartMutationRollsBackWhenCartTouchFails(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	variant := insertCartVariant(t, pool, pair.A.Store.ID())
	id, _ := ids.NewCartID()
	cart, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE carts SET created_at=clock_timestamp()+interval '1 day', updated_at=clock_timestamp()+interval '1 day', expires_at=clock_timestamp()+interval '2 days' WHERE store_id=$1::uuid AND cart_id=$2::uuid`, pair.A.Store.ID(), cart.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, 1, 1); err == nil {
		t.Fatal("mutation succeeded despite failing cart touch")
	}
	var version, lines int64
	if err := pool.QueryRow(context.Background(), `SELECT c.version, count(l.variant_id) FROM carts c LEFT JOIN cart_lines l ON l.store_id=c.store_id AND l.cart_id=c.cart_id WHERE c.store_id=$1::uuid AND c.cart_id=$2::uuid GROUP BY c.version`, pair.A.Store.ID(), cart.ID).Scan(&version, &lines); err != nil {
		t.Fatal(err)
	}
	if version != 1 || lines != 0 {
		t.Fatalf("version = %d, lines = %d", version, lines)
	}
}

func TestCartReadsUseOneCommittedSnapshot(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	variant := insertCartVariant(t, pool, pair.A.Store.ID())
	id, _ := ids.NewCartID()
	if _, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, 1, 1); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for expected := int64(2); expected < 30; expected++ {
			if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, expected, expected); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for {
		cart, err := repository.Get(context.Background(), pair.A.Store.ID(), owner)
		if err != nil {
			t.Fatal(err)
		}
		if len(cart.Lines) != 1 || cart.Lines[0].Quantity != cart.Version-1 {
			t.Fatalf("torn cart = %#v", cart)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}

func TestCartExpiryIsIdempotent(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	id, _ := ids.NewCartID()
	cart, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE carts SET created_at=clock_timestamp()-interval '31 days', expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1::uuid AND cart_id=$2::uuid`, pair.A.Store.ID(), cart.ID); err != nil {
		t.Fatal(err)
	}
	count, err := repository.ExpireDue(context.Background(), 10)
	if err != nil || count != 1 {
		t.Fatalf("count = %d, error = %v", count, err)
	}
	count, err = repository.ExpireDue(context.Background(), 10)
	if err != nil || count != 0 {
		t.Fatalf("retry count = %d, error = %v", count, err)
	}
	if _, err := repository.Get(context.Background(), pair.A.Store.ID(), owner); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired cart error = %v", err)
	}
}

func TestConcurrentExpiryAndMutationTransitionOnce(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	variant := insertCartVariant(t, pool, pair.A.Store.ID())
	id, _ := ids.NewCartID()
	cart, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE carts SET created_at=clock_timestamp()-interval '31 days', expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1::uuid AND cart_id=$2::uuid`, pair.A.Store.ID(), cart.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	counts := make([]int, 2)
	errs := make([]error, 3)
	var wait sync.WaitGroup
	wait.Add(3)
	for i := range 2 {
		go func(i int) {
			defer wait.Done()
			<-start
			counts[i], errs[i] = repository.ExpireDue(context.Background(), 10)
		}(i)
	}
	go func() {
		defer wait.Done()
		<-start
		_, errs[2] = repository.SetLine(context.Background(), pair.A.Store.ID(), owner, variant, 1, 1)
	}()
	close(start)
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || (!errors.Is(errs[2], application.ErrConflict) && !errors.Is(errs[2], application.ErrNotFound)) || counts[0]+counts[1] != 1 {
		t.Fatalf("counts = %#v, errors = %#v", counts, errs)
	}
	var status string
	var version, lines int64
	if err := pool.QueryRow(context.Background(), `SELECT c.status, c.version, count(l.variant_id) FROM carts c LEFT JOIN cart_lines l ON l.store_id=c.store_id AND l.cart_id=c.cart_id WHERE c.store_id=$1::uuid AND c.cart_id=$2::uuid GROUP BY c.status,c.version`, pair.A.Store.ID(), cart.ID).Scan(&status, &version, &lines); err != nil {
		t.Fatal(err)
	}
	if status != "expired" || version != 2 || lines != 0 {
		t.Fatalf("status = %s, version = %d, lines = %d", status, version, lines)
	}
}

func TestCartLineLimitIsEnforcedWithoutVersionChange(t *testing.T) {
	pool := cartPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := NewRepository(gormtest.FromPool(t, pool))
	owner := application.OwnerFromUser(pair.A.UserID)
	id, _ := ids.NewCartID()
	cart, _, err := repository.CreateOrGet(context.Background(), pair.A.Store.ID(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	product, _ := ids.NewProductID()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,published_at,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,'Bounds','bounds-'||$2::text,'','published',$3,1,$3,$3)`, pair.A.Store.ID(), product, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `WITH variants AS (INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) SELECT $1::uuid,gen_random_uuid(),$2::uuid,'BOUND-'||g::text,'{}',0,'active',1,$3,$3 FROM generate_series(1,$4) g RETURNING variant_id) INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) SELECT $1::uuid,$5::uuid,variant_id,1,$3,$3 FROM variants`, pair.A.Store.ID(), product, now, domain.MaxLines, cart.ID); err != nil {
		t.Fatal(err)
	}
	extra := insertCartVariant(t, pool, pair.A.Store.ID())
	if _, err := repository.SetLine(context.Background(), pair.A.Store.ID(), owner, extra, 1, 1); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("line limit error = %v", err)
	}
	current, err := repository.Get(context.Background(), pair.A.Store.ID(), owner)
	if err != nil || current.Version != 1 || len(current.Lines) != domain.MaxLines {
		t.Fatalf("cart = %#v, error = %v", current, err)
	}
}

func insertCartVariant(t testing.TB, pool *pgxpool.Pool, store ids.StoreID) ids.VariantID {
	t.Helper()
	product, _ := ids.NewProductID()
	variant, _ := ids.NewVariantID()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,published_at,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,'Cart','cart-'||$2::text,'','published',$3,1,$3,$3)`, store, product, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'{}',0,'active',1,$5,$5)`, store, variant, product, "CART-"+strings.ToUpper(variant.String()), now); err != nil {
		t.Fatal(err)
	}
	return variant
}
func cartPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(config.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}
