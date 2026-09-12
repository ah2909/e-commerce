package domain

import (
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

const TTL = 30 * 24 * time.Hour
const MaxQuantity int64 = 2147483647
const MaxLines = 100

var ErrInvalidCart = errors.New("invalid cart")

type OwnerKind string
type Status string

const (
	OwnerUser       OwnerKind = "user"
	OwnerGuest      OwnerKind = "guest"
	StatusActive    Status    = "active"
	StatusConverted Status    = "converted"
	StatusExpired   Status    = "expired"
	StatusAbandoned Status    = "abandoned"
)

type Owner struct {
	Kind OwnerKind `json:"kind"`
	ID   string    `json:"-"`
}

type Line struct {
	VariantID ids.VariantID `json:"variant_id"`
	Quantity  int64         `json:"quantity"`
}

type Cart struct {
	StoreID   ids.StoreID `json:"-"`
	ID        ids.CartID  `json:"cart_id"`
	Owner     Owner       `json:"-"`
	Currency  string      `json:"currency"`
	Status    Status      `json:"status"`
	Version   int64       `json:"version"`
	ExpiresAt time.Time   `json:"expires_at"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	ClosedAt  *time.Time  `json:"closed_at,omitempty"`
	Lines     []Line      `json:"-"`
}

func ValidateOwner(owner Owner) error {
	if owner.ID == "" || (owner.Kind != OwnerUser && owner.Kind != OwnerGuest) {
		return ErrInvalidCart
	}
	return nil
}

func ValidateQuantity(quantity int64) error {
	if quantity < 1 || quantity > MaxQuantity {
		return ErrInvalidCart
	}
	return nil
}
