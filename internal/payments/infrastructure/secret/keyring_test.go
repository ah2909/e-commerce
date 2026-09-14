package secret

import (
	"bytes"
	"errors"
	"testing"
)

func TestKeyringAuthenticatesCiphertextAndScope(t *testing.T) {
	keys := map[int][32]byte{2: [32]byte(bytes.Repeat([]byte{0x42}, 32))}
	keyring, err := New(keys, 2)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := keyring.Encrypt([]byte("account-secret"), []byte("store-a|vietqr|1"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := keyring.Decrypt(envelope, []byte("store-a|vietqr|1"))
	if err != nil || string(plaintext) != "account-secret" || envelope.KeyVersion != 2 || len(envelope.Nonce) != 12 {
		t.Fatalf("decrypt = %q, envelope = %#v, error = %v", plaintext, envelope, err)
	}
	if _, err := keyring.Decrypt(envelope, []byte("store-b|vietqr|1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong AAD error = %v", err)
	}
	tampered := envelope
	tampered.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	tampered.Ciphertext[0] ^= 1
	if _, err := keyring.Decrypt(tampered, []byte("store-a|vietqr|1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("tampered error = %v", err)
	}
	envelope.KeyVersion = 9
	if _, err := keyring.Decrypt(envelope, []byte("store-a|vietqr|1")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown version error = %v", err)
	}
}
