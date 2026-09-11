package money

import "testing"

func TestMoneyRequiresNonNegativeIntegerAndISOCurrency(t *testing.T) {
	for _, test := range []struct {
		amount int64
		code   string
		valid  bool
	}{{0, "VND", true}, {1000, "VND", true}, {-1, "VND", false}, {1, "vnd", false}, {1, "VN", false}} {
		value, err := New(test.amount, test.code)
		if (err == nil) != test.valid {
			t.Fatalf("New(%d,%q) error = %v", test.amount, test.code, err)
		}
		if err == nil && !value.Valid() {
			t.Fatalf("valid value rejected: %#v", value)
		}
	}
}
