//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorydomain "github.com/ah2909/e-commerce/internal/inventory/domain"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	paymentsapplication "github.com/ah2909/e-commerce/internal/payments/application"
	paymentsdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	paymentsecret "github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	paymentvietqr "github.com/ah2909/e-commerce/internal/payments/infrastructure/vietqr"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
)

func TestUnpaidCancellationIsIdempotentTenantScopedAndReleasesReservation(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := orderRepository(t, pool, eventingpostgres.NewWriter())
	service := application.NewService(repository)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "cancel-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	command := application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationUser, ActorID: pair.A.UserID.String(), IdempotencyKey: "cancel-unpaid", Reason: "Changed my mind", RequestID: "cancel-unpaid"}
	result, err := service.Cancel(context.Background(), command)
	if err != nil || result.State != domain.StateCancelled || !result.Created {
		t.Fatalf("Cancel() = %#v, %v", result, err)
	}
	replay, err := service.Cancel(context.Background(), command)
	if err != nil || replay.Created || replay.State != domain.StateCancelled {
		t.Fatalf("Cancel() replay = %#v, %v", replay, err)
	}
	foreign := command
	foreign.StoreID, foreign.ActorID, foreign.IdempotencyKey = pair.B.Store.ID(), pair.B.UserID.String(), "foreign-cancel"
	if _, err := service.Cancel(context.Background(), foreign); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign cancellation error = %v", err)
	}
	var orderState, paymentState, reservationState string
	var onHand int64
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if orderState != "cancelled" || paymentState != "expired" || reservationState != "released" || onHand != 2 {
		t.Fatalf("states/stock = %s/%s/%s/%d", orderState, paymentState, reservationState, onHand)
	}
	for _, query := range []string{
		`SELECT count(*) FROM order_cancellations WHERE store_id=$1 AND order_id=$2`,
		`SELECT count(*) FROM audit_records WHERE store_id=$1 AND subject_id=$2 AND action='order.cancelled'`,
		`SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.cancelled.v1'`,
	} {
		var count int
		if err := pool.QueryRow(context.Background(), query, pair.A.Store.ID(), checkout.Order.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("effect count = %d, error = %v", count, err)
		}
	}
}

func TestConcurrentUnpaidCancellationReplaysRecordedOutcome(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	service := application.NewService(orderRepository(t, pool, eventingpostgres.NewWriter()))
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "concurrent-cancel-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	command := application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationUser, ActorID: pair.A.UserID.String(), IdempotencyKey: "concurrent-cancel", Reason: "Changed my mind", RequestID: "concurrent-cancel"}
	results := make([]application.CancellationResult, 2)
	errorsFound := make([]error, 2)
	ready := make(chan struct{})
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-ready
			results[index], errorsFound[index] = service.Cancel(context.Background(), command)
		}(index)
	}
	close(ready)
	wait.Wait()
	created := 0
	for index := range results {
		if errorsFound[index] != nil || results[index].State != domain.StateCancelled {
			t.Fatalf("Cancel()[%d] = %#v, %v", index, results[index], errorsFound[index])
		}
		if results[index].Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created outcomes = %d", created)
	}
}

