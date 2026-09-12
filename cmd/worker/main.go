package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/config"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/logging"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancel := context.WithTimeout(ctx, cfg.DependencyTimeout)
	defer cancel()
	connection, err := database.Open(startupCtx, cfg.DatabaseURL, logger, database.DefaultPoolSettings())
	if err != nil {
		return err
	}
	defer func() {
		if err := connection.SQL.Close(); err != nil {
			logger.Warn("close PostgreSQL pool", "error", err)
		}
	}()
	schemaVersion, err := database.VerifySchemaRange(startupCtx, connection.GORM, database.MinimumCompatibleSchemaVersion, database.CurrentSchemaVersion)
	if err != nil {
		return err
	}

	logger.Info("worker started", "environment", cfg.AppEnv, "kafka_broker_count", len(cfg.KafkaBrokers))
	if schemaVersion >= 5 {
		repository := inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		service := inventoryapplication.NewService(repository)
		runExpiry(ctx, service, cfg.InventoryExpiryBatch, cfg.InventoryExpiryPoll, cfg.DependencyTimeout, logger)
	} else {
		<-ctx.Done()
	}
	logger.Info("worker stopped")
	return nil
}

type expiryProcessor interface {
	ExpireDue(context.Context, int, string) (int, error)
}

func runExpiry(ctx context.Context, processor expiryProcessor, batch int, poll, timeout time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		operationCtx, cancel := context.WithTimeout(ctx, timeout)
		correlationID, err := ids.NewEventID()
		if err == nil {
			processed, processErr := processor.ExpireDue(operationCtx, batch, correlationID.String())
			if processErr != nil && !errors.Is(processErr, context.Canceled) {
				logger.Error("inventory expiry failed", "error", processErr)
			} else if processed > 0 {
				logger.Info("inventory reservations expired", "count", processed)
			}
		} else {
			logger.Error("inventory expiry correlation failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
