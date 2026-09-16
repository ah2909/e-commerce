package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Cipher interface {
	Encrypt([]byte, []byte) (secret.Envelope, error)
	Decrypt(secret.Envelope, []byte) ([]byte, error)
}

type QREncoder interface {
	Encode(string, string, string, int64) (string, string, error)
}

type PaymentOrders interface {
	LoadForPaymentInstructionInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID) (ordersapplication.PaymentOrder, error)
	LockForPaymentInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID) (ordersapplication.PayableOrder, error)
	MarkPaidInTransaction(context.Context, *gorm.DB, ids.StoreID, ordersapplication.PayableOrder, ids.PaymentID, time.Time, string) error
}

type PaymentInventory interface {
	ValidateOrderPaymentInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, time.Time) error
	ConfirmOrderInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, time.Time, string) error
}

type PaymentAudit interface {
	Insert(context.Context, *gorm.DB, auditdomain.Record) error
}

type PaymentEvents interface {
	Append(context.Context, *gorm.DB, eventdomain.Event) error
}

type PaymentAuthorizer interface {
	HasPermissionInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.UserID, string) (bool, error)
}

type Repository struct {
	db        *gorm.DB
	cipher    Cipher
	qr        QREncoder
	orders    PaymentOrders
	inventory PaymentInventory
	audit     PaymentAudit
	events    PaymentEvents
	authorize PaymentAuthorizer
}

func NewRepository(db *gorm.DB, cipher Cipher, qr QREncoder, orders PaymentOrders, inventory PaymentInventory, audit PaymentAudit, events PaymentEvents, authorize PaymentAuthorizer) *Repository {
	return &Repository{db: db, cipher: cipher, qr: qr, orders: orders, inventory: inventory, audit: audit, events: events, authorize: authorize}
}

