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

type MoMoRefundRequest struct {
	MoMoCredentials
	AmountMinor         int64
	ProviderOrderID     string
	RequestID           string
	OriginalTransaction int64
	Description         string
}

type MoMoRefundResponse struct {
	ResultCode          int
	RefundTransactionID int64
	Success             bool
	Retryable           bool
	NotFound            bool
}

type MoMoRefundGateway interface {
	Refund(context.Context, MoMoRefundRequest) (MoMoRefundResponse, error)
	QueryRefund(context.Context, MoMoRefundRequest) (MoMoRefundResponse, error)
}

type RefundClaim struct {
	Refund              domain.Refund
	Credentials         MoMoCredentials
	OriginalTransaction int64
	ProviderOrderID     string
	RequestID           string
	ShouldQuery         bool
	ShouldCall          bool
}

type RefundRepository interface {
	GetRefund(context.Context, ids.StoreID, ids.RefundID) (domain.Refund, error)
	ClaimMoMoRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string, [32]byte) (RefundClaim, error)
	RecordMoMoRefundFailure(context.Context, ids.StoreID, ids.RefundID, int, bool) (domain.Refund, error)
	ApplyRefundSuccess(context.Context, ids.StoreID, ids.RefundID, ids.UserID, int, int64, string, time.Time) (domain.Refund, error)
	SettleVietQRRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string, [32]byte, string, time.Time, string) (domain.Refund, error)
}

type MoMoGatewayError struct {
	Final bool
}

func (err *MoMoGatewayError) Error() string { return "MoMo gateway request failed" }

type momoComponents struct {
	repository    MoMoRepository
	gateway       MoMoGateway
	refunds       RefundRepository
	refundGateway MoMoRefundGateway
}

type configuredService struct {
	Service
	momo momoComponents
}

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) WithMoMo(repository MoMoRepository, gateway MoMoGateway) configuredService {
	components := momoComponents{repository: repository, gateway: gateway}
	components.refunds, _ = any(repository).(RefundRepository)
	components.refundGateway, _ = any(gateway).(MoMoRefundGateway)
	return configuredService{Service: service, momo: components}
}

func (service configuredService) GetRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID) (domain.Refund, error) {
	if service.momo.refunds == nil || storeID == "" || refundID == "" {
		return domain.Refund{}, domain.ErrInvalidPayment
	}
	return service.momo.refunds.GetRefund(ctx, storeID, refundID)
}

func (service configuredService) ExecuteMoMoRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key string) (domain.Refund, error) {
	if service.momo.refunds == nil || service.momo.refundGateway == nil || storeID == "" || refundID == "" || actorID == "" || !domain.ValidIdempotencyKey(key) {
		return domain.Refund{}, domain.ErrInvalidPayment
	}
	fingerprint := sha256.Sum256([]byte(refundID.String() + "\x00momo"))
	claim, err := service.momo.refunds.ClaimMoMoRefund(ctx, storeID, refundID, actorID, key, fingerprint)
	if err != nil || (!claim.ShouldCall && !claim.ShouldQuery) {
		return claim.Refund, err
	}
	request := MoMoRefundRequest{MoMoCredentials: claim.Credentials, AmountMinor: claim.Refund.AmountMinor, ProviderOrderID: claim.ProviderOrderID, RequestID: claim.RequestID, OriginalTransaction: claim.OriginalTransaction, Description: "Full refund"}
	var response MoMoRefundResponse
	if claim.ShouldQuery {
		response, err = service.momo.refundGateway.QueryRefund(ctx, request)
		if err == nil && response.NotFound {
			response, err = service.momo.refundGateway.Refund(ctx, request)
		}
	} else {
		response, err = service.momo.refundGateway.Refund(ctx, request)
	}
	if err != nil {
		var gatewayErr *MoMoGatewayError
		final := errors.As(err, &gatewayErr) && gatewayErr.Final
		_, recordErr := service.momo.refunds.RecordMoMoRefundFailure(ctx, storeID, refundID, 0, !final)
		if recordErr != nil {
			return domain.Refund{}, recordErr
		}
		if final {
			return domain.Refund{}, ErrUnavailable
		}
		return domain.Refund{}, fmt.Errorf("execute MoMo refund: %w", ErrDependency)
	}
	if response.Success {
		return service.momo.refunds.ApplyRefundSuccess(ctx, storeID, refundID, actorID, response.ResultCode, response.RefundTransactionID, key, time.Now().UTC())
	}
	value, recordErr := service.momo.refunds.RecordMoMoRefundFailure(ctx, storeID, refundID, response.ResultCode, response.Retryable)
	if recordErr != nil {
		return domain.Refund{}, recordErr
	}
	if response.Retryable {
		return domain.Refund{}, ErrDependency
	}
	return value, ErrUnavailable
}

func (service configuredService) SettleVietQRRefund(ctx context.Context, storeID ids.StoreID, refundID ids.RefundID, actorID ids.UserID, key, reference string, settledAt time.Time, requestID string) (domain.Refund, error) {
	reference = strings.TrimSpace(reference)
	if service.momo.refunds == nil || storeID == "" || refundID == "" || actorID == "" || !domain.ValidIdempotencyKey(key) || len(reference) < 1 || len(reference) > 128 || settledAt.IsZero() || strings.TrimSpace(requestID) == "" {
		return domain.Refund{}, domain.ErrInvalidPayment
	}
	fingerprint := sha256.Sum256([]byte(reference + "\x00" + settledAt.UTC().Format(time.RFC3339Nano)))
	return service.momo.refunds.SettleVietQRRefund(ctx, storeID, refundID, actorID, key, fingerprint, reference, settledAt.UTC(), requestID)
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
