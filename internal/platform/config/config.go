package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

const (
	defaultAppEnv                  = "development"
	defaultHTTPAddress             = ":8080"
	defaultLogLevel                = "info"
	defaultShutdownTimeout         = 10 * time.Second
	defaultDependencyTimeout       = 2 * time.Second
	defaultTenantResolutionTimeout = 500 * time.Millisecond
	defaultPasswordHashConcurrency = 2
	defaultFacebookGraphVersion    = "v23.0"
	defaultAccessTokenTTL          = 15 * time.Minute
	defaultSessionTTL              = 30 * 24 * time.Hour
	defaultSocialTransactionTTL    = 10 * time.Minute
	defaultSocialExchangeTTL       = time.Minute
	defaultTenantBaseDomain        = "example.test"
	defaultAuthHost                = "auth.example.test"
	maxShutdownTimeout             = 2 * time.Minute
	maxDependencyTimeout           = 30 * time.Second
	maxTenantResolutionTimeout     = 5 * time.Second
	maxAccessTokenTTL              = time.Hour
	maxSessionTTL                  = 90 * 24 * time.Hour
	maxSocialTransactionTTL        = 30 * time.Minute
	maxSocialExchangeTTL           = 5 * time.Minute
	maxRateWindow                  = 24 * time.Hour
	defaultInventoryExpiryPoll     = 5 * time.Second
	maxInventoryExpiryPoll         = time.Minute
	defaultCartExpiryPoll          = time.Minute
	maxCartExpiryPoll              = 15 * time.Minute
	defaultMoMoRequestTimeout      = 35 * time.Second
	maxMoMoRequestTimeout          = 60 * time.Second
	defaultEventPoll               = time.Second
	defaultEventLease              = 30 * time.Second
	defaultEventRetryBase          = time.Second
	defaultEventRetryMax           = 5 * time.Minute
	defaultDBMaxOpen               = 20
	defaultDBMaxIdle               = 10
	defaultDBConnectionLifetime    = 30 * time.Minute
	defaultDBConnectionIdleTime    = 5 * time.Minute
	defaultDBStatementTimeout      = 15 * time.Second
	defaultDBLockTimeout           = 3 * time.Second
	defaultDBIdleTransaction       = 15 * time.Second
	defaultSlowQueryThreshold      = 500 * time.Millisecond
)

type RateLimit struct {
	Maximum int
	Window  time.Duration
}

type CredentialRateLimit struct {
	PeerMaximum     int
	IdentityMaximum int
	Window          time.Duration
}

type Eventing struct {
	Batch       int
	Poll        time.Duration
	Lease       time.Duration
	MaxAttempts int
	RetryBase   time.Duration
	RetryMax    time.Duration
}

type RoutingMode string

const (
	RoutingMultiStore  RoutingMode = "multi_store"
	RoutingSingleStore RoutingMode = "single_store"
)

type Config struct {
	AppEnv                     string
	HTTPAddress                string
	MetricsAddress             string
	OTLPTraceEndpoint          string
	LogLevel                   slog.Level
	DatabaseURL                string
	DatabaseMaxOpen            int
	DatabaseMaxIdle            int
	DatabaseConnectionLifetime time.Duration
	DatabaseConnectionIdleTime time.Duration
	DatabaseStatementTimeout   time.Duration
	DatabaseLockTimeout        time.Duration
	DatabaseIdleTransaction    time.Duration
	SlowQueryThreshold         time.Duration
	RedisAddress               string
	RedisTLSEnabled            bool
	KafkaBrokers               []string
	KafkaTLSEnabled            bool
	KafkaSASLUsername          string
	KafkaSASLPassword          string
	ShutdownTimeout            time.Duration
	DependencyTimeout          time.Duration
	TenantResolutionTimeout    time.Duration
	PasswordHashConcurrency    int
	AccessTokenTTL             time.Duration
	SessionTTL                 time.Duration
	SocialTransactionTTL       time.Duration
	SocialExchangeTTL          time.Duration
	TenantBaseDomain           string
	RoutingMode                RoutingMode
	PlatformHost               string
	SingleStoreHost            string
	SingleStoreID              ids.StoreID
	AuthHost                   string
	PlatformAdminToken         [32]byte
	TrustedProxyCIDRs          []netip.Prefix
	CORSAllowedOrigins         []string
	RegistrationRateLimit      CredentialRateLimit
	LoginRateLimit             CredentialRateLimit
	TokenRateLimit             CredentialRateLimit
	SocialRateLimit            CredentialRateLimit
	GuestCreationRateLimit     RateLimit
	PlatformProvisionRateLimit RateLimit
	GoogleClientID             string
	GoogleClientSecret         string
	GoogleRedirectURL          string
	FacebookAppID              string
	FacebookAppSecret          string
	FacebookRedirectURL        string
	FacebookGraphVersion       string
	SocialReturnPaths          []string
	InventoryExpiryBatch       int
	InventoryExpiryPoll        time.Duration
	CartExpiryBatch            int
	CartExpiryPoll             time.Duration
	PaymentConfigKeys          map[int][32]byte
	PaymentConfigActiveVersion int
	MoMoAPIBaseURL             string
	MoMoRedirectURL            string
	MoMoPublicURL              string
	MoMoRequestTimeout         time.Duration
	Eventing                   Eventing
}

