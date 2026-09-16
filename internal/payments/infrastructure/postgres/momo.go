package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type momoConfigSecret struct {
	PartnerCode      string `json:"partner_code"`
	AccessCredential string `json:"access_credential"`
	SigningMaterial  string `json:"signing_material"`
	CallbackToken    string `json:"callback_token"`
}

type momoAttemptSecret struct {
	momoConfigSecret
	PayURL    string `json:"pay_url,omitempty"`
	Deeplink  string `json:"deeplink,omitempty"`
	QRCodeURL string `json:"qr_code_url,omitempty"`
}

type callbackRouteRow struct {
	TokenDigest   []byte    `gorm:"column:token_digest"`
	StoreID       string    `gorm:"column:store_id"`
	Provider      string    `gorm:"column:provider"`
	ConfigVersion int64     `gorm:"column:config_version"`
	Ciphertext    []byte    `gorm:"column:ciphertext"`
	Nonce         []byte    `gorm:"column:nonce"`
	KeyVersion    int       `gorm:"column:key_version"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (callbackRouteRow) TableName() string { return "payment_callback_routes" }

type webhookReceiptRow struct {
	StoreID       string    `gorm:"column:store_id"`
	Provider      string    `gorm:"column:provider"`
	ProviderEvent string    `gorm:"column:provider_event_id"`
	AttemptID     string    `gorm:"column:attempt_id"`
	PaymentID     string    `gorm:"column:payment_id"`
	BodyDigest    []byte    `gorm:"column:body_digest"`
	ResultCode    int       `gorm:"column:result_code"`
	Outcome       string    `gorm:"column:outcome"`
	ReceivedAt    time.Time `gorm:"column:received_at"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (webhookReceiptRow) TableName() string { return "payment_webhook_receipts" }

func (repository *Repository) GetMoMoConfig(ctx context.Context, storeID ids.StoreID) (domain.MoMoConfig, error) {
	var row providerConfigRow
	if err := repository.db.WithContext(ctx).Where("store_id=? AND provider='momo'", storeID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.MoMoConfig{}, application.ErrNotFound
	} else if err != nil {
		return domain.MoMoConfig{}, err
	}
	if _, err := repository.decryptMoMoConfig(row); err != nil {
		return domain.MoMoConfig{}, err
	}
	return domain.MoMoConfig{StoreID: storeID, MaskedPartner: row.PublicIdentifier, Enabled: row.Enabled, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (repository *Repository) ReplaceMoMoConfig(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, requestID string, expected int64, draft domain.MoMoConfigDraft, callbackToken string, callbackDigest [32]byte) (domain.MoMoConfig, error) {
	var result domain.MoMoConfig
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, storeID, actorID, "store.manage")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		var current providerConfigRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND provider='momo'", storeID.String()).First(&current).Error
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
		secretValue := momoConfigSecret{PartnerCode: draft.PartnerCode, AccessCredential: draft.AccessKey, SigningMaterial: draft.SecretKey, CallbackToken: callbackToken}
		plaintext, err := json.Marshal(secretValue)
		if err != nil {
			return err
		}
		envelope, err := repository.cipher.Encrypt(plaintext, configAAD(storeID, domain.ProviderMoMo, version))
		if err != nil {
			return application.ErrDependency
		}
		createdAt := now
		if current.Version > 0 {
			createdAt = current.CreatedAt
		}
		row := providerConfigRow{StoreID: storeID.String(), Provider: domain.ProviderMoMo, PublicIdentifier: domain.MaskPartnerCode(draft.PartnerCode), Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion, Enabled: draft.Enabled, Version: version, CreatedAt: createdAt, UpdatedAt: now}
		if current.Version == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return classify(err)
			}
		} else {
			update := tx.Model(&providerConfigRow{}).Where("store_id=? AND provider='momo' AND version=?", storeID.String(), expected).Updates(map[string]any{"public_identifier": row.PublicIdentifier, "ciphertext": row.Ciphertext, "nonce": row.Nonce, "key_version": row.KeyVersion, "enabled": row.Enabled, "version": row.Version, "updated_at": row.UpdatedAt})
			if update.Error != nil || update.RowsAffected != 1 {
				if update.Error != nil {
					return update.Error
				}
				return application.ErrConflict
			}
		}
		routeEnvelope, err := repository.cipher.Encrypt(plaintext, callbackAAD(callbackDigest))
		if err != nil {
			return application.ErrDependency
		}
		route := callbackRouteRow{TokenDigest: callbackDigest[:], StoreID: storeID.String(), Provider: domain.ProviderMoMo, ConfigVersion: version, Ciphertext: routeEnvelope.Ciphertext, Nonce: routeEnvelope.Nonce, KeyVersion: routeEnvelope.KeyVersion, CreatedAt: now}
		if err := tx.Create(&route).Error; err != nil {
			return classify(err)
		}
		if err := repository.appendAudit(ctx, tx, storeID, actorID, "payment.provider_configured", "payment_provider_config", storeID.String(), requestID, now); err != nil {
			return err
		}
		result = domain.MoMoConfig{StoreID: storeID, MaskedPartner: row.PublicIdentifier, Enabled: row.Enabled, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
		return nil
	})
	return result, err
}

