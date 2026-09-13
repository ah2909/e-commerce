package application

import (
	"context"
	"errors"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
)

var ErrNotFound = errors.New("shipping rule not found")
var ErrConflict = errors.New("shipping rule conflict")

type Repository interface {
	Get(context.Context, ids.StoreID) (domain.Rule, error)
	Replace(context.Context, ids.StoreID, ids.ShippingRuleID, int64, domain.Draft) (domain.Rule, error)
}

type Service struct{ repository Repository }

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) Get(ctx context.Context, storeID ids.StoreID) (domain.Rule, error) {
	if storeID == "" {
		return domain.Rule{}, domain.ErrInvalidRule
	}
	return service.repository.Get(ctx, storeID)
}

func (service Service) Replace(ctx context.Context, storeID ids.StoreID, expected int64, draft domain.Draft) (domain.Rule, error) {
	validated, err := domain.ValidateDraft(draft)
	if err != nil || storeID == "" || expected < 0 {
		return domain.Rule{}, domain.ErrInvalidRule
	}
	id, err := ids.NewShippingRuleID()
	if err != nil {
		return domain.Rule{}, err
	}
	return service.repository.Replace(ctx, storeID, id, expected, validated)
}
