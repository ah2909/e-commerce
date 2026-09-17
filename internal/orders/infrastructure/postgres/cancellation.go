package postgres

import (
	"bytes"
	"context"
	"errors"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	paymentapplication "github.com/ah2909/e-commerce/internal/payments/application"
	paymentdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type cancellationRow struct {
	StoreID            string    `gorm:"column:store_id"`
	CancellationID     string    `gorm:"column:cancellation_id"`
	OrderID            string    `gorm:"column:order_id"`
	RefundID           *string   `gorm:"column:refund_id"`
	IdempotencyKey     string    `gorm:"column:idempotency_key"`
	RequestFingerprint []byte    `gorm:"column:request_fingerprint"`
	ActorType          string    `gorm:"column:actor_type"`
	ActorID            string    `gorm:"column:actor_id"`
	Reason             string    `gorm:"column:reason"`
	Outcome            string    `gorm:"column:outcome"`
	CreatedAt          time.Time `gorm:"column:created_at"`
}

func (cancellationRow) TableName() string { return "order_cancellations" }

func (repository *Repository) Cancel(ctx context.Context, command application.CancellationCommand, fingerprint [32]byte) (application.CancellationResult, error) {
	var result application.CancellationResult
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", command.StoreID.String()+":cancellation:"+command.IdempotencyKey).Error; err != nil {
			return err
		}
		if command.ActorType == application.CancellationWorkforce {
			actorID, err := ids.ParseUserID(command.ActorID)
			if err != nil || repository.authorize == nil {
				return application.ErrConflict
			}
			allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, command.StoreID, actorID, "order.cancel")
			if err != nil {
				return err
			}
			if !allowed {
				return application.ErrForbidden
			}
		}
		var replay cancellationRow
		if err := tx.Where("store_id=? AND idempotency_key=?", command.StoreID.String(), command.IdempotencyKey).First(&replay).Error; err == nil {
			if !bytes.Equal(replay.RequestFingerprint, fingerprint[:]) {
				return application.ErrConflict
			}
			result = cancellationResult(replay, false)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var order orderRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", command.StoreID.String(), command.OrderID.String()).First(&order).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		if !cancellationActorOwns(command, order) {
			return application.ErrNotFound
		}
		state := domain.State(order.State)
		if (command.ActorType == application.CancellationWorkforce && !domain.CanWorkforceCancel(state)) || (command.ActorType != application.CancellationWorkforce && !domain.CanShopperCancel(state)) {
			return application.ErrUnprocessable
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		cancellationID, err := ids.NewCancellationID()
		if err != nil {
			return err
		}
		row := cancellationRow{StoreID: command.StoreID.String(), CancellationID: cancellationID.String(), OrderID: command.OrderID.String(), IdempotencyKey: command.IdempotencyKey, RequestFingerprint: fingerprint[:], ActorType: string(command.ActorType), ActorID: command.ActorID, Reason: command.Reason, CreatedAt: now}
		if state == domain.StatePendingPayment {
			if _, err := repository.payments.CancelPendingInTransaction(ctx, tx, command.StoreID, command.OrderID, now); err != nil {
				return mapCancellationError(err)
			}
			changed, err := repository.inventory.ReleaseOrderInTransaction(ctx, tx, command.StoreID, command.OrderID, now, command.RequestID)
			if err != nil || !changed {
				if err != nil {
					return err
				}
				return application.ErrConflict
			}
			update := tx.Model(&orderRow{}).Where("store_id=? AND order_id=? AND state=? AND version=?", command.StoreID.String(), command.OrderID.String(), order.State, order.Version).Updates(map[string]any{"state": domain.StateCancelled, "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
			if update.Error != nil || update.RowsAffected != 1 {
				return application.ErrConflict
			}
			if err := repository.insertHistory(ctx, tx, command.StoreID, command.OrderID, state, domain.StateCancelled, command.Reason, now); err != nil {
				return err
			}
			if err := repository.appendEvent(ctx, tx, command.StoreID, command.OrderID, "order.cancelled.v1", order.Version+1, now, command.RequestID, map[string]any{"store_id": command.StoreID.String(), "order_id": command.OrderID.String(), "reason": command.Reason, "actor_type": string(command.ActorType)}); err != nil {
				return err
			}
			row.Outcome = "cancelled"
			if err := repository.appendCancellationAudit(ctx, tx, command, "order.cancelled", "order", command.OrderID.String(), now); err != nil {
				return err
			}
			result = application.CancellationResult{OrderID: command.OrderID, State: domain.StateCancelled, Created: true}
		} else {
			actorID, err := ids.ParseUserID(command.ActorID)
			if err != nil {
				return application.ErrConflict
			}
			allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, command.StoreID, actorID, "payment.refund")
			if err != nil {
				return err
			}
			if !allowed {
				return application.ErrForbidden
			}
			refundID, err := ids.NewRefundID()
			if err != nil {
				return err
			}
			refund, err := repository.payments.CreateRefundInTransaction(ctx, tx, command.StoreID, refundID, command.OrderID, actorID, command.Reason, command.IdempotencyKey, fingerprint, now)
			if err != nil {
				return mapCancellationError(err)
			}
			update := tx.Model(&orderRow{}).Where("store_id=? AND order_id=? AND state=? AND version=?", command.StoreID.String(), command.OrderID.String(), order.State, order.Version).Updates(map[string]any{"state": domain.StateRefundPending, "version": gorm.Expr("version + 2"), "updated_at": now})
			if update.Error != nil || update.RowsAffected != 1 {
				return application.ErrConflict
			}
			if err := repository.insertHistory(ctx, tx, command.StoreID, command.OrderID, state, domain.StateCancellationRequested, command.Reason, now); err != nil {
				return err
			}
			if err := repository.insertHistory(ctx, tx, command.StoreID, command.OrderID, domain.StateCancellationRequested, domain.StateRefundPending, "refund_requested", now); err != nil {
				return err
			}
			if err := repository.appendEvent(ctx, tx, command.StoreID, command.OrderID, "order.cancellation_requested.v1", order.Version+1, now, command.RequestID, map[string]any{"store_id": command.StoreID.String(), "order_id": command.OrderID.String(), "reason": command.Reason, "actor_type": "workforce"}); err != nil {
				return err
			}
			refundValue := refund.ID.String()
			row.Outcome, row.RefundID = "refund_pending", &refundValue
			if err := repository.appendCancellationAudit(ctx, tx, command, "refund.requested", "refund", refund.ID.String(), now); err != nil {
				return err
			}
			result = application.CancellationResult{OrderID: command.OrderID, State: domain.StateRefundPending, RefundID: refund.ID, Created: true}
		}
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
		return nil
	})
	return result, err
}

