package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type CreateStoreCommand struct {
	CreatorID       domain.UserID
	Name            string
	DefaultCurrency string
	Locale          string
	Timezone        string
}

type CreateStore struct {
	repository Repository
	newStoreID StoreIDGenerator
	now        func() time.Time
}

func NewCreateStore(repository Repository, newStoreID StoreIDGenerator, now func() time.Time) CreateStore {
	return CreateStore{repository: repository, newStoreID: newStoreID, now: now}
}

func (useCase CreateStore) Execute(ctx context.Context, command CreateStoreCommand) (domain.Store, error) {
	if _, err := domain.ParseUserID(command.CreatorID.String()); err != nil {
		return domain.Store{}, fmt.Errorf("create store: creator: %w", err)
	}
	storeID, err := useCase.newStoreID()
	if err != nil {
		return domain.Store{}, fmt.Errorf("create store: generate ID: %w", err)
	}
	currencyCode := defaultValue(command.DefaultCurrency, domain.DefaultCurrency)
	locale := defaultValue(command.Locale, domain.DefaultLocale)
	timezone := defaultValue(command.Timezone, domain.DefaultTimezone)
	now := useCase.now().UTC()
	store, err := domain.NewStore(storeID, command.Name, currencyCode, locale, timezone, now)
	if err != nil {
		return domain.Store{}, fmt.Errorf("create store: %w", err)
	}
	membership, err := domain.NewMembership(storeID, command.CreatorID, now)
	if err != nil {
		return domain.Store{}, fmt.Errorf("create store membership: %w", err)
	}
	if err := useCase.repository.CreateWithMembership(ctx, store, membership); err != nil {
		return domain.Store{}, fmt.Errorf("create store persistence: %w", err)
	}
	return store, nil
}

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
