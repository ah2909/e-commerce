package postgres

import (
	"context"
	"errors"

	"github.com/ah2909/e-commerce/internal/customers/application"
	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) ResolveCheckoutAddress(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, userID ids.UserID, addressID ids.AddressID) (domain.Profile, domain.Address, error) {
	var customer customerRow
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND user_id=? AND archived_at IS NULL", storeID, userID).First(&customer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Profile{}, domain.Address{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Profile{}, domain.Address{}, err
	}
	var value addressRow
	err = tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND customer_id=? AND address_id=? AND archived_at IS NULL", storeID, customer.CustomerID, addressID).First(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Profile{}, domain.Address{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Profile{}, domain.Address{}, err
	}
	return profile(customer), address(value), nil
}
