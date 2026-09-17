package postgres

import (
	"context"
	"errors"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type refundRow struct {
	StoreID                     string     `gorm:"column:store_id"`
	RefundID                    string     `gorm:"column:refund_id"`
	OrderID                     string     `gorm:"column:order_id"`
	PaymentID                   string     `gorm:"column:payment_id"`
	Provider                    string     `gorm:"column:provider"`
	AmountMinor                 int64      `gorm:"column:amount_minor"`
	Currency                    string     `gorm:"column:currency"`
	Reason                      string     `gorm:"column:reason"`
	State                       string     `gorm:"column:state"`
	RequestedBy                 string     `gorm:"column:requested_by"`
	RequestID                   string     `gorm:"column:request_id"`
	CancellationKey             string     `gorm:"column:cancellation_key"`
	CancellationFingerprint     []byte     `gorm:"column:cancellation_fingerprint"`
	ExecutionKey                *string    `gorm:"column:execution_key"`
	ExecutionFingerprint        []byte     `gorm:"column:execution_fingerprint"`
	ProviderOrderID             *string    `gorm:"column:provider_order_id"`
	ProviderRequestID           *string    `gorm:"column:provider_request_id"`
	ProviderTransactionID       *int64     `gorm:"column:provider_transaction_id"`
	ProviderRefundTransactionID *int64     `gorm:"column:provider_refund_transaction_id"`
	ProviderResultCode          *int       `gorm:"column:provider_result_code"`
	ExternalReference           *string    `gorm:"column:external_reference"`
	SettledAt                   *time.Time `gorm:"column:settled_at"`
	Version                     int64      `gorm:"column:version"`
	CreatedAt                   time.Time  `gorm:"column:created_at"`
	UpdatedAt                   time.Time  `gorm:"column:updated_at"`
}

func (refundRow) TableName() string { return "refunds" }

func (participant Participant) CancelPendingInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time) (domain.Payment, error) {
	var row paymentRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).First(&row).Error; err != nil {
		return domain.Payment{}, err
	}
	if row.State != "pending" {
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	update := tx.WithContext(ctx).Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='pending' AND version=?", storeID.String(), row.PaymentID, row.Version).Updates(map[string]any{"state": "expired", "version": gorm.Expr("version + 1"), "updated_at": now.UTC(), "closed_at": now.UTC()})
	if update.Error != nil || update.RowsAffected != 1 {
		if update.Error != nil {
			return domain.Payment{}, update.Error
		}
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	if err := tx.WithContext(ctx).Model(&attemptRow{}).Where("store_id=? AND payment_id=? AND state IN ('creating','retryable','action_required')", storeID.String(), row.PaymentID).Updates(map[string]any{"state": "failed", "updated_at": now.UTC()}).Error; err != nil {
		return domain.Payment{}, err
	}
	row.State, row.Version, row.UpdatedAt, row.ClosedAt = "expired", row.Version+1, now.UTC(), ptr(now.UTC())
	return mapPayment(row)
}

func (participant Participant) CreateRefundInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, refundID ids.RefundID, orderID ids.OrderID, actorID ids.UserID, reason, key string, fingerprint [32]byte, now time.Time) (domain.Refund, error) {
	var existing refundRow
	if err := tx.WithContext(ctx).Where("store_id=? AND cancellation_key=?", storeID.String(), key).First(&existing).Error; err == nil {
		if !equalDigest(existing.CancellationFingerprint, fingerprint) {
			return domain.Refund{}, application.ErrConflict
		}
		return mapRefund(existing, false), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Refund{}, err
	}
	var payment paymentRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).First(&payment).Error; err != nil {
		return domain.Refund{}, err
	}
	if payment.State != "succeeded" || payment.AmountMinor <= 0 {
		return domain.Refund{}, domain.ErrInvalidPayment
	}
	var providerTransaction *int64
	if payment.Provider == domain.ProviderMoMo {
		var attempt attemptRow
		if err := tx.WithContext(ctx).Where("store_id=? AND payment_id=? AND provider='momo' AND state='succeeded'", storeID.String(), payment.PaymentID).First(&attempt).Error; err != nil || attempt.ProviderTransactionID == nil {
			return domain.Refund{}, domain.ErrInvalidPayment
		}
		providerTransaction = attempt.ProviderTransactionID
	}
	row := refundRow{StoreID: storeID.String(), RefundID: refundID.String(), OrderID: orderID.String(), PaymentID: payment.PaymentID, Provider: payment.Provider, AmountMinor: payment.AmountMinor, Currency: payment.Currency, Reason: reason, State: "pending", RequestedBy: actorID.String(), RequestID: key, CancellationKey: key, CancellationFingerprint: fingerprint[:], ProviderTransactionID: providerTransaction, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return domain.Refund{}, classify(err)
	}
	update := tx.WithContext(ctx).Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='succeeded' AND version=?", storeID.String(), payment.PaymentID, payment.Version).Updates(map[string]any{"state": "refund_pending", "version": gorm.Expr("version + 1"), "updated_at": now.UTC()})
	if update.Error != nil || update.RowsAffected != 1 {
		if update.Error != nil {
			return domain.Refund{}, update.Error
		}
		return domain.Refund{}, domain.ErrInvalidPayment
	}
	if participant.events == nil {
		return domain.Refund{}, errors.New("refund events unavailable")
	}
	eventID, err := ids.NewEventID()
	if err != nil {
		return domain.Refund{}, err
	}
	event, err := eventdomain.New(storeID, eventID, "refund.requested.v1", "refund", refundID.String(), 1, now, key, map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "payment_id": payment.PaymentID, "refund_id": refundID.String()})
	if err != nil {
		return domain.Refund{}, err
	}
	if err := participant.events.Append(ctx, tx, event); err != nil {
		return domain.Refund{}, err
	}
	return mapRefund(row, true), nil
}

func mapRefund(row refundRow, created bool) domain.Refund {
	result := domain.Refund{StoreID: ids.StoreID(row.StoreID), ID: ids.RefundID(row.RefundID), OrderID: ids.OrderID(row.OrderID), PaymentID: ids.PaymentID(row.PaymentID), Provider: row.Provider, AmountMinor: row.AmountMinor, Currency: row.Currency, Reason: row.Reason, State: row.State, ProviderResultCode: row.ProviderResultCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, SettledAt: row.SettledAt, Created: created}
	if row.ExternalReference != nil {
		result.ExternalReference = *row.ExternalReference
	}
	return result
}
