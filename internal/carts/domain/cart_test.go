package domain

import "testing"

func TestOwnerAndQuantityValidation(t *testing.T) {
	if ValidateOwner(Owner{Kind: OwnerUser, ID: "user"}) != nil || ValidateOwner(Owner{Kind: OwnerGuest, ID: "guest"}) != nil {
		t.Fatal("valid owner rejected")
	}
	if ValidateOwner(Owner{Kind: "staff", ID: "user"}) == nil || ValidateOwner(Owner{Kind: OwnerUser}) == nil {
		t.Fatal("invalid owner accepted")
	}
	if ValidateQuantity(1) != nil || ValidateQuantity(MaxQuantity) != nil {
		t.Fatal("valid quantity rejected")
	}
	if ValidateQuantity(0) == nil || ValidateQuantity(MaxQuantity+1) == nil {
		t.Fatal("invalid quantity accepted")
	}
}
