package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	cartsapplication "github.com/ah2909/e-commerce/internal/carts/application"
	cartspostgres "github.com/ah2909/e-commerce/internal/carts/infrastructure/postgres"
	cartshttp "github.com/ah2909/e-commerce/internal/carts/transport/http"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	catalogpostgres "github.com/ah2909/e-commerce/internal/catalog/infrastructure/postgres"
	cataloghttp "github.com/ah2909/e-commerce/internal/catalog/transport/http"
	customersapplication "github.com/ah2909/e-commerce/internal/customers/application"
	customerspostgres "github.com/ah2909/e-commerce/internal/customers/infrastructure/postgres"
	customershttp "github.com/ah2909/e-commerce/internal/customers/transport/http"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	identitypassword "github.com/ah2909/e-commerce/internal/identity/infrastructure/password"
	identitypostgres "github.com/ah2909/e-commerce/internal/identity/infrastructure/postgres"
	identitysocial "github.com/ah2909/e-commerce/internal/identity/infrastructure/social"
	"github.com/ah2909/e-commerce/internal/identity/infrastructure/token"
	identityhttp "github.com/ah2909/e-commerce/internal/identity/transport/http"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	inventoryhttp "github.com/ah2909/e-commerce/internal/inventory/transport/http"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	onboardingpostgres "github.com/ah2909/e-commerce/internal/onboarding/infrastructure/postgres"
	onboardinghttp "github.com/ah2909/e-commerce/internal/onboarding/transport/http"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	orderspostgres "github.com/ah2909/e-commerce/internal/orders/infrastructure/postgres"
	ordershttp "github.com/ah2909/e-commerce/internal/orders/transport/http"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/adminauth"
	"github.com/ah2909/e-commerce/internal/platform/clientaddr"
	"github.com/ah2909/e-commerce/internal/platform/config"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/health"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/logging"
	"github.com/ah2909/e-commerce/internal/platform/ratelimit"
	"github.com/ah2909/e-commerce/internal/platform/redisstore"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	pricingapplication "github.com/ah2909/e-commerce/internal/pricing/application"
	pricingpostgres "github.com/ah2909/e-commerce/internal/pricing/infrastructure/postgres"
	pricinghttp "github.com/ah2909/e-commerce/internal/pricing/transport/http"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingapplication "github.com/ah2909/e-commerce/internal/shipping/application"
	shippingpostgres "github.com/ah2909/e-commerce/internal/shipping/infrastructure/postgres"
	shippinghttp "github.com/ah2909/e-commerce/internal/shipping/transport/http"
	storesapplication "github.com/ah2909/e-commerce/internal/stores/application"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	storeshttp "github.com/ah2909/e-commerce/internal/stores/transport/http"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("API stopped", "error", err)
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

	startupCtx, startupCancel := context.WithTimeout(ctx, cfg.DependencyTimeout)
	defer startupCancel()
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

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddress})
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Warn("close Redis client", "error", err)
		}
	}()

	healthHandler := health.New([]health.Dependency{
		{Name: "postgresql", Check: connection.SQL.PingContext},
		{Name: "redis", Check: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	}, cfg.DependencyTimeout)
	engine := httpserver.NewEngine(logger)
	engine.Use(httpserver.CORS(cfg.CORSAllowedOrigins))
	engine.Use(clientaddr.New(cfg.TrustedProxyCIDRs).GinMiddleware())
	healthHandler.Register(engine)
	storeRepository := storespostgres.NewRepository(connection.GORM, auditpostgres.NewWriter())
	identityRepository := identitypostgres.NewRepository(connection.GORM)
	hostMiddleware := tenant.NewHostMiddleware(storesapplication.NewResolveStore(storeRepository), cfg.TenantBaseDomain, cfg.AuthHost, logger, cfg.TenantResolutionTimeout)
	passwordHasher, err := identitypassword.NewArgon2ID(cfg.PasswordHashConcurrency)
	if err != nil {
		return fmt.Errorf("initialize password hasher: %w", err)
	}
	identityService := identityapplication.NewService(identityRepository, passwordHasher, storeRepository, token.Generator{}, identityapplication.SessionSettings{AccessTTL: cfg.AccessTokenTTL, SessionTTL: cfg.SessionTTL}, time.Now)
	providerClient := &http.Client{
		Timeout:       cfg.DependencyTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	providers := make(map[identitydomain.Provider]identityapplication.SocialProvider)
	if cfg.GoogleClientID != "" {
		google, err := identitysocial.NewGoogle(ctx, providerClient, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
		if err != nil {
			return fmt.Errorf("initialize Google login: %w", err)
		}
		providers[identitydomain.ProviderGoogle] = google
	}
	if cfg.FacebookAppID != "" {
		facebook, err := identitysocial.NewFacebook(providerClient, cfg.FacebookAppID, cfg.FacebookAppSecret, cfg.FacebookRedirectURL, cfg.FacebookGraphVersion)
		if err != nil {
			return fmt.Errorf("initialize Facebook login: %w", err)
		}
		providers[identitydomain.ProviderFacebook] = facebook
	}
	socialService := identityapplication.NewSocialService(identityRepository, identityService, redisstore.NewSocial(redisClient, cfg.DependencyTimeout), providers, cfg.SocialReturnPaths, identityapplication.SocialSettings{TransactionTTL: cfg.SocialTransactionTTL, ExchangeTTL: cfg.SocialExchangeTTL}, time.Now)
	identitySettings := identityhttp.Settings{
		OperationTimeout: cfg.DependencyTimeout,
		TenantBaseDomain: cfg.TenantBaseDomain,
		AuthHost:         cfg.AuthHost,
		Registration:     identityhttp.CredentialRatePolicy{PeerMaximum: int64(cfg.RegistrationRateLimit.PeerMaximum), IdentityMaximum: int64(cfg.RegistrationRateLimit.IdentityMaximum), Window: cfg.RegistrationRateLimit.Window},
		Login:            identityhttp.CredentialRatePolicy{PeerMaximum: int64(cfg.LoginRateLimit.PeerMaximum), IdentityMaximum: int64(cfg.LoginRateLimit.IdentityMaximum), Window: cfg.LoginRateLimit.Window},
		Token:            identityhttp.CredentialRatePolicy{PeerMaximum: int64(cfg.TokenRateLimit.PeerMaximum), IdentityMaximum: int64(cfg.TokenRateLimit.IdentityMaximum), Window: cfg.TokenRateLimit.Window},
		Social:           identityhttp.CredentialRatePolicy{PeerMaximum: int64(cfg.SocialRateLimit.PeerMaximum), IdentityMaximum: int64(cfg.SocialRateLimit.IdentityMaximum), Window: cfg.SocialRateLimit.Window},
		GuestCreation:    identityhttp.RatePolicy{Maximum: int64(cfg.GuestCreationRateLimit.Maximum), Window: cfg.GuestCreationRateLimit.Window},
	}
	identityHandler := identityhttp.NewHandler(identityService, socialService, ratelimit.NewRedis(redisClient, cfg.DependencyTimeout), logger, identitySettings)
	identityHandler.Register(engine, hostMiddleware)
	onboardingRepository := onboardingpostgres.NewRepository(connection.GORM, storeRepository, identityRepository)
	onboardingService := onboardingapplication.NewService(onboardingRepository, identityService, ids.NewStoreID, time.Now)
	onboardingHandler := onboardinghttp.NewHandler(onboardingService, ratelimit.NewRedis(redisClient, cfg.DependencyTimeout), logger, cfg.DependencyTimeout, int64(cfg.PlatformProvisionRateLimit.Maximum), cfg.PlatformProvisionRateLimit.Window)
	onboardingHandler.Register(engine, hostMiddleware, adminauth.New(cfg.PlatformAdminToken).AuthenticateGin())
	membershipManager := storesapplication.NewMembershipManager(storeRepository, identityRepository, time.Now)
	tenantMiddleware := tenant.NewMiddleware(storesapplication.NewResolveTenant(storeRepository), logger, cfg.TenantResolutionTimeout)
	storesHandler := storeshttp.NewHandler(membershipManager, identityHandler.AuthenticateUser(), tenantMiddleware, logger, cfg.DependencyTimeout)
	storesHandler.Register(engine, hostMiddleware)
	if catalogAvailable(schemaVersion) {
		catalogRepository := catalogpostgres.NewRepository(connection.GORM)
		catalogService := catalogapplication.NewService(catalogRepository, catalogapplication.DefaultIDGenerator(), time.Now)
		var pricingService pricingapplication.Service
		var inventoryService inventoryapplication.Service
		var pricingRepository *pricingpostgres.Repository
		var inventoryRepository *inventorypostgres.Repository
		if pricingAvailable(schemaVersion) {
			pricingRepository = pricingpostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker())
			pricingService = pricingapplication.NewService(pricingRepository, ids.NewPriceListID, time.Now)
			publication := catalogpostgres.NewPublicationCoordinator(connection.GORM, pricingRepository, eventingpostgres.NewWriter(), ids.NewEventID)
			catalogService = catalogService.WithPublication(publication, pricingRepository)
			pricingHandler := pricinghttp.NewHandler(pricingService, identityHandler.AuthenticateUser(), tenantMiddleware, logger, cfg.DependencyTimeout)
			pricingHandler.Register(engine, hostMiddleware)
		}
		if inventoryAvailable(schemaVersion) {
			inventoryRepository = inventorypostgres.NewRepository(connection.GORM, catalogpostgres.NewVariantLocker(), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
			inventoryService = inventoryapplication.NewService(inventoryRepository)
			catalogService = catalogService.WithAvailability(inventoryService)
			inventoryHandler := inventoryhttp.NewHandler(inventoryService, identityHandler.AuthenticateUser(), tenantMiddleware, logger, cfg.DependencyTimeout)
			inventoryHandler.Register(engine, hostMiddleware)
		}
		catalogHandler := cataloghttp.NewHandler(catalogService, identityHandler.AuthenticateUser(), tenantMiddleware, logger, cfg.DependencyTimeout)
		catalogHandler.Register(engine, hostMiddleware)
		if pricingAvailable(schemaVersion) {
			catalogHandler.RegisterPublication(engine, hostMiddleware)
		}
		if customersAvailable(schemaVersion) {
			customerRepository := customerspostgres.NewRepository(connection.GORM)
			customerService := customersapplication.NewService(customerRepository)
			customershttp.NewHandler(customerService, identityHandler.AuthenticateShopper(), logger, cfg.DependencyTimeout).Register(engine, hostMiddleware)
			cartRepository := cartspostgres.NewRepository(connection.GORM)
			cartService := cartsapplication.NewService(cartRepository, catalogService, pricingService, inventoryService)
			cartshttp.NewHandler(cartService, identityHandler.AuthenticateShopper(), logger, cfg.DependencyTimeout).Register(engine, hostMiddleware)
			if ordersAvailable(schemaVersion) {
				shippingRepository := shippingpostgres.NewRepository(connection.GORM)
				shippingService := shippingapplication.NewService(shippingRepository)
				shippinghttp.NewHandler(shippingService, identityHandler.AuthenticateUser(), tenantMiddleware, logger, cfg.DependencyTimeout).Register(engine, hostMiddleware)
				orderRepository := orderspostgres.NewRepository(connection.GORM, cartRepository, catalogRepository, pricingRepository, customerRepository, shippingRepository, inventoryRepository, paymentspostgres.NewParticipant(), eventingpostgres.NewWriter())
				orderService := ordersapplication.NewService(orderRepository)
				ordershttp.NewHandler(orderService, identityService, identityHandler.AuthenticateShopper(), logger, cfg.DependencyTimeout).Register(engine, hostMiddleware)
			}
		}
	} else {
		logger.Info("Catalog routes await schema migration", "schema_version", schemaVersion)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      apiWriteTimeout(cfg.DependencyTimeout),
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("listen on HTTP address: %w", err)
	}

	logger.Info("API started", "environment", cfg.AppEnv, "address", cfg.HTTPAddress)
	if err := httpserver.Serve(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		return err
	}
	logger.Info("API stopped")
	return nil
}

func catalogAvailable(schemaVersion int64) bool   { return schemaVersion >= 3 }
func pricingAvailable(schemaVersion int64) bool   { return schemaVersion >= 4 }
func inventoryAvailable(schemaVersion int64) bool { return schemaVersion >= 5 }
func customersAvailable(schemaVersion int64) bool { return schemaVersion >= 6 }
func ordersAvailable(schemaVersion int64) bool    { return schemaVersion >= 7 }

func apiWriteTimeout(dependencyTimeout time.Duration) time.Duration {
	return max(15*time.Second, dependencyTimeout+5*time.Second)
}
