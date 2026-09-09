package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrOnboardingConflict = errors.New("onboarding conflict")

type Repository interface {
	Create(context.Context, storesdomain.Store, storesdomain.Membership, identityapplication.PreparedAccount, string, string) error
}

type IdentityPreparer interface {
	PrepareAccount(context.Context, ids.StoreID, string, string) (identityapplication.PreparedAccount, error)
}

type Command struct {
	Subdomain       string
	StoreName       string
	DefaultCurrency string
	Locale          string
	Timezone        string
	Email           string
	Password        string
	RequestID       string
	PlatformActorID string
}

type Result struct {
	Store       storesdomain.Store
	OwnerUserID ids.UserID
}

type Service struct {
	repository Repository
	identity   IdentityPreparer
	newStoreID func() (ids.StoreID, error)
	now        func() time.Time
}

func NewService(repository Repository, identity IdentityPreparer, newStoreID func() (ids.StoreID, error), now func() time.Time) Service {
	return Service{repository: repository, identity: identity, newStoreID: newStoreID, now: now}
}

func (service Service) Execute(ctx context.Context, command Command) (Result, error) {
	if command.RequestID == "" {
		return Result{}, fmt.Errorf("onboarding: request ID is required")
	}
	if command.PlatformActorID == "" {
		return Result{}, fmt.Errorf("onboarding: platform actor is required")
	}
	storeID, err := service.newStoreID()
	if err != nil {
		return Result{}, fmt.Errorf("onboarding: store ID: %w", err)
	}
	now := service.now().UTC()
	store, err := storesdomain.NewStore(storeID, command.Subdomain, command.StoreName, defaultValue(command.DefaultCurrency, storesdomain.DefaultCurrency), defaultValue(command.Locale, storesdomain.DefaultLocale), defaultValue(command.Timezone, storesdomain.DefaultTimezone), now)
	if err != nil {
		return Result{}, err
	}
	prepared, err := service.identity.PrepareAccount(ctx, storeID, command.Email, command.Password)
	if err != nil {
		return Result{}, err
	}
	membership, err := storesdomain.NewMembership(storeID, prepared.User.ID, now)
	if err != nil {
		return Result{}, err
	}
	if err := service.repository.Create(ctx, store, membership, prepared, command.PlatformActorID, command.RequestID); err != nil {
		return Result{}, fmt.Errorf("onboarding persistence: %w", err)
	}
	return Result{Store: store, OwnerUserID: prepared.User.ID}, nil
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
