package main

import "testing"

func TestValidateAppURLProtectsSandboxSecrets(t *testing.T) {
	for _, value := range []string{"http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "https://sandbox.example"} {
		if err := validateAppURL(value); err != nil {
			t.Fatalf("validateAppURL(%q) = %v", value, err)
		}
	}
	for _, value := range []string{"http://sandbox.example", "http://10.0.0.2:8080", "https://user:pass@sandbox.example"} {
		if err := validateAppURL(value); err == nil {
			t.Fatalf("validateAppURL(%q) accepted unsafe URL", value)
		}
	}
}
