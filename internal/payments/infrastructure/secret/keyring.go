package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var ErrUnavailable = errors.New("payment configuration key unavailable")

type Envelope struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type Keyring struct {
	keys   map[int][32]byte
	active int
}

func New(keys map[int][32]byte, active int) (Keyring, error) {
	if active < 1 || len(keys) == 0 {
		return Keyring{}, ErrUnavailable
	}
	if _, ok := keys[active]; !ok {
		return Keyring{}, ErrUnavailable
	}
	cloned := make(map[int][32]byte, len(keys))
	for version, key := range keys {
		if version < 1 {
			return Keyring{}, ErrUnavailable
		}
		cloned[version] = key
	}
	return Keyring{keys: cloned, active: active}, nil
}

func (keyring Keyring) Encrypt(plaintext, additionalData []byte) (Envelope, error) {
	aead, err := keyring.aead(keyring.active)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate payment configuration nonce: %w", err)
	}
	return Envelope{Ciphertext: aead.Seal(nil, nonce, plaintext, additionalData), Nonce: nonce, KeyVersion: keyring.active}, nil
}

func (keyring Keyring) Decrypt(envelope Envelope, additionalData []byte) ([]byte, error) {
	aead, err := keyring.aead(envelope.KeyVersion)
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(envelope.Nonce) != aead.NonceSize() {
		return nil, ErrUnavailable
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, additionalData)
	if err != nil {
		return nil, ErrUnavailable
	}
	return plaintext, nil
}

func (keyring Keyring) aead(version int) (cipher.AEAD, error) {
	key, ok := keyring.keys[version]
	if !ok {
		return nil, ErrUnavailable
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, ErrUnavailable
	}
	return cipher.NewGCM(block)
}
