package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

const MaxQuantity int64 = 2147483647
const ReservationTTL = 10 * time.Minute

var (
	ErrInvalidInventory  = errors.New("invalid inventory")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrInvalidTransition = errors.New("invalid reservation transition")
)

type ReservationStatus string

const (
	ReservationActive    ReservationStatus = "active"
	ReservationConfirmed ReservationStatus = "confirmed"
	ReservationReleased  ReservationStatus = "released"
	ReservationExpired   ReservationStatus = "expired"
)

type StockItem struct {
	StoreID   ids.StoreID   `json:"-"`
	VariantID ids.VariantID `json:"variant_id"`
	OnHand    int64         `json:"on_hand"`
	Reserved  int64         `json:"reserved"`
	Available int64         `json:"available"`
	Version   int64         `json:"version"`
	CreatedAt time.Time     `json:"created_at,omitzero"`
	UpdatedAt time.Time     `json:"updated_at,omitzero"`
}

type Adjustment struct {
	VariantID       ids.VariantID
	QuantityDelta   int64
	Reason          string
	ExpectedVersion int64
	IdempotencyKey  string
	ActorID         ids.UserID
	RequestID       string
}

type Allocation struct {
	VariantID ids.VariantID `json:"variant_id"`
	Quantity  int64         `json:"quantity"`
}

type Reservation struct {
	StoreID     ids.StoreID       `json:"-"`
	ID          ids.ReservationID `json:"reservation_id"`
	OrderID     ids.OrderID       `json:"order_id"`
	VariantID   ids.VariantID     `json:"variant_id"`
	Quantity    int64             `json:"quantity"`
	Status      ReservationStatus `json:"status"`
	ExpiresAt   time.Time         `json:"expires_at"`
	Version     int64             `json:"version"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	ClosedAt    *time.Time        `json:"closed_at,omitempty"`
	CloseReason string            `json:"close_reason,omitempty"`
}

func ValidateAdjustment(value Adjustment) (Adjustment, error) {
	value.Reason = strings.TrimSpace(value.Reason)
	value.IdempotencyKey = strings.TrimSpace(value.IdempotencyKey)
	value.RequestID = strings.TrimSpace(value.RequestID)
	if value.VariantID == "" || value.ActorID == "" || value.QuantityDelta == 0 || value.QuantityDelta < -MaxQuantity || value.QuantityDelta > MaxQuantity || len(value.Reason) < 1 || len(value.Reason) > 240 || value.ExpectedVersion < 0 || len(value.IdempotencyKey) < 1 || len(value.IdempotencyKey) > 128 || len(value.RequestID) < 1 || len(value.RequestID) > 128 {
		return Adjustment{}, ErrInvalidInventory
	}
	return value, nil
}

func ValidateAllocations(values []Allocation) ([]Allocation, error) {
	if len(values) == 0 || len(values) > 100 {
		return nil, ErrInvalidInventory
	}
	seen := make(map[ids.VariantID]struct{}, len(values))
	result := make([]Allocation, len(values))
	copy(result, values)
	for _, value := range result {
		if value.VariantID == "" || value.Quantity < 1 || value.Quantity > MaxQuantity {
			return nil, ErrInvalidInventory
		}
		if _, ok := seen[value.VariantID]; ok {
			return nil, ErrInvalidInventory
		}
		seen[value.VariantID] = struct{}{}
	}
	return result, nil
}

func NewImplicitStock(storeID ids.StoreID, variantID ids.VariantID) (StockItem, error) {
	if storeID == "" || variantID == "" {
		return StockItem{}, ErrInvalidInventory
	}
	return StockItem{StoreID: storeID, VariantID: variantID}, nil
}

func Availability(onHand, reserved int64) (int64, error) {
	if onHand < 0 || onHand > MaxQuantity || reserved < 0 || reserved > onHand {
		return 0, ErrInvalidInventory
	}
	return onHand - reserved, nil
}

func CanTransition(current, target ReservationStatus) bool {
	if current == target {
		return true
	}
	return current == ReservationActive && (target == ReservationConfirmed || target == ReservationReleased || target == ReservationExpired)
}
