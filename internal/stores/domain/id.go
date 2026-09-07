package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidID = errors.New("invalid identifier")

type StoreID string

type UserID string

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

func NewStoreID() (StoreID, error) {
	value, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("generate store ID: %w", err)
	}
	return StoreID(value), nil
}

func NewUserID() (UserID, error) {
	value, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("generate user ID: %w", err)
	}
	return UserID(value), nil
}

func (id StoreID) String() string {
	return string(id)
}

func (id UserID) String() string {
	return string(id)
}

func parseUUID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", ErrInvalidID
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return "", ErrInvalidID
	}
	allZero := true
	for _, part := range decoded {
		if part != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "", ErrInvalidID
	}
	return value, nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
