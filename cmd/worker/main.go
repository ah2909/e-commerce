package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	analyticspostgres "github.com/ah2909/e-commerce/internal/analytics/infrastructure/postgres"
	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	cartspostgres "github.com/ah2909/e-commerce/internal/carts/infrastructure/postgres"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	customerspostgres "github.com/ah2909/e-commerce/internal/customers/infrastructure/postgres"
	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventingkafka "github.com/ah2909/e-commerce/internal/eventing/infrastructure/kafka"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	notificationspostgres "github.com/ah2909/e-commerce/internal/notifications/infrastructure/postgres"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	orderspostgres "github.com/ah2909/e-commerce/internal/orders/infrastructure/postgres"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/config"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/logging"
	pricingpostgres "github.com/ah2909/e-commerce/internal/pricing/infrastructure/postgres"
	searchpostgres "github.com/ah2909/e-commerce/internal/search/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingpostgres "github.com/ah2909/e-commerce/internal/shipping/infrastructure/postgres"
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
	if schemaVersion >= 6 {
		go runCartExpiry(ctx, cartspostgres.NewRepository(connection.GORM), cfg.CartExpiryBatch, cfg.CartExpiryPoll, cfg.DependencyTimeout, logger)
	}
	if schemaVersion >= 7 {
		catalogRepository := catalogpostgres.NewRepository(connection.GORM)
		pricingRepository := pricingpostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker())
		inventoryRepository := inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		orderRepository := orderspostgres.NewRepository(connection.GORM, cartspostgres.NewRepository(connection.GORM), catalogRepository, pricingRepository, customerspostgres.NewRepository(connection.GORM), shippingpostgres.NewRepository(connection.GORM), inventoryRepository, paymentspostgres.NewParticipant(), eventingpostgres.NewWriter())
		orderService := ordersapplication.NewService(orderRepository)
		go runExpiry(ctx, orderService, cfg.InventoryExpiryBatch, cfg.InventoryExpiryPoll, cfg.DependencyTimeout, logger)
		if schemaVersion >= 12 {
			producer, err := eventingkafka.NewProducer(cfg.KafkaBrokers, cfg.DependencyTimeout)
			if err != nil {
				return err
			}
			defer producer.Close()
			publisher, err := eventapplication.NewPublisher(eventingpostgres.NewOutbox(connection.GORM), producer, eventapplication.PublisherSettings{Batch: cfg.Eventing.Batch, Lease: cfg.Eventing.Lease, MaxAttempts: cfg.Eventing.MaxAttempts, BaseBackoff: cfg.Eventing.RetryBase, MaxBackoff: cfg.Eventing.RetryMax, PublishTimeout: cfg.DependencyTimeout}, time.Now)
			if err != nil {
				return fmt.Errorf("initialize outbox publisher: %w", err)
			}
			catalogService := catalogapplication.NewService(catalogRepository, catalogapplication.DefaultIDGenerator(), time.Now).WithPublication(nil, pricingRepository)
			searchConsumer, err := eventapplication.NewConsumer("search-projection-v1", eventingpostgres.NewDelivery(connection.GORM, searchpostgres.NewEffect(catalogService)), cfg.Eventing.MaxAttempts, time.Now)
			if err != nil {
				return err
			}
			analyticsConsumer, err := eventapplication.NewConsumer("analytics-projection-v1", eventingpostgres.NewDelivery(connection.GORM, analyticspostgres.NewEffect()), cfg.Eventing.MaxAttempts, time.Now)
			if err != nil {
				return err
			}
			emailConsumer, err := eventapplication.NewConsumer("email-intents-v1", eventingpostgres.NewDelivery(connection.GORM, notificationspostgres.NewEffect(orderService)), cfg.Eventing.MaxAttempts, time.Now)
			if err != nil {
				return err
			}
			var eventWorkers sync.WaitGroup
			eventWorkers.Add(4)
			go func() {
				defer eventWorkers.Done()
				runPublisher(ctx, publisher, cfg.Eventing.Poll, logger)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, "search-projection-v1", []string{eventapplication.TopicCatalog}, cfg.DependencyTimeout, searchConsumer, logger)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, "analytics-projection-v1", []string{eventapplication.TopicCatalog, eventapplication.TopicInventory, eventapplication.TopicOrders, eventapplication.TopicPayments, eventapplication.TopicShipping}, cfg.DependencyTimeout, analyticsConsumer, logger)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, "email-intents-v1", []string{eventapplication.TopicOrders}, cfg.DependencyTimeout, emailConsumer, logger)
			}()
			<-ctx.Done()
			eventWorkers.Wait()
			logger.Info("event workers drained")
		}
		<-ctx.Done()
	} else if schemaVersion >= 5 {
		repository := inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		service := inventoryapplication.NewService(repository)
		runExpiry(ctx, service, cfg.InventoryExpiryBatch, cfg.InventoryExpiryPoll, cfg.DependencyTimeout, logger)
	} else {
		<-ctx.Done()
	}
	logger.Info("worker stopped")
	return nil
}

type batchPublisher interface {
	ProcessBatch(context.Context) (int, error)
}

func runPublisher(ctx context.Context, publisher batchPublisher, poll time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		processed, err := publisher.ProcessBatch(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("outbox publication failed", "error", err)
		} else if processed > 0 {
			logger.Info("outbox publication batch completed", "count", processed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runConsumer(ctx context.Context, brokers []string, group string, topics []string, timeout time.Duration, processor eventingkafka.Processor, logger *slog.Logger) {
	for ctx.Err() == nil {
		err := eventingkafka.RunConsumer(ctx, eventingkafka.ConsumerSettings{Brokers: brokers, Group: group, Topics: topics, Timeout: timeout}, processor)
		if err != nil && ctx.Err() == nil {
			logger.Error("event consumer stopped", "group", group, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

type cartExpiryProcessor interface {
	ExpireDue(context.Context, int) (int, error)
}

func runCartExpiry(ctx context.Context, processor cartExpiryProcessor, batch int, poll, timeout time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		operationCtx, cancel := context.WithTimeout(ctx, timeout)
		processed, err := processor.ExpireDue(operationCtx, batch)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("cart expiry failed", "error", err)
		} else if processed > 0 {
			logger.Info("carts expired", "count", processed)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
				logger.Error("order expiry failed", "error", processErr)
			} else if processed > 0 {
				logger.Info("pending orders expired", "count", processed)
			}
		} else {
			logger.Error("order expiry correlation failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
