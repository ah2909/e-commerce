package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type momoRepositoryFake struct {
	claim       MoMoClaim
	finalizedID ids.PaymentAttemptID
	failedState string
	processed   bool
	refundClaim RefundClaim
	refundState string
}

func (*momoRepositoryFake) GetMoMoConfig(context.Context, ids.StoreID) (domain.MoMoConfig, error) {
	return domain.MoMoConfig{}, nil
}

func (*momoRepositoryFake) ReplaceMoMoConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.MoMoConfigDraft, string, [32]byte) (domain.MoMoConfig, error) {
	return domain.MoMoConfig{}, nil
}

func (repository *momoRepositoryFake) ClaimMoMoAttempt(context.Context, InstructionCommand, ids.PaymentAttemptID, [32]byte) (MoMoClaim, error) {
	return repository.claim, nil
}

func (repository *momoRepositoryFake) FinalizeMoMoAttempt(_ context.Context, _ ids.StoreID, attemptID ids.PaymentAttemptID, response MoMoCreateResponse) (domain.MoMoAttempt, error) {
	repository.finalizedID = attemptID
	result := repository.claim.Attempt
	result.State = "action_required"
	result.PayURL = response.PayURL
	return result, nil
}

func (repository *momoRepositoryFake) FailMoMoAttempt(_ context.Context, _ ids.StoreID, attemptID ids.PaymentAttemptID, final bool) (domain.MoMoAttempt, error) {
	state := "retryable"
	if final {
		state = "failed"
	}
	repository.failedState = state
	return domain.MoMoAttempt{AttemptID: attemptID, State: state}, nil
}

func (repository *momoRepositoryFake) ResolveMoMoCallback(context.Context, [32]byte) (MoMoCallbackContext, error) {
	return MoMoCallbackContext{StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111"), Credentials: MoMoCredentials{PartnerCode: "partner", AccessKey: "access-key", SecretKey: "secret-key-123456"}}, nil
}

func (repository *momoRepositoryFake) ProcessMoMoIPN(context.Context, MoMoCallbackContext, domain.MoMoIPN, [32]byte, string) (domain.WebhookResult, error) {
	repository.processed = true
	return domain.WebhookResult{Outcome: "paid", Created: true}, nil
}

func (repository *momoRepositoryFake) GetRefund(context.Context, ids.StoreID, ids.RefundID) (domain.Refund, error) {
	return repository.refundClaim.Refund, nil
}

func (repository *momoRepositoryFake) ClaimMoMoRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string, [32]byte) (RefundClaim, error) {
	return repository.refundClaim, nil
}

func (repository *momoRepositoryFake) RecordMoMoRefundFailure(_ context.Context, _ ids.StoreID, _ ids.RefundID, _ int, retryable bool) (domain.Refund, error) {
	repository.refundState = "failed"
	if retryable {
		repository.refundState = "reconciliation_required"
	}
	result := repository.refundClaim.Refund
	result.State = repository.refundState
	return result, nil
}

func (repository *momoRepositoryFake) ApplyRefundSuccess(context.Context, ids.StoreID, ids.RefundID, ids.UserID, int, int64, string, time.Time) (domain.Refund, error) {
	repository.refundState = "succeeded"
	result := repository.refundClaim.Refund
	result.State = repository.refundState
	return result, nil
}

func (*momoRepositoryFake) SettleVietQRRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string, [32]byte, string, time.Time, string) (domain.Refund, error) {
	return domain.Refund{}, nil
}

type momoGatewayFake struct {
	calls       int
	err         error
	refundCalls int
	queryCalls  int
	query       MoMoRefundResponse
	refund      MoMoRefundResponse
}

func (gateway *momoGatewayFake) Create(context.Context, MoMoCreateRequest) (MoMoCreateResponse, error) {
	gateway.calls++
	return MoMoCreateResponse{ResultCode: 0, PayURL: "https://test-payment.momo.vn/pay"}, gateway.err
}

func (*momoGatewayFake) VerifyIPN([]byte, MoMoCredentials) (domain.MoMoIPN, error) {
	return domain.MoMoIPN{Transaction: 1}, nil
}