type providerConfigRow struct {
	StoreID          string    `gorm:"column:store_id"`
	Provider         string    `gorm:"column:provider"`
	BankBIN          string    `gorm:"column:bank_bin"`
	AccountLast4     string    `gorm:"column:account_last4"`
	PublicIdentifier string    `gorm:"column:public_identifier"`
	Ciphertext       []byte    `gorm:"column:ciphertext"`
	Nonce            []byte    `gorm:"column:nonce"`
	KeyVersion       int       `gorm:"column:key_version"`
	Enabled          bool      `gorm:"column:enabled"`
	Version          int64     `gorm:"column:version"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (providerConfigRow) TableName() string { return "payment_provider_configs" }

type attemptRow struct {
	StoreID               string    `gorm:"column:store_id"`
	AttemptID             string    `gorm:"column:attempt_id"`
	PaymentID             string    `gorm:"column:payment_id"`
	OrderID               string    `gorm:"column:order_id"`
	Provider              string    `gorm:"column:provider"`
	IdempotencyKey        string    `gorm:"column:idempotency_key"`
	RequestFingerprint    []byte    `gorm:"column:request_fingerprint"`
	TransferReference     string    `gorm:"column:transfer_reference"`
	AmountMinor           int64     `gorm:"column:amount_minor"`
	Currency              string    `gorm:"column:currency"`
	ProviderConfigVersion int64     `gorm:"column:provider_config_version"`
	InstructionCiphertext []byte    `gorm:"column:instruction_ciphertext"`
	InstructionNonce      []byte    `gorm:"column:instruction_nonce"`
	InstructionKeyVersion int       `gorm:"column:instruction_key_version"`
	State                 string    `gorm:"column:state"`
	ProviderResultCode    *int      `gorm:"column:provider_result_code"`
	ExpiresAt             time.Time `gorm:"column:expires_at"`
	CreatedAt             time.Time `gorm:"column:created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at"`
}

func (attemptRow) TableName() string { return "payment_attempts" }

type confirmationRow struct {
	StoreID             string    `gorm:"column:store_id"`
	ConfirmationID      string    `gorm:"column:confirmation_id"`
	PaymentID           string    `gorm:"column:payment_id"`
	AttemptID           *string   `gorm:"column:attempt_id"`
	IdempotencyKey      string    `gorm:"column:idempotency_key"`
	RequestFingerprint  []byte    `gorm:"column:request_fingerprint"`
	TransferReference   string    `gorm:"column:transfer_reference"`
	ObservedAmountMinor int64     `gorm:"column:observed_amount_minor"`
	Currency            string    `gorm:"column:currency"`
	ActorID             string    `gorm:"column:actor_id"`
	ReceivedAt          time.Time `gorm:"column:received_at"`
	Outcome             string    `gorm:"column:outcome"`
	ReconciliationID    *string   `gorm:"column:reconciliation_id"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (confirmationRow) TableName() string { return "payment_manual_confirmations" }

type reconciliationRow struct {
	StoreID             string    `gorm:"column:store_id"`
	ReconciliationID    string    `gorm:"column:reconciliation_id"`
	PaymentID           string    `gorm:"column:payment_id"`
	AttemptID           *string   `gorm:"column:attempt_id"`
	TransferReference   string    `gorm:"column:transfer_reference"`
	ObservedAmountMinor int64     `gorm:"column:observed_amount_minor"`
	ExpectedAmountMinor int64     `gorm:"column:expected_amount_minor"`
	Currency            string    `gorm:"column:currency"`
	Reason              string    `gorm:"column:reason"`
	Status              string    `gorm:"column:status"`
	ReceivedAt          time.Time `gorm:"column:received_at"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (reconciliationRow) TableName() string { return "payment_reconciliation_items" }

type configSecret struct {
	BankBIN       string `json:"bank_bin"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

func (repository *Repository) GetConfig(ctx context.Context, storeID ids.StoreID) (domain.VietQRConfig, error) {
	var row providerConfigRow
	if err := repository.db.WithContext(ctx).Where("store_id=? AND provider='vietqr'", storeID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.VietQRConfig{}, application.ErrNotFound
	} else if err != nil {
		return domain.VietQRConfig{}, err
	}
	return repository.mapConfig(row)
}

func (repository *Repository) ReplaceConfig(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, requestID string, expected int64, draft domain.VietQRConfigDraft) (domain.VietQRConfig, error) {
	var result domain.VietQRConfig
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, storeID, actorID, "store.manage")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		var current providerConfigRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND provider='vietqr'", storeID.String()).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) && expected != 0 {
			return application.ErrConflict
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && current.Version != expected {
			return application.ErrConflict
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		version := expected + 1
		plaintext, err := json.Marshal(configSecret{BankBIN: draft.BankBIN, AccountNumber: draft.AccountNumber, AccountName: draft.AccountName})
		if err != nil {
			return err
		}
		envelope, err := repository.cipher.Encrypt(plaintext, configAAD(storeID, domain.ProviderVietQR, version))
		if err != nil {
			return application.ErrDependency
		}
		createdAt := now
		if current.Version > 0 {
			createdAt = current.CreatedAt
		}
		row := providerConfigRow{StoreID: storeID.String(), Provider: domain.ProviderVietQR, BankBIN: draft.BankBIN, AccountLast4: draft.AccountNumber[len(draft.AccountNumber)-4:], Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion, Enabled: draft.Enabled, Version: version, CreatedAt: createdAt, UpdatedAt: now}
		if current.Version == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return classify(err)
			}
		} else {
			update := tx.Model(&providerConfigRow{}).Where("store_id=? AND provider='vietqr' AND version=?", storeID.String(), expected).Updates(map[string]any{"bank_bin": row.BankBIN, "account_last4": row.AccountLast4, "ciphertext": row.Ciphertext, "nonce": row.Nonce, "key_version": row.KeyVersion, "enabled": row.Enabled, "version": row.Version, "updated_at": row.UpdatedAt})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return application.ErrConflict
			}
		}
		if err := repository.appendAudit(ctx, tx, storeID, actorID, "payment.provider_configured", "payment_provider_config", storeID.String(), requestID, now); err != nil {
			return err
		}
		result = domain.VietQRConfig{StoreID: storeID, BankBIN: row.BankBIN, MaskedAccount: domain.MaskAccount(draft.AccountNumber), AccountName: draft.AccountName, Enabled: row.Enabled, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		return nil
	})
	return result, err
}

