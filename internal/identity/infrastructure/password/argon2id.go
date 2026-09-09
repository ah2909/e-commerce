package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"golang.org/x/crypto/argon2"
)

const (
	memory      = 64 * 1024
	iterations  = 3
	parallelism = 1
	saltLength  = 16
	keyLength   = 32
)

var ErrInvalidHash = errors.New("invalid password hash")

type Argon2ID struct{ slots chan struct{} }

func NewArgon2ID(maxConcurrent int) (*Argon2ID, error) {
	if maxConcurrent < 1 || maxConcurrent > 32 {
		return nil, errors.New("password hash concurrency must be between 1 and 32")
	}
	return &Argon2ID{slots: make(chan struct{}, maxConcurrent)}, nil
}

func (hasher *Argon2ID) Hash(ctx context.Context, value string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := hasher.derive(ctx, value, salt, memory, iterations, parallelism, keyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (hasher *Argon2ID) Verify(ctx context.Context, value, encoded string) (bool, bool, error) {
	mem, iter, lanes, salt, expected, err := parse(encoded)
	if err != nil {
		return false, false, err
	}
	length64 := uint64(len(expected))
	if length64 > math.MaxUint32 {
		return false, false, ErrInvalidHash
	}
	length := uint32(length64)
	actual, err := hasher.derive(ctx, value, salt, mem, iter, lanes, length)
	if err != nil {
		return false, false, err
	}
	valid := subtle.ConstantTimeCompare(actual, expected) == 1
	return valid, valid && (mem != memory || iter != iterations || lanes != parallelism || len(expected) != keyLength), nil
}

func (hasher *Argon2ID) derive(ctx context.Context, value string, salt []byte, mem, iter uint32, lanes uint8, length uint32) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	select {
	case hasher.slots <- struct{}{}:
	default:
		return nil, application.ErrAuthenticationBusy
	}
	defer func() { <-hasher.slots }()
	return argon2.IDKey([]byte(value), salt, iter, mem, lanes, length), nil
}

func parse(encoded string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	mem64, err1 := strconv.ParseUint(strings.TrimPrefix(parameters[0], "m="), 10, 32)
	iter64, err2 := strconv.ParseUint(strings.TrimPrefix(parameters[1], "t="), 10, 32)
	lanes64, err3 := strconv.ParseUint(strings.TrimPrefix(parameters[2], "p="), 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	key, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || mem64 < 19*1024 || mem64 > 1024*1024 || iter64 < 1 || iter64 > 20 || lanes64 < 1 || lanes64 > 16 || len(salt) < 16 || len(key) < 16 || len(key) > 64 {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	return uint32(mem64), uint32(iter64), uint8(lanes64), salt, key, nil
}
