package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

const testStoreID = "018f0f7d-7b3d-7d2e-8f4f-3b86f31f2741"

func TestNewStoreVietnamDefaultsAndUnicode(t *testing.T) {
	id, err := ParseStoreID(testStoreID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 10, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	decomposed := "  Cửa hàng A\u0301nh Dương  "

	store, err := NewStore(id, decomposed, DefaultCurrency, DefaultLocale, DefaultTimezone, now)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.Name() != norm.NFC.String(strings.TrimSpace(decomposed)) || !norm.NFC.IsNormalString(store.Name()) {
		t.Fatalf("Name() = %q, want NFC Vietnamese name", store.Name())
	}
	if store.DefaultCurrency() != "VND" || store.Locale() != "vi-VN" || store.Timezone() != "Asia/Ho_Chi_Minh" {
		t.Fatalf("regional settings = %s %s %s", store.DefaultCurrency(), store.Locale(), store.Timezone())
	}
	if store.Status() != StoreStatusActive || store.SettingsVersion() != 1 {
		t.Fatalf("status/version = %s/%d", store.Status(), store.SettingsVersion())
	}
	if !store.CreatedAt().Equal(now) || store.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt() = %v, want UTC instant", store.CreatedAt())
	}
}

func TestNewStorePreservesExplicitInternationalSettings(t *testing.T) {
	id, _ := ParseStoreID(testStoreID)
	store, err := NewStore(id, "Global Store", "usd", "en-US", "America/New_York", time.Now())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.DefaultCurrency() != "USD" || store.Locale() != "en-US" || store.Timezone() != "America/New_York" {
		t.Fatalf("settings = %s %s %s", store.DefaultCurrency(), store.Locale(), store.Timezone())
	}
}

func TestRestoreStoreRejectsInvalidValues(t *testing.T) {
	id, _ := ParseStoreID(testStoreID)
	now := time.Now()
	valid := StoreValues{ID: id, Name: "Cửa hàng", Status: StoreStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1, CreatedAt: now, UpdatedAt: now}
	tests := []struct {
		name   string
		mutate func(*StoreValues)
	}{
		{name: "empty name", mutate: func(v *StoreValues) { v.Name = " " }},
		{name: "long name", mutate: func(v *StoreValues) { v.Name = strings.Repeat("đ", 121) }},
		{name: "status", mutate: func(v *StoreValues) { v.Status = "deleted" }},
		{name: "currency", mutate: func(v *StoreValues) { v.DefaultCurrency = "ZZZ" }},
		{name: "locale", mutate: func(v *StoreValues) { v.Locale = "_" }},
		{name: "timezone", mutate: func(v *StoreValues) { v.Timezone = "Vietnam/Unknown" }},
		{name: "version", mutate: func(v *StoreValues) { v.SettingsVersion = 0 }},
		{name: "timestamps", mutate: func(v *StoreValues) { v.UpdatedAt = now.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := valid
			test.mutate(&values)
			_, err := RestoreStore(values)
			if !errors.Is(err, ErrInvalidStore) {
				t.Fatalf("RestoreStore() error = %v", err)
			}
		})
	}
}

func TestTypedIDs(t *testing.T) {
	tests := []string{"", "00000000-0000-0000-0000-000000000000", "not-a-uuid", "018f0f7d7b3d7d2e8f4f3b86f31f2741"}
	for _, value := range tests {
		if _, err := ParseStoreID(value); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("ParseStoreID(%q) error = %v", value, err)
		}
	}
	generated, err := NewStoreID()
	if err != nil {
		t.Fatalf("NewStoreID() error = %v", err)
	}
	if _, err := ParseStoreID(generated.String()); err != nil {
		t.Fatalf("generated ID = %q: %v", generated, err)
	}
}
