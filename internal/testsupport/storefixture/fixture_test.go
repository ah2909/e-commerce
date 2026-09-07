//go:build integration

package storefixture

import (
	"errors"
	"testing"
)

func TestBuildPairRetainsFirstStoreWhenSecondCreationFails(t *testing.T) {
	calls := 0
	pair, created, err := buildPair(func() (Store, error) {
		calls++
		if calls == 2 {
			return Store{}, errors.New("second creation failed")
		}
		return Store{}, nil
	})
	if err == nil || calls != 2 || len(created) != 1 || pair.A != created[0] {
		t.Fatalf("buildPair() = pair %#v, created %d, calls %d, error %v", pair, len(created), calls, err)
	}
}
