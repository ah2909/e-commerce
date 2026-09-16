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

type momoGatewayFake struct {
	calls int
	err   error
}

func (gateway *momoGatewayFake) Create(context.Context, MoMoCreateRequest) (MoMoCreateResponse, error) {
	gateway.calls++
	return MoMoCreateResponse{ResultCode: 0, PayURL: "https://test-payment.momo.vn/pay"}, gateway.err
}

func (*momoGatewayFake) VerifyIPN([]byte, MoMoCredentials) (domain.MoMoIPN, error) {
	return domain.MoMoIPN{Transaction: 1}, nil
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
