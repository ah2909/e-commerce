package postgres

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) ConfirmOrderInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time, correlationID string) error {
	rows, err := repository.lockPayableReservations(ctx, tx, storeID, orderID, now)
	if err != nil {
		return err
	}
	ordered := append([]reservationRow(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].VariantID < ordered[j].VariantID })
	stocks := make(map[string]stockRow, len(ordered))
	for _, reservation := range ordered {
		var stock stockRow
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id=?", storeID.String(), reservation.VariantID).First(&stock).Error; err != nil {
			return fmt.Errorf("lock stock for payment confirmation: %w", err)
		}
		if stock.OnHand < reservation.Quantity {
			return domain.ErrInsufficientStock
		}
		stock.OnHand -= reservation.Quantity
		stock.Version++
		stock.UpdatedAt = now.UTC()
		update := tx.WithContext(ctx).Model(&stockRow{}).Where("store_id=? AND variant_id=? AND version=?", storeID.String(), reservation.VariantID, stock.Version-1).Updates(map[string]any{"on_hand": stock.OnHand, "version": stock.Version, "updated_at": stock.UpdatedAt})
		if update.Error != nil || update.RowsAffected != 1 {
			if update.Error != nil {
				return update.Error
			}
			return application.ErrConflict
		}
		stocks[reservation.VariantID] = stock
	}
	for _, reservation := range rows {
		closed := now.UTC()
		update := tx.WithContext(ctx).Model(&reservationRow{}).Where("store_id=? AND reservation_id=? AND status='active' AND version=?", storeID.String(), reservation.ReservationID, reservation.Version).Updates(map[string]any{"status": domain.ReservationConfirmed, "version": reservation.Version + 1, "updated_at": closed, "closed_at": closed, "close_reason": "payment_confirmed"})
		if update.Error != nil || update.RowsAffected != 1 {
			if update.Error != nil {
				return update.Error
			}
			return application.ErrConflict
		}
		ledgerID, err := ids.NewLedgerEntryID()
		if err != nil {
			return err
		}
		variantID, err := ids.ParseVariantID(reservation.VariantID)
		if err != nil {
			return err
		}
		reserved, err := activeReserved(tx.WithContext(ctx), storeID, variantID)
		if err != nil {
			return err
		}
		stock := stocks[reservation.VariantID]
		reservationID := reservation.ReservationID
		ledger := ledgerRow{StoreID: storeID.String(), LedgerEntryID: ledgerID.String(), VariantID: reservation.VariantID, ReservationID: &reservationID, EntryType: "reservation_confirmation", QuantityDelta: -reservation.Quantity, ResultingOnHand: stock.OnHand, ResultingReserved: reserved, Reason: "payment_confirmed", BusinessReference: orderID.String(), CreatedAt: closed}
		if err := repository.ledger.Insert(ctx, tx, ledger); err != nil {
			return fmt.Errorf("append payment confirmation ledger: %w", err)
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.changed.v1", "inventory_stock_item", reservation.VariantID, stock.Version, closed, correlationID, map[string]any{"store_id": storeID.String(), "variant_id": reservation.VariantID, "ledger_entry_id": ledgerID.String(), "reason": "reservation_confirmation"}); err != nil {
			return err
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.reservation_confirmed.v1", "inventory_reservation", reservation.ReservationID, reservation.Version+1, closed, correlationID, map[string]any{"store_id": storeID.String(), "reservation_id": reservation.ReservationID, "order_id": orderID.String(), "variant_id": reservation.VariantID, "reason": "payment_confirmed"}); err != nil {
			return err
		}
	}
	return nil
}

func (repository *Repository) ValidateOrderPaymentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time) error {
	_, err := repository.lockPayableReservations(ctx, tx, storeID, orderID, now)
	return err
}

func (repository *Repository) lockPayableReservations(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time) ([]reservationRow, error) {
	rows, err := loadReservationRows(tx.WithContext(ctx), storeID, orderID, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, application.ErrReservationUnavailable
	}
	for _, row := range rows {
		if row.Status != string(domain.ReservationActive) || !now.Before(row.ExpiresAt) {
			return nil, application.ErrReservationUnavailable
		}
	}
	return rows, nil
}
