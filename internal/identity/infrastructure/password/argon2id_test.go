package password

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ah2909/e-commerce/internal/identity/application"
)

func TestArgon2IDHashVerifyAndUniqueSalt(t *testing.T) {
	hasher, err := NewArgon2ID(1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hasher.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hasher.Hash(context.Background(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected Argon2id encoding")
	}
	valid, rehash, err := hasher.Verify(context.Background(), "correct horse battery staple", first)
	if err != nil || !valid || rehash {
		t.Fatalf("Verify() = %v, %v, %v", valid, rehash, err)
	}
	valid, _, err = hasher.Verify(context.Background(), "wrong password", first)
	if err != nil || valid {
		t.Fatalf("wrong password Verify() = %v, %v", valid, err)
	}
}

func TestArgon2IDRejectsMalformedOrDangerousHashes(t *testing.T) {
	hasher, _ := NewArgon2ID(1)
	values := []string{"bad", "$argon2id$v=19$m=9999999,t=3,p=1$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcA"}
	for _, value := range values {
		if _, _, err := hasher.Verify(context.Background(), "password", value); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Verify malformed hash error = %v", err)
		}
	}
	if _, err := NewArgon2ID(0); err == nil {
		t.Fatal("zero hash concurrency accepted")
	}
}

func TestArgon2IDAdmissionRejectsCapacityAndCancellationWithoutWaiting(t *testing.T) {
	hasher, _ := NewArgon2ID(1)
	hasher.slots <- struct{}{}
	if _, err := hasher.Hash(context.Background(), "correct horse battery staple"); !errors.Is(err, application.ErrAuthenticationBusy) {
		t.Fatalf("Hash() at capacity error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hasher.Hash(canceled, "correct horse battery staple"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash() canceled error = %v", err)
	}
	<-hasher.slots
}
