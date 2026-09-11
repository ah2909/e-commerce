package money

import (
	"errors"
	"regexp"
)

var ErrInvalid = errors.New("invalid money")
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

func New(amountMinor int64, currency string) (Money, error) {
	if amountMinor < 0 || !currencyPattern.MatchString(currency) {
		return Money{}, ErrInvalid
	}
	return Money{AmountMinor: amountMinor, Currency: currency}, nil
}

func (value Money) Valid() bool {
	_, err := New(value.AmountMinor, value.Currency)
	return err == nil
}