func TestWorkforceCancellationSerializesAfterConcurrentPaymentConfirmation(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := application.NewService(orders)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "cancel-confirm-race"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x63}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	setup := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	if _, err := setup.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "cancel-confirm-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "RACE", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := setup.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "cancel-confirm-instruction", CorrelationID: "cancel-confirm-instruction"})
	if err != nil {
		t.Fatal(err)
	}
	locked, release := make(chan struct{}), make(chan struct{})
	confirmation := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, blockingPaymentOrders{delegate: orders, locked: locked, release: release}, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	confirmationDone := make(chan error, 1)
	go func() {
		_, err := confirmation.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "cancel-confirm-payment", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "cancel-confirm-payment", CorrelationID: "cancel-confirm-payment"})
		confirmationDone <- err
	}()
	<-locked
	type cancelResult struct {
		value application.CancellationResult
		err   error
	}
	cancellationDone := make(chan cancelResult, 1)
	go func() {
		value, err := orderService.Cancel(context.Background(), application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationWorkforce, ActorID: pair.A.UserID.String(), IdempotencyKey: "cancel-confirm-cancel", Reason: "Customer request", RequestID: "cancel-confirm-cancel"})
		cancellationDone <- cancelResult{value: value, err: err}
	}()
	waitForBlockedQueries(t, pool, "orders", 1)
	close(release)
	if err := <-confirmationDone; err != nil {
		t.Fatalf("confirmation error = %v", err)
	}
	cancelled := <-cancellationDone
	if cancelled.err != nil || cancelled.value.State != domain.StateRefundPending || cancelled.value.RefundID == "" {
		t.Fatalf("cancellation = %#v, %v", cancelled.value, cancelled.err)
	}
	var orderState, paymentState, reservationState string
	var onHand, refunds int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand,(SELECT count(*) FROM refunds f WHERE f.store_id=o.store_id AND f.order_id=o.order_id) FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand, &refunds); err != nil {
		t.Fatal(err)
	}
	if orderState != "refund_pending" || paymentState != "refund_pending" || reservationState != "confirmed" || onHand != 1 || refunds != 1 {
		t.Fatalf("race state/effects = %s/%s/%s on_hand=%d refunds=%d", orderState, paymentState, reservationState, onHand, refunds)
	}
}

