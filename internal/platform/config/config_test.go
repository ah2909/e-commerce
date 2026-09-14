package config

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadFrom(t *testing.T) {
	values := validEnvironment()
	values["APP_ENV"] = "test"
	values["HTTP_ADDRESS"] = "127.0.0.1:9090"
	values["LOG_LEVEL"] = "debug"
	values["KAFKA_BROKERS"] = "kafka-1:9092, kafka-2:9092"
	values["SHUTDOWN_TIMEOUT"] = "15s"
	values["DEPENDENCY_TIMEOUT"] = "3s"
	values["TENANT_RESOLUTION_TIMEOUT"] = "750ms"
	values["PASSWORD_HASH_CONCURRENCY"] = "4"
	values["ACCESS_TOKEN_TTL"] = "10m"
	values["SESSION_TTL"] = "48h"
	values["SOCIAL_TRANSACTION_TTL"] = "8m"
	values["SOCIAL_EXCHANGE_TTL"] = "45s"
	values["LOGIN_RATE_PEER_MAXIMUM"] = "12"
	values["LOGIN_RATE_IDENTITY_MAXIMUM"] = "4"
	values["LOGIN_RATE_WINDOW"] = "10m"
	values["TOKEN_RATE_PEER_MAXIMUM"] = "18"
	values["TOKEN_RATE_IDENTITY_MAXIMUM"] = "7"
	values["TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,2001:db8::/32"
	values["CORS_ALLOWED_ORIGINS"] = "http://localhost:5173, https://admin.example.test"
	values["GOOGLE_CLIENT_ID"] = "google-client"
	values["GOOGLE_CLIENT_SECRET"] = "google-secret"
	values["GOOGLE_REDIRECT_URL"] = "https://auth.example.test/api/v1/auth/social/google/callback"
	values["SOCIAL_RETURN_PATHS"] = "/auth/complete,/settings/security"
	values["INVENTORY_EXPIRY_BATCH"] = "25"
	values["INVENTORY_EXPIRY_POLL"] = "2s"
	values["CART_EXPIRY_BATCH"] = "30"
	values["CART_EXPIRY_POLL"] = "3m"
	values["PAYMENT_CONFIG_KEYS"] = "3:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x24}, 32))
	values["PAYMENT_CONFIG_ACTIVE_KEY_VERSION"] = "3"

	got, err := LoadFrom(mapLookup(values))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.AppEnv != "test" || got.HTTPAddress != "127.0.0.1:9090" || got.LogLevel != slog.LevelDebug {
		t.Fatalf("LoadFrom() basic fields = %#v", got)
	}
	if len(got.KafkaBrokers) != 2 || got.KafkaBrokers[1] != "kafka-2:9092" {
		t.Fatalf("LoadFrom() KafkaBrokers = %#v", got.KafkaBrokers)
	}
	if got.ShutdownTimeout != 15*time.Second || got.DependencyTimeout != 3*time.Second {
		t.Fatalf("LoadFrom() timeouts = %s, %s", got.ShutdownTimeout, got.DependencyTimeout)
	}
	if got.TenantResolutionTimeout != 750*time.Millisecond {
		t.Fatalf("LoadFrom() TenantResolutionTimeout = %s", got.TenantResolutionTimeout)
	}
	if got.PasswordHashConcurrency != 4 || got.GoogleClientID != "google-client" || len(got.SocialReturnPaths) != 2 {
		t.Fatalf("LoadFrom() identity fields = %#v", got)
	}
	if got.AccessTokenTTL != 10*time.Minute || got.SessionTTL != 48*time.Hour || got.SocialTransactionTTL != 8*time.Minute || got.SocialExchangeTTL != 45*time.Second {
		t.Fatalf("LoadFrom() identity TTLs = %#v", got)
	}
	if got.LoginRateLimit.PeerMaximum != 12 || got.LoginRateLimit.IdentityMaximum != 4 || got.LoginRateLimit.Window != 10*time.Minute {
		t.Fatalf("LoadFrom() login rate = %#v", got.LoginRateLimit)
	}
	if got.TokenRateLimit.PeerMaximum != 18 || got.TokenRateLimit.IdentityMaximum != 7 || len(got.TrustedProxyCIDRs) != 2 {
		t.Fatalf("LoadFrom() token/proxy fields = %#v, %#v", got.TokenRateLimit, got.TrustedProxyCIDRs)
	}
	if len(got.CORSAllowedOrigins) != 2 || got.CORSAllowedOrigins[1] != "https://admin.example.test" {
		t.Fatalf("LoadFrom() CORS origins = %#v", got.CORSAllowedOrigins)
	}
	if !bytes.Equal(got.PlatformAdminToken[:], bytes.Repeat([]byte{0x42}, 32)) {
		t.Fatal("LoadFrom() platform token mismatch")
	}
	if got.InventoryExpiryBatch != 25 || got.InventoryExpiryPoll != 2*time.Second {
		t.Fatalf("LoadFrom() inventory worker fields = %#v", got)
	}
	if got.CartExpiryBatch != 30 || got.CartExpiryPoll != 3*time.Minute {
		t.Fatalf("LoadFrom() cart worker fields = %#v", got)
	}
	if got.PaymentConfigActiveVersion != 3 || got.PaymentConfigKeys[3] != [32]byte(bytes.Repeat([]byte{0x24}, 32)) {
		t.Fatalf("LoadFrom() payment keyring mismatch")
	}
}

func TestLoadFromDefaults(t *testing.T) {
	got, err := LoadFrom(mapLookup(validEnvironment()))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.AppEnv != defaultAppEnv || got.HTTPAddress != defaultHTTPAddress || got.LogLevel != slog.LevelInfo {
		t.Fatalf("LoadFrom() defaults = %#v", got)
	}
	if got.ShutdownTimeout != defaultShutdownTimeout || got.DependencyTimeout != defaultDependencyTimeout {
		t.Fatalf("LoadFrom() timeout defaults = %s, %s", got.ShutdownTimeout, got.DependencyTimeout)
	}
	if got.TenantResolutionTimeout != defaultTenantResolutionTimeout {
		t.Fatalf("LoadFrom() tenant timeout default = %s", got.TenantResolutionTimeout)
	}
	if got.PasswordHashConcurrency != defaultPasswordHashConcurrency || got.FacebookGraphVersion != defaultFacebookGraphVersion {
		t.Fatalf("LoadFrom() identity defaults = %#v", got)
	}
	if got.AccessTokenTTL != defaultAccessTokenTTL || got.SessionTTL != defaultSessionTTL || got.SocialTransactionTTL != defaultSocialTransactionTTL || got.SocialExchangeTTL != defaultSocialExchangeTTL {
		t.Fatalf("LoadFrom() identity TTL defaults = %#v", got)
	}
	if got.InventoryExpiryBatch != 50 || got.InventoryExpiryPoll != defaultInventoryExpiryPoll {
		t.Fatalf("LoadFrom() inventory worker defaults = %#v", got)
	}
	if got.CartExpiryBatch != 50 || got.CartExpiryPoll != defaultCartExpiryPoll {
		t.Fatalf("LoadFrom() cart worker defaults = %#v", got)
	}
}

func TestLoadFromRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "missing database", key: "DATABASE_URL", value: "", want: "DATABASE_URL is required"},
		{name: "missing platform token", key: "PLATFORM_ADMIN_TOKEN", value: "", want: "PLATFORM_ADMIN_TOKEN is required"},
		{name: "invalid platform token", key: "PLATFORM_ADMIN_TOKEN", value: "not-base64!", want: "PLATFORM_ADMIN_TOKEN"},
		{name: "short platform token", key: "PLATFORM_ADMIN_TOKEN", value: "c2hvcnQ", want: "exactly 32 bytes"},
		{name: "invalid trusted proxy", key: "TRUSTED_PROXY_CIDRS", value: "not-a-prefix", want: "TRUSTED_PROXY_CIDRS"},
		{name: "wildcard CORS origin", key: "CORS_ALLOWED_ORIGINS", value: "*", want: "CORS_ALLOWED_ORIGINS"},
		{name: "CORS origin path", key: "CORS_ALLOWED_ORIGINS", value: "https://admin.example.test/path", want: "CORS_ALLOWED_ORIGINS"},
		{name: "CORS origin credentials", key: "CORS_ALLOWED_ORIGINS", value: "https://user@admin.example.test", want: "CORS_ALLOWED_ORIGINS"},
		{name: "invalid database", key: "DATABASE_URL", value: "redis://localhost:6379", want: "DATABASE_URL"},
		{name: "invalid redis", key: "REDIS_ADDRESS", value: "localhost", want: "REDIS_ADDRESS"},
		{name: "nonnumeric Redis port", key: "REDIS_ADDRESS", value: "localhost:invalid", want: "REDIS_ADDRESS"},
		{name: "zero Redis port", key: "REDIS_ADDRESS", value: "localhost:0", want: "REDIS_ADDRESS"},
		{name: "out of range Redis port", key: "REDIS_ADDRESS", value: "localhost:70000", want: "REDIS_ADDRESS"},
		{name: "invalid kafka", key: "KAFKA_BROKERS", value: "localhost", want: "KAFKA_BROKERS"},
		{name: "invalid log level", key: "LOG_LEVEL", value: "verbose", want: "LOG_LEVEL"},
		{name: "zero shutdown", key: "SHUTDOWN_TIMEOUT", value: "0s", want: "SHUTDOWN_TIMEOUT"},
		{name: "large dependency timeout", key: "DEPENDENCY_TIMEOUT", value: "31s", want: "DEPENDENCY_TIMEOUT"},
		{name: "large tenant resolution timeout", key: "TENANT_RESOLUTION_TIMEOUT", value: "6s", want: "TENANT_RESOLUTION_TIMEOUT"},
		{name: "invalid hash concurrency", key: "PASSWORD_HASH_CONCURRENCY", value: "0", want: "PASSWORD_HASH_CONCURRENCY"},
		{name: "large access TTL", key: "ACCESS_TOKEN_TTL", value: "61m", want: "ACCESS_TOKEN_TTL"},
		{name: "session shorter than access", key: "SESSION_TTL", value: "10m", want: "SESSION_TTL"},
		{name: "large social transaction TTL", key: "SOCIAL_TRANSACTION_TTL", value: "31m", want: "SOCIAL_TRANSACTION_TTL"},
		{name: "zero login rate", key: "LOGIN_RATE_PEER_MAXIMUM", value: "0", want: "LOGIN_RATE_PEER_MAXIMUM"},
		{name: "large rate window", key: "TOKEN_RATE_WINDOW", value: "25h", want: "TOKEN_RATE_WINDOW"},
		{name: "invalid tenant base", key: "TENANT_BASE_DOMAIN", value: "bad_domain", want: "AUTH_HOST"},
		{name: "mismatched auth host", key: "AUTH_HOST", value: "login.example.test", want: "AUTH_HOST"},
		{name: "partial Google", key: "GOOGLE_CLIENT_ID", value: "client", want: "GOOGLE"},
		{name: "wrong Google callback path", key: "GOOGLE_REDIRECT_URL", value: "https://auth.example.test/wrong", want: "exact AUTH_HOST callback"},
		{name: "Google callback query", key: "GOOGLE_REDIRECT_URL", value: "https://auth.example.test/api/v1/auth/social/google/callback?next=x", want: "exact AUTH_HOST callback"},
		{name: "external return URL", key: "SOCIAL_RETURN_PATHS", value: "https://evil.example/callback", want: "SOCIAL_RETURN_PATHS"},
		{name: "large inventory batch", key: "INVENTORY_EXPIRY_BATCH", value: "101", want: "INVENTORY_EXPIRY_BATCH"},
		{name: "large inventory poll", key: "INVENTORY_EXPIRY_POLL", value: "61s", want: "INVENTORY_EXPIRY_POLL"},
		{name: "payment keys without active version", key: "PAYMENT_CONFIG_KEYS", value: "1:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), want: "configured together"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := validEnvironment()
			values[test.key] = test.value
			_, err := LoadFrom(mapLookup(values))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadFrom() error = %v, want containing %q", err, test.want)
			}
			if strings.Contains(err.Error(), "ecommerce:ecommerce") {
				t.Fatalf("LoadFrom() exposed credentials: %v", err)
			}
		})
	}
}