func (repository *Repository) CreateInstruction(ctx context.Context, command application.InstructionCommand, attemptID ids.PaymentAttemptID, reference string, fingerprint [32]byte) (domain.Instruction, error) {
	var result domain.Instruction
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := advisoryLock(tx, command.StoreID.String()+":vietqr-instruction:"+command.IdempotencyKey); err != nil {
			return err
		}
		var existing attemptRow
		err := tx.Where("store_id=? AND provider='vietqr' AND idempotency_key=?", command.StoreID.String(), command.IdempotencyKey).First(&existing).Error
		if err == nil {
			if !equalDigest(existing.RequestFingerprint, fingerprint) {
				return application.ErrConflict
			}
			mapped, mapErr := repository.mapInstruction(existing, false)
			result = mapped
			return mapErr
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("store_id=? AND payment_id=? AND provider='vietqr'", command.StoreID.String(), command.PaymentID.String()).First(&existing).Error; err == nil {
			mapped, mapErr := repository.mapInstruction(existing, false)
			result = mapped
			return mapErr
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		order, err := repository.orders.LoadForPaymentInstructionInTransaction(ctx, tx, command.StoreID, command.OrderID)
		if errors.Is(err, ordersapplication.ErrNotFound) {
			return application.ErrNotFound
		}
		if err != nil {
			return err
		}
		if order.State != ordersdomain.StatePendingPayment {
			return application.ErrUnavailable
		}
		var payment paymentRow
		if err := tx.Where("store_id=? AND payment_id=? AND order_id=? AND provider='vietqr' AND state='pending'", command.StoreID.String(), command.PaymentID.String(), command.OrderID.String()).First(&payment).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		var config providerConfigRow
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND provider='vietqr' AND enabled", command.StoreID.String()).First(&config).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrUnavailable
		} else if err != nil {
			return err
		}
		secretValue, err := repository.decryptConfig(config)
		if err != nil {
			return err
		}
		snapshot, err := json.Marshal(configSecret{BankBIN: config.BankBIN, AccountNumber: secretValue.AccountNumber, AccountName: secretValue.AccountName})
		if err != nil {
			return err
		}
		envelope, err := repository.cipher.Encrypt(snapshot, attemptAAD(command.StoreID, domain.ProviderVietQR, attemptID))
		if err != nil {
			return application.ErrDependency
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		if !now.Before(order.ReservationExpiresAt) {
			return application.ErrUnavailable
		}
		row := attemptRow{StoreID: command.StoreID.String(), AttemptID: attemptID.String(), PaymentID: command.PaymentID.String(), OrderID: command.OrderID.String(), Provider: domain.ProviderVietQR, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: fingerprint[:], TransferReference: reference, AmountMinor: payment.AmountMinor, Currency: payment.Currency, ProviderConfigVersion: config.Version, InstructionCiphertext: envelope.Ciphertext, InstructionNonce: envelope.Nonce, InstructionKeyVersion: envelope.KeyVersion, State: "action_required", ExpiresAt: order.ReservationExpiresAt, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
		if err := repository.appendEvent(ctx, tx, command.StoreID, "payment_attempt", attemptID.String(), "payment.action_required.v1", 1, now, command.CorrelationID, map[string]any{"store_id": command.StoreID.String(), "order_id": command.OrderID.String(), "payment_id": command.PaymentID.String(), "attempt_id": attemptID.String(), "provider": domain.ProviderVietQR, "expires_at": order.ReservationExpiresAt}); err != nil {
			return err
		}
		mapped, err := repository.mapInstruction(row, true)
		result = mapped
		return err
	})
	return result, err
}

func (repository *Repository) Confirm(ctx context.Context, command application.ConfirmationCommand, fingerprint [32]byte) (domain.Confirmation, error) {
	var result domain.Confirmation
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := advisoryLock(tx, command.StoreID.String()+":vietqr-confirm:"+command.PaymentID.String()); err != nil {
			return err
		}
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, command.StoreID, command.ActorID, "payment.confirm")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		var existing confirmationRow
		err = tx.Where("store_id=? AND payment_id=? AND idempotency_key=?", command.StoreID.String(), command.PaymentID.String(), command.IdempotencyKey).First(&existing).Error
		if err == nil {
			if !equalDigest(existing.RequestFingerprint, fingerprint) {
				return application.ErrConflict
			}
			result = mapConfirmation(existing, false)
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("store_id=? AND payment_id=? AND transfer_reference=?", command.StoreID.String(), command.PaymentID.String(), command.TransferReference).First(&existing).Error; err == nil {
			if !equalDigest(existing.RequestFingerprint, fingerprint) {
				return application.ErrConflict
			}
			result = mapConfirmation(existing, false)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var payment paymentRow
		if err := tx.Select("store_id", "payment_id", "order_id", "provider", "amount_minor", "currency", "state", "version", "created_at", "updated_at", "closed_at").Where("store_id=? AND payment_id=? AND provider='vietqr'", command.StoreID.String(), command.PaymentID.String()).First(&payment).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		orderID := ids.OrderID(payment.OrderID)
		order, err := repository.orders.LockForPaymentInTransaction(ctx, tx, command.StoreID, orderID)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND payment_id=?", command.StoreID.String(), command.PaymentID.String()).First(&payment).Error; err != nil {
			return err
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		if command.ReceivedAt.After(now.Add(5 * time.Minute)) {
			return domain.ErrInvalidPayment
		}
		var attempt attemptRow
		attemptErr := tx.Where("store_id=? AND payment_id=? AND transfer_reference=?", command.StoreID.String(), command.PaymentID.String(), command.TransferReference).First(&attempt).Error
		reason := ""
		if errors.Is(attemptErr, gorm.ErrRecordNotFound) {
			reason = "wrong_reference"
		} else if attemptErr != nil {
			return attemptErr
		} else if command.ObservedAmountMinor != payment.AmountMinor {
			reason = "wrong_amount"
		} else if order.State != ordersdomain.StatePendingPayment || payment.State != "pending" {
			reason = "payment_closed"
		} else if !now.Before(order.ReservationExpiresAt) || command.ReceivedAt.After(order.ReservationExpiresAt) {
			reason = "late"
		}
		if reason == "" {
			err := repository.inventory.ValidateOrderPaymentInTransaction(ctx, tx, command.StoreID, orderID, now)
			if errors.Is(err, inventoryapplication.ErrReservationUnavailable) {
				reason = "payment_closed"
			} else if err != nil {
				return err
			}
		}
		confirmationID, err := ids.NewEventID()
		if err != nil {
			return err
		}
		if reason != "" {
			reconciliationID, err := ids.NewReconciliationID()
			if err != nil {
				return err
			}
			var attemptID *string
			if attempt.AttemptID != "" {
				attemptID = &attempt.AttemptID
			}
			reconciliation := reconciliationRow{StoreID: command.StoreID.String(), ReconciliationID: reconciliationID.String(), PaymentID: command.PaymentID.String(), AttemptID: attemptID, TransferReference: command.TransferReference, ObservedAmountMinor: command.ObservedAmountMinor, ExpectedAmountMinor: payment.AmountMinor, Currency: "VND", Reason: reason, Status: "unresolved", ReceivedAt: command.ReceivedAt, CreatedAt: now}
			if err := tx.Create(&reconciliation).Error; err != nil {
				return classify(err)
			}
			receipt := confirmationRow{StoreID: command.StoreID.String(), ConfirmationID: confirmationID.String(), PaymentID: command.PaymentID.String(), AttemptID: attemptID, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: fingerprint[:], TransferReference: command.TransferReference, ObservedAmountMinor: command.ObservedAmountMinor, Currency: "VND", ActorID: command.ActorID.String(), ReceivedAt: command.ReceivedAt, Outcome: "reconciliation_required", ReconciliationID: ptrString(reconciliationID.String()), CreatedAt: now}
			if err := tx.Create(&receipt).Error; err != nil {
				return classify(err)
			}
			if payment.State == "pending" {
				update := tx.Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND version=?", command.StoreID.String(), command.PaymentID.String(), payment.Version).Updates(map[string]any{"state": "reconciliation_required", "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected != 1 {
					return application.ErrConflict
				}
			}
			if attempt.AttemptID != "" {
				if err := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=?", command.StoreID.String(), attempt.AttemptID).Updates(map[string]any{"state": "reconciliation_required", "updated_at": now}).Error; err != nil {
					return err
				}
			}
			if err := repository.appendAudit(ctx, tx, command.StoreID, command.ActorID, "payment.reconciliation_recorded", "payment", command.PaymentID.String(), command.RequestID, now); err != nil {
				return err
			}
			if err := repository.appendEvent(ctx, tx, command.StoreID, "payment_reconciliation", reconciliationID.String(), "payment.reconciliation_required.v1", 1, now, command.CorrelationID, map[string]any{"store_id": command.StoreID.String(), "payment_id": command.PaymentID.String(), "attempt_id": valueOrEmpty(attemptID), "reconciliation_id": reconciliationID.String(), "reason": reason}); err != nil {
				return err
			}
			result = mapConfirmation(receipt, true)
			return nil
		}
		if err := repository.inventory.ConfirmOrderInTransaction(ctx, tx, command.StoreID, orderID, now, command.CorrelationID); err != nil {
			if errors.Is(err, inventoryapplication.ErrConflict) {
				return application.ErrConflict
			}
			return err
		}
		paymentUpdate := tx.Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='pending' AND version=?", command.StoreID.String(), command.PaymentID.String(), payment.Version).Updates(map[string]any{"state": "succeeded", "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
		if paymentUpdate.Error != nil {
			return paymentUpdate.Error
		}
		if paymentUpdate.RowsAffected != 1 {
			return application.ErrConflict
		}
		if err := repository.orders.MarkPaidInTransaction(ctx, tx, command.StoreID, order, command.PaymentID, now, command.CorrelationID); err != nil {
			return err
		}
		attemptUpdate := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state='action_required'", command.StoreID.String(), attempt.AttemptID).Updates(map[string]any{"state": "succeeded", "updated_at": now})
		if attemptUpdate.Error != nil {
			return attemptUpdate.Error
		}
		if attemptUpdate.RowsAffected != 1 {
			return application.ErrConflict
		}
		receipt := confirmationRow{StoreID: command.StoreID.String(), ConfirmationID: confirmationID.String(), PaymentID: command.PaymentID.String(), AttemptID: &attempt.AttemptID, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: fingerprint[:], TransferReference: command.TransferReference, ObservedAmountMinor: command.ObservedAmountMinor, Currency: "VND", ActorID: command.ActorID.String(), ReceivedAt: command.ReceivedAt, Outcome: "paid", CreatedAt: now}
		if err := tx.Create(&receipt).Error; err != nil {
			return classify(err)
		}
		if err := repository.appendAudit(ctx, tx, command.StoreID, command.ActorID, "payment.confirmed", "payment", command.PaymentID.String(), command.RequestID, now); err != nil {
			return err
		}
		if err := repository.appendEvent(ctx, tx, command.StoreID, "payment", command.PaymentID.String(), "payment.confirmed.v1", payment.Version+1, now, command.CorrelationID, map[string]any{"store_id": command.StoreID.String(), "order_id": orderID.String(), "payment_id": command.PaymentID.String(), "attempt_id": attempt.AttemptID, "provider": domain.ProviderVietQR}); err != nil {
			return err
		}
		result = mapConfirmation(receipt, true)
		return nil
	})
	return result, err
}

