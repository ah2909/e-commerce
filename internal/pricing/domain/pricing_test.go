package domain

import (
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestPriceListValidationAndArchive(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	listID, _ := ids.ParsePriceListID("22222222-2222-4222-8222-222222222222")
	variant, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	now := time.Date(2026, 9, 10, 1, 2, 3, 0, time.FixedZone("ICT", 7*60*60))
	until := now.Add(time.Hour)
	list, err := NewPriceList(store, listID, PriceListDraft{Name: " Retail ", Channel: ChannelStorefront, Currency: CurrencyVND, Priority: 10, ValidFrom: &now, ValidUntil: &until, Prices: []PriceDraft{{VariantID: variant, AmountMinor: 150000}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if list.Name != "Retail" || list.ValidFrom.Location() != time.UTC || list.Prices[0].Money.AmountMinor != 150000 {
		t.Fatalf("list = %#v", list)
	}
	archived, err := list.Archived(now.Add(time.Minute))
	if err != nil || archived.Status != StatusArchived || archived.Version != 2 {
		t.Fatalf("archive = %#v, %v", archived, err)
	}
}

func TestPriceListRejectsInvalidMoneyWindowAndDuplicates(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	listID, _ := ids.ParsePriceListID("22222222-2222-4222-8222-222222222222")
	variant, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	now := time.Now()
	before := now.Add(-time.Hour)
	for _, draft := range []PriceListDraft{
		{Name: "x", Channel: ChannelStorefront, Currency: CurrencyVND, Prices: []PriceDraft{{VariantID: variant, AmountMinor: -1}}},
		{Name: "x", Channel: ChannelStorefront, Currency: CurrencyVND, ValidFrom: &now, ValidUntil: &before, Prices: []PriceDraft{{VariantID: variant, AmountMinor: 1}}},
		{Name: "x", Channel: ChannelStorefront, Currency: CurrencyVND, Prices: []PriceDraft{{VariantID: variant, AmountMinor: 1}, {VariantID: variant, AmountMinor: 2}}},
	} {
		if _, err := NewPriceList(store, listID, draft, now); err == nil {
			t.Fatalf("accepted invalid draft %#v", draft)
		}
	}
}
