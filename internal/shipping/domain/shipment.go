package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrInvalidShipment = errors.New("invalid shipment")

type ShipmentState string

const (
	ShipmentProcessing ShipmentState = "processing"
	ShipmentShipped    ShipmentState = "shipped"
	ShipmentCompleted  ShipmentState = "completed"
)

type Shipment struct {
	StoreID        ids.StoreID    `json:"-"`
	ID             ids.ShipmentID `json:"shipment_id"`
	OrderID        ids.OrderID    `json:"order_id"`
	State          ShipmentState  `json:"state"`
	Carrier        string         `json:"carrier"`
	TrackingNumber string         `json:"tracking_number"`
	Version        int64          `json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ShippedAt      *time.Time     `json:"shipped_at"`
	CompletedAt    *time.Time     `json:"completed_at"`
}

type Details struct {
	Carrier        string `json:"carrier"`
	TrackingNumber string `json:"tracking_number"`
}

func ValidateDetails(value Details) (Details, error) {
	value.Carrier = strings.Join(strings.Fields(value.Carrier), " ")
	value.TrackingNumber = strings.TrimSpace(value.TrackingNumber)
	if len(value.Carrier) < 1 || len(value.Carrier) > 120 || len(value.TrackingNumber) < 1 || len(value.TrackingNumber) > 160 {
		return Details{}, ErrInvalidShipment
	}
	for _, char := range value.TrackingNumber {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return Details{}, ErrInvalidShipment
		}
	}
	return value, nil
}

func CanTransition(from, to ShipmentState) bool {
	return (from == ShipmentProcessing && to == ShipmentShipped) || (from == ShipmentShipped && to == ShipmentCompleted)
}
