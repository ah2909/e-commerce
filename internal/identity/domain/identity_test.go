package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseEmailCanonicalizesASCIIAddress(t *testing.T) {
	email, err := ParseEmail("  Owner.Example@Example.COM  ")
	if err != nil {
		t.Fatal(err)
	}
	if email.Presented() != "Owner.Example@Example.COM" || email.Canonical() != "owner.example@example.com" {
		t.Fatalf("unexpected email: %#v", email)
	}
}

func TestParseEmailRejectsUnsafeForms(t *testing.T) {
	values := []string{"", "Display <user@example.com>", "người@example.com", "user@example.com,other@example.com", strings.Repeat("a", 245) + "@example.com"}
	for _, value := range values {
		if _, err := ParseEmail(value); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("ParseEmail(%q) error = %v", value, err)
		}
	}
}

func TestValidatePasswordUsesUnmodifiedUTF8Bytes(t *testing.T) {
	if err := ValidatePassword(" mật-khẩu-安全 "); err != nil {
		t.Fatalf("valid UTF-8 password rejected: %v", err)
	}
	if !errors.Is(ValidatePassword("short"), ErrInvalidPassword) || !errors.Is(ValidatePassword(strings.Repeat("x", 129)), ErrInvalidPassword) {
		t.Fatal("password byte limits were not enforced")
	}
}
