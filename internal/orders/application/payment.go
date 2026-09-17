package application

import (
	"time"

	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type PayableOrder struct {
	OrderID              ids.OrderID
	State                domain.State
	Version              int64
	ReservationExpiresAt time.Time
}

type PaymentOrder struct {
	OrderID              ids.OrderID
	State                domain.State
	ReservationExpiresAt time.Time
}

type RefundLine struct {
	VariantID ids.VariantID
	Quantity  int64
}

type RefundOrder struct {
	OrderID ids.OrderID
	State   domain.State
	Version int64
	Lines   []RefundLine
}
