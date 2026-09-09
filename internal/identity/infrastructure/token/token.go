package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
)

const rawLength = 32

type Generator struct{}

func (Generator) Generate() (string, identitydomain.TokenDigest, error) {
	return Generate()
}

func (Generator) Parse(value string) (identitydomain.TokenDigest, error) {
	return Parse(value)
}

func (Generator) NewSessionID() (string, error) {
	return identitydomain.NewSessionID()
}

func Generate() (string, identitydomain.TokenDigest, error) {
	raw := make([]byte, rawLength)
	if _, err := rand.Read(raw); err != nil {
		return "", identitydomain.TokenDigest{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	return value, sha256.Sum256([]byte(value)), nil
}

func Parse(value string) (identitydomain.TokenDigest, error) {
	if len(value) != base64.RawURLEncoding.EncodedLen(rawLength) {
		return identitydomain.TokenDigest{}, errors.New("invalid token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != rawLength {
		return identitydomain.TokenDigest{}, errors.New("invalid token")
	}
	return sha256.Sum256([]byte(value)), nil
}
