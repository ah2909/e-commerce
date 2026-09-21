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
	"github.com/ah2909/e-commerce/internal/platform/observability"
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
	connection, err := database.Open(startupCtx, cfg.DatabaseURL, logger, database.PoolSettings{MaxOpenConnections: cfg.DatabaseMaxOpen, MaxIdleConnections: cfg.DatabaseMaxIdle, ConnectionLifetime: cfg.DatabaseConnectionLifetime, ConnectionIdleTime: cfg.DatabaseConnectionIdleTime, StatementTimeout: cfg.DatabaseStatementTimeout, LockTimeout: cfg.DatabaseLockTimeout, IdleTransaction: cfg.DatabaseIdleTransaction, SlowQueryThreshold: cfg.SlowQueryThreshold})
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
	metrics := observability.NewRegistry(connection.SQL)
	go func() {
		if err := observability.Serve(ctx, cfg.MetricsAddress, metrics.Handler(), cfg.ShutdownTimeout); err != nil && ctx.Err() == nil {
			logger.Error("metrics server stopped", "error", err)
			stop()
		}
	}()

	logger.Info("worker started", "environment", cfg.AppEnv, "kafka_broker_count", len(cfg.KafkaBrokers))
	if schemaVersion >= 6 {
		go runCartExpiry(ctx, cartspostgres.NewRepository(connection.GORM), cfg.CartExpiryBatch, cfg.CartExpiryPoll, cfg.DependencyTimeout, logger, metrics)
	}
	if schemaVersion >= 7 {
		catalogRepository := catalogpostgres.NewRepository(connection.GORM)
		pricingRepository := pricingpostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker())
		inventoryRepository := inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		orderRepository := orderspostgres.NewRepository(connection.GORM, cartspostgres.NewRepository(connection.GORM), catalogRepository, pricingRepository, customerspostgres.NewRepository(connection.GORM), shippingpostgres.NewRepository(connection.GORM), inventoryRepository, paymentspostgres.NewParticipant(), eventingpostgres.NewWriter())
		orderService := ordersapplication.NewService(orderRepository)
		go runExpiry(ctx, orderService, cfg.InventoryExpiryBatch, cfg.InventoryExpiryPoll, cfg.DependencyTimeout, logger, metrics)
		if schemaVersion >= 12 {
			kafkaSecurity := eventingkafka.Security{TLS: cfg.KafkaTLSEnabled, Username: cfg.KafkaSASLUsername, Password: cfg.KafkaSASLPassword}
			producer, err := eventingkafka.NewProducerWithSecurity(cfg.KafkaBrokers, cfg.DependencyTimeout, kafkaSecurity)
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
			eventWorkers.Add(5)
			go func() {
				defer eventWorkers.Done()
				runPublisher(ctx, publisher, cfg.Eventing.Poll, logger, metrics)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, kafkaSecurity, "search-projection-v1", []string{eventapplication.TopicCatalog}, cfg.DependencyTimeout, searchConsumer, logger, metrics)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, kafkaSecurity, "analytics-projection-v1", []string{eventapplication.TopicCatalog, eventapplication.TopicInventory, eventapplication.TopicOrders, eventapplication.TopicPayments, eventapplication.TopicShipping}, cfg.DependencyTimeout, analyticsConsumer, logger, metrics)
			}()
			go func() {
				defer eventWorkers.Done()
				runConsumer(ctx, cfg.KafkaBrokers, kafkaSecurity, "email-intents-v1", []string{eventapplication.TopicOrders}, cfg.DependencyTimeout, emailConsumer, logger, metrics)
			}()
			go func() {
				defer eventWorkers.Done()
				runEventMetrics(ctx, eventingpostgres.NewOutbox(connection.GORM), eventingpostgres.NewDelivery(connection.GORM, nil), metrics, logger)
			}()
			<-ctx.Done()
			eventWorkers.Wait()
			logger.Info("event workers drained")
		}
		<-ctx.Done()
	} else if schemaVersion >= 5 {
		repository := inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		service := inventoryapplication.NewService(repository)
		runExpiry(ctx, service, cfg.InventoryExpiryBatch, cfg.InventoryExpiryPoll, cfg.DependencyTimeout, logger, metrics)
	} else {
		<-ctx.Done()
	}
	logger.Info("worker stopped")
	return nil
}

type eventBacklogReader interface {
	Backlog(context.Context) (eventingpostgres.Backlog, error)
}

