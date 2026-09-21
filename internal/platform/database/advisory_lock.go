package database

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	paymentKeyWriterLockNamespace int32 = 1162035021
	maxPaymentKeyLockVersion      int   = 2147483647
)

type AdvisoryLock struct {
	conn      *sql.Conn
	namespace int32
	key       int
	shared    bool
}

func HoldPaymentKeyWriterLock(ctx context.Context, pool *sql.DB, version int) (*AdvisoryLock, error) {
	if version < 1 || version > maxPaymentKeyLockVersion {
		return nil, fmt.Errorf("invalid payment key writer lock version")
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open payment key writer lock connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock_shared($1,$2)`, paymentKeyWriterLockNamespace, version); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire payment key writer lock: %w", err)
	}
	return &AdvisoryLock{conn: conn, namespace: paymentKeyWriterLockNamespace, key: version, shared: true}, nil
}

func TryPaymentKeyRetirementLock(ctx context.Context, pool *sql.DB, version int) (*AdvisoryLock, bool, error) {
	if version < 1 || version > maxPaymentKeyLockVersion {
		return nil, false, fmt.Errorf("invalid payment key retirement lock version")
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("open payment key retirement lock connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1,$2)`, paymentKeyWriterLockNamespace, version).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("acquire payment key retirement lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return &AdvisoryLock{conn: conn, namespace: paymentKeyWriterLockNamespace, key: version}, true, nil
}

func (lock *AdvisoryLock) Release(ctx context.Context) error {
	if lock == nil || lock.conn == nil {
		return nil
	}
	function := "pg_advisory_unlock"
	if lock.shared {
		function = "pg_advisory_unlock_shared"
	}
	var released bool
	err := lock.conn.QueryRowContext(ctx, `SELECT `+function+`($1,$2)`, lock.namespace, lock.key).Scan(&released)
	closeErr := lock.conn.Close()
	lock.conn = nil
	if err != nil {
		return fmt.Errorf("release payment key lock: %w", err)
	}
	if !released {
		return fmt.Errorf("payment key lock was not held")
	}
	if closeErr != nil {
		return fmt.Errorf("close payment key lock connection: %w", closeErr)
	}
	return nil
}