func cancellationActorOwns(command application.CancellationCommand, row orderRow) bool {
	if command.ActorType == application.CancellationWorkforce {
		return true
	}
	if command.ActorType == application.CancellationUser {
		return row.UserID != nil && *row.UserID == command.ActorID
	}
	return row.GuestID != nil && *row.GuestID == command.ActorID
}

func cancellationResult(row cancellationRow, created bool) application.CancellationResult {
	state := domain.StateCancelled
	var refundID ids.RefundID
	if row.Outcome == "refund_pending" {
		state = domain.StateRefundPending
		if row.RefundID != nil {
			refundID = ids.RefundID(*row.RefundID)
		}
	}
	return application.CancellationResult{OrderID: ids.OrderID(row.OrderID), State: state, RefundID: refundID, Created: created}
}

func (repository *Repository) insertHistory(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, from, to domain.State, reason string, now time.Time) error {
	historyID, err := ids.NewOrderHistoryID()
	if err != nil {
		return err
	}
	fromValue := string(from)
	return tx.WithContext(ctx).Create(&historyRow{StoreID: storeID.String(), HistoryID: historyID.String(), OrderID: orderID.String(), FromState: &fromValue, ToState: string(to), Reason: reason, OccurredAt: now.UTC()}).Error
}

func (repository *Repository) appendCancellationAudit(ctx context.Context, tx *gorm.DB, command application.CancellationCommand, action, subjectType, subjectID string, now time.Time) error {
	if repository.audit == nil {
		return errors.New("cancellation audit unavailable")
	}
	actorType := auditdomain.ActorUser
	if command.ActorType == application.CancellationGuest {
		actorType = auditdomain.ActorGuest
	}
	record, err := auditdomain.NewRecord(command.StoreID, actorType, command.ActorID, action, subjectType, subjectID, command.RequestID, now)
	if err != nil {
		return err
	}
	return repository.audit.Insert(ctx, tx, record)
}

func mapCancellationError(err error) error {
	if errors.Is(err, paymentdomain.ErrInvalidPayment) || errors.Is(err, paymentapplication.ErrConflict) {
		return application.ErrConflict
	}
	return err
}
