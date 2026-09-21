package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

type RotationKeyring interface {
	Cipher
	ActiveVersion() int
	HasVersion(int) bool
}

type KeyReference struct {
	Kind       string
	KeyVersion int
	Count      int64
}

type KeyRotator struct {
	db      *gorm.DB
	keyring RotationKeyring
}

func NewKeyRotator(db *gorm.DB, keyring RotationKeyring) *KeyRotator {
	return &KeyRotator{db: db, keyring: keyring}
}

func (rotator *KeyRotator) Status(ctx context.Context) ([]KeyReference, error) {
	var values []KeyReference
	query := `
SELECT 'provider_config' AS kind,key_version,count(*) AS count FROM payment_provider_configs GROUP BY key_version
UNION ALL SELECT 'attempt_snapshot',instruction_key_version,count(*) FROM payment_attempts GROUP BY instruction_key_version
UNION ALL SELECT 'callback_route',key_version,count(*) FROM payment_callback_routes GROUP BY key_version
ORDER BY key_version,kind`
	if err := rotator.db.WithContext(ctx).Raw(query).Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("inspect payment key references: %w", err)
	}
	return values, nil
}

func (rotator *KeyRotator) ReferenceCount(ctx context.Context, version int) (int64, error) {
	var count int64
	query := `SELECT (SELECT count(*) FROM payment_provider_configs WHERE key_version=?) + (SELECT count(*) FROM payment_attempts WHERE instruction_key_version=?) + (SELECT count(*) FROM payment_callback_routes WHERE key_version=?)`
	if err := rotator.db.WithContext(ctx).Raw(query, version, version, version).Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("count payment key references: %w", err)
	}
	return count, nil
}

func (rotator *KeyRotator) RotateBatch(ctx context.Context, source, target, limit int) (int, error) {
	if source < 1 || target != rotator.keyring.ActiveVersion() || source == target || limit < 1 || limit > 500 || !rotator.keyring.HasVersion(source) {
		return 0, errors.New("invalid payment key rotation")
	}
	processed := 0
	err := rotator.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		remaining := limit
		count, err := rotator.rotateConfigs(ctx, tx, source, remaining)
		processed += count
		remaining -= count
		if err != nil || remaining == 0 {
			return err
		}
		count, err = rotator.rotateAttempts(ctx, tx, source, remaining)
		processed += count
		remaining -= count
		if err != nil || remaining == 0 {
			return err
		}
		count, err = rotator.rotateCallbacks(ctx, tx, source, remaining)
		processed += count
		return err
	})
	if err != nil {
		return 0, err
	}
	return processed, nil
}

func (rotator *KeyRotator) rotateConfigs(ctx context.Context, tx *gorm.DB, source, limit int) (int, error) {
	var rows []providerConfigRow
	if err := tx.WithContext(ctx).Raw(`SELECT * FROM payment_provider_configs WHERE key_version=? ORDER BY store_id,provider FOR UPDATE SKIP LOCKED LIMIT ?`, source, limit).Scan(&rows).Error; err != nil {
		return 0, fmt.Errorf("claim payment provider configurations: %w", err)
	}
	for _, row := range rows {
		aad := configAAD(ids.StoreID(row.StoreID), row.Provider, row.Version)
		if err := rotator.reencrypt(ctx, tx, "payment_provider_configs", "store_id=? AND provider=? AND key_version=?", []any{row.StoreID, row.Provider, source}, row.Ciphertext, row.Nonce, row.KeyVersion, aad, "ciphertext", "nonce", "key_version"); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func (rotator *KeyRotator) rotateAttempts(ctx context.Context, tx *gorm.DB, source, limit int) (int, error) {
	var rows []attemptRow
	if err := tx.WithContext(ctx).Raw(`SELECT * FROM payment_attempts WHERE instruction_key_version=? ORDER BY store_id,attempt_id FOR UPDATE SKIP LOCKED LIMIT ?`, source, limit).Scan(&rows).Error; err != nil {
		return 0, fmt.Errorf("claim payment attempt snapshots: %w", err)
	}
	for _, row := range rows {
		aad := attemptAAD(ids.StoreID(row.StoreID), row.Provider, ids.PaymentAttemptID(row.AttemptID))
		if err := rotator.reencrypt(ctx, tx, "payment_attempts", "store_id=? AND attempt_id=? AND instruction_key_version=?", []any{row.StoreID, row.AttemptID, source}, row.InstructionCiphertext, row.InstructionNonce, row.InstructionKeyVersion, aad, "instruction_ciphertext", "instruction_nonce", "instruction_key_version"); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func (rotator *KeyRotator) rotateCallbacks(ctx context.Context, tx *gorm.DB, source, limit int) (int, error) {
	var rows []callbackRouteRow
	if err := tx.WithContext(ctx).Raw(`SELECT * FROM payment_callback_routes WHERE key_version=? ORDER BY token_digest FOR UPDATE SKIP LOCKED LIMIT ?`, source, limit).Scan(&rows).Error; err != nil {
		return 0, fmt.Errorf("claim payment callback routes: %w", err)
	}
	for _, row := range rows {
		if len(row.TokenDigest) != 32 {
			return 0, errors.New("payment callback route has invalid digest")
		}
		var digest [32]byte
		copy(digest[:], row.TokenDigest)
		if err := rotator.reencrypt(ctx, tx, "payment_callback_routes", "token_digest=? AND key_version=?", []any{row.TokenDigest, source}, row.Ciphertext, row.Nonce, row.KeyVersion, callbackAAD(digest), "ciphertext", "nonce", "key_version"); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

func (rotator *KeyRotator) reencrypt(ctx context.Context, tx *gorm.DB, table, predicate string, args []any, ciphertext, nonce []byte, version int, aad []byte, ciphertextColumn, nonceColumn, versionColumn string) error {
	plaintext, err := rotator.keyring.Decrypt(secret.Envelope{Ciphertext: ciphertext, Nonce: nonce, KeyVersion: version}, aad)
	if err != nil {
		return fmt.Errorf("decrypt %s rotation record: %w", table, err)
	}
	envelope, err := rotator.keyring.Encrypt(plaintext, aad)
	if err != nil {
		return fmt.Errorf("encrypt %s rotation record: %w", table, err)
	}
	updates := map[string]any{ciphertextColumn: envelope.Ciphertext, nonceColumn: envelope.Nonce, versionColumn: envelope.KeyVersion}
	result := tx.WithContext(ctx).Table(table).Where(predicate, args...).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update %s rotation record: %w", table, result.Error)
	}
	if result.RowsAffected != 1 {
		return errors.New("payment key rotation lost record ownership")
	}
	return nil
}
