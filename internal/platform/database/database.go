package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const CurrentSchemaVersion int64 = 2

type PoolSettings struct {
	MaxOpenConnections int
	MaxIdleConnections int
	ConnectionLifetime time.Duration
	ConnectionIdleTime time.Duration
}

func DefaultPoolSettings() PoolSettings {
	return PoolSettings{MaxOpenConnections: 20, MaxIdleConnections: 10, ConnectionLifetime: 30 * time.Minute, ConnectionIdleTime: 5 * time.Minute}
}

type Connection struct {
	GORM *gorm.DB
	SQL  *sql.DB
}

func Open(ctx context.Context, databaseURL string, logger *slog.Logger, settings PoolSettings) (Connection, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{DisableAutomaticPing: true, Logger: redactedLogger{logger: logger, level: gormlogger.Error}})
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
	var version int64
	err := db.WithContext(ctx).Raw(`SELECT COALESCE(MAX(version_id),0) FROM goose_db_version WHERE is_applied`).Scan(&version).Error
	if err != nil {
		return fmt.Errorf("read Goose schema version: %w", err)
	}
	if version != expected {
		return fmt.Errorf("incompatible Goose schema version: got %d, want %d", version, expected)
	}
	return nil
}

type redactedLogger struct {
	logger *slog.Logger
	level  gormlogger.LogLevel
}

func (logger redactedLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	logger.level = level
	return logger
}

func (logger redactedLogger) Info(context.Context, string, ...any)  {}
func (logger redactedLogger) Warn(context.Context, string, ...any)  {}
func (logger redactedLogger) Error(context.Context, string, ...any) {}

func (logger redactedLogger) Trace(ctx context.Context, started time.Time, result func() (string, int64), err error) {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) || logger.level == gormlogger.Silent || logger.logger == nil {
		return
	}
	logger.logger.Error("database operation failed", "request_id", httpserver.RequestID(ctx), "duration_ms", time.Since(started).Milliseconds(), "error", err)
}
