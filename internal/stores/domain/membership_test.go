package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestMembershipValidation(t *testing.T) {
	storeID, _ := ids.ParseStoreID(testStoreID)
	userID, _ := ids.ParseUserID("028f0f7d-7b3d-7d2e-8f4f-3b86f31f2742")
	now := time.Now()
	membership, err := NewMembership(storeID, userID, now)
	if err != nil {
		t.Fatalf("NewMembership() error = %v", err)
	}
	if membership.Status() != MembershipStatusActive || membership.StoreID() != storeID || membership.UserID() != userID {
		t.Fatalf("membership = %#v", membership)
	}
	_, err = RestoreMembership(storeID, userID, "revoked", now, now)
	if !errors.Is(err, ErrInvalidMembership) {
		t.Fatalf("RestoreMembership() error = %v", err)
	}
}
