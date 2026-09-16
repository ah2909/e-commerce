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

func TestValidateMoMoConfigAndRedaction(t *testing.T) {
	value, err := ValidateMoMoConfig(MoMoConfigDraft{PartnerCode: " MOMOTESTPARTNER ", AccessKey: " access-key-123 ", SecretKey: " secret-key-123456789 ", Enabled: true})
	if err != nil || value.PartnerCode != "MOMOTESTPARTNER" || MaskPartnerCode(value.PartnerCode) != "MO***********ER" {
		t.Fatalf("ValidateMoMoConfig() = %#v, %v", value, err)
	}
	for _, invalid := range []MoMoConfigDraft{
		{PartnerCode: "x", AccessKey: "access-key", SecretKey: "secret-key-123456"},
		{PartnerCode: "partner", AccessKey: "short", SecretKey: "secret-key-123456"},
		{PartnerCode: "partner", AccessKey: "access-key", SecretKey: "short"},
		{PartnerCode: "par\ntner", AccessKey: "access-key", SecretKey: "secret-key-123456"},
	} {
		if _, err := ValidateMoMoConfig(invalid); err == nil {
			t.Fatalf("ValidateMoMoConfig(%#v) accepted invalid input", invalid)
		}
	}
}

func TestValidActionURLRestrictsProviderOrigin(t *testing.T) {
	if !ValidActionURL("https://test-payment.momo.vn/v2/gateway/pay?t=x", true) {
		t.Fatal("ValidActionURL() rejected sandbox URL")
	}
	for _, value := range []string{"http://test-payment.momo.vn/pay", "https://evil.example/pay", "https://payment.momo.vn/pay"} {
		if ValidActionURL(value, true) {
			t.Fatalf("ValidActionURL(%q) accepted unsafe URL", value)
		}
	}
}
