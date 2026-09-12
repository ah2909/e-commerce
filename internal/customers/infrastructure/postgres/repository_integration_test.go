//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ah2909/e-commerce/internal/customers/application"
	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProfilesAndAddressesAreUserAndStoreScoped(t *testing.T) {
	pool := customerPool(t)
	pair := storefixture.NewPair(t, pool)
	service := application.NewService(NewRepository(gormtest.FromPool(t, pool)))
	profile, err := service.PutProfile(context.Background(), pair.A.Store.ID(), pair.A.UserID, 0, domain.ProfileDraft{FullName: "Nguyen Van A", Email: "A@example.com", Phone: "0901234567"})
	if err != nil || profile.Version != 1 {
		t.Fatalf("profile = %#v, error = %v", profile, err)
	}
	if _, err := service.PutProfile(context.Background(), pair.A.Store.ID(), pair.A.UserID, 0, domain.ProfileDraft{FullName: "Duplicate", Email: "a@example.com", Phone: "0901234567"}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("duplicate profile error = %v", err)
	}
	address, err := service.CreateAddress(context.Background(), pair.A.Store.ID(), pair.A.UserID, domain.AddressDraft{Label: "Home", RecipientName: "Nguyen Van A", Phone: "0901234567", Line1: "1 Nguyen Hue", Ward: "Ben Nghe", District: "District 1", Province: "Ho Chi Minh City", CountryCode: "VN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAddress(context.Background(), pair.B.Store.ID(), pair.B.UserID, address.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store address error = %v", err)
	}
	if _, err := service.GetAddress(context.Background(), pair.A.Store.ID(), pair.B.UserID, address.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user address error = %v", err)
	}
	if _, err := service.GetProfile(context.Background(), pair.B.Store.ID(), pair.A.UserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-store profile error = %v", err)
	}
	if _, err := service.GetProfile(context.Background(), pair.A.Store.ID(), pair.B.UserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user profile error = %v", err)
	}
	if _, err := service.ReplaceAddress(context.Background(), pair.A.Store.ID(), pair.B.UserID, address.ID, 1, domain.AddressDraft{Label: "Other", RecipientName: "Other", Phone: "0901234567", Line1: "3 Le Loi", Ward: "Ben Nghe", District: "District 1", Province: "Ho Chi Minh City", CountryCode: "VN"}); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user replace error = %v", err)
	}
	if err := service.ArchiveAddress(context.Background(), pair.A.Store.ID(), pair.B.UserID, address.ID, 1); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-user archive error = %v", err)
	}
	if _, err := service.CreateAddress(context.Background(), pair.A.Store.ID(), pair.A.UserID, domain.AddressDraft{Label: "Work", RecipientName: "Nguyen Van A", Phone: "0901234567", Line1: "2 Le Loi", Ward: "Ben Nghe", District: "District 1", Province: "Ho Chi Minh City", CountryCode: "VN"}); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListAddresses(context.Background(), pair.A.Store.ID(), pair.A.UserID, 1, "")
	if err != nil || page.NextCursor == "" {
		t.Fatalf("page = %#v, error = %v", page, err)
	}
	if _, err := service.ListAddresses(context.Background(), pair.B.Store.ID(), pair.B.UserID, 1, page.NextCursor); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign cursor error = %v", err)
	}
	if err := service.ArchiveAddress(context.Background(), pair.A.Store.ID(), pair.A.UserID, address.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAddress(context.Background(), pair.A.Store.ID(), pair.A.UserID, address.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("archived address error = %v", err)
	}
}

func customerPool(t testing.TB) *pgxpool.Pool {
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
