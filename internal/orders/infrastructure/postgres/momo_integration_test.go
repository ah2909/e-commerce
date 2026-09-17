//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorydomain "github.com/ah2909/e-commerce/internal/inventory/domain"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	paymentsapplication "github.com/ah2909/e-commerce/internal/payments/application"
	paymentsdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	paymentmomo "github.com/ah2909/e-commerce/internal/payments/infrastructure/momo"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	paymentsecret "github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	paymentvietqr "github.com/ah2909/e-commerce/internal/payments/infrastructure/vietqr"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

type momoGatewayFixture struct {
	request        paymentsapplication.MoMoCreateRequest
	ipn            paymentsdomain.MoMoIPN
	response       *paymentsapplication.MoMoCreateResponse
	err            error
	calls          int
	refundResponse paymentsapplication.MoMoRefundResponse
	queryResponse  paymentsapplication.MoMoRefundResponse
	refundErr      error
	queryErr       error
	refundCalls    int
	queryCalls     int
}

type validatingMoMoGateway struct{ *momoGatewayFixture }

type webhookCall struct {
	result paymentsdomain.WebhookResult
	err    error
}

func (gateway validatingMoMoGateway) VerifyIPN(raw []byte, credentials paymentsapplication.MoMoCredentials) (paymentsdomain.MoMoIPN, error) {
	return (paymentmomo.Client{}).VerifyIPN(raw, credentials)
}

type preblockingPaymentOrders struct {
	delegate paymentspostgres.PaymentOrders
	reached  chan struct{}
	release  chan struct{}
}

func (orders preblockingPaymentOrders) LoadForPaymentInstructionInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.PaymentOrder, error) {
	return orders.delegate.LoadForPaymentInstructionInTransaction(ctx, tx, storeID, orderID)
}

func (orders preblockingPaymentOrders) LockForPaymentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.PayableOrder, error) {
	close(orders.reached)
	<-orders.release
	return orders.delegate.LockForPaymentInTransaction(ctx, tx, storeID, orderID)
}

func (orders preblockingPaymentOrders) MarkPaidInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, order ordersapplication.PayableOrder, paymentID ids.PaymentID, now time.Time, correlationID string) error {
	return orders.delegate.MarkPaidInTransaction(ctx, tx, storeID, order, paymentID, now, correlationID)
}

func (orders preblockingPaymentOrders) LockForRefundInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.RefundOrder, error) {
	return orders.delegate.LockForRefundInTransaction(ctx, tx, storeID, orderID)
}

func (orders preblockingPaymentOrders) MarkRefundedInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, order ordersapplication.RefundOrder, refundID ids.RefundID, now time.Time, correlationID string) error {
	return orders.delegate.MarkRefundedInTransaction(ctx, tx, storeID, order, refundID, now, correlationID)
}

func (gateway *momoGatewayFixture) Create(_ context.Context, request paymentsapplication.MoMoCreateRequest) (paymentsapplication.MoMoCreateResponse, error) {
	gateway.request = request
	gateway.calls++
	if gateway.err != nil {
		return paymentsapplication.MoMoCreateResponse{}, gateway.err
	}
	if gateway.response != nil {
		return *gateway.response, nil
	}
	return paymentsapplication.MoMoCreateResponse{ResultCode: 0, PayURL: "https://test-payment.momo.vn/v2/gateway/pay?t=fixture", Deeplink: "momo://app?action=pay", QRCodeURL: "000201010212MOMOFIXTURE"}, nil
}

func (gateway *momoGatewayFixture) VerifyIPN(_ []byte, credentials paymentsapplication.MoMoCredentials) (paymentsdomain.MoMoIPN, error) {
	if credentials.PartnerCode != gateway.request.PartnerCode || credentials.SecretKey != gateway.request.SecretKey {
		return paymentsdomain.MoMoIPN{}, errors.New("credential mismatch")
	}
	return gateway.ipn, nil
}

func (gateway *momoGatewayFixture) Refund(context.Context, paymentsapplication.MoMoRefundRequest) (paymentsapplication.MoMoRefundResponse, error) {
	gateway.refundCalls++
	return gateway.refundResponse, gateway.refundErr
}

