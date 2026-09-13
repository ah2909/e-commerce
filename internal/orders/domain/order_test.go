package domain

import (
	"math"
	"testing"
)

func TestCheckedMoneyRejectsOverflow(t *testing.T) {
	if _, err := CheckedLineTotal(math.MaxInt64, 2); err == nil {
		t.Fatal("expected multiplication overflow")
	}
	if _, err := CheckedAdd(math.MaxInt64, 1); err == nil {
		t.Fatal("expected addition overflow")
	}
}

func TestOrderTransitionsDenyByDefault(t *testing.T) {
	if !CanTransition(StatePendingPayment, StateExpired) || CanTransition(StateExpired, StatePaid) || CanTransition(StatePendingPayment, "unknown") {
		t.Fatal("unexpected transition policy")
	}
}
