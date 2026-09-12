package domain

import (
	"errors"
	"testing"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestAvailabilityRejectsNegativeAvailable(t *testing.T) {
	if _, err := Availability(2, 3); !errors.Is(err, ErrInvalidInventory) {
		t.Fatalf("error = %v", err)
	}
	available, err := Availability(3, 2)
	if err != nil || available != 1 {
		t.Fatalf("available = %d, error = %v", available, err)
	}
}

func TestValidateAllocationsRejectsDuplicates(t *testing.T) {
	variant, _ := ids.ParseVariantID("11111111-1111-4111-8111-111111111111")
	_, err := ValidateAllocations([]Allocation{{VariantID: variant, Quantity: 1}, {VariantID: variant, Quantity: 1}})
	if !errors.Is(err, ErrInvalidInventory) {
		t.Fatalf("error = %v", err)
	}
}

func TestReservationTransitionsAreTerminal(t *testing.T) {
	if !CanTransition(ReservationActive, ReservationConfirmed) || !CanTransition(ReservationReleased, ReservationReleased) || CanTransition(ReservationReleased, ReservationConfirmed) {
		t.Fatal("unexpected transition policy")
	}
}
