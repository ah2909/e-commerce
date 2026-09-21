package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	paymentsecret "github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	"github.com/ah2909/e-commerce/internal/platform/config"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/logging"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("payment key rotation failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	keyring, err := paymentsecret.New(cfg.PaymentConfigKeys, cfg.PaymentConfigActiveVersion)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	logger := logging.New(os.Stdout, slog.LevelInfo)
	connection, err := database.Open(ctx, cfg.DatabaseURL, logger, database.DefaultPoolSettings())
	if err != nil {
		return err
	}
	defer func() { _ = connection.SQL.Close() }()
	if _, err := database.VerifySchemaRange(ctx, connection.GORM, 9, database.CurrentSchemaVersion); err != nil {
		return err
	}
	rotator := paymentspostgres.NewKeyRotator(connection.GORM, keyring)
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return usage()
		}
		status, err := rotator.Status(ctx)
		if err != nil {
			return err
		}
		for _, item := range status {
			logger.Info("payment key reference", "kind", item.Kind, "key_version", item.KeyVersion, "count", item.Count)
		}
		return nil
	case "verify-retired":
		if len(args) != 2 {
			return usage()
		}
		version, err := positiveInteger(args[1], 1_000_000)
		if err != nil {
			return err
		}
		retirementLock, acquired, err := database.TryPaymentKeyRetirementLock(ctx, connection.SQL, version)
		if err != nil {
			return err
		}
		if !acquired {
			return fmt.Errorf("payment key version %d still has active writers", version)
		}
		defer func() { _ = retirementLock.Release(context.Background()) }()
		count, err := rotator.ReferenceCount(ctx, version)
		if err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("payment key version %d still has %d references", version, count)
		}
		logger.Info("payment key has no references", "key_version", version)
		return nil
	case "rotate":
		if len(args) != 4 {
			return usage()
		}
		source, err := positiveInteger(args[1], 1_000_000)
		if err != nil {
			return err
		}
		target, err := positiveInteger(args[2], 1_000_000)
		if err != nil {
			return err
		}
		batch, err := positiveInteger(args[3], 500)
		if err != nil {
			return err
		}
		var total int
		for {
			processed, err := rotator.RotateBatch(ctx, source, target, batch)
			if err != nil {
				return err
			}
			total += processed
			logger.Info("payment key rotation batch", "source_version", source, "target_version", target, "processed", processed, "total", total)
			if processed == 0 {
				return nil
			}
		}
	default:
		return usage()
	}
}

func positiveInteger(raw string, maximum int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, errors.New("numeric argument is outside its allowed range")
	}
	return value, nil
}

func usage() error {
	return errors.New("usage: payment-key-rotate status | rotate <source-version> <active-target-version> <batch> | verify-retired <version>")
}
