package postgres

import (
	"context"
	"encoding/json"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

func (repository *Repository) ResolveCheckoutVariants(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variantIDs []ids.VariantID) (map[ids.VariantID]application.CartVariant, error) {
	values := make(map[ids.VariantID]application.CartVariant, len(variantIDs))
	if len(variantIDs) == 0 {
		return values, nil
	}
	idsText := make([]string, len(variantIDs))
	for i := range variantIDs {
		idsText[i] = variantIDs[i].String()
	}
	var rows []cartVariantRow
	err := tx.WithContext(ctx).Raw(`SELECT v.variant_id,p.name AS product_name,v.sku,v.option_values FROM catalog_variants v JOIN catalog_products p ON p.store_id=v.store_id AND p.product_id=v.product_id WHERE v.store_id=? AND v.variant_id IN ? AND v.status='active' AND p.status='published' ORDER BY v.variant_id FOR SHARE OF p,v`, storeID, idsText).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		options := map[string]string{}
		if err := json.Unmarshal(row.OptionValues, &options); err != nil {
			return nil, err
		}
		id := ids.VariantID(row.VariantID)
		values[id] = application.CartVariant{VariantID: id, ProductName: row.ProductName, SKU: row.SKU, OptionValues: options}
	}
	return values, nil
}
