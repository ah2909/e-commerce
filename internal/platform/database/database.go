package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const CurrentSchemaVersion int64 = 13
const MinimumCompatibleSchemaVersion int64 = 3

type PoolSettings struct {
	MaxOpenConnections int
	MaxIdleConnections int
	ConnectionLifetime time.Duration
	ConnectionIdleTime time.Duration
	StatementTimeout   time.Duration
	LockTimeout        time.Duration
	IdleTransaction    time.Duration
	SlowQueryThreshold time.Duration
}

func DefaultPoolSettings() PoolSettings {
	return PoolSettings{MaxOpenConnections: 20, MaxIdleConnections: 10, ConnectionLifetime: 30 * time.Minute, ConnectionIdleTime: 5 * time.Minute, StatementTimeout: 15 * time.Second, LockTimeout: 3 * time.Second, IdleTransaction: 15 * time.Second, SlowQueryThreshold: 500 * time.Millisecond}
}

type Connection struct {
	GORM *gorm.DB
	SQL  *sql.DB
}

func Open(ctx context.Context, databaseURL string, logger *slog.Logger, settings PoolSettings) (Connection, error) {
	databaseURL, err := withRuntimeTimeouts(databaseURL, settings)
	if err != nil {
		return Connection{}, err
	}
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{DisableAutomaticPing: true, Logger: redactedLogger{logger: logger, level: gormlogger.Error, slow: settings.SlowQueryThreshold}})
	if err != nil {
		return Connection{}, errors.New("initialize PostgreSQL: invalid connection configuration")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return Connection{}, fmt.Errorf("access PostgreSQL pool: %w", err)
	}
	if err := configureAndPing(ctx, sqlDB, settings); err != nil {
		_ = sqlDB.Close()
		return Connection{}, err
	}
	return Connection{GORM: db, SQL: sqlDB}, nil
}

func withRuntimeTimeouts(databaseURL string, settings PoolSettings) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", errors.New("initialize PostgreSQL: invalid connection configuration")
	}
	query := parsed.Query()
	query.Set("statement_timeout", strconv.FormatInt(settings.StatementTimeout.Milliseconds(), 10))
	query.Set("lock_timeout", strconv.FormatInt(settings.LockTimeout.Milliseconds(), 10))
	query.Set("idle_in_transaction_session_timeout", strconv.FormatInt(settings.IdleTransaction.Milliseconds(), 10))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func configureAndPing(ctx context.Context, sqlDB *sql.DB, settings PoolSettings) error {
	sqlDB.SetMaxOpenConns(settings.MaxOpenConnections)
	sqlDB.SetMaxIdleConns(settings.MaxIdleConnections)
	sqlDB.SetConnMaxLifetime(settings.ConnectionLifetime)
	sqlDB.SetConnMaxIdleTime(settings.ConnectionIdleTime)
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	return nil
}

func VerifySchema(ctx context.Context, db *gorm.DB, expected int64) error {
	_, err := VerifySchemaRange(ctx, db, expected, expected)
	return err
}

func VerifySchemaRange(ctx context.Context, db *gorm.DB, minimum, maximum int64) (int64, error) {
	var version int64
	err := db.WithContext(ctx).Raw(`SELECT COALESCE(MAX(version_id),0) FROM goose_db_version WHERE is_applied`).Scan(&version).Error
	if err != nil {
		return 0, fmt.Errorf("read Goose schema version: %w", err)
	}
	if err := validateSchemaVersion(version, minimum, maximum); err != nil {
		return 0, err
	}
	return version, nil
}

func validateSchemaVersion(version, minimum, maximum int64) error {
	if minimum < 1 || maximum < minimum || version < minimum || version > maximum {
		return fmt.Errorf("incompatible Goose schema version: got %d, want %d through %d", version, minimum, maximum)
	}
	return nil
}

type redactedLogger struct {
	logger *slog.Logger
	level  gormlogger.LogLevel
	slow   time.Duration
}

func (logger redactedLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	logger.level = level
	return logger
}

func (logger redactedLogger) Info(context.Context, string, ...any)  {}
func (logger redactedLogger) Warn(context.Context, string, ...any)  {}
func (logger redactedLogger) Error(context.Context, string, ...any) {}

func (logger redactedLogger) Trace(ctx context.Context, started time.Time, result func() (string, int64), err error) {
	if logger.level == gormlogger.Silent || logger.logger == nil {
		return
	}
	duration := time.Since(started)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.logger.Error("database operation failed", "request_id", httpserver.RequestID(ctx), "duration_ms", duration.Milliseconds(), "error", err)
		return
	}
	if logger.slow > 0 && duration >= logger.slow {
		logger.logger.Warn("slow database operation", "request_id", httpserver.RequestID(ctx), "duration_ms", duration.Milliseconds())
	}
}
