package domain

import (
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var ErrInvalidRule = errors.New("invalid shipping rule")

type Draft struct {
	Mode        string `json:"mode"`
	AmountMinor int64  `json:"flat_amount_minor"`
}

type Rule struct {
	StoreID   ids.StoreID        `json:"-"`
	ID        ids.ShippingRuleID `json:"shipping_rule_id"`
	Mode      string             `json:"mode"`
	Price     money.Money        `json:"price"`
	Version   int64              `json:"version"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

func ValidateDraft(draft Draft) (Draft, error) {
	if draft.Mode != "free" && draft.Mode != "flat" {
		return Draft{}, ErrInvalidRule
	}
	if draft.AmountMinor < 0 || (draft.Mode == "free" && draft.AmountMinor != 0) {
		return Draft{}, ErrInvalidRule
	}
	return draft, nil
}
