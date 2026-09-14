package domain

import (
	"errors"
	"testing"
)

func TestValidateVietQRConfig(t *testing.T) {
	got, err := ValidateVietQRConfig(VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: " Nguyen Van A ", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountName != "NGUYEN VAN A" || MaskAccount(got.AccountNumber) != "*********2738" {
		t.Fatalf("validated config = %#v", got)
	}
	for _, invalid := range []VietQRConfigDraft{
		{BankBIN: "97043", AccountNumber: "0017040702738", AccountName: "NGUYEN VAN A"},
		{BankBIN: "970436", AccountNumber: "12x4", AccountName: "NGUYEN VAN A"},
		{BankBIN: "970436", AccountNumber: "1234", AccountName: "NGUYỄN VĂN A"},
	} {
		if _, err := ValidateVietQRConfig(invalid); !errors.Is(err, ErrInvalidPayment) {
			t.Fatalf("ValidateVietQRConfigConfig(%#v) error = %v", invalid, err)
		}
	}
}
