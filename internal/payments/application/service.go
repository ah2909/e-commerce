package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
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

type MoMoCredentials struct {
	PartnerCode   string
	AccessKey     string
	SecretKey     string
	CallbackToken string
}

type MoMoClaim struct {
	Attempt     domain.MoMoAttempt
	Credentials MoMoCredentials
	AmountMinor int64
	MoMoOrderID string
	RequestID   string
	ShouldCall  bool
}

type MoMoCreateRequest struct {
	MoMoCredentials
	AmountMinor int64
	MoMoOrderID string
	RequestID   string
	OrderInfo   string
	StoreID     ids.StoreID
}

type MoMoCreateResponse struct {
	ResultCode int
	Message    string
	PayURL     string
	Deeplink   string
	QRCodeURL  string
	Final      bool
	Retryable  bool
}

type MoMoCallbackContext struct {
	StoreID       ids.StoreID
	ConfigVersion int64
	Credentials   MoMoCredentials
}

type MoMoRepository interface {
	GetMoMoConfig(context.Context, ids.StoreID) (domain.MoMoConfig, error)
	ReplaceMoMoConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.MoMoConfigDraft, string, [32]byte) (domain.MoMoConfig, error)
	ClaimMoMoAttempt(context.Context, InstructionCommand, ids.PaymentAttemptID, [32]byte) (MoMoClaim, error)
	FinalizeMoMoAttempt(context.Context, ids.StoreID, ids.PaymentAttemptID, MoMoCreateResponse) (domain.MoMoAttempt, error)
	FailMoMoAttempt(context.Context, ids.StoreID, ids.PaymentAttemptID, bool) (domain.MoMoAttempt, error)
	ResolveMoMoCallback(context.Context, [32]byte) (MoMoCallbackContext, error)
	ProcessMoMoIPN(context.Context, MoMoCallbackContext, domain.MoMoIPN, [32]byte, string) (domain.WebhookResult, error)
}

type MoMoGateway interface {
	Create(context.Context, MoMoCreateRequest) (MoMoCreateResponse, error)
	VerifyIPN([]byte, MoMoCredentials) (domain.MoMoIPN, error)
}

type MoMoGatewayError struct {
	Final bool
}

func (err *MoMoGatewayError) Error() string { return "MoMo gateway request failed" }

type momoComponents struct {
	repository MoMoRepository
	gateway    MoMoGateway
}

type configuredService struct {
	Service
	momo momoComponents
}

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) WithMoMo(repository MoMoRepository, gateway MoMoGateway) configuredService {
	return configuredService{Service: service, momo: momoComponents{repository: repository, gateway: gateway}}
}

func (service configuredService) GetMoMoConfig(ctx context.Context, storeID ids.StoreID) (domain.MoMoConfig, error) {
	if storeID == "" {
		return domain.MoMoConfig{}, domain.ErrInvalidPayment
	}
	return service.momo.repository.GetMoMoConfig(ctx, storeID)
}

func (service configuredService) ReplaceMoMoConfig(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, requestID string, expected int64, draft domain.MoMoConfigDraft) (domain.MoMoConfig, error) {
	validated, err := domain.ValidateMoMoConfig(draft)
	if err != nil || storeID == "" || actorID == "" || strings.TrimSpace(requestID) == "" || expected < 0 {
		return domain.MoMoConfig{}, domain.ErrInvalidPayment
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return domain.MoMoConfig{}, fmt.Errorf("generate MoMo callback token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return service.momo.repository.ReplaceMoMoConfig(ctx, storeID, actorID, requestID, expected, validated, token, digest)
}

func (service configuredService) StartMoMo(ctx context.Context, command InstructionCommand) (domain.MoMoAttempt, error) {
	if command.StoreID == "" || command.OrderID == "" || command.PaymentID == "" || !domain.ValidIdempotencyKey(command.IdempotencyKey) || strings.TrimSpace(command.CorrelationID) == "" {
		return domain.MoMoAttempt{}, domain.ErrInvalidPayment
	}
	attemptID, err := ids.NewPaymentAttemptID()
	if err != nil {
		return domain.MoMoAttempt{}, err
	}
	fingerprint := sha256.Sum256([]byte(command.OrderID.String() + "\x00" + command.PaymentID.String()))
	claim, err := service.momo.repository.ClaimMoMoAttempt(ctx, command, attemptID, fingerprint)
	if err != nil || !claim.ShouldCall {
		return claim.Attempt, err
	}
	response, callErr := service.momo.gateway.Create(ctx, MoMoCreateRequest{MoMoCredentials: claim.Credentials, AmountMinor: claim.AmountMinor, MoMoOrderID: claim.MoMoOrderID, RequestID: claim.RequestID, OrderInfo: "Order " + command.OrderID.String(), StoreID: command.StoreID})
	if callErr != nil {
		var gatewayErr *MoMoGatewayError
		final := errors.As(callErr, &gatewayErr) && gatewayErr.Final
		result, finalizeErr := service.momo.repository.FailMoMoAttempt(ctx, command.StoreID, claim.Attempt.AttemptID, final)
		if finalizeErr != nil {
			return domain.MoMoAttempt{}, finalizeErr
		}
		if final {
			return result, ErrUnavailable
		}
		return domain.MoMoAttempt{}, fmt.Errorf("create MoMo payment: %w", ErrDependency)
	}
	result, err := service.momo.repository.FinalizeMoMoAttempt(ctx, command.StoreID, claim.Attempt.AttemptID, response)
	if err != nil {
		return domain.MoMoAttempt{}, err
	}
	if response.Retryable {
		return domain.MoMoAttempt{}, ErrDependency
	}
	if response.Final && response.ResultCode != 0 {
		return result, ErrUnavailable
	}
	return result, nil
}

func (service configuredService) HandleMoMoIPN(ctx context.Context, callbackToken string, raw []byte, correlationID string) (domain.WebhookResult, error) {
	if len(callbackToken) < 32 || len(callbackToken) > 128 || len(raw) == 0 || len(raw) > 16<<10 || strings.TrimSpace(correlationID) == "" {
		return domain.WebhookResult{}, domain.ErrInvalidPayment
	}
	callbackDigest := sha256.Sum256([]byte(callbackToken))
	resolved, err := service.momo.repository.ResolveMoMoCallback(ctx, callbackDigest)
	if err != nil {
		return domain.WebhookResult{}, err
	}
	ipn, err := service.momo.gateway.VerifyIPN(raw, resolved.Credentials)
	if err != nil {
		return domain.WebhookResult{}, domain.ErrInvalidPayment
	}
	bodyDigest := sha256.Sum256(raw)
	return service.momo.repository.ProcessMoMoIPN(ctx, resolved, ipn, bodyDigest, correlationID)
}

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
