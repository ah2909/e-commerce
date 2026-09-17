package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	inventorydomain "github.com/ah2909/e-commerce/internal/inventory/domain"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) GetRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID) (domain.Refund, error) {
	var row refundRow
	if err := repository.db.WithContext(ctx).Where("store_id=? AND refund_id=?", storeID.String(), refundID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Refund{}, application.ErrNotFound
	} else if err != nil {
		return domain.Refund{}, err
	}
	return mapRefund(row, false), nil
}

func (repository *Repository) ClaimMoMoRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key string, fingerprint [32]byte) (application.RefundClaim, error) {
	var result application.RefundClaim
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, storeID, actorID, "payment.refund")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		var row refundRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND refund_id=? AND provider='momo'", storeID.String(), refundID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		var payment paymentRow
		if err := tx.Where("store_id=? AND payment_id=?", storeID.String(), row.PaymentID).First(&payment).Error; err != nil || !refundMatchesPayment(row, payment) {
			return application.ErrConflict
		}
		if (row.State == "succeeded" && payment.State != "refunded") || (row.State != "succeeded" && payment.State != "refund_pending") {
			return application.ErrConflict
		}
		if row.State == "succeeded" || row.State == "failed" {
			result.Refund = mapRefund(row, false)
			return nil
		}
		if row.ExecutionKey != nil && (*row.ExecutionKey != key || !equalDigest(row.ExecutionFingerprint, fingerprint)) {
			return application.ErrConflict
		}
		var attempt attemptRow
		if err := tx.Where("store_id=? AND payment_id=? AND provider='momo' AND state='succeeded'", storeID.String(), row.PaymentID).First(&attempt).Error; err != nil {
			return application.ErrConflict
		}
		secretValue, err := repository.decryptMoMoAttempt(attempt)
		if err != nil {
			return err
		}
		if attempt.ProviderTransactionID == nil || *attempt.ProviderTransactionID <= 0 || row.ProviderTransactionID == nil || *row.ProviderTransactionID != *attempt.ProviderTransactionID || attempt.OrderID != row.OrderID || attempt.AmountMinor != row.AmountMinor || attempt.Currency != row.Currency {
			return application.ErrConflict
		}
		providerOrderID := "RF" + strings.ReplaceAll(refundID.String(), "-", "")
		requestID := refundID.String()
		shouldQuery := row.State == "reconciliation_required" || row.State == "processing"
		if row.ExecutionKey == nil {
			now, err := databaseTime(tx)
			if err != nil {
				return err
			}
			update := tx.Model(&refundRow{}).Where("store_id=? AND refund_id=? AND state='pending'", storeID.String(), refundID.String()).Updates(map[string]any{"execution_key": key, "execution_fingerprint": fingerprint[:], "provider_order_id": providerOrderID, "provider_request_id": requestID, "state": "processing", "version": gorm.Expr("version + 1"), "updated_at": now})
			if update.Error != nil || update.RowsAffected != 1 {
				return application.ErrConflict
			}
			row.ExecutionKey, row.ExecutionFingerprint, row.ProviderOrderID, row.ProviderRequestID, row.State, row.Version, row.UpdatedAt = &key, fingerprint[:], &providerOrderID, &requestID, "processing", row.Version+1, now
		}
		result = application.RefundClaim{Refund: mapRefund(row, false), Credentials: toCredentials(secretValue.momoConfigSecret), OriginalTransaction: *attempt.ProviderTransactionID, ProviderOrderID: providerOrderID, RequestID: requestID, ShouldQuery: shouldQuery, ShouldCall: !shouldQuery}
		return nil
	})
	return result, err
}

func (repository *Repository) RecordMoMoRefundFailure(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, resultCode int, retryable bool) (domain.Refund, error) {
	var result domain.Refund
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row refundRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND refund_id=? AND provider='momo'", storeID.String(), refundID.String()).First(&row).Error; err != nil {
			return err
		}
		if row.State == "succeeded" || row.State == "failed" {
			result = mapRefund(row, false)
			return nil
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		state := "failed"
		if retryable {
			state = "reconciliation_required"
		}
		update := tx.Model(&refundRow{}).Where("store_id=? AND refund_id=? AND state IN ('processing','reconciliation_required')", storeID.String(), refundID.String()).Updates(map[string]any{"state": state, "provider_result_code": resultCode, "version": gorm.Expr("version + 1"), "updated_at": now})
		if update.Error != nil || update.RowsAffected != 1 {
			return application.ErrConflict
		}
		row.State, row.ProviderResultCode, row.Version, row.UpdatedAt = state, &resultCode, row.Version+1, now
		if !retryable {
			if err := repository.appendEvent(ctx, tx, storeID, "refund", refundID.String(), "refund.failed.v1", row.Version, now, refundID.String(), map[string]any{"store_id": storeID.String(), "order_id": row.OrderID, "payment_id": row.PaymentID, "refund_id": row.RefundID, "failure_class": "provider_final"}); err != nil {
				return err
			}
		}
		result = mapRefund(row, false)
		return nil
	})
	return result, err
}

func (repository *Repository) ApplyRefundSuccess(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, resultCode int, providerTransactionID int64, correlationID string, _ time.Time) (domain.Refund, error) {
	return repository.applyRefund(ctx, storeID, refundID, actorID, resultCode, providerTransactionID, "", correlationID, nil)
}

