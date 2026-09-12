package domain

import "testing"

func TestValidateProfileAndVietnameseAddress(t *testing.T) {
	profile, email, phone, err := ValidateProfileDraft(ProfileDraft{FullName: " Nguyen Van A ", Email: " A@Example.com ", Phone: "0901 234 567"})
	if err != nil || profile.FullName != "Nguyen Van A" || email != "a@example.com" || phone != "+84901234567" {
		t.Fatalf("profile = %#v, email = %q, phone = %q, error = %v", profile, email, phone, err)
	}
	address, canonical, err := ValidateAddressDraft(AddressDraft{Label: " Home ", RecipientName: " Nguyen Van A ", Phone: "+84 901 234 567", Line1: " 1 Nguyen Hue ", Ward: "Ben Nghe", District: "District 1", Province: "Ho Chi Minh City", CountryCode: "vn"})
	if err != nil || address.CountryCode != "VN" || canonical != "+84901234567" {
		t.Fatalf("address = %#v, phone = %q, error = %v", address, canonical, err)
	}
}

func TestRejectsInvalidCustomerData(t *testing.T) {
	for _, draft := range []ProfileDraft{
		{FullName: "A", Email: "not-email", Phone: "+84901234567"},
		{FullName: "A", Email: "@xx", Phone: "+84901234567"},
		{FullName: "A", Email: "a@example.com", Phone: "+012345678"},
	} {
		if _, _, _, err := ValidateProfileDraft(draft); err == nil {
			t.Fatalf("invalid profile accepted: %#v", draft)
		}
	}
	if _, _, err := ValidateAddressDraft(AddressDraft{Label: "Home", RecipientName: "A", Phone: "0901234567", Line1: "x", Ward: "w", District: "d", Province: "p", CountryCode: "US"}); err == nil {
		t.Fatal("non-Vietnamese address accepted")
	}
}
