package main

import (
	"strings"
	"testing"
)

func TestRunValidatesArgumentsBeforeConnecting(t *testing.T) {
	tests := [][]string{nil, {"down"}, {"up", "extra"}}
	for _, arguments := range tests {
		err := run(arguments, func(string) (string, bool) { return "", false })
		if err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatalf("run(%v) error = %v", arguments, err)
		}
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := run([]string{"up"}, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunRejectsUnsafeTestDatabaseBeforeConnecting(t *testing.T) {
	values := map[string]string{
		"TEST_DATABASE_URL":            "postgres://user@production.example/ecommerce_test",
		"ALLOW_TEST_DATABASE_MUTATION": "true",
	}
	for _, command := range []string{"test-up", "test-reset"} {
		err := run([]string{command}, func(key string) (string, bool) { value, ok := values[key]; return value, ok })
		if err == nil || !strings.Contains(err.Error(), "host must be loopback") {
			t.Fatalf("run(%s) error = %v", command, err)
		}
	}
}
