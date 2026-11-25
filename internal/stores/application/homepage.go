package application

import (
	"context"
	"errors"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrHomepageConflict = errors.New("storefront homepage version conflict")

type HomepageRepository interface {
	GetPublishedHomepage(context.Context, ids.StoreID) (domain.Homepage, error)
	GetHomepage(context.Context, ids.StoreID) (domain.HomepageState, error)
	SaveHomepageDraft(context.Context, ids.StoreID, ids.UserID, domain.Homepage, int64, string) (domain.HomepageState, error)
	PublishHomepage(context.Context, ids.StoreID, ids.UserID, int64, string) (domain.HomepageState, error)
}

type HomepageService struct{ repository HomepageRepository }

func NewHomepageService(repository HomepageRepository) HomepageService {
	return HomepageService{repository: repository}
}

func (s HomepageService) Published(ctx context.Context, storeID ids.StoreID) (domain.Homepage, error) {
	if storeID == "" {
		return domain.Homepage{}, ErrStoreUnavailable
	}
	return s.repository.GetPublishedHomepage(ctx, storeID)
}

func (s HomepageService) Get(ctx context.Context, storeID ids.StoreID) (domain.HomepageState, error) {
	if storeID == "" {
		return domain.HomepageState{}, ErrTenantAccessDenied
	}
	return s.repository.GetHomepage(ctx, storeID)
}

func (s HomepageService) SaveDraft(ctx context.Context, storeID ids.StoreID, userID ids.UserID, draft domain.Homepage, expected int64, requestID string) (domain.HomepageState, error) {
	if err := validateHomepageWrite(storeID, userID, expected, requestID); err != nil {
		return domain.HomepageState{}, err
	}
	if err := draft.Validate(); err != nil {
		return domain.HomepageState{}, err
	}
	return s.repository.SaveHomepageDraft(ctx, storeID, userID, draft, expected, requestID)
}

func (s HomepageService) Publish(ctx context.Context, storeID ids.StoreID, userID ids.UserID, expected int64, requestID string) (domain.HomepageState, error) {
	if err := validateHomepageWrite(storeID, userID, expected, requestID); err != nil {
		return domain.HomepageState{}, err
	}
	return s.repository.PublishHomepage(ctx, storeID, userID, expected, requestID)
}

func validateHomepageWrite(storeID ids.StoreID, userID ids.UserID, expected int64, requestID string) error {
	if storeID == "" || userID == "" || requestID == "" {
		return ErrTenantAccessDenied
	}
	if expected < 0 || expected >= domain.HomepageMaxVersion {
		return domain.ErrInvalidHomepage
	}
	return nil
}