func (repository *Repository) ClaimMoMoAttempt(ctx context.Context, command application.InstructionCommand, attemptID ids.PaymentAttemptID, fingerprint [32]byte) (application.MoMoClaim, error) {
	var result application.MoMoClaim
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := advisoryLock(tx, command.StoreID.String()+":momo-attempt:"+command.IdempotencyKey); err != nil {
			return err
		}
		var existing attemptRow
		err := tx.Where("store_id=? AND provider='momo' AND idempotency_key=?", command.StoreID.String(), command.IdempotencyKey).First(&existing).Error
		if err == nil {
			if !equalDigest(existing.RequestFingerprint, fingerprint) {
				return application.ErrConflict
			}
			secretValue, secretErr := repository.decryptMoMoAttempt(existing)
			if secretErr != nil {
				return secretErr
			}
			result = application.MoMoClaim{Attempt: mapMoMoAttempt(existing, secretValue, false), Credentials: toCredentials(secretValue.momoConfigSecret), AmountMinor: existing.AmountMinor, MoMoOrderID: existing.TransferReference, RequestID: existing.AttemptID, ShouldCall: existing.State == "creating" || existing.State == "retryable"}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		order, err := repository.orders.LoadForPaymentInstructionInTransaction(ctx, tx, command.StoreID, command.OrderID)
		if err != nil {
			return err
		}
		if order.State != ordersdomain.StatePendingPayment {
			return application.ErrUnavailable
		}
		var payment paymentRow
		if err := tx.Where("store_id=? AND payment_id=? AND order_id=? AND provider='momo' AND state='pending'", command.StoreID.String(), command.PaymentID.String(), command.OrderID.String()).First(&payment).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		var config providerConfigRow
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND provider='momo' AND enabled", command.StoreID.String()).First(&config).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrUnavailable
		} else if err != nil {
			return err
		}
		configValue, err := repository.decryptMoMoConfig(config)
		if err != nil {
			return err
		}
		snapshot := momoAttemptSecret{momoConfigSecret: configValue}
		plaintext, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		envelope, err := repository.cipher.Encrypt(plaintext, attemptAAD(command.StoreID, domain.ProviderMoMo, attemptID))
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
		providerOrderID := "MM" + strings.ReplaceAll(attemptID.String(), "-", "")
		row := attemptRow{StoreID: command.StoreID.String(), AttemptID: attemptID.String(), PaymentID: command.PaymentID.String(), OrderID: command.OrderID.String(), Provider: domain.ProviderMoMo, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: fingerprint[:], TransferReference: providerOrderID, AmountMinor: payment.AmountMinor, Currency: payment.Currency, ProviderConfigVersion: config.Version, InstructionCiphertext: envelope.Ciphertext, InstructionNonce: envelope.Nonce, InstructionKeyVersion: envelope.KeyVersion, State: "creating", ExpiresAt: order.ReservationExpiresAt, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
		result = application.MoMoClaim{Attempt: mapMoMoAttempt(row, snapshot, true), Credentials: toCredentials(configValue), AmountMinor: row.AmountMinor, MoMoOrderID: providerOrderID, RequestID: attemptID.String(), ShouldCall: true}
		return nil
	})
	return result, err
}

