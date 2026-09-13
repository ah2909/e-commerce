package postgres

import (
	"context"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"gorm.io/gorm"
)

func (repository *Repository) ResolveCheckoutPrices(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	if len(variants) == 0 {
		return map[ids.VariantID]money.Money{}, nil
	}
	var locked []struct{ PriceListID string }
	err := tx.WithContext(ctx).Raw(`SELECT l.price_list_id FROM pricing_price_lists l JOIN pricing_prices p ON p.store_id=l.store_id AND p.price_list_id=l.price_list_id WHERE l.store_id=? AND p.variant_id IN ? AND l.status='active' AND l.channel='storefront' AND l.currency='VND' ORDER BY l.price_list_id,p.variant_id FOR SHARE OF l,p`, storeID, variantStrings(variants)).Scan(&locked).Error
	if err != nil {
		return nil, err
	}
	return repository.ResolveCurrentInTransaction(ctx, tx, storeID, variants)
}
