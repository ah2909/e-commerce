package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalid = errors.New("invalid identifier")

type StoreID string
type UserID string
type GuestID string

func ParseStoreID(value string) (StoreID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse store ID: %w", err)
	}
	return StoreID(value), nil
}

func ParseUserID(value string) (UserID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse user ID: %w", err)
	}
	return UserID(value), nil
}

func ParseGuestID(value string) (GuestID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse guest ID: %w", err)
	}
	return GuestID(value), nil
}

func NewStoreID() (StoreID, error) { value, err := newUUID(); return StoreID(value), err }
func NewUserID() (UserID, error)   { value, err := newUUID(); return UserID(value), err }
func NewGuestID() (GuestID, error) { value, err := newUUID(); return GuestID(value), err }

func (id StoreID) String() string { return string(id) }
func (id UserID) String() string  { return string(id) }
func (id GuestID) String() string { return string(id) }

func parseUUID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", ErrInvalid
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != 16 {
		return "", ErrInvalid
	}
	var nonzero bool
	for _, part := range decoded {
		nonzero = nonzero || part != 0
	}
	if !nonzero {
		return "", ErrInvalid
	}
	return value, nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
