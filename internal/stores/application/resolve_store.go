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
