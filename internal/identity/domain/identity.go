package domain

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrInvalidEmail    = errors.New("invalid email")
	ErrInvalidPassword = errors.New("invalid password")
	ErrInvalidProvider = errors.New("invalid social provider")
)

type UserStatus string
type Provider string
type TokenDigest [32]byte

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
	ProviderGoogle     Provider   = "google"
	ProviderFacebook   Provider   = "facebook"
)

type Email struct {
	presented string
	canonical string
}

func ParseEmail(value string) (Email, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 || !utf8.ValidString(value) {
		return Email{}, ErrInvalidEmail
	}
	for _, r := range value {
		if r > 127 {
			return Email{}, ErrInvalidEmail
		}
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Name != "" || parsed.Address != value || strings.Count(value, "@") != 1 {
		return Email{}, ErrInvalidEmail
	}
	parts := strings.Split(value, "@")
	if parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], ".") || strings.HasSuffix(parts[1], ".") {
		return Email{}, ErrInvalidEmail
	}
	return Email{presented: value, canonical: strings.ToLower(value)}, nil
}

func ValidatePassword(value string) error {
	if len(value) < 12 || len(value) > 128 || !utf8.ValidString(value) {
		return ErrInvalidPassword
	}
	return nil
}

func ParseProvider(value string) (Provider, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(value)))
	if provider != ProviderGoogle && provider != ProviderFacebook {
		return "", ErrInvalidProvider
	}
	return provider, nil
}

func (email Email) Presented() string { return email.presented }
func (email Email) Canonical() string { return email.canonical }

type User struct {
	ID        ids.UserID
	Email     *Email
	Status    UserStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SocialIdentity struct {
	Provider Provider
	Subject  string
}

func NewSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