func (gateway *momoGatewayFixture) QueryRefund(context.Context, paymentsapplication.MoMoRefundRequest) (paymentsapplication.MoMoRefundResponse, error) {
	gateway.queryCalls++
	return gateway.queryResponse, gateway.queryErr
}

func signedMoMoIPN(request paymentsapplication.MoMoCreateRequest, amount, transaction int64, resultCode int) []byte {
	responseTime := time.Now().UnixMilli()
	message := "Successful."
	canonical := "accessKey=" + request.AccessKey + "&amount=" + strconv.FormatInt(amount, 10) + "&extraData=&message=" + message + "&orderId=" + request.MoMoOrderID + "&orderInfo=" + request.OrderInfo + "&orderType=momo_wallet&partnerCode=" + request.PartnerCode + "&payType=qr&requestId=" + request.RequestID + "&responseTime=" + strconv.FormatInt(responseTime, 10) + "&resultCode=" + strconv.Itoa(resultCode) + "&transId=" + strconv.FormatInt(transaction, 10)
	mac := hmac.New(sha256.New, []byte(request.SecretKey))
	_, _ = mac.Write([]byte(canonical))
	body := map[string]any{"partnerCode": request.PartnerCode, "orderId": request.MoMoOrderID, "requestId": request.RequestID, "amount": amount, "orderInfo": request.OrderInfo, "orderType": "momo_wallet", "transId": transaction, "resultCode": resultCode, "message": message, "payType": "qr", "responseTime": responseTime, "extraData": "", "signature": hex.EncodeToString(mac.Sum(nil))}
	raw, _ := json.Marshal(body)
	return raw
}

