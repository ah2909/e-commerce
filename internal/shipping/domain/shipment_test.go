package domain

import (
	"errors"
	"testing"
)

func TestValidateShipmentDetails(t *testing.T) {
	got, err := ValidateDetails(Details{Carrier: "  Viettel   Post ", TrackingNumber: " VP123 "})
	if err != nil || got.Carrier != "Viettel Post" || got.TrackingNumber != "VP123" {
		t.Fatalf("details = %#v, error = %v", got, err)
	}
	if _, err := ValidateDetails(Details{Carrier: "Carrier", TrackingNumber: "bad value"}); !errors.Is(err, ErrInvalidShipment) {
		t.Fatalf("invalid tracking error = %v", err)
	}
}

func TestShipmentTransitions(t *testing.T) {
	if !CanTransition(ShipmentProcessing, ShipmentShipped) || !CanTransition(ShipmentShipped, ShipmentCompleted) || CanTransition(ShipmentProcessing, ShipmentCompleted) || CanTransition(ShipmentCompleted, ShipmentShipped) {
		t.Fatal("shipment transition policy mismatch")
	}
}