func (repository *Repository) FinalizeMoMoAttempt(ctx context.Context, storeID ids.StoreID, attemptID ids.PaymentAttemptID, response application.MoMoCreateResponse) (domain.MoMoAttempt, error) {
	var result domain.MoMoAttempt
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row attemptRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND attempt_id=? AND provider='momo'", storeID.String(), attemptID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		secretValue, err := repository.decryptMoMoAttempt(row)
		if err != nil {
			return err
		}
		if row.State == "action_required" || row.State == "failed" {
			result = mapMoMoAttempt(row, secretValue, false)
			return nil
		}
		if row.State != "creating" && row.State != "retryable" {
			return application.ErrConflict
		}
		secretValue.PayURL, secretValue.Deeplink, secretValue.QRCodeURL = response.PayURL, response.Deeplink, response.QRCodeURL
		plaintext, err := json.Marshal(secretValue)
		if err != nil {
			return err
		}
		envelope, err := repository.cipher.Encrypt(plaintext, attemptAAD(storeID, domain.ProviderMoMo, attemptID))
		if err != nil {
			return application.ErrDependency
		}
		state := "action_required"
		if response.Retryable {
			state = "retryable"
		} else if response.Final && response.ResultCode != 0 {
			state = "failed"
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		update := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state IN ('creating','retryable')", storeID.String(), attemptID.String()).Updates(map[string]any{"instruction_ciphertext": envelope.Ciphertext, "instruction_nonce": envelope.Nonce, "instruction_key_version": envelope.KeyVersion, "provider_result_code": response.ResultCode, "state": state, "updated_at": now})
		if update.Error != nil || update.RowsAffected != 1 {
			if update.Error != nil {
				return update.Error
			}
			return application.ErrConflict
		}
		row.InstructionCiphertext, row.InstructionNonce, row.InstructionKeyVersion, row.ProviderResultCode, row.State, row.UpdatedAt = envelope.Ciphertext, envelope.Nonce, envelope.KeyVersion, &response.ResultCode, state, now
		switch state {
		case "action_required":
			if err := repository.appendEvent(ctx, tx, storeID, "payment_attempt", attemptID.String(), "payment.action_required.v1", 1, now, attemptID.String(), map[string]any{"store_id": storeID.String(), "order_id": row.OrderID, "payment_id": row.PaymentID, "attempt_id": row.AttemptID, "provider": domain.ProviderMoMo, "expires_at": row.ExpiresAt}); err != nil {
				return err
			}
		case "failed":
			if err := repository.appendEvent(ctx, tx, storeID, "payment_attempt", attemptID.String(), "payment.failed.v1", 1, now, attemptID.String(), map[string]any{"store_id": storeID.String(), "order_id": row.OrderID, "payment_id": row.PaymentID, "attempt_id": row.AttemptID, "provider": domain.ProviderMoMo, "failure_class": "provider_final"}); err != nil {
				return err
			}
		}
		result = mapMoMoAttempt(row, secretValue, true)
		return nil
	})
	return result, err
}

func (repository *Repository) FailMoMoAttempt(ctx context.Context, storeID ids.StoreID, attemptID ids.PaymentAttemptID, final bool) (domain.MoMoAttempt, error) {
	var result domain.MoMoAttempt
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row attemptRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND attempt_id=? AND provider='momo'", storeID.String(), attemptID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		secretValue, err := repository.decryptMoMoAttempt(row)
		if err != nil {
			return err
		}
		if row.State != "creating" && row.State != "retryable" {
			result = mapMoMoAttempt(row, secretValue, false)
			return nil
		}
		state := "retryable"
		if final {
			state = "failed"
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		update := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state IN ('creating','retryable')", storeID.String(), attemptID.String()).Updates(map[string]any{"state": state, "updated_at": now})
		if update.Error != nil || update.RowsAffected != 1 {
			if update.Error != nil {
				return update.Error
			}
			return application.ErrConflict
		}
		row.State, row.UpdatedAt = state, now
		if final {
			if err := repository.appendEvent(ctx, tx, storeID, "payment_attempt", attemptID.String(), "payment.failed.v1", 1, now, attemptID.String(), map[string]any{"store_id": storeID.String(), "order_id": row.OrderID, "payment_id": row.PaymentID, "attempt_id": row.AttemptID, "provider": domain.ProviderMoMo, "failure_class": "provider_http_permanent"}); err != nil {
				return err
			}
		}
		result = mapMoMoAttempt(row, secretValue, false)
		return nil
	})
	return result, err
}