func (gateway *momoGatewayFake) Refund(context.Context, MoMoRefundRequest) (MoMoRefundResponse, error) {
	gateway.refundCalls++
	return gateway.refund, gateway.err
}

func (gateway *momoGatewayFake) QueryRefund(context.Context, MoMoRefundRequest) (MoMoRefundResponse, error) {
	gateway.queryCalls++
	return gateway.query, gateway.err
}

func TestStartMoMoFinalizesClaimedAttemptIdentity(t *testing.T) {
	attemptID := ids.PaymentAttemptID("22222222-2222-4222-8222-222222222222")
	repository := &momoRepositoryFake{claim: MoMoClaim{Attempt: domain.MoMoAttempt{AttemptID: attemptID, PaymentID: ids.PaymentID("33333333-3333-4333-8333-333333333333"), OrderID: ids.OrderID("44444444-4444-4444-8444-444444444444"), ExpiresAt: time.Now().Add(time.Minute)}, Credentials: MoMoCredentials{PartnerCode: "partner", AccessKey: "access-key", SecretKey: "secret-key-123456", CallbackToken: "callback"}, AmountMinor: 1000, ShouldCall: true}}
	gateway := &momoGatewayFake{}
	service := NewService(nil).WithMoMo(repository, gateway)
	command := InstructionCommand{StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111"), OrderID: repository.claim.Attempt.OrderID, PaymentID: repository.claim.Attempt.PaymentID, IdempotencyKey: "momo-start", CorrelationID: "request"}
	result, err := service.StartMoMo(context.Background(), command)
	if err != nil || result.State != "action_required" || gateway.calls != 1 || repository.finalizedID != attemptID {
		t.Fatalf("StartMoMo() = %#v, %v, calls=%d finalized=%s", result, err, gateway.calls, repository.finalizedID)
	}
}

func TestStartMoMoLeavesAmbiguousAttemptRetryable(t *testing.T) {
	repository := &momoRepositoryFake{claim: MoMoClaim{Attempt: domain.MoMoAttempt{AttemptID: ids.PaymentAttemptID("22222222-2222-4222-8222-222222222222")}, Credentials: MoMoCredentials{}, AmountMinor: 1000, ShouldCall: true}}
	gateway := &momoGatewayFake{err: context.DeadlineExceeded}
	service := NewService(nil).WithMoMo(repository, gateway)
	_, err := service.StartMoMo(context.Background(), InstructionCommand{StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111"), OrderID: ids.OrderID("44444444-4444-4444-8444-444444444444"), PaymentID: ids.PaymentID("33333333-3333-4333-8333-333333333333"), IdempotencyKey: "momo-start", CorrelationID: "request"})
	if !errors.Is(err, ErrDependency) || repository.finalizedID != "" || repository.failedState != "retryable" {
		t.Fatalf("StartMoMo() error = %v, finalized=%s failed=%s", err, repository.finalizedID, repository.failedState)
	}
}

func TestStartMoMoPersistsPermanentGatewayFailure(t *testing.T) {
	repository := &momoRepositoryFake{claim: MoMoClaim{Attempt: domain.MoMoAttempt{AttemptID: ids.PaymentAttemptID("22222222-2222-4222-8222-222222222222")}, AmountMinor: 1000, ShouldCall: true}}
	gateway := &momoGatewayFake{err: &MoMoGatewayError{Final: true}}
	service := NewService(nil).WithMoMo(repository, gateway)
	_, err := service.StartMoMo(context.Background(), InstructionCommand{StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111"), OrderID: ids.OrderID("44444444-4444-4444-8444-444444444444"), PaymentID: ids.PaymentID("33333333-3333-4333-8333-333333333333"), IdempotencyKey: "momo-start", CorrelationID: "request"})
	if !errors.Is(err, ErrUnavailable) || repository.failedState != "failed" {
		t.Fatalf("StartMoMo() error = %v, failed=%s", err, repository.failedState)
	}
}

func TestHandleMoMoIPNRequiresBoundedInput(t *testing.T) {
	repository := &momoRepositoryFake{}
	service := NewService(nil).WithMoMo(repository, &momoGatewayFake{})
	if _, err := service.HandleMoMoIPN(context.Background(), "short", []byte(`{}`), "request"); !errors.Is(err, domain.ErrInvalidPayment) {
		t.Fatalf("HandleMoMoIPN() error = %v", err)
	}
	if _, err := service.HandleMoMoIPN(context.Background(), "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", []byte(`{}`), "request"); err != nil || !repository.processed {
		t.Fatalf("HandleMoMoIPN() error = %v, processed=%v", err, repository.processed)
	}
}

func TestExecuteMoMoRefundQueriesAmbiguousAttemptBeforeRetry(t *testing.T) {
	refundID := ids.RefundID("55555555-5555-4555-8555-555555555555")
	repository := &momoRepositoryFake{refundClaim: RefundClaim{Refund: domain.Refund{ID: refundID, AmountMinor: 1000}, OriginalTransaction: 44, ProviderOrderID: "refund-order", RequestID: "refund-request", ShouldQuery: true}}
	gateway := &momoGatewayFake{query: MoMoRefundResponse{NotFound: true}, refund: MoMoRefundResponse{ResultCode: 0, RefundTransactionID: 77, Success: true}}
	service := NewService(nil).WithMoMo(repository, gateway)
	result, err := service.ExecuteMoMoRefund(context.Background(), ids.StoreID("11111111-1111-4111-8111-111111111111"), refundID, ids.UserID("66666666-6666-4666-8666-666666666666"), "refund-execution")
	if err != nil || result.State != "succeeded" || gateway.queryCalls != 1 || gateway.refundCalls != 1 {
		t.Fatalf("ExecuteMoMoRefund() = %#v, %v, queries=%d refunds=%d", result, err, gateway.queryCalls, gateway.refundCalls)
	}
}

func TestExecuteMoMoRefundDoesNotRepeatSettledProviderRefund(t *testing.T) {
	refundID := ids.RefundID("55555555-5555-4555-8555-555555555555")
	repository := &momoRepositoryFake{refundClaim: RefundClaim{Refund: domain.Refund{ID: refundID, AmountMinor: 1000}, OriginalTransaction: 44, ProviderOrderID: "refund-order", RequestID: "refund-request", ShouldQuery: true}}
	gateway := &momoGatewayFake{query: MoMoRefundResponse{ResultCode: 0, RefundTransactionID: 77, Success: true}}
	service := NewService(nil).WithMoMo(repository, gateway)
	result, err := service.ExecuteMoMoRefund(context.Background(), ids.StoreID("11111111-1111-4111-8111-111111111111"), refundID, ids.UserID("66666666-6666-4666-8666-666666666666"), "refund-execution")
	if err != nil || result.State != "succeeded" || gateway.queryCalls != 1 || gateway.refundCalls != 0 {
		t.Fatalf("ExecuteMoMoRefund() = %#v, %v, queries=%d refunds=%d", result, err, gateway.queryCalls, gateway.refundCalls)
	}
}

func TestExecuteMoMoRefundPersistsPermanentGatewayFailure(t *testing.T) {
	refundID := ids.RefundID("55555555-5555-4555-8555-555555555555")
	repository := &momoRepositoryFake{refundClaim: RefundClaim{Refund: domain.Refund{ID: refundID, AmountMinor: 1000}, OriginalTransaction: 44, ProviderOrderID: "refund-order", RequestID: "refund-request", ShouldCall: true}}
	gateway := &momoGatewayFake{err: &MoMoGatewayError{Final: true}}
	service := NewService(nil).WithMoMo(repository, gateway)
	_, err := service.ExecuteMoMoRefund(context.Background(), ids.StoreID("11111111-1111-4111-8111-111111111111"), refundID, ids.UserID("66666666-6666-4666-8666-666666666666"), "refund-execution")
	if !errors.Is(err, ErrUnavailable) || repository.refundState != "failed" {
		t.Fatalf("ExecuteMoMoRefund() error=%v state=%s", err, repository.refundState)
	}
}
