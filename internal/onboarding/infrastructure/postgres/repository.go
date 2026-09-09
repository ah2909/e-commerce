package postgres

import (
	"context"
	"errors"
	"fmt"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool     *pgxpool.Pool
	stores   StoreWriter
	identity IdentityWriter
}

type StoreWriter interface {
	InsertStore(context.Context, pgx.Tx, storesdomain.Store) error
	InsertMembership(context.Context, pgx.Tx, storesdomain.Membership) error
	InsertStoreCreatedAudit(context.Context, pgx.Tx, storesdomain.Store, string, string) error
}

type IdentityWriter interface {
	InsertAccount(context.Context, pgx.Tx, identityapplication.PreparedAccount) error
}

func NewRepository(pool *pgxpool.Pool, stores StoreWriter, identity IdentityWriter) *Repository {
	return &Repository{pool: pool, stores: stores, identity: identity}
}

func (repository *Repository) Create(ctx context.Context, store storesdomain.Store, membership storesdomain.Membership, account identityapplication.PreparedAccount, platformActorID, requestID string) error {
	if store.ID() != membership.StoreID() || store.ID() != account.User.StoreID || membership.UserID() != account.User.ID || platformActorID == "" {
		return errors.New("onboarding scope mismatch")
	}
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		if err := repository.stores.InsertStore(ctx, tx, store); err != nil {
			return err
		}
		if err := repository.identity.InsertAccount(ctx, tx, account); err != nil {
			return err
		}
		if err := repository.stores.InsertMembership(ctx, tx, membership); err != nil {
			return err
		}
		return repository.stores.InsertStoreCreatedAudit(ctx, tx, store, platformActorID, requestID)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return onboardingapplication.ErrOnboardingConflict
	}
	if err != nil {
		return fmt.Errorf("onboarding transaction: %w", err)
	}
	return nil
}
