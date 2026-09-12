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

type Config struct {
	AppEnv                     string
	HTTPAddress                string
	LogLevel                   slog.Level
	DatabaseURL                string
	RedisAddress               string
	KafkaBrokers               []string
	ShutdownTimeout            time.Duration
	DependencyTimeout          time.Duration
	TenantResolutionTimeout    time.Duration
	PasswordHashConcurrency    int
	AccessTokenTTL             time.Duration
	SessionTTL                 time.Duration
	SocialTransactionTTL       time.Duration
	SocialExchangeTTL          time.Duration
	TenantBaseDomain           string
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
}

type LookupFunc func(string) (string, bool)

func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

func LoadFrom(lookup LookupFunc) (Config, error) {
	appEnv := optional(lookup, "APP_ENV", defaultAppEnv)
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
	if err := validateAddress("REDIS_ADDRESS", redisAddress, false); err != nil {
		return Config{}, err
	}

	kafkaBrokers, err := parseAddresses("KAFKA_BROKERS", kafkaBrokerText)
	if err != nil {
		return Config{}, err
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
	if err := validateTenantHosts(tenantBaseDomain, authHost); err != nil {
		return Config{}, err
	}
	platformAdminToken, err := platformToken(lookup)
	if err != nil {
		return Config{}, err
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

	return Config{
		AppEnv:                     appEnv,
		HTTPAddress:                httpAddress,
		LogLevel:                   logLevel,
		DatabaseURL:                databaseURL,
		RedisAddress:               redisAddress,
		KafkaBrokers:               kafkaBrokers,
		ShutdownTimeout:            shutdownTimeout,
		DependencyTimeout:          dependencyTimeout,
		TenantResolutionTimeout:    tenantResolutionTimeout,
		PasswordHashConcurrency:    passwordHashConcurrency,
		AccessTokenTTL:             accessTokenTTL,
		SessionTTL:                 sessionTTL,
		SocialTransactionTTL:       socialTransactionTTL,
		SocialExchangeTTL:          socialExchangeTTL,
		TenantBaseDomain:           tenantBaseDomain,
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
	}, nil
}

func validateTenantHosts(baseDomain, authHost string) error {
	if !validHostname(baseDomain) || !validHostname(authHost) || authHost != "auth."+baseDomain {
		return fmt.Errorf("AUTH_HOST must be auth.TENANT_BASE_DOMAIN and both must be valid hostnames")
	}
	return nil
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
