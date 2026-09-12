package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

type blockingDriver struct{}
type blockingConnection struct{}

func (blockingDriver) Open(string) (driver.Conn, error) { return blockingConnection{}, nil }
func (blockingConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (blockingConnection) Close() error              { return nil }
func (blockingConnection) Begin() (driver.Tx, error) { return nil, errors.New("unsupported") }
func (blockingConnection) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func init() { sql.Register("task007_blocking_ping", blockingDriver{}) }

func TestRedactedLoggerDoesNotEvaluateSuccessfulSQL(t *testing.T) {
	evaluated := false
	redactedLogger{logger: slog.New(slog.DiscardHandler), level: logger.Error}.Trace(context.Background(), time.Now(), func() (string, int64) {
		evaluated = true
		return "secret", 1
	}, nil)
	if evaluated {
		t.Fatal("successful SQL was evaluated for logging")
	}
}

func TestRedactedLoggerDoesNotEvaluateFailedSQL(t *testing.T) {
	evaluated := false
	redactedLogger{logger: slog.New(slog.DiscardHandler), level: logger.Error}.Trace(context.Background(), time.Now(), func() (string, int64) {
		evaluated = true
		return "password_hash=secret", 0
	}, errors.New("database unavailable"))
	if evaluated {
		t.Fatal("failed SQL was evaluated for logging")
	}
}

func TestConfigureAndPingHonorsContextDeadline(t *testing.T) {
	db, err := sql.Open("task007_blocking_ping", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := configureAndPing(ctx, db, DefaultPoolSettings()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("configureAndPing() error = %v", err)
	}
}

func TestDefaultPoolSettingsAreBounded(t *testing.T) {
	settings := DefaultPoolSettings()
	if settings.MaxOpenConnections <= 0 || settings.MaxIdleConnections < 0 || settings.MaxIdleConnections > settings.MaxOpenConnections || settings.ConnectionLifetime <= 0 || settings.ConnectionIdleTime <= 0 {
		t.Fatalf("invalid pool settings: %+v", settings)
	}
}

func TestSchemaCompatibilityRange(t *testing.T) {
	for _, version := range []int64{MinimumCompatibleSchemaVersion, CurrentSchemaVersion} {
		if err := validateSchemaVersion(version, MinimumCompatibleSchemaVersion, CurrentSchemaVersion); err != nil {
			t.Fatalf("version %d: %v", version, err)
		}
	}
	for _, version := range []int64{MinimumCompatibleSchemaVersion - 1, CurrentSchemaVersion + 1} {
		if err := validateSchemaVersion(version, MinimumCompatibleSchemaVersion, CurrentSchemaVersion); err == nil {
			t.Fatalf("version %d was accepted", version)
		}
	}
}
