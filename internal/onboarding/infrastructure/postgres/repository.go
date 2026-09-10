package postgres

import (
	"context"
	"errors"
	"fmt"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"gorm.io/gorm"
)

type Repository struct {
	db       *gorm.DB
	stores   StoreWriter
	identity IdentityWriter
}

type StoreWriter interface {
	InsertStore(context.Context, *gorm.DB, storesdomain.Store) error
	InsertMembership(context.Context, *gorm.DB, storesdomain.Membership) error
	InsertStoreCreatedAudit(context.Context, *gorm.DB, storesdomain.Store, string, string) error
}

type IdentityWriter interface {
	InsertAccount(context.Context, *gorm.DB, identityapplication.PreparedAccount) error
}

func NewRepository(db *gorm.DB, stores StoreWriter, identity IdentityWriter) *Repository {
	return &Repository{db: db, stores: stores, identity: identity}
}

func (repository *Repository) Create(ctx context.Context, store storesdomain.Store, membership storesdomain.Membership, account identityapplication.PreparedAccount, platformActorID, requestID string) error {
	if store.ID() != membership.StoreID() || store.ID() != account.User.StoreID || membership.UserID() != account.User.ID || platformActorID == "" {
		return errors.New("onboarding scope mismatch")
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
	if sqlState(err) == "23505" {
		return onboardingapplication.ErrOnboardingConflict
	}
	if err != nil {
		return fmt.Errorf("onboarding transaction: %w", err)
	}
	return nil
}

type stateError interface{ SQLState() string }

func sqlState(err error) string {
	var state stateError
	if errors.As(err, &state) {
		return state.SQLState()
	}
	return ""
}
