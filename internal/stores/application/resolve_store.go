package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrStoreUnavailable = errors.New("store unavailable")

type ResolveStore struct{ repository StoreResolverRepository }

func NewResolveStore(repository StoreResolverRepository) ResolveStore {
	return ResolveStore{repository: repository}
}

func (useCase ResolveStore) Execute(ctx context.Context, subdomainValue string) (StoreRecord, error) {
	subdomain, err := domain.ParseSubdomain(subdomainValue)
	if err != nil {
		return StoreRecord{}, ErrStoreUnavailable
	}
	record, found, err := useCase.repository.ResolveStoreBySubdomain(ctx, subdomain)
	if err != nil {
		return StoreRecord{}, err
	}
	if !found || record.Status != domain.StoreStatusActive {
		return StoreRecord{}, ErrStoreUnavailable
	}
	if record.Subdomain != subdomain {
		return StoreRecord{}, fmt.Errorf("resolve store: repository returned mismatched subdomain")
	}
	if _, err := ids.ParseStoreID(record.StoreID.String()); err != nil {
		return StoreRecord{}, fmt.Errorf("resolve store: repository returned invalid store ID")
	}
	return record, nil
}

func (useCase ResolveStore) ExecuteID(ctx context.Context, storeID ids.StoreID) (StoreRecord, error) {
	if _, err := ids.ParseStoreID(storeID.String()); err != nil {
		return StoreRecord{}, ErrStoreUnavailable
	}
	repository, ok := useCase.repository.(StoreIDResolverRepository)
	if !ok {
		return StoreRecord{}, errors.New("resolve store: ID lookup is unavailable")
	}
	record, found, err := repository.ResolveStoreByID(ctx, storeID)
	if err != nil {
		return StoreRecord{}, err
	}
	if !found || record.Status != domain.StoreStatusActive {
		return StoreRecord{}, ErrStoreUnavailable
	}
	if record.StoreID != storeID {
		return StoreRecord{}, fmt.Errorf("resolve store: repository returned mismatched store ID")
	}
	return record, nil
}