type deadLetterBacklogReader interface {
	DeadLetterBacklog(context.Context) (eventingpostgres.DeadLetterBacklog, error)
}

func runEventMetrics(ctx context.Context, outbox eventBacklogReader, delivery deadLetterBacklogReader, registry *observability.Registry, logger *slog.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		backlog, err := outbox.Backlog(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("collect outbox metrics", "error", err)
		} else if err == nil {
			registry.Set("commerce_outbox_records", "Outbox publication state.", map[string]string{"state": "due"}, float64(backlog.Due))
			registry.Set("commerce_outbox_records", "Outbox publication state.", map[string]string{"state": "retrying"}, float64(backlog.Retrying))
			registry.Set("commerce_outbox_records", "Outbox publication state.", map[string]string{"state": "terminal"}, float64(backlog.Terminal))
			age := float64(0)
			if backlog.OldestDue != nil {
				age = max(0, time.Since(*backlog.OldestDue).Seconds())
			}
			registry.Set("commerce_outbox_oldest_due_seconds", "Age of the oldest currently due outbox record.", nil, age)
		}
		deadLetters, err := delivery.DeadLetterBacklog(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("collect dead-letter metrics", "error", err)
		} else if err == nil {
			registry.Set("commerce_event_dead_letters", "Consumer dead-letter state.", map[string]string{"state": "open"}, float64(deadLetters.Open))
			registry.Set("commerce_event_dead_letters", "Consumer dead-letter state.", map[string]string{"state": "retryable"}, float64(deadLetters.Retryable))
			registry.Set("commerce_event_dead_letters", "Consumer dead-letter state.", map[string]string{"state": "terminal"}, float64(deadLetters.Terminal))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type batchPublisher interface {
	ProcessBatch(context.Context) (int, error)
}

func runPublisher(ctx context.Context, publisher batchPublisher, poll time.Duration, logger *slog.Logger, registries ...*observability.Registry) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		processed, err := publisher.ProcessBatch(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("outbox publication failed", "error", err)
			for _, registry := range registries {
				registry.Inc("commerce_worker_operations_total", "Worker operation outcomes.", map[string]string{"operation": "outbox_publish", "result": "error"})
			}
		} else if processed > 0 {
			logger.Info("outbox publication batch completed", "count", processed)
			for _, registry := range registries {
				registry.Add("commerce_worker_records_total", "Worker records completed.", map[string]string{"operation": "outbox_publish"}, float64(processed))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runConsumer(ctx context.Context, brokers []string, security eventingkafka.Security, group string, topics []string, timeout time.Duration, processor eventingkafka.Processor, logger *slog.Logger, registries ...*observability.Registry) {
	for ctx.Err() == nil {
		err := eventingkafka.RunConsumer(ctx, eventingkafka.ConsumerSettings{Brokers: brokers, Group: group, Topics: topics, Timeout: timeout, Security: security}, processor)
		if err != nil && ctx.Err() == nil {
			logger.Error("event consumer stopped", "group", group, "error", err)
			for _, registry := range registries {
				registry.Inc("commerce_worker_operations_total", "Worker operation outcomes.", map[string]string{"operation": group, "result": "error"})
			}
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

func runCartExpiry(ctx context.Context, processor cartExpiryProcessor, batch int, poll, timeout time.Duration, logger *slog.Logger, registries ...*observability.Registry) {
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
			for _, registry := range registries {
				registry.Inc("commerce_worker_operations_total", "Worker operation outcomes.", map[string]string{"operation": "cart_expiry", "result": "error"})
			}
		} else if processed > 0 {
			logger.Info("carts expired", "count", processed)
			for _, registry := range registries {
				registry.Add("commerce_worker_records_total", "Worker records completed.", map[string]string{"operation": "cart_expiry"}, float64(processed))
			}
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

func runExpiry(ctx context.Context, processor expiryProcessor, batch int, poll, timeout time.Duration, logger *slog.Logger, registries ...*observability.Registry) {
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
				for _, registry := range registries {
					registry.Inc("commerce_worker_operations_total", "Worker operation outcomes.", map[string]string{"operation": "order_expiry", "result": "error"})
				}
			} else if processed > 0 {
				logger.Info("pending orders expired", "count", processed)
				for _, registry := range registries {
					registry.Add("commerce_worker_records_total", "Worker records completed.", map[string]string{"operation": "order_expiry"}, float64(processed))
				}
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