type LookupFunc func(string) (string, bool)

func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

func LoadFrom(lookup LookupFunc) (Config, error) {
	appEnv := optional(lookup, "APP_ENV", defaultAppEnv)
	if appEnv != "development" && appEnv != "test" && appEnv != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	httpAddress := optional(lookup, "HTTP_ADDRESS", defaultHTTPAddress)
	logLevelText := optional(lookup, "LOG_LEVEL", defaultLogLevel)
	databaseURL, err := required(lookup, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	redisAddress, err := required(lookup, "REDIS_ADDRESS")
	if err != nil {
		return Config{}, err
	}
	kafkaBrokerText, err := required(lookup, "KAFKA_BROKERS")
	if err != nil {
		return Config{}, err
	}

	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(logLevelText)); err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL is invalid")
	}
	if err := validateAddress("HTTP_ADDRESS", httpAddress, true); err != nil {
		return Config{}, err
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return Config{}, err
	}
	databaseMaxOpen, err := integer(lookup, "DATABASE_MAX_OPEN", defaultDBMaxOpen, 2, 200)
	if err != nil {
		return Config{}, err
	}
	databaseMaxIdle, err := integer(lookup, "DATABASE_MAX_IDLE", defaultDBMaxIdle, 0, 200)
	if err != nil || databaseMaxIdle > databaseMaxOpen {
		return Config{}, fmt.Errorf("DATABASE_MAX_IDLE must be between zero and DATABASE_MAX_OPEN")
	}
	databaseConnectionLifetime, err := duration(lookup, "DATABASE_CONNECTION_LIFETIME", defaultDBConnectionLifetime, 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	databaseConnectionIdleTime, err := duration(lookup, "DATABASE_CONNECTION_IDLE_TIME", defaultDBConnectionIdleTime, time.Hour)
	if err != nil {
		return Config{}, err
	}
	databaseStatementTimeout, err := duration(lookup, "DATABASE_STATEMENT_TIMEOUT", defaultDBStatementTimeout, 2*time.Minute)
	if err != nil {
		return Config{}, err
	}
	databaseLockTimeout, err := duration(lookup, "DATABASE_LOCK_TIMEOUT", defaultDBLockTimeout, 30*time.Second)
	if err != nil || databaseLockTimeout >= databaseStatementTimeout {
		return Config{}, fmt.Errorf("DATABASE_LOCK_TIMEOUT must be positive and less than DATABASE_STATEMENT_TIMEOUT")
	}
	databaseIdleTransaction, err := duration(lookup, "DATABASE_IDLE_TRANSACTION_TIMEOUT", defaultDBIdleTransaction, 2*time.Minute)
	if err != nil {
		return Config{}, err
	}
	slowQueryThreshold, err := duration(lookup, "SLOW_QUERY_THRESHOLD", defaultSlowQueryThreshold, 30*time.Second)
	if err != nil || slowQueryThreshold >= databaseStatementTimeout {
		return Config{}, fmt.Errorf("SLOW_QUERY_THRESHOLD must be positive and less than DATABASE_STATEMENT_TIMEOUT")
	}
	metricsAddress := optionalEmpty(lookup, "METRICS_ADDRESS")
	if metricsAddress != "" {
		if err := validateAddress("METRICS_ADDRESS", metricsAddress, true); err != nil {
			return Config{}, err
		}
	}
	otlpTraceEndpoint := optionalEmpty(lookup, "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err := validateOTLPEndpoint(otlpTraceEndpoint); err != nil {
		return Config{}, err
	}
	if err := validateAddress("REDIS_ADDRESS", redisAddress, false); err != nil {
		return Config{}, err
	}
	redisTLSEnabled, err := boolean(lookup, "REDIS_TLS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	kafkaBrokers, err := parseAddresses("KAFKA_BROKERS", kafkaBrokerText)
	if err != nil {
		return Config{}, err
	}
	kafkaTLSEnabled, err := boolean(lookup, "KAFKA_TLS_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	kafkaSASLUsername := optionalEmpty(lookup, "KAFKA_SASL_USERNAME")
	kafkaSASLPassword := optionalEmpty(lookup, "KAFKA_SASL_PASSWORD")
	if (kafkaSASLUsername == "") != (kafkaSASLPassword == "") {
		return Config{}, fmt.Errorf("KAFKA_SASL_USERNAME and KAFKA_SASL_PASSWORD must be configured together")
	}
	shutdownTimeout, err := duration(lookup, "SHUTDOWN_TIMEOUT", defaultShutdownTimeout, maxShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	dependencyTimeout, err := duration(lookup, "DEPENDENCY_TIMEOUT", defaultDependencyTimeout, maxDependencyTimeout)
	if err != nil {
		return Config{}, err
	}
	tenantResolutionTimeout, err := duration(lookup, "TENANT_RESOLUTION_TIMEOUT", defaultTenantResolutionTimeout, maxTenantResolutionTimeout)
	if err != nil {
		return Config{}, err
	}
	passwordHashConcurrency, err := integer(lookup, "PASSWORD_HASH_CONCURRENCY", defaultPasswordHashConcurrency, 1, 32)
	if err != nil {
		return Config{}, err
	}
	accessTokenTTL, err := duration(lookup, "ACCESS_TOKEN_TTL", defaultAccessTokenTTL, maxAccessTokenTTL)
	if err != nil {
		return Config{}, err
	}
	sessionTTL, err := duration(lookup, "SESSION_TTL", defaultSessionTTL, maxSessionTTL)
	if err != nil || sessionTTL <= accessTokenTTL {
		return Config{}, fmt.Errorf("SESSION_TTL must be greater than ACCESS_TOKEN_TTL and at most %s", maxSessionTTL)
	}
	socialTransactionTTL, err := duration(lookup, "SOCIAL_TRANSACTION_TTL", defaultSocialTransactionTTL, maxSocialTransactionTTL)
	if err != nil {
		return Config{}, err
	}
	socialExchangeTTL, err := duration(lookup, "SOCIAL_EXCHANGE_TTL", defaultSocialExchangeTTL, maxSocialExchangeTTL)
	if err != nil {
		return Config{}, err
	}
	tenantBaseDomain := strings.ToLower(optional(lookup, "TENANT_BASE_DOMAIN", defaultTenantBaseDomain))
	authHost := strings.ToLower(optional(lookup, "AUTH_HOST", defaultAuthHost))
	routingMode := RoutingMode(strings.ToLower(optional(lookup, "TENANT_ROUTING_MODE", string(RoutingMultiStore))))
	platformHost := strings.ToLower(optional(lookup, "PLATFORM_HOST", tenantBaseDomain))
	singleStoreHost := strings.ToLower(optionalEmpty(lookup, "SINGLE_STORE_HOST"))
	singleStoreID, err := parseRouting(routingMode, tenantBaseDomain, platformHost, authHost, singleStoreHost, optionalEmpty(lookup, "SINGLE_STORE_ID"))
	if err != nil {
		return Config{}, err
	}
	platformAdminToken, err := platformToken(lookup)
	if err != nil {
		return Config{}, err
	}
	if appEnv == "production" && optionalEmpty(lookup, "PLATFORM_ADMIN_TOKEN") == "ZGV2LXBsYXRmb3JtLWFkbWluLXRva2VuLTAwMDAwMDA" {
		return Config{}, fmt.Errorf("PLATFORM_ADMIN_TOKEN must not use the development value in production")
	}
	trustedProxyCIDRs, err := parseCIDRs(optionalEmpty(lookup, "TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	corsAllowedOrigins, err := parseOrigins(optionalEmpty(lookup, "CORS_ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	registrationRate, err := credentialRateLimit(lookup, "REGISTRATION", 10, 5, time.Hour)
	if err != nil {
		return Config{}, err
	}
	loginRate, err := credentialRateLimit(lookup, "LOGIN", 20, 5, 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	tokenRate, err := credentialRateLimit(lookup, "TOKEN", 30, 10, time.Minute)
	if err != nil {
		return Config{}, err
	}
	socialRate, err := credentialRateLimit(lookup, "SOCIAL", 20, 10, time.Hour)
	if err != nil {
		return Config{}, err
	}
	guestCreationRate, err := rateLimit(lookup, "GUEST_CREATION", 20, time.Hour)
	if err != nil {
		return Config{}, err
	}
	platformProvisionRate, err := rateLimit(lookup, "PLATFORM_PROVISION", 10, time.Hour)
	if err != nil {
		return Config{}, err
	}
	googleClientID := optionalEmpty(lookup, "GOOGLE_CLIENT_ID")
	googleClientSecret := optionalEmpty(lookup, "GOOGLE_CLIENT_SECRET")
	googleRedirectURL := optionalEmpty(lookup, "GOOGLE_REDIRECT_URL")
	if err := validateProvider("GOOGLE", googleClientID, googleClientSecret, googleRedirectURL, authHost, "/api/v1/auth/social/google/callback"); err != nil {
		return Config{}, err
	}
	facebookAppID := optionalEmpty(lookup, "FACEBOOK_APP_ID")
	facebookAppSecret := optionalEmpty(lookup, "FACEBOOK_APP_SECRET")
	facebookRedirectURL := optionalEmpty(lookup, "FACEBOOK_REDIRECT_URL")
	if err := validateProvider("FACEBOOK", facebookAppID, facebookAppSecret, facebookRedirectURL, authHost, "/api/v1/auth/social/facebook/callback"); err != nil {
		return Config{}, err
	}
	returnPaths, err := parseReturnPaths(optional(lookup, "SOCIAL_RETURN_PATHS", "/auth/social/complete"))
	if err != nil {
		return Config{}, err
	}
	inventoryExpiryBatch, err := integer(lookup, "INVENTORY_EXPIRY_BATCH", 50, 1, 100)
	if err != nil {
		return Config{}, err
	}
	inventoryExpiryPoll, err := duration(lookup, "INVENTORY_EXPIRY_POLL", defaultInventoryExpiryPoll, maxInventoryExpiryPoll)
	if err != nil {
		return Config{}, err
	}
	cartExpiryBatch, err := integer(lookup, "CART_EXPIRY_BATCH", 50, 1, 100)
	if err != nil {
		return Config{}, err
	}
	cartExpiryPoll, err := duration(lookup, "CART_EXPIRY_POLL", defaultCartExpiryPoll, maxCartExpiryPoll)
	if err != nil {
		return Config{}, err
	}
	paymentKeys, activePaymentKey, err := paymentKeyring(lookup, appEnv)
	if err != nil {
		return Config{}, err
	}
	momoAPIBaseURL := optional(lookup, "MOMO_API_BASE_URL", momoDefaultAPI(appEnv))
	momoRedirectURL := optional(lookup, "MOMO_REDIRECT_URL", "https://"+tenantBaseDomain+"/payments/momo/return")
	momoPublicURL := optional(lookup, "MOMO_PUBLIC_URL", "https://"+tenantBaseDomain)
	momoRequestTimeout, err := duration(lookup, "MOMO_REQUEST_TIMEOUT", defaultMoMoRequestTimeout, maxMoMoRequestTimeout)
	if err != nil || momoRequestTimeout < 30*time.Second {
		return Config{}, fmt.Errorf("MOMO_REQUEST_TIMEOUT must be between 30s and %s", maxMoMoRequestTimeout)
	}
	if err := validateMoMoURLs(appEnv, momoAPIBaseURL, momoRedirectURL, momoPublicURL); err != nil {
		return Config{}, err
	}
	eventBatch, err := integer(lookup, "EVENT_BATCH", 50, 1, 500)
	if err != nil {
		return Config{}, err
	}
	eventPoll, err := duration(lookup, "EVENT_POLL", defaultEventPoll, time.Minute)
	if err != nil {
		return Config{}, err
	}
	eventLease, err := duration(lookup, "EVENT_CLAIM_LEASE", defaultEventLease, 10*time.Minute)
	if err != nil {
		return Config{}, err
	}
	eventMaxAttempts, err := integer(lookup, "EVENT_MAX_ATTEMPTS", 12, 1, 100)
	if err != nil {
		return Config{}, err
	}
	eventRetryBase, err := duration(lookup, "EVENT_RETRY_BASE", defaultEventRetryBase, time.Minute)
	if err != nil {
		return Config{}, err
	}
	eventRetryMax, err := duration(lookup, "EVENT_RETRY_MAX", defaultEventRetryMax, 24*time.Hour)
	if err != nil || eventRetryMax < eventRetryBase {
		return Config{}, fmt.Errorf("EVENT_RETRY_MAX must be at least EVENT_RETRY_BASE and at most 24h0m0s")
	}
	if err := validateProduction(appEnv, databaseURL, tenantBaseDomain, platformHost, authHost, singleStoreHost, metricsAddress, otlpTraceEndpoint, redisTLSEnabled, kafkaTLSEnabled, kafkaSASLUsername); err != nil {
		return Config{}, err
	}

	return Config{
		AppEnv:                     appEnv,
		HTTPAddress:                httpAddress,
		MetricsAddress:             metricsAddress,
		OTLPTraceEndpoint:          otlpTraceEndpoint,
		LogLevel:                   logLevel,
		DatabaseURL:                databaseURL,
		DatabaseMaxOpen:            databaseMaxOpen,
		DatabaseMaxIdle:            databaseMaxIdle,
		DatabaseConnectionLifetime: databaseConnectionLifetime,
		DatabaseConnectionIdleTime: databaseConnectionIdleTime,
		DatabaseStatementTimeout:   databaseStatementTimeout,
		DatabaseLockTimeout:        databaseLockTimeout,
		DatabaseIdleTransaction:    databaseIdleTransaction,
		SlowQueryThreshold:         slowQueryThreshold,
		RedisAddress:               redisAddress,
		RedisTLSEnabled:            redisTLSEnabled,
		KafkaBrokers:               kafkaBrokers,
		KafkaTLSEnabled:            kafkaTLSEnabled,
		KafkaSASLUsername:          kafkaSASLUsername,
		KafkaSASLPassword:          kafkaSASLPassword,
		ShutdownTimeout:            shutdownTimeout,
		DependencyTimeout:          dependencyTimeout,
		TenantResolutionTimeout:    tenantResolutionTimeout,
		PasswordHashConcurrency:    passwordHashConcurrency,
		AccessTokenTTL:             accessTokenTTL,
		SessionTTL:                 sessionTTL,
		SocialTransactionTTL:       socialTransactionTTL,
		SocialExchangeTTL:          socialExchangeTTL,
		TenantBaseDomain:           tenantBaseDomain,
		RoutingMode:                routingMode,
		PlatformHost:               platformHost,
		SingleStoreHost:            singleStoreHost,
		SingleStoreID:              singleStoreID,
		AuthHost:                   authHost,
		PlatformAdminToken:         platformAdminToken,
		TrustedProxyCIDRs:          trustedProxyCIDRs,
		CORSAllowedOrigins:         corsAllowedOrigins,
		RegistrationRateLimit:      registrationRate,
		LoginRateLimit:             loginRate,
		TokenRateLimit:             tokenRate,
		SocialRateLimit:            socialRate,
		GuestCreationRateLimit:     guestCreationRate,
		PlatformProvisionRateLimit: platformProvisionRate,
		GoogleClientID:             googleClientID,
		GoogleClientSecret:         googleClientSecret,
		GoogleRedirectURL:          googleRedirectURL,
		FacebookAppID:              facebookAppID,
		FacebookAppSecret:          facebookAppSecret,
		FacebookRedirectURL:        facebookRedirectURL,
		FacebookGraphVersion:       optional(lookup, "FACEBOOK_GRAPH_VERSION", defaultFacebookGraphVersion),
		SocialReturnPaths:          returnPaths,
		InventoryExpiryBatch:       inventoryExpiryBatch,
		InventoryExpiryPoll:        inventoryExpiryPoll,
		CartExpiryBatch:            cartExpiryBatch,
		CartExpiryPoll:             cartExpiryPoll,
		PaymentConfigKeys:          paymentKeys,
		PaymentConfigActiveVersion: activePaymentKey,
		MoMoAPIBaseURL:             momoAPIBaseURL,
		MoMoRedirectURL:            momoRedirectURL,
		MoMoPublicURL:              momoPublicURL,
		MoMoRequestTimeout:         momoRequestTimeout,
		Eventing:                   Eventing{Batch: eventBatch, Poll: eventPoll, Lease: eventLease, MaxAttempts: eventMaxAttempts, RetryBase: eventRetryBase, RetryMax: eventRetryMax},
	}, nil
}

func momoDefaultAPI(appEnv string) string {
	if appEnv == "production" {
		return "https://payment.momo.vn"
	}
	return "https://test-payment.momo.vn"
}

func validateMoMoURLs(appEnv, apiBase, redirect, public string) error {
	api, err := url.Parse(apiBase)
	if err != nil || api.Scheme != "https" || api.User != nil || api.Port() != "" || api.RawQuery != "" || api.Fragment != "" || api.Path != "" || (api.Hostname() != "test-payment.momo.vn" && api.Hostname() != "payment.momo.vn") {
		return fmt.Errorf("MOMO_API_BASE_URL must be an exact supported MoMo HTTPS origin")
	}
	if appEnv == "production" && api.Hostname() != "payment.momo.vn" {
		return fmt.Errorf("MOMO_API_BASE_URL must use the production MoMo origin in production")
	}
	for name, value := range map[string]string{"MOMO_REDIRECT_URL": redirect, "MOMO_PUBLIC_URL": public} {
		parsed, parseErr := url.Parse(value)
		if parseErr != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" || len(value) > 2048 {
			return fmt.Errorf("%s must be an HTTPS URL", name)
		}
		if name == "MOMO_PUBLIC_URL" && (parsed.RawQuery != "" || parsed.Fragment != "") {
			return fmt.Errorf("MOMO_PUBLIC_URL must not contain a query or fragment")
		}
	}
	return nil
}

func parseRouting(mode RoutingMode, baseDomain, platformHost, authHost, singleHost, singleID string) (ids.StoreID, error) {
	if !validHostname(baseDomain) || !validHostname(platformHost) || !validHostname(authHost) {
		return "", fmt.Errorf("TENANT_BASE_DOMAIN, PLATFORM_HOST, and AUTH_HOST must be valid hostnames")
	}
	if platformHost == authHost {
		return "", fmt.Errorf("PLATFORM_HOST and AUTH_HOST must be distinct")
	}
	switch mode {
	case RoutingMultiStore:
		if platformHost != baseDomain || authHost != "auth."+baseDomain {
			return "", fmt.Errorf("multi_store routing requires PLATFORM_HOST=TENANT_BASE_DOMAIN and AUTH_HOST=auth.TENANT_BASE_DOMAIN")
		}
		if singleHost != "" || singleID != "" {
			return "", fmt.Errorf("SINGLE_STORE_HOST and SINGLE_STORE_ID are only valid in single_store routing")
		}
		return "", nil
	case RoutingSingleStore:
		if !validHostname(singleHost) || singleHost == platformHost || singleHost == authHost || strings.HasSuffix(singleHost, "."+platformHost) || strings.HasSuffix(authHost, "."+singleHost) {
			return "", fmt.Errorf("single_store routing requires a distinct exact SINGLE_STORE_HOST")
		}
		storeID, err := ids.ParseStoreID(singleID)
		if err != nil {
			return "", fmt.Errorf("SINGLE_STORE_ID must be a valid UUID")
		}
		return storeID, nil
	default:
		return "", fmt.Errorf("TENANT_ROUTING_MODE must be multi_store or single_store")
	}
}

func validHostname(value string) bool {
	if value == "" || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, ":") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func redirectMatches(value, expectedHost, expectedPath string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), expectedHost) && parsed.Port() == "" && parsed.User == nil && parsed.EscapedPath() == expectedPath && parsed.RawQuery == "" && parsed.Fragment == ""
}

func rateLimit(lookup LookupFunc, prefix string, defaultMaximum int, defaultWindow time.Duration) (RateLimit, error) {
	maximum, err := integer(lookup, prefix+"_RATE_MAXIMUM", defaultMaximum, 1, 10000)
	if err != nil {
		return RateLimit{}, err
	}
	window, err := duration(lookup, prefix+"_RATE_WINDOW", defaultWindow, maxRateWindow)
	if err != nil {
		return RateLimit{}, err
	}
	return RateLimit{Maximum: maximum, Window: window}, nil
}

func credentialRateLimit(lookup LookupFunc, prefix string, defaultPeer, defaultIdentity int, defaultWindow time.Duration) (CredentialRateLimit, error) {
	peer, err := integer(lookup, prefix+"_RATE_PEER_MAXIMUM", defaultPeer, 1, 10000)
	if err != nil {
		return CredentialRateLimit{}, err
	}
	identity, err := integer(lookup, prefix+"_RATE_IDENTITY_MAXIMUM", defaultIdentity, 1, 10000)
	if err != nil {
		return CredentialRateLimit{}, err
	}
	window, err := duration(lookup, prefix+"_RATE_WINDOW", defaultWindow, maxRateWindow)
	if err != nil {
		return CredentialRateLimit{}, err
	}
	return CredentialRateLimit{PeerMaximum: peer, IdentityMaximum: identity, Window: window}, nil
}

func optionalEmpty(lookup LookupFunc, key string) string {
	value, _ := lookup(key)
	return strings.TrimSpace(value)
}

func optional(lookup LookupFunc, key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func required(lookup LookupFunc, key string) (string, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return strings.TrimSpace(value), nil
}

func duration(lookup LookupFunc, key string, fallback, maximum time.Duration) (time.Duration, error) {
	text := optional(lookup, key, fallback.String())
	value, err := time.ParseDuration(text)
	if err != nil || value <= 0 || value > maximum {
		return 0, fmt.Errorf("%s must be greater than zero and at most %s", key, maximum)
	}
	return value, nil
}

func integer(lookup LookupFunc, key string, fallback, minimum, maximum int) (int, error) {
	text := optional(lookup, key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(text)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func boolean(lookup LookupFunc, key string, fallback bool) (bool, error) {
	text := optional(lookup, key, strconv.FormatBool(fallback))
	value, err := strconv.ParseBool(text)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value, nil
}

func validateProvider(prefix, clientID, secret, redirectURL, authHost, callbackPath string) error {
	configured := clientID != "" || secret != "" || redirectURL != ""
	if !configured {
		return nil
	}
	if clientID == "" || secret == "" || !redirectMatches(redirectURL, authHost, callbackPath) {
		return fmt.Errorf("%s social login configuration must be complete and use its exact AUTH_HOST callback URL", prefix)
	}
	return nil
}

func platformToken(lookup LookupFunc) ([32]byte, error) {
	value, err := required(lookup, "PLATFORM_ADMIN_TOKEN")
	if err != nil {
		return [32]byte{}, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("PLATFORM_ADMIN_TOKEN must be unpadded base64url encoding exactly 32 bytes")
	}
	return [32]byte(decoded), nil
}

func paymentKeyring(lookup LookupFunc, appEnv string) (map[int][32]byte, int, error) {
	rawKeys := optionalEmpty(lookup, "PAYMENT_CONFIG_KEYS")
	rawActive := optionalEmpty(lookup, "PAYMENT_CONFIG_ACTIVE_KEY_VERSION")
	if rawKeys == "" && rawActive == "" {
		return nil, 0, nil
	}
	if rawKeys == "" || rawActive == "" {
		return nil, 0, fmt.Errorf("PAYMENT_CONFIG_KEYS and PAYMENT_CONFIG_ACTIVE_KEY_VERSION must be configured together")
	}
	active, err := strconv.Atoi(rawActive)
	if err != nil || active < 1 {
		return nil, 0, fmt.Errorf("PAYMENT_CONFIG_ACTIVE_KEY_VERSION must be a positive integer")
	}
	parts := strings.Split(rawKeys, ",")
	if len(parts) > 8 {
		return nil, 0, fmt.Errorf("PAYMENT_CONFIG_KEYS must contain at most 8 keys")
	}
	keys := make(map[int][32]byte, len(parts))
	for _, part := range parts {
		versionText, encoded, ok := strings.Cut(strings.TrimSpace(part), ":")
		version, parseErr := strconv.Atoi(versionText)
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if !ok || parseErr != nil || version < 1 || decodeErr != nil || len(decoded) != 32 {
			return nil, 0, fmt.Errorf("PAYMENT_CONFIG_KEYS must use version:unpadded-base64url-32-byte entries")
		}
		if _, exists := keys[version]; exists {
			return nil, 0, fmt.Errorf("PAYMENT_CONFIG_KEYS contains a duplicate version")
		}
		key := [32]byte(decoded)
		if appEnv == "production" && key == [32]byte{} {
			return nil, 0, fmt.Errorf("PAYMENT_CONFIG_KEYS must not contain the development key in production")
		}
		keys[version] = key
	}
	if _, ok := keys[active]; !ok {
		return nil, 0, fmt.Errorf("PAYMENT_CONFIG_ACTIVE_KEY_VERSION must identify a configured key")
	}
	return keys, active, nil
}

func parseCIDRs(value string) ([]netip.Prefix, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS must contain at most 32 prefixes")
	}
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS contains an invalid prefix")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func parseOrigins(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS must contain at most 32 origins")
	}
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		parsed, err := url.Parse(strings.TrimSpace(part))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS must contain only HTTP origins without paths")
		}
		origin := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func parseReturnPaths(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		parsed, err := url.Parse(path)
		if err != nil || path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("SOCIAL_RETURN_PATHS must contain only absolute-path references")
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func validateDatabaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return fmt.Errorf("DATABASE_URL must be a PostgreSQL URL with a host")
	}
	return nil
}

func validateProduction(appEnv, databaseURL, baseDomain, platformHost, authHost, singleStoreHost, metricsAddress, otlpEndpoint string, redisTLS, kafkaTLS bool, kafkaUsername string) error {
	if appEnv != "production" {
		return nil
	}
	parsed, _ := url.Parse(databaseURL)
	sslMode := strings.ToLower(parsed.Query().Get("sslmode"))
	if sslMode != "verify-full" {
		return fmt.Errorf("DATABASE_URL must use sslmode=verify-full in production")
	}
	if metricsAddress == "" {
		return fmt.Errorf("METRICS_ADDRESS is required in production")
	}
	if !strings.HasPrefix(otlpEndpoint, "https://") {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must use HTTPS in production")
	}
	if !redisTLS {
		return fmt.Errorf("REDIS_TLS_ENABLED must be true in production")
	}
	if !kafkaTLS || kafkaUsername == "" {
		return fmt.Errorf("kafka TLS and SASL credentials are required in production")
	}
	for name, host := range map[string]string{"TENANT_BASE_DOMAIN": baseDomain, "PLATFORM_HOST": platformHost, "AUTH_HOST": authHost, "SINGLE_STORE_HOST": singleStoreHost} {
		if host != "" && (host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test")) {
			return fmt.Errorf("%s must be a public deployment hostname in production", name)
		}
	}
	return nil
}

func validateOTLPEndpoint(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func parseAddresses(key, value string) ([]string, error) {
	parts := strings.Split(value, ",")
	addresses := make([]string, 0, len(parts))
	for _, part := range parts {
		address := strings.TrimSpace(part)
		if err := validateAddress(key, address, false); err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func validateAddress(key, value string, allowEmptyHost bool) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" || (!allowEmptyHost && host == "") {
		return fmt.Errorf("%s must contain a valid host and port", key)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("%s must contain a valid host and port", key)
	}
	return nil
}
