package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) ReleaseOrderInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time, correlationID string) (bool, error) {
	rows, err := loadReservationRows(tx.WithContext(ctx), storeID, orderID, true)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ReservationID < rows[j].ReservationID })
	variants := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Status != string(domain.ReservationActive) {
			return false, application.ErrConflict
		}
		variants = append(variants, row.VariantID)
	}
	sort.Strings(variants)
	var stocks []stockRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id IN ?", storeID.String(), variants).Order("variant_id").Find(&stocks).Error; err != nil {
		return false, err
	}
	if len(stocks) != len(variants) {
		return false, errors.New("reservation stock missing")
	}
	for _, row := range rows {
		result := tx.WithContext(ctx).Model(&reservationRow{}).Where("store_id=? AND reservation_id=? AND status='active' AND version=?", storeID.String(), row.ReservationID, row.Version).Updates(map[string]any{"status": domain.ReservationReleased, "version": gorm.Expr("version + 1"), "updated_at": now.UTC(), "closed_at": now.UTC(), "close_reason": "order_cancelled"})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return false, result.Error
			}
			return false, application.ErrConflict
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.reservation_released.v1", "inventory_reservation", row.ReservationID, row.Version+1, now, correlationID, map[string]any{"store_id": storeID.String(), "reservation_id": row.ReservationID, "order_id": orderID.String(), "variant_id": row.VariantID, "reason": "order_cancelled"}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (repository *Repository) RestockRefundInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, refundID ids.RefundID, allocations []domain.Allocation, now time.Time, correlationID string) error {
	ordered := append([]domain.Allocation(nil), allocations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].VariantID.String() < ordered[j].VariantID.String() })
	for _, allocation := range ordered {
		var stock stockRow
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id=?", storeID.String(), allocation.VariantID.String()).First(&stock).Error; err != nil {
			return fmt.Errorf("lock stock for refund: %w", err)
		}
		if allocation.Quantity < 1 || stock.OnHand > domain.MaxQuantity-allocation.Quantity {
			return domain.ErrInvalidInventory
		}
		stock.OnHand += allocation.Quantity
		stock.Version++
		stock.UpdatedAt = now.UTC()
		update := tx.WithContext(ctx).Model(&stockRow{}).Where("store_id=? AND variant_id=? AND version=?", storeID.String(), allocation.VariantID.String(), stock.Version-1).Updates(map[string]any{"on_hand": stock.OnHand, "version": stock.Version, "updated_at": stock.UpdatedAt})
		if update.Error != nil || update.RowsAffected != 1 {
			if update.Error != nil {
				return update.Error
			}
			return application.ErrConflict
		}
		reserved, err := activeReserved(tx.WithContext(ctx), storeID, allocation.VariantID)
		if err != nil {
			return err
		}
		ledgerID, err := ids.NewLedgerEntryID()
		if err != nil {
			return err
		}
		ledger := ledgerRow{StoreID: storeID.String(), LedgerEntryID: ledgerID.String(), VariantID: allocation.VariantID.String(), EntryType: "refund_restock", QuantityDelta: allocation.Quantity, ResultingOnHand: stock.OnHand, ResultingReserved: reserved, Reason: "refund_succeeded", BusinessReference: refundID.String(), CreatedAt: now.UTC()}
		if err := repository.ledger.Insert(ctx, tx, ledger); err != nil {
			return err
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.changed.v1", "inventory_stock_item", allocation.VariantID.String(), stock.Version, now, correlationID, map[string]any{"store_id": storeID.String(), "variant_id": allocation.VariantID.String(), "ledger_entry_id": ledgerID.String(), "refund_id": refundID.String(), "reason": "refund_restock"}); err != nil {
			return err
		}
	}
	return nil
}
