package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrNotFound    = errors.New("payment resource not found")
	ErrConflict    = errors.New("payment operation conflict")
	ErrForbidden   = errors.New("payment operation forbidden")
	ErrUnavailable = errors.New("payment operation unavailable")
	ErrDependency  = errors.New("payment dependency unavailable")
)

type Repository interface {
	GetConfig(context.Context, ids.StoreID) (domain.VietQRConfig, error)
	ReplaceConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.VietQRConfigDraft) (domain.VietQRConfig, error)
	CreateInstruction(context.Context, InstructionCommand, ids.PaymentAttemptID, string, [32]byte) (domain.Instruction, error)
	Confirm(context.Context, ConfirmationCommand, [32]byte) (domain.Confirmation, error)
	ListReconciliation(context.Context, ids.StoreID, int, time.Time, ids.ReconciliationID) ([]domain.Reconciliation, error)
}

type InstructionCommand struct {
	StoreID        ids.StoreID
	OrderID        ids.OrderID
	PaymentID      ids.PaymentID
	IdempotencyKey string
	CorrelationID  string
}

type ConfirmationCommand struct {
	StoreID             ids.StoreID
	PaymentID           ids.PaymentID
	ActorID             ids.UserID
	IdempotencyKey      string
	TransferReference   string
	ObservedAmountMinor int64
	ReceivedAt          time.Time
	RequestID           string
	CorrelationID       string
}

type Service struct{ repository Repository }

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) GetConfig(ctx context.Context, storeID ids.StoreID) (domain.VietQRConfig, error) {
	if storeID == "" {
		return domain.VietQRConfig{}, domain.ErrInvalidPayment
	}
	return service.repository.GetConfig(ctx, storeID)
}

func (service Service) ReplaceConfig(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, requestID string, expected int64, draft domain.VietQRConfigDraft) (domain.VietQRConfig, error) {
	validated, err := domain.ValidateVietQRConfig(draft)
	if err != nil || storeID == "" || actorID == "" || requestID == "" || expected < 0 {
		return domain.VietQRConfig{}, domain.ErrInvalidPayment
	}
	return service.repository.ReplaceConfig(ctx, storeID, actorID, requestID, expected, validated)
}

func (service Service) CreateInstruction(ctx context.Context, command InstructionCommand) (domain.Instruction, error) {
	if command.StoreID == "" || command.OrderID == "" || command.PaymentID == "" || !domain.ValidIdempotencyKey(command.IdempotencyKey) || strings.TrimSpace(command.CorrelationID) == "" {
		return domain.Instruction{}, domain.ErrInvalidPayment
	}
	attemptID, err := ids.NewPaymentAttemptID()
	if err != nil {
		return domain.Instruction{}, err
	}
	reference, err := newReference()
	if err != nil {
		return domain.Instruction{}, err
	}
	fingerprint := sha256.Sum256([]byte(command.OrderID.String() + "\x00" + command.PaymentID.String()))
	return service.repository.CreateInstruction(ctx, command, attemptID, reference, fingerprint)
}

func (service Service) Confirm(ctx context.Context, command ConfirmationCommand) (domain.Confirmation, error) {
	command.TransferReference = strings.TrimSpace(command.TransferReference)
	if command.StoreID == "" || command.PaymentID == "" || command.ActorID == "" || !domain.ValidIdempotencyKey(command.IdempotencyKey) || !domain.ValidTransferReference(command.TransferReference) || command.ObservedAmountMinor < 0 || command.ReceivedAt.IsZero() || command.RequestID == "" || command.CorrelationID == "" {
		return domain.Confirmation{}, domain.ErrInvalidPayment
	}
	command.ReceivedAt = command.ReceivedAt.UTC()
	fingerprint := sha256.Sum256([]byte(command.TransferReference + "\x00" + strconv.FormatInt(command.ObservedAmountMinor, 10) + "\x00" + command.ReceivedAt.Format(time.RFC3339Nano)))
	return service.repository.Confirm(ctx, command, fingerprint)
}

func (service Service) ListReconciliation(ctx context.Context, storeID ids.StoreID, limit int, before time.Time, beforeID ids.ReconciliationID) ([]domain.Reconciliation, error) {
	if storeID == "" || limit < 1 || limit > 100 || (before.IsZero() != (beforeID == "")) {
		return nil, domain.ErrInvalidPayment
	}
	return service.repository.ListReconciliation(ctx, storeID, limit, before.UTC(), beforeID)
}

func newReference() (string, error) {
	var raw [11]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "VQ" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}