func (repository *Repository) ResolveMoMoCallback(ctx context.Context, digest [32]byte) (application.MoMoCallbackContext, error) {
	var row callbackRouteRow
	if err := repository.db.WithContext(ctx).Where("token_digest=? AND provider='momo'", digest[:]).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return application.MoMoCallbackContext{}, application.ErrNotFound
	} else if err != nil {
		return application.MoMoCallbackContext{}, err
	}
	plaintext, err := repository.cipher.Decrypt(secret.Envelope{Ciphertext: row.Ciphertext, Nonce: row.Nonce, KeyVersion: row.KeyVersion}, callbackAAD(digest))
	if err != nil {
		return application.MoMoCallbackContext{}, application.ErrDependency
	}
	var value momoConfigSecret
	if json.Unmarshal(plaintext, &value) != nil {
		return application.MoMoCallbackContext{}, application.ErrDependency
	}
	if _, err := domain.ValidateMoMoConfig(domain.MoMoConfigDraft{PartnerCode: value.PartnerCode, AccessKey: value.AccessCredential, SecretKey: value.SigningMaterial, Enabled: true}); err != nil || len(value.CallbackToken) < 32 {
		return application.MoMoCallbackContext{}, application.ErrDependency
	}
	return application.MoMoCallbackContext{StoreID: ids.StoreID(row.StoreID), ConfigVersion: row.ConfigVersion, Credentials: toCredentials(value)}, nil
}

