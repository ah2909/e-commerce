package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppEnv            = "development"
	defaultHTTPAddress       = ":8080"
	defaultLogLevel          = "info"
	defaultShutdownTimeout   = 10 * time.Second
	defaultDependencyTimeout = 2 * time.Second
	maxShutdownTimeout       = 2 * time.Minute
	maxDependencyTimeout     = 30 * time.Second
)

type Config struct {
	AppEnv            string
	HTTPAddress       string
	LogLevel          slog.Level
	DatabaseURL       string
	RedisAddress      string
	KafkaBrokers      []string
	ShutdownTimeout   time.Duration
	DependencyTimeout time.Duration
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

	return Config{
		AppEnv:            appEnv,
		HTTPAddress:       httpAddress,
		LogLevel:          logLevel,
		DatabaseURL:       databaseURL,
		RedisAddress:      redisAddress,
		KafkaBrokers:      kafkaBrokers,
		ShutdownTimeout:   shutdownTimeout,
		DependencyTimeout: dependencyTimeout,
	}, nil
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