func (repository *Repository) SettleVietQRRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key string, fingerprint [32]byte, reference string, settledAt time.Time, requestID string) (domain.Refund, error) {
	return repository.applyRefund(ctx, storeID, refundID, actorID, 0, 0, reference, requestID, func(row *refundRow) error {
		if row.Provider != domain.ProviderVietQR {
			return application.ErrNotFound
		}
		if row.ExecutionKey != nil {
			if *row.ExecutionKey != key || !equalDigest(row.ExecutionFingerprint, fingerprint) {
				return application.ErrConflict
			}
			return nil
		}
		row.ExecutionKey, row.ExecutionFingerprint, row.ExternalReference, row.SettledAt = &key, fingerprint[:], &reference, &settledAt
		return nil
	})
}

func (repository *Repository) applyRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, resultCode int, providerRefundTransactionID int64, reference, correlationID string, prepare func(*refundRow) error) (domain.Refund, error) {
	var result domain.Refund
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, storeID, actorID, "payment.refund")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", storeID.String()+":refund-apply:"+refundID.String()).Error; err != nil {
			return err
		}
		var probe refundRow
		if err := tx.Where("store_id=? AND refund_id=?", storeID.String(), refundID.String()).First(&probe).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		order, err := repository.orders.LockForRefundInTransaction(ctx, tx, storeID, ids.OrderID(probe.OrderID))
		if err != nil {
			return err
		}
		var payment paymentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND payment_id=?", storeID.String(), probe.PaymentID).First(&payment).Error; err != nil {
			return err
		}
		var row refundRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND refund_id=?", storeID.String(), refundID.String()).First(&row).Error; err != nil {
			return err
		}
		if row.State == "succeeded" {
			if !refundMatchesPayment(row, payment) || payment.State != "refunded" {
				return application.ErrConflict
			}
			if prepare != nil {
				if err := prepare(&row); err != nil {
					return err
				}
			}
			result = mapRefund(row, false)
			return nil
		}
		if order.State != ordersdomain.StateRefundPending || !refundMatchesPayment(row, payment) || payment.State != "refund_pending" || (row.State != "pending" && row.State != "processing" && row.State != "reconciliation_required") {
			return application.ErrConflict
		}
		if (row.Provider == domain.ProviderMoMo) != (providerRefundTransactionID > 0) {
			return application.ErrConflict
		}
		if prepare != nil {
			if err := prepare(&row); err != nil {
				return err
			}
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		settledAt := now
		if row.SettledAt != nil {
			if row.SettledAt.After(now) || row.SettledAt.Before(row.CreatedAt) || payment.ClosedAt == nil || row.SettledAt.Before(*payment.ClosedAt) {
				return domain.ErrInvalidPayment
			}
			settledAt = row.SettledAt.UTC()
		}
		allocations := make([]inventorydomain.Allocation, len(order.Lines))
		for index, line := range order.Lines {
			allocations[index] = inventorydomain.Allocation{VariantID: line.VariantID, Quantity: line.Quantity}
		}
		if err := repository.inventory.RestockRefundInTransaction(ctx, tx, storeID, refundID, allocations, now, correlationID); err != nil {
			return err
		}
		paymentUpdate := tx.Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='refund_pending' AND version=?", storeID.String(), payment.PaymentID, payment.Version).Updates(map[string]any{"state": "refunded", "version": gorm.Expr("version + 1"), "updated_at": now})
		if paymentUpdate.Error != nil || paymentUpdate.RowsAffected != 1 {
			return application.ErrConflict
		}
		updates := map[string]any{"state": "succeeded", "provider_result_code": resultCode, "version": gorm.Expr("version + 1"), "updated_at": now, "settled_at": settledAt}
		if providerRefundTransactionID > 0 {
			updates["provider_refund_transaction_id"] = providerRefundTransactionID
		}
		if row.ExecutionKey != nil {
			updates["execution_key"], updates["execution_fingerprint"] = *row.ExecutionKey, row.ExecutionFingerprint
		}
		if row.ExternalReference != nil {
			updates["external_reference"] = *row.ExternalReference
		}
		refundUpdate := tx.Model(&refundRow{}).Where("store_id=? AND refund_id=? AND version=?", storeID.String(), refundID.String(), row.Version).Updates(updates)
		if refundUpdate.Error != nil || refundUpdate.RowsAffected != 1 {
			return application.ErrConflict
		}
		if err := repository.orders.MarkRefundedInTransaction(ctx, tx, storeID, order, refundID, now, correlationID); err != nil {
			return err
		}
		audit, err := auditdomain.NewRecord(storeID, auditdomain.ActorUser, actorID.String(), "refund.settled", "refund", refundID.String(), correlationID, now)
		if err != nil {
			return err
		}
		if err := repository.audit.Insert(ctx, tx, audit); err != nil {
			return err
		}
		if err := repository.appendEvent(ctx, tx, storeID, "refund", refundID.String(), "refund.succeeded.v1", row.Version+1, now, correlationID, map[string]any{"store_id": storeID.String(), "order_id": row.OrderID, "payment_id": row.PaymentID, "refund_id": row.RefundID}); err != nil {
			return err
		}
		row.State, row.ProviderResultCode, row.ProviderRefundTransactionID, row.SettledAt, row.Version, row.UpdatedAt = "succeeded", &resultCode, &providerRefundTransactionID, &settledAt, row.Version+1, now
		if reference != "" {
			row.ExternalReference = &reference
		}
		result = mapRefund(row, false)
		return nil
	})
	return result, err
}

func refundMatchesPayment(refund refundRow, payment paymentRow) bool {
	return refund.PaymentID == payment.PaymentID && refund.OrderID == payment.OrderID && refund.Provider == payment.Provider && refund.AmountMinor == payment.AmountMinor && refund.Currency == payment.Currency && refund.AmountMinor > 0
}
