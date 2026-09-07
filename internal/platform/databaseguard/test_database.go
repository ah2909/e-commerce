package databaseguard

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrUnsafeTestDatabase = errors.New("unsafe test database")

func Validate(databaseURL, mutationAllowed string) error {
	_, err := Parse(databaseURL, mutationAllowed)
	return err
}

func Parse(databaseURL, mutationAllowed string) (*pgx.ConnConfig, error) {
	if mutationAllowed != "true" {
		return nil, fmt.Errorf("%w: ALLOW_TEST_DATABASE_MUTATION must equal true", ErrUnsafeTestDatabase)
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: TEST_DATABASE_URL is invalid", ErrUnsafeTestDatabase)
	}
	if err := ValidateConfig(config, mutationAllowed); err != nil {
		return nil, err
	}
	return config, nil
}

func ValidateConfig(config *pgx.ConnConfig, mutationAllowed string) error {
	if mutationAllowed != "true" {
		return fmt.Errorf("%w: ALLOW_TEST_DATABASE_MUTATION must equal true", ErrUnsafeTestDatabase)
	}
	if config == nil || !isLoopback(config.Host) {
		return fmt.Errorf("%w: effective database host must be loopback", ErrUnsafeTestDatabase)
	}
	for _, fallback := range config.Fallbacks {
		if !isLoopback(fallback.Host) {
			return fmt.Errorf("%w: every fallback database host must be loopback", ErrUnsafeTestDatabase)
		}
	}
	if config.Database == "" || strings.Contains(config.Database, "/") || !strings.HasSuffix(config.Database, "_test") {
		return fmt.Errorf("%w: effective database name must end in _test", ErrUnsafeTestDatabase)
	}
	return nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