func TestMoMoAttemptAndIPNSettleAtomicallyIdempotentlyAndTenantScoped(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "momo-payment")
	command.PaymentProvider = "momo"
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x61}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	inventory := orders.inventory.(*inventorypostgres.Repository)
	paymentRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	gateway := &momoGatewayFixture{}
	service := paymentsapplication.NewService(paymentRepository).WithMoMo(paymentRepository, gateway)
	config, err := service.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOTESTPARTNER", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true})
	if err != nil || config.MaskedPartner == "MOMOTESTPARTNER" {
		t.Fatalf("ReplaceMoMoConfig() = %#v, %v", config, err)
	}
	if _, err := service.GetMoMoConfig(context.Background(), pair.B.Store.ID()); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("cross-store config error = %v", err)
	}
	var ciphertext []byte
	if err := pool.QueryRow(context.Background(), `SELECT ciphertext FROM payment_provider_configs WHERE store_id=$1 AND provider='momo'`, pair.A.Store.ID()).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("sandbox-secret-key-123456")) {
		t.Fatalf("stored MoMo config exposed secret, error = %v", err)
	}
	start := paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-attempt", CorrelationID: "momo-attempt"}
	attempt, err := service.StartMoMo(context.Background(), start)
	if err != nil || attempt.State != "action_required" || !attempt.Created || gateway.calls != 1 {
		t.Fatalf("StartMoMo() = %#v, %v, calls=%d", attempt, err, gateway.calls)
	}
	replay, err := service.StartMoMo(context.Background(), start)
	if err != nil || replay.Created || replay.AttemptID != attempt.AttemptID || gateway.calls != 1 {
		t.Fatalf("StartMoMo() replay = %#v, %v, calls=%d", replay, err, gateway.calls)
	}
	if _, err := service.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-config-rotation", 1, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOTESTPARTNER", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.HandleMoMoIPN(context.Background(), "wrong-callback-token-that-is-long-enough-1234", []byte(`{"result":"success"}`), "momo-ipn"); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("wrong callback error = %v", err)
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878650, ResultCode: 1001, Message: "Customer cancelled", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	failedAttempt, err := service.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"cancelled"}`), "momo-failed-ipn")
	if err != nil || failedAttempt.Outcome != "failed" {
		t.Fatalf("failed HandleMoMoIPN() = %#v, %v", failedAttempt, err)
	}
	closedReplay, err := service.StartMoMo(context.Background(), start)
	if err != nil || closedReplay.State != "failed" || gateway.calls != 1 {
		t.Fatalf("closed attempt replay = %#v, %v, calls=%d", closedReplay, err, gateway.calls)
	}
	start.IdempotencyKey = "momo-attempt-retry"
	attempt, err = service.StartMoMo(context.Background(), start)
	if err != nil || attempt.State != "action_required" || gateway.calls != 2 {
		t.Fatalf("new attempt = %#v, %v, calls=%d", attempt, err, gateway.calls)
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", ResultCode: 7000, Message: "Pending", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	pending, err := service.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"pending"}`), "momo-pending-ipn")
	if err != nil || pending.Outcome != "retryable" {
		t.Fatalf("pending HandleMoMoIPN() = %#v, %v", pending, err)
	}
	var pendingState string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM payment_attempts WHERE store_id=$1 AND attempt_id=$2`, pair.A.Store.ID(), attempt.AttemptID).Scan(&pendingState); err != nil || pendingState != "retryable" {
		t.Fatalf("pending attempt state = %q, %v", pendingState, err)
	}
	reached, release := make(chan struct{}), make(chan struct{})
	staleGateway := &momoGatewayFixture{request: gateway.request, ipn: paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878656, ResultCode: 1001, Message: "Failed", PayType: "qr", ResponseMS: time.Now().UnixMilli()}}
	staleRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, preblockingPaymentOrders{delegate: orders, reached: reached, release: release}, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	staleService := paymentsapplication.NewService(staleRepository).WithMoMo(staleRepository, staleGateway)
	staleDone := make(chan webhookCall, 1)
	go func() {
		result, err := staleService.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"stale-failure"}`), "momo-stale-failure")
		staleDone <- webhookCall{result: result, err: err}
	}()
	<-reached
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878653, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	raw := []byte(`{"result":"success"}`)
	results := make([]paymentsdomain.WebhookResult, 2)
	errorsFound := make([]error, 2)
	ready := make(chan struct{})
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-ready
			results[index], errorsFound[index] = service.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, raw, "momo-ipn")
		}(index)
	}
	close(ready)
	wait.Wait()
	created := 0
	for index, settled := range results {
		if errorsFound[index] != nil || settled.Outcome != "paid" {
			t.Fatalf("HandleMoMoIPN(%d) = %#v, %v", index, settled, errorsFound[index])
		}
		if settled.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created webhook outcomes = %d", created)
	}
	close(release)
	stale := <-staleDone
	if stale.err != nil || stale.result.Outcome != "failed" {
		t.Fatalf("stale failure = %#v, %v", stale.result, stale.err)
	}
	var orderState, paymentState, attemptState string
	var onHand, receipts, ledgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,a.state FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN payment_attempts a ON a.store_id=p.store_id AND a.payment_id=p.payment_id WHERE o.store_id=$1 AND o.order_id=$2 AND a.attempt_id=$3`, pair.A.Store.ID(), checkout.Order.ID, attempt.AttemptID).Scan(&orderState, &paymentState, &attemptState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT s.on_hand FROM inventory_stock_items s JOIN inventory_reservations r ON r.store_id=s.store_id AND r.variant_id=s.variant_id WHERE r.store_id=$1 AND r.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_webhook_receipts WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "paid" || paymentState != "succeeded" || attemptState != "succeeded" || onHand != 1 || receipts != 4 || ledgers != 1 {
		t.Fatalf("states/effects = %s/%s/%s on_hand=%d receipts=%d ledgers=%d", orderState, paymentState, attemptState, onHand, receipts, ledgers)
	}
	gateway.ipn.Transaction = 4088878655
	second, err := service.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"second-success"}`), "momo-second-success")
	if err != nil || second.Outcome != "reconciliation_required" {
		t.Fatalf("second success = %#v, %v", second, err)
	}
	var finalAttempt, finalPayment string
	var failedEvents int
	if err := pool.QueryRow(context.Background(), `SELECT a.state,p.state,(SELECT count(*) FROM outbox_events e WHERE e.store_id=a.store_id AND e.event_type='payment.failed.v1' AND e.aggregate_id=a.attempt_id) FROM payment_attempts a JOIN payments p ON p.store_id=a.store_id AND p.payment_id=a.payment_id WHERE a.store_id=$1 AND a.attempt_id=$2`, pair.A.Store.ID(), attempt.AttemptID).Scan(&finalAttempt, &finalPayment, &failedEvents); err != nil {
		t.Fatal(err)
	}
	if finalAttempt != "succeeded" || finalPayment != "succeeded" || failedEvents != 0 {
		t.Fatalf("late result state = %s/%s failed events=%d", finalAttempt, finalPayment, failedEvents)
	}
}