func (repository *Repository) ProcessMoMoIPN(ctx context.Context, callback application.MoMoCallbackContext, ipn domain.MoMoIPN, bodyDigest [32]byte, correlationID string) (domain.WebhookResult, error) {
	var result domain.WebhookResult
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if ipn.ResultCode == 0 && ipn.Transaction <= 0 {
			return domain.ErrInvalidPayment
		}
		providerEventID := strconv.FormatInt(ipn.Transaction, 10)
		if ipn.Transaction == 0 {
			providerEventID = ipn.RequestID + "-" + strconv.Itoa(ipn.ResultCode)
		}
		if err := advisoryLock(tx, callback.StoreID.String()+":momo-ipn:"+providerEventID); err != nil {
			return err
		}
		var existing webhookReceiptRow
		err := tx.Where("store_id=? AND provider='momo' AND provider_event_id=?", callback.StoreID.String(), providerEventID).First(&existing).Error
		if err == nil {
			if !equalDigest(existing.BodyDigest, bodyDigest) {
				return application.ErrConflict
			}
			result = domain.WebhookResult{Outcome: existing.Outcome, Created: false}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		attemptID, err := ids.ParsePaymentAttemptID(ipn.RequestID)
		if err != nil {
			return domain.ErrInvalidPayment
		}
		var attempt attemptRow
		if err := tx.Where("store_id=? AND attempt_id=? AND provider='momo'", callback.StoreID.String(), attemptID.String()).First(&attempt).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		if attempt.ProviderConfigVersion != callback.ConfigVersion || ipn.PartnerCode != callback.Credentials.PartnerCode {
			return domain.ErrInvalidPayment
		}
		var payment paymentRow
		if err := tx.Where("store_id=? AND payment_id=? AND provider='momo'", callback.StoreID.String(), attempt.PaymentID).First(&payment).Error; err != nil {
			return err
		}
		orderID := ids.OrderID(payment.OrderID)
		order, err := repository.orders.LockForPaymentInTransaction(ctx, tx, callback.StoreID, orderID)
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND payment_id=?", callback.StoreID.String(), payment.PaymentID).First(&payment).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND attempt_id=? AND provider='momo'", callback.StoreID.String(), attempt.AttemptID).First(&attempt).Error; err != nil {
			return err
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		receivedAt := time.UnixMilli(ipn.ResponseMS).UTC()
		if receivedAt.After(now.Add(5 * time.Minute)) {
			return domain.ErrInvalidPayment
		}
		outcome := "failed"
		if ipn.ResultCode == 0 {
			reason := ""
			if ipn.OrderID != attempt.TransferReference {
				reason = "wrong_reference"
			} else if ipn.AmountMinor != payment.AmountMinor {
				reason = "wrong_amount"
			} else if attempt.State == "failed" || order.State != ordersdomain.StatePendingPayment || payment.State != "pending" {
				reason = "payment_closed"
			} else if !now.Before(order.ReservationExpiresAt) || receivedAt.After(order.ReservationExpiresAt) {
				reason = "late"
			}
			if reason == "" {
				validationErr := repository.inventory.ValidateOrderPaymentInTransaction(ctx, tx, callback.StoreID, orderID, now)
				if errors.Is(validationErr, inventoryapplication.ErrReservationUnavailable) {
					reason = "payment_closed"
				} else if validationErr != nil {
					return validationErr
				}
			}
			if reason != "" {
				reconciliationID, err := ids.NewReconciliationID()
				if err != nil {
					return err
				}
				reference := providerEventID
				reconciliation := reconciliationRow{StoreID: callback.StoreID.String(), ReconciliationID: reconciliationID.String(), PaymentID: payment.PaymentID, AttemptID: &attempt.AttemptID, TransferReference: reference, ObservedAmountMinor: ipn.AmountMinor, ExpectedAmountMinor: payment.AmountMinor, Currency: "VND", Reason: reason, Status: "unresolved", ReceivedAt: receivedAt, CreatedAt: now}
				if err := tx.Create(&reconciliation).Error; err != nil {
					return classify(err)
				}
				if payment.State == "pending" {
					update := tx.Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='pending' AND version=?", callback.StoreID.String(), payment.PaymentID, payment.Version).Updates(map[string]any{"state": "reconciliation_required", "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
					if update.Error != nil || update.RowsAffected != 1 {
						return application.ErrConflict
					}
				}
				if attempt.State != "succeeded" && attempt.State != "reconciliation_required" {
					attemptUpdate := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state=?", callback.StoreID.String(), attempt.AttemptID, attempt.State).Updates(map[string]any{"state": "reconciliation_required", "provider_result_code": ipn.ResultCode, "updated_at": now})
					if attemptUpdate.Error != nil || attemptUpdate.RowsAffected != 1 {
						if attemptUpdate.Error != nil {
							return attemptUpdate.Error
						}
						return application.ErrConflict
					}
				}
				if err := repository.appendEvent(ctx, tx, callback.StoreID, "payment_reconciliation", reconciliationID.String(), "payment.reconciliation_required.v1", 1, now, correlationID, map[string]any{"store_id": callback.StoreID.String(), "payment_id": payment.PaymentID, "attempt_id": attempt.AttemptID, "reconciliation_id": reconciliationID.String(), "reason": reason}); err != nil {
					return err
				}
				outcome = "reconciliation_required"
			} else {
				if err := repository.inventory.ConfirmOrderInTransaction(ctx, tx, callback.StoreID, orderID, now, correlationID); err != nil {
					return err
				}
				paymentUpdate := tx.Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='pending' AND version=?", callback.StoreID.String(), payment.PaymentID, payment.Version).Updates(map[string]any{"state": "succeeded", "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
				if paymentUpdate.Error != nil || paymentUpdate.RowsAffected != 1 {
					return application.ErrConflict
				}
				if err := repository.orders.MarkPaidInTransaction(ctx, tx, callback.StoreID, order, ids.PaymentID(payment.PaymentID), now, correlationID); err != nil {
					return err
				}
				attemptUpdate := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state IN ('creating','retryable','action_required')", callback.StoreID.String(), attempt.AttemptID).Updates(map[string]any{"state": "succeeded", "provider_result_code": ipn.ResultCode, "updated_at": now})
				if attemptUpdate.Error != nil || attemptUpdate.RowsAffected != 1 {
					if attemptUpdate.Error != nil {
						return attemptUpdate.Error
					}
					return application.ErrConflict
				}
				if err := repository.appendEvent(ctx, tx, callback.StoreID, "payment", payment.PaymentID, "payment.confirmed.v1", payment.Version+1, now, correlationID, map[string]any{"store_id": callback.StoreID.String(), "order_id": orderID.String(), "payment_id": payment.PaymentID, "attempt_id": attempt.AttemptID, "provider": domain.ProviderMoMo}); err != nil {
					return err
				}
				outcome = "paid"
			}
		} else if domain.ClassifyMoMoResult(ipn.ResultCode) == domain.MoMoResultRetryable {
			outcome = "retryable"
			if attempt.State == "creating" || attempt.State == "action_required" {
				attemptUpdate := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state=?", callback.StoreID.String(), attempt.AttemptID, attempt.State).Updates(map[string]any{"state": "retryable", "provider_result_code": ipn.ResultCode, "updated_at": now})
				if attemptUpdate.Error != nil || attemptUpdate.RowsAffected != 1 {
					if attemptUpdate.Error != nil {
						return attemptUpdate.Error
					}
					return application.ErrConflict
				}
			}
		} else if attempt.State != "succeeded" && attempt.State != "reconciliation_required" && attempt.State != "failed" {
			attemptUpdate := tx.Model(&attemptRow{}).Where("store_id=? AND attempt_id=? AND state=?", callback.StoreID.String(), attempt.AttemptID, attempt.State).Updates(map[string]any{"state": "failed", "provider_result_code": ipn.ResultCode, "updated_at": now})
			if attemptUpdate.Error != nil || attemptUpdate.RowsAffected != 1 {
				if attemptUpdate.Error != nil {
					return attemptUpdate.Error
				}
				return application.ErrConflict
			}
			if err := repository.appendEvent(ctx, tx, callback.StoreID, "payment_attempt", attempt.AttemptID, "payment.failed.v1", 2, now, correlationID, map[string]any{"store_id": callback.StoreID.String(), "order_id": orderID.String(), "payment_id": payment.PaymentID, "attempt_id": attempt.AttemptID, "provider": domain.ProviderMoMo, "failure_class": "provider_final"}); err != nil {
				return err
			}
		}
		receipt := webhookReceiptRow{StoreID: callback.StoreID.String(), Provider: domain.ProviderMoMo, ProviderEvent: providerEventID, AttemptID: attempt.AttemptID, PaymentID: payment.PaymentID, BodyDigest: bodyDigest[:], ResultCode: ipn.ResultCode, Outcome: outcome, ReceivedAt: receivedAt, CreatedAt: now}
		if err := tx.Create(&receipt).Error; err != nil {
			return classify(err)
		}
		result = domain.WebhookResult{Outcome: outcome, Created: true}
		return nil
	})
	return result, err
}

