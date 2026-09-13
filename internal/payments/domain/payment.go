package domain

import (
	"errors"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var ErrInvalidPayment = errors.New("invalid payment")

type Payment struct {
	StoreID  ids.StoreID
	ID       ids.PaymentID
	OrderID  ids.OrderID
	Provider string
	Amount   money.Money
	State    string
	Version  int64
}