func TestMoMoMismatchedSuccessRequiresReconciliationWithoutConsumingInventory(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1).addressID, "momo-mismatch")
	command.PaymentProvider = "momo"
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x62}, 32))}, 1)
	paymentRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, orders.inventory.(*inventorypostgres.Repository), auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	gateway := &momoGatewayFixture{}
	service := paymentsapplication.NewService(paymentRepository).WithMoMo(paymentRepository, gateway)
	if _, err := service.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-mismatch-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOMISMATCH", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-mismatch-attempt", CorrelationID: "momo-mismatch-attempt"}); err != nil {
		t.Fatal(err)
	}
	oldRequest := gateway.request
	verifiedService := paymentsapplication.NewService(paymentRepository).WithMoMo(paymentRepository, validatingMoMoGateway{momoGatewayFixture: gateway})
	invalidRaw := signedMoMoIPN(oldRequest, oldRequest.AmountMinor, 4088878654, 0)
	invalidRaw[len(invalidRaw)-2] ^= 1
	if _, err := verifiedService.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, invalidRaw, "momo-invalid-signature"); !errors.Is(err, paymentsdomain.ErrInvalidPayment) {
		t.Fatalf("tampered signature error = %v", err)
	}
	var invalidReceipts int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_webhook_receipts WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&invalidReceipts); err != nil || invalidReceipts != 0 {
		t.Fatalf("tampered signature receipts = %d, %v", invalidReceipts, err)
	}
	result, err := verifiedService.HandleMoMoIPN(context.Background(), oldRequest.CallbackToken, signedMoMoIPN(oldRequest, oldRequest.AmountMinor+1, 4088878654, 0), "momo-mismatch-ipn")
	if err != nil || result.Outcome != "reconciliation_required" {
		t.Fatalf("HandleMoMoIPN() = %#v, %v", result, err)
	}
	var orderState, paymentState, reservationState string
	var onHand, reconciliations int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN inventory_reservations r ON r.store_id=o.store_id AND r.order_id=o.order_id JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_reconciliation_items WHERE store_id=$1 AND payment_id=$2 AND reason='wrong_amount'`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&reconciliations); err != nil {
		t.Fatal(err)
	}
	if orderState != "pending_payment" || paymentState != "reconciliation_required" || reservationState != "active" || onHand != 1 || reconciliations != 1 {
		t.Fatalf("states/effects = %s/%s/%s on_hand=%d reconciliations=%d", orderState, paymentState, reservationState, onHand, reconciliations)
	}
	if _, err := verifiedService.ReplaceMoMoConfig(context.Background(), pair.B.Store.ID(), pair.B.UserID, "momo-foreign-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOMISMATCH", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	secondCommand := userCheckoutCommand(pair.B.Store.ID(), pair.B.UserID, insertCheckoutFixture(t, pool, pair.B.Store.ID(), pair.B.UserID, 1).addressID, "momo-foreign-route")
	secondCommand.PaymentProvider = "momo"
	secondCheckout, err := ordersapplication.NewService(orders).Checkout(context.Background(), secondCommand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedService.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.B.Store.ID(), OrderID: secondCheckout.Order.ID, PaymentID: secondCheckout.Order.Payment.ID, IdempotencyKey: "momo-foreign-attempt", CorrelationID: "momo-foreign-attempt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedService.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, signedMoMoIPN(oldRequest, oldRequest.AmountMinor, 4088878661, 0), "momo-cross-route"); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("cross-version callback error = %v", err)
	}
}

func TestMoMoSuccessRollsBackEveryEffectWhenOutboxFails(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "momo-rollback")
	command.PaymentProvider = "momo"
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x63}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	repository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores)
	gateway := &momoGatewayFixture{}
	service := paymentsapplication.NewService(repository).WithMoMo(repository, gateway)
	if _, err := service.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-rollback-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOROLLBACK", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	attempt, err := service.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-rollback-attempt", CorrelationID: "momo-rollback-attempt"})
	if err != nil {
		t.Fatal(err)
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878660, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	failingRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), failingPaymentEvents{}, stores)
	failingService := paymentsapplication.NewService(failingRepository).WithMoMo(failingRepository, gateway)
	_, err = failingService.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"rollback"}`), "momo-rollback-ipn")
	if !errors.Is(err, errInjectedPaymentEvent) {
		t.Fatalf("HandleMoMoIPN() error = %v", err)
	}
	var orderState, paymentState, attemptState, reservationState string
	var onHand, receipts, ledgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,a.state,r.status,s.on_hand FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN payment_attempts a ON a.store_id=p.store_id AND a.payment_id=p.payment_id JOIN inventory_reservations r ON r.store_id=o.store_id AND r.order_id=o.order_id JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2 AND a.attempt_id=$3`, pair.A.Store.ID(), checkout.Order.ID, attempt.AttemptID).Scan(&orderState, &paymentState, &attemptState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_webhook_receipts WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "pending_payment" || paymentState != "pending" || attemptState != "action_required" || reservationState != "active" || onHand != 2 || receipts != 0 || ledgers != 0 {
		t.Fatalf("rollback effects = %s/%s/%s/%s on_hand=%d receipts=%d ledgers=%d", orderState, paymentState, attemptState, reservationState, onHand, receipts, ledgers)
	}
}

func TestMoMoInitiationFailuresEmitOnceAndRetryStableIdentity(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x64}, 32))}, 1)
	repository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, orders.inventory.(*inventorypostgres.Repository), auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	for _, store := range []struct {
		storeID ids.StoreID
		userID  ids.UserID
		request string
	}{{pair.A.Store.ID(), pair.A.UserID, "momo-final-config"}, {pair.B.Store.ID(), pair.B.UserID, "momo-retry-config"}} {
		service := paymentsapplication.NewService(repository).WithMoMo(repository, &momoGatewayFixture{})
		if _, err := service.ReplaceMoMoConfig(context.Background(), store.storeID, store.userID, store.request, 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOFAILURE", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	checkoutFor := func(storeID ids.StoreID, userID ids.UserID, key string) ordersdomain.Order {
		command := userCheckoutCommand(storeID, userID, insertCheckoutFixture(t, pool, storeID, userID, 1).addressID, key)
		command.PaymentProvider = "momo"
		result, err := ordersapplication.NewService(orders).Checkout(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		return result.Order
	}
	orderA := checkoutFor(pair.A.Store.ID(), pair.A.UserID, "momo-final-order")
	finalResponse := paymentsapplication.MoMoCreateResponse{ResultCode: 1001, Final: true}
	finalGateway := &momoGatewayFixture{response: &finalResponse}
	finalService := paymentsapplication.NewService(repository).WithMoMo(repository, finalGateway)
	finalCommand := paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: orderA.ID, PaymentID: orderA.Payment.ID, IdempotencyKey: "momo-final-attempt", CorrelationID: "momo-final-attempt"}
	failed, err := finalService.StartMoMo(context.Background(), finalCommand)
	if !errors.Is(err, paymentsapplication.ErrUnavailable) || failed.State != "failed" {
		t.Fatalf("final StartMoMo() = %#v, %v", failed, err)
	}
	if replay, err := finalService.StartMoMo(context.Background(), finalCommand); err != nil || replay.State != "failed" || finalGateway.calls != 1 {
		t.Fatalf("final replay = %#v, %v, calls=%d", replay, err, finalGateway.calls)
	}
	assertFailedEventCount(t, pool, pair.A.Store.ID(), failed.AttemptID, 1)

	orderB := checkoutFor(pair.B.Store.ID(), pair.B.UserID, "momo-retry-order")
	retryResponse := paymentsapplication.MoMoCreateResponse{ResultCode: 7000, Retryable: true}
	retryGateway := &momoGatewayFixture{response: &retryResponse}
	retryService := paymentsapplication.NewService(repository).WithMoMo(repository, retryGateway)
	retryCommand := paymentsapplication.InstructionCommand{StoreID: pair.B.Store.ID(), OrderID: orderB.ID, PaymentID: orderB.Payment.ID, IdempotencyKey: "momo-retry-attempt", CorrelationID: "momo-retry-attempt"}
	if _, err := retryService.StartMoMo(context.Background(), retryCommand); !errors.Is(err, paymentsapplication.ErrDependency) {
		t.Fatalf("retryable StartMoMo() error = %v", err)
	}
	requestID := retryGateway.request.RequestID
	assertFailedEventCount(t, pool, pair.B.Store.ID(), ids.PaymentAttemptID(requestID), 0)
	retryGateway.response = nil
	retryGateway.err = &paymentsapplication.MoMoGatewayError{Final: true}
	failed, err = retryService.StartMoMo(context.Background(), retryCommand)
	if !errors.Is(err, paymentsapplication.ErrUnavailable) || failed.State != "failed" || retryGateway.request.RequestID != requestID {
		t.Fatalf("permanent retry = %#v, %v, request=%s", failed, err, retryGateway.request.RequestID)
	}
	if _, err := retryService.StartMoMo(context.Background(), retryCommand); err != nil || retryGateway.calls != 2 {
		t.Fatalf("permanent replay error = %v, calls=%d", err, retryGateway.calls)
	}
	assertFailedEventCount(t, pool, pair.B.Store.ID(), failed.AttemptID, 1)
}

func assertFailedEventCount(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, attemptID ids.PaymentAttemptID, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='payment.failed.v1'`, storeID, attemptID).Scan(&count); err != nil || count != want {
		t.Fatalf("failed event count = %d, %v, want %d", count, err, want)
	}
}

