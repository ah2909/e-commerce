package application

import (
	"context"
	"errors"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrAppearanceConflict = errors.New("storefront appearance version conflict")

type AppearanceRepository interface {
	GetAppearance(context.Context, ids.StoreID) (domain.Appearance, error)
	SaveAppearance(context.Context, ids.StoreID, ids.UserID, domain.Appearance, string) (domain.Appearance, error)
}

type AppearanceService struct{ repository AppearanceRepository }

func NewAppearanceService(repository AppearanceRepository) AppearanceService {
	return AppearanceService{repository: repository}
}

func (s AppearanceService) Get(ctx context.Context, storeID ids.StoreID) (domain.Appearance, error) {
	if storeID == "" {
		return domain.Appearance{}, ErrStoreUnavailable
	}
	return s.repository.GetAppearance(ctx, storeID)
}

func (s AppearanceService) Save(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, a domain.Appearance, requestID string) (domain.Appearance, error) {
	if storeID == "" || actorID == "" || requestID == "" {
		return domain.Appearance{}, ErrTenantAccessDenied
	}
	if err := a.Validate(); err != nil {
		return domain.Appearance{}, err
	}
	return s.repository.SaveAppearance(ctx, storeID, actorID, a, requestID)
}
