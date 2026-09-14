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
