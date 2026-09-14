package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) LockForPaymentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (application.PayableOrder, error) {
	var row orderRow
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return application.PayableOrder{}, application.ErrNotFound
	}
	if err != nil {
		return application.PayableOrder{}, err
	}
	return application.PayableOrder{OrderID: orderID, State: domain.State(row.State), Version: row.Version, ReservationExpiresAt: row.ReservationExpiresAt}, nil
}

func (repository *Repository) LoadForPaymentInstructionInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (application.PaymentOrder, error) {
	var row orderRow
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return application.PaymentOrder{}, application.ErrNotFound
	}
	if err != nil {
		return application.PaymentOrder{}, err
	}
	return application.PaymentOrder{OrderID: orderID, State: domain.State(row.State), ReservationExpiresAt: row.ReservationExpiresAt}, nil
}

func (repository *Repository) MarkPaidInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, order application.PayableOrder, paymentID ids.PaymentID, now time.Time, correlationID string) error {
	if !domain.CanTransition(order.State, domain.StatePaid) {
		return application.ErrConflict
	}
	result := tx.WithContext(ctx).Model(&orderRow{}).Where("store_id=? AND order_id=? AND state=? AND version=?", storeID.String(), order.OrderID.String(), string(order.State), order.Version).Updates(map[string]any{"state": domain.StatePaid, "version": gorm.Expr("version + 1"), "updated_at": now.UTC()})
	if result.Error != nil || result.RowsAffected != 1 {
		if result.Error != nil {
			return result.Error
		}
		return application.ErrConflict
	}
	from := string(order.State)
	historyID, err := ids.NewOrderHistoryID()
	if err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Create(&historyRow{StoreID: storeID.String(), HistoryID: historyID.String(), OrderID: order.OrderID.String(), FromState: &from, ToState: string(domain.StatePaid), Reason: "payment_confirmed", OccurredAt: now.UTC()}).Error; err != nil {
		return err
	}
	return repository.appendEvent(ctx, tx, storeID, order.OrderID, "order.paid.v1", order.Version+1, now, correlationID, map[string]any{"store_id": storeID.String(), "order_id": order.OrderID.String(), "payment_id": paymentID.String()})
}
