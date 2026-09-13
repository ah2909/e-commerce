package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/application"
	"github.com/ah2909/e-commerce/internal/carts/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) LockForCheckout(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, owner domain.Owner, expected int64) (domain.Cart, error) {
	var row cartRow
	err := ownerQuery(tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND status='active'", storeID), owner).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Cart{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Cart{}, err
	}
	var now time.Time
	if err := tx.WithContext(ctx).Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
		return domain.Cart{}, err
	}
	if row.Version != expected || !row.ExpiresAt.After(now) {
		return domain.Cart{}, application.ErrConflict
	}
	return loadTx(tx.WithContext(ctx), storeID, row.CartID)
}

func (repository *Repository) ConvertForCheckout(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, cartID ids.CartID, orderID ids.OrderID, expected int64, now time.Time) error {
	result := tx.WithContext(ctx).Model(&cartRow{}).Where("store_id=? AND cart_id=? AND status='active' AND version=?", storeID, cartID, expected).Updates(map[string]any{"status": domain.StatusConverted, "converted_order_id": orderID, "version": gorm.Expr("version + 1"), "updated_at": now.UTC(), "closed_at": now.UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return application.ErrConflict
	}
	return nil
}