func (repository *Repository) decryptMoMoConfig(row providerConfigRow) (momoConfigSecret, error) {
	plaintext, err := repository.cipher.Decrypt(secret.Envelope{Ciphertext: row.Ciphertext, Nonce: row.Nonce, KeyVersion: row.KeyVersion}, configAAD(ids.StoreID(row.StoreID), domain.ProviderMoMo, row.Version))
	if err != nil {
		return momoConfigSecret{}, application.ErrDependency
	}
	var value momoConfigSecret
	if json.Unmarshal(plaintext, &value) != nil {
		return momoConfigSecret{}, application.ErrDependency
	}
	if _, err := domain.ValidateMoMoConfig(domain.MoMoConfigDraft{PartnerCode: value.PartnerCode, AccessKey: value.AccessCredential, SecretKey: value.SigningMaterial, Enabled: row.Enabled}); err != nil || len(value.CallbackToken) < 32 {
		return momoConfigSecret{}, application.ErrDependency
	}
	return value, nil
}

func (repository *Repository) decryptMoMoAttempt(row attemptRow) (momoAttemptSecret, error) {
	plaintext, err := repository.cipher.Decrypt(secret.Envelope{Ciphertext: row.InstructionCiphertext, Nonce: row.InstructionNonce, KeyVersion: row.InstructionKeyVersion}, attemptAAD(ids.StoreID(row.StoreID), domain.ProviderMoMo, ids.PaymentAttemptID(row.AttemptID)))
	if err != nil {
		return momoAttemptSecret{}, application.ErrDependency
	}
	var value momoAttemptSecret
	if json.Unmarshal(plaintext, &value) != nil {
		return momoAttemptSecret{}, application.ErrDependency
	}
	return value, nil
}

func mapMoMoAttempt(row attemptRow, value momoAttemptSecret, created bool) domain.MoMoAttempt {
	return domain.MoMoAttempt{AttemptID: ids.PaymentAttemptID(row.AttemptID), PaymentID: ids.PaymentID(row.PaymentID), OrderID: ids.OrderID(row.OrderID), State: row.State, PayURL: value.PayURL, Deeplink: value.Deeplink, QRCodeURL: value.QRCodeURL, ExpiresAt: row.ExpiresAt, Created: created}
}

func toCredentials(value momoConfigSecret) application.MoMoCredentials {
	return application.MoMoCredentials{PartnerCode: value.PartnerCode, AccessKey: value.AccessCredential, SecretKey: value.SigningMaterial, CallbackToken: value.CallbackToken}
}

func callbackAAD(digest [32]byte) []byte {
	return append([]byte("momo-callback|"), digest[:]...)
}