func TestLoadFromProductionRequiresExplicitPlatformToken(t *testing.T) {
	values := validEnvironment()
	values["APP_ENV"] = "production"
	delete(values, "PLATFORM_ADMIN_TOKEN")
	_, err := LoadFrom(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "PLATFORM_ADMIN_TOKEN is required") {
		t.Fatalf("LoadFrom() error = %v", err)
	}
}

func TestLoadFromProductionRejectsDevelopmentPaymentKey(t *testing.T) {
	values := validEnvironment()
	values["APP_ENV"] = "production"
	values["PAYMENT_CONFIG_KEYS"] = "1:" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	values["PAYMENT_CONFIG_ACTIVE_KEY_VERSION"] = "1"
	_, err := LoadFrom(mapLookup(values))
	if err == nil || !strings.Contains(err.Error(), "development key") {
		t.Fatalf("LoadFrom() error = %v", err)
	}
}

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_URL":         "postgres://ecommerce@localhost:5432/ecommerce?sslmode=disable",
		"REDIS_ADDRESS":        "localhost:6379",
		"KAFKA_BROKERS":        "localhost:9092",
		"TENANT_BASE_DOMAIN":   "example.test",
		"AUTH_HOST":            "auth.example.test",
		"PLATFORM_ADMIN_TOKEN": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)),
	}
}

func mapLookup(values map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