func TestVietQRFullRefundRestocksAndCompletesAtomically(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	repository := orderRepository(t, pool, eventingpostgres.NewWriter())
	service := application.NewService(repository)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := service.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "refund-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	tx := repository.db.Begin()
	order, err := repository.LockForPaymentInTransaction(context.Background(), tx, pair.A.Store.ID(), checkout.Order.ID)
	if err == nil {
		err = repository.inventory.(*inventorypostgres.Repository).ConfirmOrderInTransaction(context.Background(), tx, pair.A.Store.ID(), checkout.Order.ID, time.Now().UTC(), "refund-payment")
	}
	if err == nil {
		err = tx.Exec(`UPDATE payments SET state='succeeded',version=version+1,updated_at=clock_timestamp(),closed_at=clock_timestamp() WHERE store_id=? AND payment_id=? AND state='pending'`, pair.A.Store.ID(), checkout.Order.Payment.ID).Error
	}
	if err == nil {
		err = repository.MarkPaidInTransaction(context.Background(), tx, pair.A.Store.ID(), order, checkout.Order.Payment.ID, time.Now().UTC(), "refund-payment")
	}
	if err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	cancel, err := service.Cancel(context.Background(), application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationWorkforce, ActorID: pair.A.UserID.String(), IdempotencyKey: "paid-cancel", Reason: "Customer request", RequestID: "paid-cancel"})
	if err != nil || cancel.State != domain.StateRefundPending || cancel.RefundID == "" {
		t.Fatalf("paid Cancel() = %#v, %v", cancel, err)
	}
	for _, statement := range []string{
		`UPDATE refunds SET amount_minor=amount_minor+1 WHERE store_id=$1 AND refund_id=$2`,
		`UPDATE refunds SET currency='USD' WHERE store_id=$1 AND refund_id=$2`,
		`UPDATE refunds SET provider='momo',provider_transaction_id=1 WHERE store_id=$1 AND refund_id=$2`,
	} {
		if _, err := pool.Exec(context.Background(), statement, pair.A.Store.ID(), cancel.RefundID); err == nil {
			t.Fatalf("refund binding mutation succeeded: %s", statement)
		}
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x61}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	paymentRepository := paymentspostgres.NewRepository(repository.db, keyring, paymentvietqr.Encoder{}, repository, repository.inventory.(*inventorypostgres.Repository), auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(repository.db, auditpostgres.NewWriter()))
	configured := paymentsapplication.NewService(paymentRepository).WithMoMo(paymentRepository, &momoGatewayFixture{})
	staffID, _ := ids.NewUserID()
	for _, statement := range []string{
		`INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1,$2,'refund-staff@example.com','refund-staff@example.com','active',now(),now())`,
		`INSERT INTO store_memberships(store_id,user_id,status,role,created_at,updated_at) VALUES($1,$2,'active','staff',now(),now())`,
		`INSERT INTO store_membership_permission_sets(store_id,user_id,version,updated_at) VALUES($1,$2,1,now())`,
		`INSERT INTO store_membership_permissions(store_id,user_id,permission,version,created_at,updated_at) VALUES($1,$2,'payment.refund',1,now(),now())`,
	} {
		if _, err := pool.Exec(context.Background(), statement, pair.A.Store.ID(), staffID); err != nil {
			t.Fatal(err)
		}
	}
	stores := storespostgres.NewRepository(repository.db, auditpostgres.NewWriter())
	if _, err := stores.ReplaceStaffPermissions(context.Background(), pair.A.Store.ID(), staffID, nil, 1, pair.A.UserID, "revoke-refund", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var settledAt time.Time
	if err := pool.QueryRow(context.Background(), `SELECT created_at FROM refunds WHERE store_id=$1 AND refund_id=$2`, pair.A.Store.ID(), cancel.RefundID).Scan(&settledAt); err != nil {
		t.Fatal(err)
	}
	if _, err := configured.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, staffID, "revoked-settlement", "BANK-REF-REVOKED", settledAt, "revoked-settlement"); !errors.Is(err, paymentsapplication.ErrForbidden) {
		t.Fatalf("revoked settlement error = %v", err)
	}
	failingRepository := paymentspostgres.NewRepository(repository.db, keyring, paymentvietqr.Encoder{}, repository, repository.inventory.(*inventorypostgres.Repository), auditpostgres.NewWriter(), failingPaymentEvents{}, storespostgres.NewRepository(repository.db, auditpostgres.NewWriter()))
	failing := paymentsapplication.NewService(failingRepository).WithMoMo(failingRepository, &momoGatewayFixture{})
	if _, err := failing.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "rollback-refund", "BANK-REF-ROLLBACK", settledAt, "rollback-refund"); !errors.Is(err, errInjectedPaymentEvent) {
		t.Fatalf("rollback settlement error = %v", err)
	}
	var rollbackOrder, rollbackPayment, rollbackRefund string
	var rollbackStock, rollbackLedgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.state,s.on_hand,(SELECT count(*) FROM inventory_ledger_entries l WHERE l.store_id=o.store_id AND l.entry_type='refund_restock' AND l.business_reference=$2) FROM orders o JOIN payments p USING(store_id,order_id) JOIN refunds r USING(store_id,order_id,payment_id) JOIN inventory_stock_items s ON s.store_id=o.store_id AND s.variant_id=$3 WHERE o.store_id=$1 AND o.order_id=$4`, pair.A.Store.ID(), cancel.RefundID, fixture.variantID, checkout.Order.ID).Scan(&rollbackOrder, &rollbackPayment, &rollbackRefund, &rollbackStock, &rollbackLedgers); err != nil {
		t.Fatal(err)
	}
	if rollbackOrder != "refund_pending" || rollbackPayment != "refund_pending" || rollbackRefund != "pending" || rollbackStock != 1 || rollbackLedgers != 0 {
		t.Fatalf("rollback states/stock/ledgers = %s/%s/%s/%d/%d", rollbackOrder, rollbackPayment, rollbackRefund, rollbackStock, rollbackLedgers)
	}
	if _, err := configured.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "early-settlement", "BANK-REF-EARLY", settledAt.Add(-time.Second), "early-settlement"); !errors.Is(err, paymentsdomain.ErrInvalidPayment) {
		t.Fatalf("early settlement error = %v", err)
	}
	if _, err := configured.SettleVietQRRefund(context.Background(), pair.B.Store.ID(), cancel.RefundID, pair.B.UserID, "foreign-settlement", "BANK-REF-FOREIGN", settledAt, "foreign-settlement"); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("foreign settlement error = %v", err)
	}
	refunds := make([]paymentsdomain.Refund, 2)
	settlementErrors := make([]error, 2)
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
	ready := make(chan struct{})
	var wait sync.WaitGroup
	for index := range refunds {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-ready
			refunds[index], settlementErrors[index] = configured.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "settle-refund", "BANK-REF-123", settledAt, "settle-refund")
		}(index)
	}
	close(ready)
	waitForBlockedQueries(t, pool, "inventory_stock_items", 1)
	adjustmentDone := make(chan error, 1)
	go func() {
		_, err := inventoryapplication.NewService(repository.inventory.(*inventorypostgres.Repository)).Adjust(context.Background(), pair.A.Store.ID(), inventorydomain.Adjustment{VariantID: fixture.variantID, QuantityDelta: 1, Reason: "refund race", ExpectedVersion: stockVersion, IdempotencyKey: "refund-adjustment-race", ActorID: pair.A.UserID, RequestID: "refund-adjustment-race"})
		adjustmentDone <- err
	}()
	waitForBlockedQueries(t, pool, "inventory_stock_items", 2)
	if err := blocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	adjustmentErr := <-adjustmentDone
	if adjustmentErr != nil && !errors.Is(adjustmentErr, inventoryapplication.ErrConflict) {
		t.Fatalf("adjustment error = %v", adjustmentErr)
	}
	for index, refund := range refunds {
		if settlementErrors[index] != nil || refund.State != "succeeded" || refund.ExternalReference != "BANK-REF-123" {
			t.Fatalf("SettleVietQRRefund()[%d] = %#v, %v", index, refund, settlementErrors[index])
		}
	}
	replay, err := configured.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "settle-refund", "BANK-REF-123", settledAt, "settle-refund")
	if err != nil || replay.State != "succeeded" {
		t.Fatalf("settlement replay = %#v, %v", replay, err)
	}
	if _, err := configured.SettleVietQRRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "settle-refund", "CHANGED", settledAt, "settle-refund"); !errors.Is(err, paymentsapplication.ErrConflict) {
		t.Fatalf("changed settlement error = %v", err)
	}
	if _, err := configured.GetRefund(context.Background(), pair.B.Store.ID(), cancel.RefundID); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("cross-store refund error = %v", err)
	}
	var orderState, paymentState string
	var onHand int64
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=o.store_id AND s.variant_id=$3 WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID, fixture.variantID).Scan(&orderState, &paymentState, &onHand); err != nil {
		t.Fatal(err)
	}
	if orderState != "cancelled" || paymentState != "refunded" || (onHand != 2 && onHand != 3) {
		t.Fatalf("refunded states/stock = %s/%s/%d adjustment_error=%v", orderState, paymentState, onHand, adjustmentErr)
	}
	if (adjustmentErr == nil && onHand != 3) || (errors.Is(adjustmentErr, inventoryapplication.ErrConflict) && onHand != 2) {
		t.Fatalf("adjustment outcome/stock = %v/%d", adjustmentErr, onHand)
	}
	var ledgers, refundEvents int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND entry_type='refund_restock' AND business_reference=$2`, pair.A.Store.ID(), cancel.RefundID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='refund.succeeded.v1'`, pair.A.Store.ID(), cancel.RefundID).Scan(&refundEvents); err != nil {
		t.Fatal(err)
	}
	if ledgers != 1 || refundEvents != 1 {
		t.Fatalf("ledgers/events = %d/%d", ledgers, refundEvents)
	}
}

func TestMoMoFullRefundPersistsAmbiguityQueriesAndSettlesOnce(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	command := userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "momo-refund-checkout")
	command.PaymentProvider = "momo"
	checkout, err := application.NewService(orders).Checkout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x62}, 32))}, 1)
	paymentRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, orders.inventory.(*inventorypostgres.Repository), auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	gateway := &momoGatewayFixture{}
	payments := paymentsapplication.NewService(paymentRepository).WithMoMo(paymentRepository, gateway)
	if _, err := payments.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "momo-refund-config", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOTESTPARTNER", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := payments.StartMoMo(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "momo-refund-attempt", CorrelationID: "momo-refund-attempt"}); err != nil {
		t.Fatal(err)
	}
	gateway.ipn = paymentsdomain.MoMoIPN{PartnerCode: gateway.request.PartnerCode, OrderID: gateway.request.MoMoOrderID, RequestID: gateway.request.RequestID, AmountMinor: gateway.request.AmountMinor, OrderInfo: gateway.request.OrderInfo, OrderType: "momo_wallet", Transaction: 4088878660, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	if _, err := payments.HandleMoMoIPN(context.Background(), gateway.request.CallbackToken, []byte(`{"result":"success"}`), "momo-refund-ipn"); err != nil {
		t.Fatal(err)
	}
	cancel, err := application.NewService(orders).Cancel(context.Background(), application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationWorkforce, ActorID: pair.A.UserID.String(), IdempotencyKey: "momo-paid-cancel", Reason: "Customer request", RequestID: "momo-paid-cancel"})
	if err != nil || cancel.RefundID == "" {
		t.Fatalf("Cancel() = %#v, %v", cancel, err)
	}
	if _, err := payments.ExecuteMoMoRefund(context.Background(), pair.B.Store.ID(), cancel.RefundID, pair.B.UserID, "momo-refund-execute"); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("foreign execution error = %v", err)
	}
	gateway.refundErr = context.DeadlineExceeded
	if _, err := payments.ExecuteMoMoRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "momo-refund-execute"); !errors.Is(err, paymentsapplication.ErrDependency) {
		t.Fatalf("ambiguous execution error = %v", err)
	}
	var refundState string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM refunds WHERE store_id=$1 AND refund_id=$2`, pair.A.Store.ID(), cancel.RefundID).Scan(&refundState); err != nil || refundState != "reconciliation_required" {
		t.Fatalf("ambiguous refund state = %s, error = %v", refundState, err)
	}
	gateway.refundErr = nil
	gateway.queryResponse = paymentsapplication.MoMoRefundResponse{ResultCode: 0, RefundTransactionID: 99887766, Success: true}
	refund, err := payments.ExecuteMoMoRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "momo-refund-execute")
	if err != nil || refund.State != "succeeded" || gateway.refundCalls != 1 || gateway.queryCalls != 1 {
		t.Fatalf("ExecuteMoMoRefund() = %#v, %v, refund_calls=%d query_calls=%d", refund, err, gateway.refundCalls, gateway.queryCalls)
	}
	if _, err := payments.ExecuteMoMoRefund(context.Background(), pair.A.Store.ID(), cancel.RefundID, pair.A.UserID, "momo-refund-execute"); err != nil || gateway.refundCalls != 1 || gateway.queryCalls != 1 {
		t.Fatalf("refund replay error=%v refund_calls=%d query_calls=%d", err, gateway.refundCalls, gateway.queryCalls)
	}
	var orderState, paymentState string
	var onHand, ledgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,s.on_hand,(SELECT count(*) FROM inventory_ledger_entries l WHERE l.store_id=o.store_id AND l.entry_type='refund_restock' AND l.business_reference=$2) FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=o.store_id AND s.variant_id=$3 WHERE o.store_id=$1 AND o.order_id=$4`, pair.A.Store.ID(), cancel.RefundID, checkout.Order.Lines[0].VariantID, checkout.Order.ID).Scan(&orderState, &paymentState, &onHand, &ledgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "cancelled" || paymentState != "refunded" || onHand != 2 || ledgers != 1 {
		t.Fatalf("settled state/stock/ledgers = %s/%s/%d/%d", orderState, paymentState, onHand, ledgers)
	}
}
