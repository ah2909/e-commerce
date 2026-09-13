package postgres

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) ExpireOrderInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time, correlationID string) (bool, error) {
	rows, err := loadReservationRows(tx.WithContext(ctx), storeID, orderID, true)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ReservationID < rows[j].ReservationID })
	variants := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Status != string(domain.ReservationActive) || row.ExpiresAt.After(now) {
			return false, nil
		}
		variants = append(variants, row.VariantID)
	}
	sort.Strings(variants)
	var stocks []stockRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id IN ?", storeID, variants).Order("variant_id").Find(&stocks).Error; err != nil {
		return false, err
	}
	if len(stocks) != len(variants) {
		return false, errors.New("reservation stock missing")
	}
	for _, row := range rows {
		result := tx.WithContext(ctx).Model(&reservationRow{}).Where("store_id=? AND reservation_id=? AND status='active' AND version=?", storeID, row.ReservationID, row.Version).Updates(map[string]any{"status": domain.ReservationExpired, "version": gorm.Expr("version + 1"), "updated_at": now.UTC(), "closed_at": now.UTC(), "close_reason": "order_expired"})
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected != 1 {
			return false, nil
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.reservation_expired.v1", "inventory_reservation", row.ReservationID, row.Version+1, now, correlationID, map[string]any{"store_id": storeID.String(), "reservation_id": row.ReservationID, "order_id": orderID.String(), "variant_id": row.VariantID}); err != nil {
			return false, err
		}
	}
	return true, nil
}