func TestMoMoIPNSerializesWithExpiry(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := ordersapplication.NewService(orders)
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1).addressID, "momo-expiry-race")
	command.PaymentProvider = "momo"
	checkout, err := orderService.Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x65}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	setupRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores)
	gateway := &momoGatewayFixture{}
	setupService := paymentsapplication.NewService(setupRepository).WithMoMo(setupRepository, gateway)
	if _, err := setupService.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-expiry-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOEXPIRY", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := setupService.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-expiry-attempt", CorrelationID: "momo-expiry-attempt"}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE orders SET created_at=clock_timestamp()-interval '20 minutes',reservation_expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`,
		`UPDATE inventory_reservations SET created_at=clock_timestamp()-interval '20 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE store_id=$1 AND order_id=$2`,
	} {
		if _, err := pool.Exec(context.Background(), statement, pair.A.Store.ID(), checkout.Order.ID); err != nil {
			t.Fatal(err)
		}
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878670, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	locked, release := make(chan struct{}), make(chan struct{})
	blockedRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, blockingPaymentOrders{delegate: orders, locked: locked, release: release}, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores)
	blockedService := paymentsapplication.NewService(blockedRepository).WithMoMo(blockedRepository, gateway)
	ipnDone := make(chan webhookCall, 1)
	go func() {
		result, err := blockedService.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"expiry"}`), "momo-expiry-ipn")
		ipnDone <- webhookCall{result: result, err: err}
	}()
	<-locked
	if processed, err := orderService.ExpireDue(context.Background(), 10, "momo-expiry-race"); err != nil || processed != 0 {
		t.Fatalf("expiry while IPN locked = %d, %v", processed, err)
	}
	close(release)
	result := <-ipnDone
	if result.err != nil || result.result.Outcome != "reconciliation_required" {
		t.Fatalf("expiry IPN = %#v, %v", result.result, result.err)
	}
	if processed, err := orderService.ExpireDue(context.Background(), 10, "momo-expiry-after"); err != nil || processed != 1 {
		t.Fatalf("expiry after IPN = %d, %v", processed, err)
	}
	var orderState, paymentState, reservationState, receiptOutcome string
	var onHand, ledgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand,w.outcome FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN inventory_reservations r ON r.store_id=o.store_id AND r.order_id=o.order_id JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id JOIN payment_webhook_receipts w ON w.store_id=p.store_id AND w.payment_id=p.payment_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand, &receiptOutcome); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "expired" || paymentState != "reconciliation_required" || reservationState != "expired" || receiptOutcome != "reconciliation_required" || onHand != 1 || ledgers != 0 {
		t.Fatalf("expiry effects = %s/%s/%s/%s on_hand=%d ledgers=%d", orderState, paymentState, reservationState, receiptOutcome, onHand, ledgers)
	}
}

func TestMoMoIPNSerializesWithAdjustment(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "momo-adjustment-race")
	command.PaymentProvider = "momo"
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x66}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	repository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	gateway := &momoGatewayFixture{}
	service := paymentsapplication.NewService(repository).WithMoMo(repository, gateway)
	if _, err := service.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-adjust-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOADJUST", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-adjust-attempt", CorrelationID: "momo-adjust-attempt"}); err != nil {
		t.Fatal(err)
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878671, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	var stockVersion int64
	if err := pool.QueryRow(context.Background(), `SELECT version FROM inventory_stock_items WHERE store_id=$1 AND variant_id=$2`, pair.A.Store.ID(), fixture.variantID).Scan(&stockVersion); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(context.Background(), `SELECT 1 FROM inventory_stock_items WHERE store_id=$1 AND variant_id=$2 FOR UPDATE`, pair.A.Store.ID(), fixture.variantID); err != nil {
		t.Fatal(err)
	}
	ipnDone := make(chan webhookCall, 1)
	go func() {
		result, err := service.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"adjustment"}`), "momo-adjust-ipn")
		ipnDone <- webhookCall{result: result, err: err}
	}()
	waitForBlockedQueries(t, pool, "inventory_stock_items", 1)
	adjustmentDone := make(chan error, 1)
	go func() {
		_, err := inventoryapplication.NewService(inventory).Adjust(context.Background(), pair.A.Store.ID(), inventorydomain.Adjustment{VariantID: fixture.variantID, QuantityDelta: 1, Reason: "race restock", ExpectedVersion: stockVersion, IdempotencyKey: "momo-adjustment-race", ActorID: pair.A.UserID, RequestID: "momo-adjustment-race"})
		adjustmentDone <- err
	}()
	waitForBlockedQueries(t, pool, "inventory_stock_items", 2)
	if err := blocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	ipn := <-ipnDone
	if ipn.err != nil || ipn.result.Outcome != "paid" {
		t.Fatalf("adjustment IPN = %#v, %v", ipn.result, ipn.err)
	}
	adjustmentErr := <-adjustmentDone
	if adjustmentErr != nil && !errors.Is(adjustmentErr, inventoryapplication.ErrConflict) {
		t.Fatalf("adjustment error = %v", adjustmentErr)
	}
	var orderState, attemptState, reservationState, receiptOutcome string
	var onHand, ledgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,a.state,r.status,s.on_hand,w.outcome FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN payment_attempts a ON a.store_id=p.store_id AND a.payment_id=p.payment_id JOIN inventory_reservations r ON r.store_id=o.store_id AND r.order_id=o.order_id JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id JOIN payment_webhook_receipts w ON w.store_id=a.store_id AND w.attempt_id=a.attempt_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &attemptState, &reservationState, &onHand, &receiptOutcome); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "paid" || attemptState != "succeeded" || reservationState != "confirmed" || receiptOutcome != "paid" || (onHand != 1 && onHand != 2) || ledgers != 1 {
		t.Fatalf("adjustment effects = %s/%s/%s/%s on_hand=%d ledgers=%d error=%v", orderState, attemptState, reservationState, receiptOutcome, onHand, ledgers, adjustmentErr)
	}
}
