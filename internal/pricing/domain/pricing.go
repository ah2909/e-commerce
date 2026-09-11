package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"golang.org/x/text/unicode/norm"
)

var ErrInvalidPricing = errors.New("invalid pricing data")

type Status string

const (
	StatusActive      Status = "active"
	StatusArchived    Status = "archived"
	ChannelStorefront        = "storefront"
	CurrencyVND              = "VND"
)

type Price struct {
	StoreID     ids.StoreID     `json:"store_id"`
	PriceListID ids.PriceListID `json:"price_list_id"`
	VariantID   ids.VariantID   `json:"variant_id"`
	Money       money.Money     `json:"money"`
	Version     int64           `json:"version"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type PriceList struct {
	StoreID    ids.StoreID     `json:"store_id"`
	ID         ids.PriceListID `json:"price_list_id"`
	Name       string          `json:"name"`
	Channel    string          `json:"channel"`
	Currency   string          `json:"currency"`
	Priority   int             `json:"priority"`
	ValidFrom  *time.Time      `json:"valid_from"`
	ValidUntil *time.Time      `json:"valid_until"`
	Status     Status          `json:"status"`
	Version    int64           `json:"version"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	ArchivedAt *time.Time      `json:"archived_at"`
	Prices     []Price         `json:"prices"`
}

type PriceDraft struct {
	VariantID   ids.VariantID
	AmountMinor int64
}

type PriceListDraft struct {
	Name       string
	Channel    string
	Currency   string
	Priority   int
	ValidFrom  *time.Time
	ValidUntil *time.Time
	Prices     []PriceDraft
}

func NewPriceList(storeID ids.StoreID, id ids.PriceListID, draft PriceListDraft, now time.Time) (PriceList, error) {
	result := PriceList{StoreID: storeID, ID: id, Status: StatusActive, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := result.replace(draft, now.UTC(), false); err != nil {
		return PriceList{}, err
	}
	return result, nil
}

func (list PriceList) Replaced(draft PriceListDraft, now time.Time) (PriceList, error) {
	if list.Status != StatusActive {
		return PriceList{}, ErrInvalidPricing
	}
	if err := list.replace(draft, now.UTC(), true); err != nil {
		return PriceList{}, err
	}
	return list, nil
}

func (list PriceList) Archived(now time.Time) (PriceList, error) {
	if list.Status != StatusActive {
		return PriceList{}, ErrInvalidPricing
	}
	archived := now.UTC()
	list.Status, list.ArchivedAt, list.UpdatedAt, list.Version = StatusArchived, &archived, archived, list.Version+1
	return list, nil
}

func (list *PriceList) replace(draft PriceListDraft, now time.Time, increment bool) error {
	name := norm.NFC.String(strings.TrimSpace(draft.Name))
	if list.StoreID == "" || list.ID == "" || !validText(name, 1, 120) || draft.Channel != ChannelStorefront || draft.Currency != CurrencyVND || draft.Priority < -1000000 || draft.Priority > 1000000 || len(draft.Prices) == 0 || len(draft.Prices) > 1000 {
		return ErrInvalidPricing
	}
	from, until := utcPointer(draft.ValidFrom), utcPointer(draft.ValidUntil)
	if from != nil && until != nil && !from.Before(*until) {
		return ErrInvalidPricing
	}
	prices := make([]Price, 0, len(draft.Prices))
	seen := map[ids.VariantID]bool{}
	for _, input := range draft.Prices {
		value, err := money.New(input.AmountMinor, draft.Currency)
		if err != nil || input.VariantID == "" || seen[input.VariantID] {
			return ErrInvalidPricing
		}
		seen[input.VariantID] = true
		prices = append(prices, Price{StoreID: list.StoreID, PriceListID: list.ID, VariantID: input.VariantID, Money: value, Version: 1, CreatedAt: now, UpdatedAt: now})
	}
	list.Name, list.Channel, list.Currency, list.Priority = name, draft.Channel, draft.Currency, draft.Priority
	list.ValidFrom, list.ValidUntil, list.Prices, list.UpdatedAt = from, until, prices, now
	if increment {
		list.Version++
	}
	return nil
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func validText(value string, minimum, maximum int) bool {
	count := utf8.RuneCountInString(value)
	if count < minimum || count > maximum {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
