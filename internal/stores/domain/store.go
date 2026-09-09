package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultCurrency = "VND"
	DefaultLocale   = "vi-VN"
	DefaultTimezone = "Asia/Ho_Chi_Minh"
	maxNameRunes    = 120
)

var ErrInvalidStore = errors.New("invalid store")

type Subdomain string

var reservedSubdomains = map[string]struct{}{"admin": {}, "api": {}, "auth": {}, "www": {}}

func ParseSubdomain(value string) (Subdomain, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return "", fmt.Errorf("%w: subdomain", ErrInvalidStore)
	}
	if _, reserved := reservedSubdomains[value]; reserved {
		return "", fmt.Errorf("%w: reserved subdomain", ErrInvalidStore)
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return "", fmt.Errorf("%w: subdomain", ErrInvalidStore)
		}
	}
	return Subdomain(value), nil
}

func (subdomain Subdomain) String() string { return string(subdomain) }

type StoreStatus string

const (
	StoreStatusActive    StoreStatus = "active"
	StoreStatusSuspended StoreStatus = "suspended"
)

type Store struct {
	id              ids.StoreID
	subdomain       Subdomain
	name            string
	status          StoreStatus
	defaultCurrency string
	locale          string
	timezone        string
	settingsVersion int64
	createdAt       time.Time
	updatedAt       time.Time
}

type StoreValues struct {
	ID              ids.StoreID
	Subdomain       Subdomain
	Name            string
	Status          StoreStatus
	DefaultCurrency string
	Locale          string
	Timezone        string
	SettingsVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewStore(id ids.StoreID, subdomainValue, name, defaultCurrency, locale, timezone string, now time.Time) (Store, error) {
	subdomain, err := ParseSubdomain(subdomainValue)
	if err != nil {
		return Store{}, err
	}
	return RestoreStore(StoreValues{
		ID:              id,
		Subdomain:       subdomain,
		Name:            name,
		Status:          StoreStatusActive,
		DefaultCurrency: defaultCurrency,
		Locale:          locale,
		Timezone:        timezone,
		SettingsVersion: 1,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
}

func RestoreStore(values StoreValues) (Store, error) {
	if _, err := ids.ParseStoreID(values.ID.String()); err != nil {
		return Store{}, fmt.Errorf("%w: store ID", ErrInvalidStore)
	}
	subdomain, err := ParseSubdomain(values.Subdomain.String())
	if err != nil {
		return Store{}, err
	}
	name := norm.NFC.String(strings.TrimSpace(values.Name))
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return Store{}, fmt.Errorf("%w: name", ErrInvalidStore)
	}
	if values.Status != StoreStatusActive && values.Status != StoreStatusSuspended {
		return Store{}, fmt.Errorf("%w: status", ErrInvalidStore)
	}
	currencyCode, err := normalizeCurrency(values.DefaultCurrency)
	if err != nil {
		return Store{}, err
	}
	localeTag, err := language.Parse(strings.TrimSpace(values.Locale))
	if err != nil {
		return Store{}, fmt.Errorf("%w: locale", ErrInvalidStore)
	}
	location, err := time.LoadLocation(strings.TrimSpace(values.Timezone))
	if err != nil || location.String() == "Local" {
		return Store{}, fmt.Errorf("%w: timezone", ErrInvalidStore)
	}
	if values.SettingsVersion < 1 {
		return Store{}, fmt.Errorf("%w: settings version", ErrInvalidStore)
	}
	createdAt := values.CreatedAt.UTC()
	updatedAt := values.UpdatedAt.UTC()
	if createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return Store{}, fmt.Errorf("%w: timestamps", ErrInvalidStore)
	}
	return Store{
		id:              values.ID,
		subdomain:       subdomain,
		name:            name,
		status:          values.Status,
		defaultCurrency: currencyCode,
		locale:          localeTag.String(),
		timezone:        location.String(),
		settingsVersion: values.SettingsVersion,
		createdAt:       createdAt,
		updatedAt:       updatedAt,
	}, nil
}

func normalizeCurrency(value string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(value))
	unit, err := currency.ParseISO(code)
	if err != nil || unit.String() != code {
		return "", fmt.Errorf("%w: default currency", ErrInvalidStore)
	}
	return code, nil
}

func (s Store) ID() ids.StoreID         { return s.id }
func (s Store) Subdomain() Subdomain    { return s.subdomain }
func (s Store) Name() string            { return s.name }
func (s Store) Status() StoreStatus     { return s.status }
func (s Store) DefaultCurrency() string { return s.defaultCurrency }
func (s Store) Locale() string          { return s.locale }
func (s Store) Timezone() string        { return s.timezone }
func (s Store) SettingsVersion() int64  { return s.settingsVersion }
func (s Store) CreatedAt() time.Time    { return s.createdAt }
func (s Store) UpdatedAt() time.Time    { return s.updatedAt }
func (s Store) Values() StoreValues {
	return StoreValues{ID: s.id, Subdomain: s.subdomain, Name: s.name, Status: s.status, DefaultCurrency: s.defaultCurrency, Locale: s.locale, Timezone: s.timezone, SettingsVersion: s.settingsVersion, CreatedAt: s.createdAt, UpdatedAt: s.updatedAt}
}