func (repository *Repository) ListReconciliation(ctx context.Context, storeID ids.StoreID, limit int, before time.Time, beforeID ids.ReconciliationID) ([]domain.Reconciliation, error) {
	query := repository.db.WithContext(ctx).Where("store_id=? AND status='unresolved'", storeID.String()).Order("created_at DESC,reconciliation_id DESC").Limit(limit)
	if !before.IsZero() {
		query = query.Where("(created_at,reconciliation_id)<(?,?::uuid)", before, beforeID.String())
	}
	var rows []reconciliationRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.Reconciliation, 0, len(rows))
	for _, row := range rows {
		value := domain.Reconciliation{ID: ids.ReconciliationID(row.ReconciliationID), PaymentID: ids.PaymentID(row.PaymentID), TransferReference: row.TransferReference, ObservedAmountMinor: row.ObservedAmountMinor, ExpectedAmountMinor: row.ExpectedAmountMinor, Currency: row.Currency, Reason: row.Reason, ReceivedAt: row.ReceivedAt, CreatedAt: row.CreatedAt}
		if row.AttemptID != nil {
			value.AttemptID = ids.PaymentAttemptID(*row.AttemptID)
		}
		result = append(result, value)
	}
	return result, nil
}

func (repository *Repository) mapConfig(row providerConfigRow) (domain.VietQRConfig, error) {
	value, err := repository.decryptConfig(row)
	if err != nil {
		return domain.VietQRConfig{}, err
	}
	return domain.VietQRConfig{StoreID: ids.StoreID(row.StoreID), BankBIN: row.BankBIN, MaskedAccount: domain.MaskAccount(value.AccountNumber), AccountName: value.AccountName, Enabled: row.Enabled, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (repository *Repository) decryptConfig(row providerConfigRow) (configSecret, error) {
	plaintext, err := repository.cipher.Decrypt(secret.Envelope{Ciphertext: row.Ciphertext, Nonce: row.Nonce, KeyVersion: row.KeyVersion}, configAAD(ids.StoreID(row.StoreID), domain.ProviderVietQR, row.Version))
	if err != nil {
		return configSecret{}, application.ErrDependency
	}
	var value configSecret
	if err := json.Unmarshal(plaintext, &value); err != nil {
		return configSecret{}, application.ErrDependency
	}
	if value.BankBIN != row.BankBIN {
		return configSecret{}, application.ErrDependency
	}
	if _, err := domain.ValidateVietQRConfig(domain.VietQRConfigDraft{BankBIN: value.BankBIN, AccountNumber: value.AccountNumber, AccountName: value.AccountName, Enabled: row.Enabled}); err != nil {
		return configSecret{}, application.ErrDependency
	}
	return value, nil
}

func (repository *Repository) mapInstruction(row attemptRow, created bool) (domain.Instruction, error) {
	plaintext, err := repository.cipher.Decrypt(secret.Envelope{Ciphertext: row.InstructionCiphertext, Nonce: row.InstructionNonce, KeyVersion: row.InstructionKeyVersion}, attemptAAD(ids.StoreID(row.StoreID), domain.ProviderVietQR, ids.PaymentAttemptID(row.AttemptID)))
	if err != nil {
		return domain.Instruction{}, application.ErrDependency
	}
	var value configSecret
	if err := json.Unmarshal(plaintext, &value); err != nil {
		return domain.Instruction{}, application.ErrDependency
	}
	payload, png, err := repository.qr.Encode(value.BankBIN, value.AccountNumber, row.TransferReference, row.AmountMinor)
	if err != nil {
		return domain.Instruction{}, application.ErrDependency
	}
	amount, err := money.New(row.AmountMinor, row.Currency)
	if err != nil {
		return domain.Instruction{}, application.ErrDependency
	}
	return domain.Instruction{AttemptID: ids.PaymentAttemptID(row.AttemptID), PaymentID: ids.PaymentID(row.PaymentID), OrderID: ids.OrderID(row.OrderID), TransferReference: row.TransferReference, Amount: amount, ExpiresAt: row.ExpiresAt, Payload: payload, PNGBase64: png, Created: created}, nil
}

func mapConfirmation(row confirmationRow, created bool) domain.Confirmation {
	result := domain.Confirmation{PaymentID: ids.PaymentID(row.PaymentID), Created: created, Outcome: row.Outcome}
	if row.AttemptID != nil {
		result.AttemptID = ids.PaymentAttemptID(*row.AttemptID)
	}
	if row.ReconciliationID != nil {
		result.ReconciliationID = ids.ReconciliationID(*row.ReconciliationID)
	}
	return result
}

func (repository *Repository) appendAudit(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, actorID ids.UserID, action, subjectType, subjectID, requestID string, now time.Time) error {
	record, err := auditdomain.NewRecord(storeID, auditdomain.ActorUser, actorID.String(), action, subjectType, subjectID, requestID, now)
	if err != nil {
		return err
	}
	return repository.audit.Insert(ctx, tx, record)
}

func (repository *Repository) appendEvent(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, aggregateType, aggregateID, eventType string, version int64, now time.Time, correlationID string, payload map[string]any) error {
	eventID, err := ids.NewEventID()
	if err != nil {
		return err
	}
	event, err := eventdomain.New(storeID, eventID, eventType, aggregateType, aggregateID, version, now, correlationID, payload)
	if err != nil {
		return err
	}
	return repository.events.Append(ctx, tx, event)
}

func configAAD(storeID ids.StoreID, provider string, version int64) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d", storeID.String(), provider, version))
}

func attemptAAD(storeID ids.StoreID, provider string, attemptID ids.PaymentAttemptID) []byte {
	return []byte(storeID.String() + "|" + provider + "-attempt|" + attemptID.String())
}

func advisoryLock(tx *gorm.DB, value string) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", value).Error
}

func databaseTime(tx *gorm.DB) (time.Time, error) {
	var value time.Time
	err := tx.Raw("SELECT clock_timestamp()").Scan(&value).Error
	return value.UTC(), err
}

func equalDigest(value []byte, expected [32]byte) bool {
	if len(value) != len(expected) {
		return false
	}
	var different byte
	for index := range expected {
		different |= value[index] ^ expected[index]
	}
	return different == 0
}

func classify(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23514" || state.SQLState() == "23503") {
		return application.ErrConflict
	}
	return err
}

func ptrString(value string) *string { return &value }

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
