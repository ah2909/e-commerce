package config

import (
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
}

func TestLoadFromRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "missing database", key: "DATABASE_URL", value: "", want: "DATABASE_URL is required"},
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

func validEnvironment() map[string]string {
	return map[string]string{
		"DATABASE_URL":  "postgres://ecommerce@localhost:5432/ecommerce?sslmode=disable",
		"REDIS_ADDRESS": "localhost:6379",
		"KAFKA_BROKERS": "localhost:9092",
	}
}

func mapLookup(values map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
