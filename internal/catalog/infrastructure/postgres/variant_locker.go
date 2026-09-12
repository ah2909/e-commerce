package postgres

import (
	"context"
	"fmt"
	"sort"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type VariantLocker struct{}

func NewVariantLocker() VariantLocker { return VariantLocker{} }

func (VariantLocker) LockActiveVariants(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variantIDs []ids.VariantID) (bool, error) {
	values := make([]string, len(variantIDs))
	for index, id := range variantIDs {
		values[index] = id.String()
	}
	sort.Strings(values)
	var rows []variantRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id = ? AND variant_id IN ? AND status = 'active'", storeID.String(), values).Order("variant_id").Find(&rows).Error; err != nil {
		return false, fmt.Errorf("lock active catalog variants: %w", err)
	}
	return len(rows) == len(values), nil
}
