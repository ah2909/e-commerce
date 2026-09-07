package databaseguard

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		allowed   string
		wantError bool
	}{
		{name: "local test database", url: "postgres://user@localhost:5432/ecommerce_test?sslmode=disable", allowed: "true"},
		{name: "loopback IP", url: "postgres://user@127.0.0.1:5432/ecommerce_test", allowed: "true"},
		{name: "missing sentinel", url: "postgres://user@localhost:5432/ecommerce_test", wantError: true},
		{name: "runtime database", url: "postgres://user@localhost:5432/ecommerce", allowed: "true", wantError: true},
		{name: "remote host", url: "postgres://user@database.example/ecommerce_test", allowed: "true", wantError: true},
		{name: "query host override", url: "postgres://user@localhost/ecommerce_test?host=production.example", allowed: "true", wantError: true},
		{name: "query database override", url: "postgres://user@localhost/ecommerce_test?database=ecommerce", allowed: "true", wantError: true},
		{name: "fallback host override", url: "postgres://user@localhost/ecommerce_test?host=localhost,production.example", allowed: "true", wantError: true},
		{name: "wrong scheme", url: "mysql://localhost/ecommerce_test", allowed: "true", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.url, test.allowed)
			if test.wantError && !errors.Is(err, ErrUnsafeTestDatabase) {
				t.Fatalf("Validate() error = %v", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}
