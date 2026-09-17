//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventoryapplication "github.com/ah2909/e-commerce/internal/inventory/application"
	inventorydomain "github.com/ah2909/e-commerce/internal/inventory/domain"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	paymentsapplication "github.com/ah2909/e-commerce/internal/payments/application"
	paymentsdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	paymentsecret "github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	paymentvietqr "github.com/ah2909/e-commerce/internal/payments/infrastructure/vietqr"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

var errInjectedPaymentEvent = errors.New("injected payment event failure")

type failingPaymentEvents struct{}

func (failingPaymentEvents) Append(context.Context, *gorm.DB, eventdomain.Event) error {
	return errInjectedPaymentEvent
}

type blockingPaymentAuthorizer struct {
	delegate interface {
		HasPermissionInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.UserID, string) (bool, error)
	}
	locked  chan struct{}
	release chan struct{}
}

type blockingPaymentOrders struct {
	delegate *Repository
	locked   chan struct{}
	release  chan struct{}
}

func (orders blockingPaymentOrders) LoadForPaymentInstructionInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.PaymentOrder, error) {
	return orders.delegate.LoadForPaymentInstructionInTransaction(ctx, tx, storeID, orderID)
}

func (orders blockingPaymentOrders) LockForPaymentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.PayableOrder, error) {
	value, err := orders.delegate.LockForPaymentInTransaction(ctx, tx, storeID, orderID)
	close(orders.locked)
	<-orders.release
	return value, err
}

func (orders blockingPaymentOrders) MarkPaidInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, order ordersapplication.PayableOrder, paymentID ids.PaymentID, now time.Time, correlationID string) error {
	return orders.delegate.MarkPaidInTransaction(ctx, tx, storeID, order, paymentID, now, correlationID)
}

func (orders blockingPaymentOrders) LockForRefundInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.RefundOrder, error) {
	return orders.delegate.LockForRefundInTransaction(ctx, tx, storeID, orderID)
}

func (orders blockingPaymentOrders) MarkRefundedInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, order ordersapplication.RefundOrder, refundID ids.RefundID, now time.Time, correlationID string) error {
	return orders.delegate.MarkRefundedInTransaction(ctx, tx, storeID, order, refundID, now, correlationID)
}

func (authorizer blockingPaymentAuthorizer) HasPermissionInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, userID ids.UserID, permission string) (bool, error) {
	allowed, err := authorizer.delegate.HasPermissionInTransaction(ctx, tx, storeID, userID, permission)
	if permission == string(storesdomain.PermissionPaymentConfirm) {
		close(authorizer.locked)
		<-authorizer.release
	}
	return allowed, err
}

func waitForBlockedQueries(t *testing.T, pool *pgxpool.Pool, fragment string, minimum int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query ILIKE '%'||$1||'%'`, fragment).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= minimum {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe %d blocked queries containing %q", minimum, fragment)
}

func TestVietQRInstructionAndConfirmationAreAtomicIdempotentAndTenantScoped(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := ordersapplication.NewService(orders)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "vietqr-payment"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x42}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	inventory := orders.inventory.(*inventorypostgres.Repository)
	paymentRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	service := paymentsapplication.NewService(paymentRepository)
	config, err := service.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "config-request", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "NGUYEN VAN A", Enabled: true})
	if err != nil || config.MaskedAccount != "*********2738" {
		t.Fatalf("config = %#v, error = %v", config, err)
	}
	if _, err := service.GetConfig(context.Background(), pair.B.Store.ID()); !errors.Is(err, paymentsapplication.ErrNotFound) {
		t.Fatalf("cross-store config error = %v", err)
	}
	var ciphertext []byte
	if err := pool.QueryRow(context.Background(), `SELECT ciphertext FROM payment_provider_configs WHERE store_id=$1 AND provider='vietqr'`, pair.A.Store.ID()).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("0017040702738")) {
		t.Fatalf("stored config exposed account, error = %v", err)
	}
	command := paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "instruction-1", CorrelationID: "instruction-request"}
	instruction, err := service.CreateInstruction(context.Background(), command)
	if err != nil || !instruction.Created || instruction.TransferReference == "" || instruction.Payload == "" || instruction.PNGBase64 == "" || instruction.Amount.AmountMinor != checkout.Order.GrandTotal.AmountMinor {
		t.Fatalf("instruction = %#v, error = %v", instruction, err)
	}
	replay, err := service.CreateInstruction(context.Background(), command)
	if err != nil || replay.Created || replay.AttemptID != instruction.AttemptID || replay.TransferReference != instruction.TransferReference {
		t.Fatalf("instruction replay = %#v, error = %v", replay, err)
	}
	command.IdempotencyKey = "instruction-2"
	alternateReplay, err := service.CreateInstruction(context.Background(), command)
	if err != nil || alternateReplay.Created || alternateReplay.AttemptID != instruction.AttemptID {
		t.Fatalf("instruction alternate replay = %#v, error = %v", alternateReplay, err)
	}
	confirmationCommand := paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "confirmation-1", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "confirmation-request", CorrelationID: "confirmation-request"}
	confirmations := make([]paymentsdomain.Confirmation, 2)
	confirmationErrors := make([]error, 2)
	commands := []paymentsapplication.ConfirmationCommand{confirmationCommand, confirmationCommand}
	commands[1].IdempotencyKey = "confirmation-2"
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range confirmations {
		go func(index int) {
			defer wait.Done()
			<-start
			confirmations[index], confirmationErrors[index] = service.Confirm(context.Background(), commands[index])
		}(index)
	}
	close(start)
	wait.Wait()
	createdConfirmations := 0
	for index, confirmation := range confirmations {
		if confirmationErrors[index] != nil || confirmation.Outcome != "paid" {
			t.Fatalf("confirmation %d = %#v, error = %v", index, confirmation, confirmationErrors[index])
		}
		if confirmation.Created {
			createdConfirmations++
		}
	}
	if createdConfirmations != 1 {
		t.Fatalf("created confirmations = %d", createdConfirmations)
	}
	confirmationReplay, err := service.Confirm(context.Background(), confirmationCommand)
	if err != nil || confirmationReplay.Created || confirmationReplay.Outcome != "paid" {
		t.Fatalf("confirmation replay = %#v, error = %v", confirmationReplay, err)
	}
	var orderState, paymentState, reservationState string
	var onHand, ledgers, audits, paymentEvents, orderEvents int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int{
		`SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`: &ledgers,
		`SELECT count(*) FROM audit_records WHERE store_id=$1 AND subject_id=$2 AND action='payment.confirmed'`:                               &audits,
		`SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='payment.confirmed.v1'`:                      &paymentEvents,
		`SELECT count(*) FROM outbox_events WHERE store_id=$1 AND aggregate_id=$2 AND event_type='order.paid.v1'`:                             &orderEvents,
	} {
		identifier := checkout.Order.ID.String()
		if target == &audits || target == &paymentEvents {
			identifier = checkout.Order.Payment.ID.String()
		}
		if err := pool.QueryRow(context.Background(), query, pair.A.Store.ID(), identifier).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if orderState != "paid" || paymentState != "succeeded" || reservationState != "confirmed" || onHand != 1 || ledgers != 1 || audits != 1 || paymentEvents != 1 || orderEvents != 1 {
		t.Fatalf("states/effects = %s/%s/%s on_hand=%d ledger=%d audit=%d payment_event=%d order_event=%d", orderState, paymentState, reservationState, onHand, ledgers, audits, paymentEvents, orderEvents)
	}
	changedObservation := confirmationCommand
	changedObservation.IdempotencyKey = "confirmation-changed"
	changedObservation.ObservedAmountMinor++
	if _, err := service.Confirm(context.Background(), changedObservation); !errors.Is(err, paymentsapplication.ErrConflict) {
		t.Fatalf("changed observation error = %v", err)
	}
	for index := range 3 {
		observation := confirmationCommand
		observation.IdempotencyKey = fmt.Sprintf("post-success-%d", index)
		observation.TransferReference = fmt.Sprintf("UNRELATED-%d", index)
		observation.ReceivedAt = observation.ReceivedAt.Add(time.Duration(index+1) * time.Second)
		result, err := service.Confirm(context.Background(), observation)
		if err != nil || result.Outcome != "reconciliation_required" {
			t.Fatalf("post-success observation %d = %#v, error = %v", index, result, err)
		}
	}
	var postSuccessReconciliations, postSuccessEvents int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_reconciliation_items WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&postSuccessReconciliations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_type='payment.reconciliation_required.v1' AND payload->>'payment_id'=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&postSuccessEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT state FROM payments WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&paymentState); err != nil {
		t.Fatal(err)
	}
	if paymentState != "succeeded" || postSuccessReconciliations != 3 || postSuccessEvents != 3 {
		t.Fatalf("post-success state/effects = %s reconciliation=%d events=%d", paymentState, postSuccessReconciliations, postSuccessEvents)
	}

	fixtureB := insertCheckoutFixture(t, pool, pair.B.Store.ID(), pair.B.UserID, 1)
	checkoutB, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.B.Store.ID(), pair.B.UserID, fixtureB.addressID, "vietqr-reconciliation"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceConfig(context.Background(), pair.B.Store.ID(), pair.B.UserID, "config-b", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040709999", AccountName: "STORE B", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instructionB, err := service.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.B.Store.ID(), OrderID: checkoutB.Order.ID, PaymentID: checkoutB.Order.Payment.ID, IdempotencyKey: "instruction-b", CorrelationID: "instruction-b"})
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := service.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.B.Store.ID(), PaymentID: checkoutB.Order.Payment.ID, ActorID: pair.B.UserID, IdempotencyKey: "confirmation-b", TransferReference: instructionB.TransferReference, ObservedAmountMinor: checkoutB.Order.GrandTotal.AmountMinor + 1, ReceivedAt: time.Now().UTC(), RequestID: "confirmation-b", CorrelationID: "confirmation-b"})
	if err != nil || reconciled.Outcome != "reconciliation_required" || reconciled.ReconciliationID == "" {
		t.Fatalf("reconciliation = %#v, error = %v", reconciled, err)
	}
	items, err := service.ListReconciliation(context.Background(), pair.B.Store.ID(), 10, time.Time{}, "")
	if err != nil || len(items) != 1 || items[0].Reason != "wrong_amount" {
		t.Fatalf("reconciliation items = %#v, error = %v", items, err)
	}
	if foreign, err := service.ListReconciliation(context.Background(), pair.A.Store.ID(), 10, time.Time{}, ""); err != nil || len(foreign) != 3 {
		t.Fatalf("cross-store reconciliation = %#v, error = %v", foreign, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.B.Store.ID(), checkoutB.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if orderState != "pending_payment" || paymentState != "reconciliation_required" || reservationState != "active" || onHand != 1 {
		t.Fatalf("reconciliation states = %s/%s/%s on_hand=%d", orderState, paymentState, reservationState, onHand)
	}
}

func TestVietQRConfirmationRollsBackWhenOutboxFails(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := ordersapplication.NewService(orders)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "vietqr-rollback"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x35}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	repository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores)
	service := paymentsapplication.NewService(repository)
	if _, err := service.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "rollback-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "ROLLBACK", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := service.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "rollback-instruction", CorrelationID: "rollback-instruction"})
	if err != nil {
		t.Fatal(err)
	}
	failingRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), failingPaymentEvents{}, stores)
	failingService := paymentsapplication.NewService(failingRepository)
	_, err = failingService.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "rollback-confirmation", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "rollback-confirmation", CorrelationID: "rollback-confirmation"})
	if !errors.Is(err, errInjectedPaymentEvent) {
		t.Fatalf("confirmation error = %v", err)
	}
	var orderState, paymentState, reservationState string
	var onHand, confirmations, ledgers, audits int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	for query, target := range map[string]*int{
		`SELECT count(*) FROM payment_manual_confirmations WHERE store_id=$1 AND payment_id=$2`:                 &confirmations,
		`SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2`:             &ledgers,
		`SELECT count(*) FROM audit_records WHERE store_id=$1 AND subject_id=$2 AND action='payment.confirmed'`: &audits,
	} {
		identifier := checkout.Order.ID.String()
		if target == &confirmations || target == &audits {
			identifier = checkout.Order.Payment.ID.String()
		}
		if err := pool.QueryRow(context.Background(), query, pair.A.Store.ID(), identifier).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if orderState != "pending_payment" || paymentState != "pending" || reservationState != "active" || onHand != 2 || confirmations != 0 || ledgers != 0 || audits != 0 {
		t.Fatalf("rollback state/effects = %s/%s/%s on_hand=%d confirmations=%d ledgers=%d audits=%d", orderState, paymentState, reservationState, onHand, confirmations, ledgers, audits)
	}
}

func TestVietQRReleasedReservationBecomesReconciliation(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "vietqr-released"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x45}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	service := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	if _, err := service.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "released-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "RELEASED", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := service.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "released-instruction", CorrelationID: "released-instruction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inventory_reservations SET status='released',version=version+1,updated_at=clock_timestamp(),closed_at=clock_timestamp(),close_reason='test_release' WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), checkout.Order.ID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "released-confirmation", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "released-confirmation", CorrelationID: "released-confirmation"})
	if err != nil || result.Outcome != "reconciliation_required" {
		t.Fatalf("confirmation = %#v, error = %v", result, err)
	}
	var orderState, paymentState, reservationState string
	var onHand, confirmationLedgers int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&confirmationLedgers); err != nil {
		t.Fatal(err)
	}
	if orderState != "pending_payment" || paymentState != "reconciliation_required" || reservationState != "released" || onHand != 2 || confirmationLedgers != 0 {
		t.Fatalf("states/effects = %s/%s/%s on_hand=%d ledgers=%d", orderState, paymentState, reservationState, onHand, confirmationLedgers)
	}
}

func TestVietQRConfirmationSerializesWithExpiry(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := ordersapplication.NewService(orders)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1).addressID, "vietqr-expiry-race"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x61}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	setupService := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	if _, err := setupService.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "expiry-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "EXPIRY", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := setupService.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "expiry-instruction", CorrelationID: "expiry-instruction"})
	if err != nil {
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
	locked, release := make(chan struct{}), make(chan struct{})
	confirmService := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, blockingPaymentOrders{delegate: orders, locked: locked, release: release}, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	confirmationDone := make(chan error, 1)
	go func() {
		_, err := confirmService.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "expiry-confirmation", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "expiry-confirmation", CorrelationID: "expiry-confirmation"})
		confirmationDone <- err
	}()
	<-locked
	type expiryResult struct {
		processed int
		err       error
	}
	expiryDone := make(chan expiryResult, 1)
	go func() {
		processed, err := orderService.ExpireDue(context.Background(), 10, "expiry-race")
		expiryDone <- expiryResult{processed: processed, err: err}
	}()
	firstExpiry := <-expiryDone
	if firstExpiry.err != nil || firstExpiry.processed != 0 {
		t.Fatalf("expiry while order locked = %d, error = %v", firstExpiry.processed, firstExpiry.err)
	}
	close(release)
	if err := <-confirmationDone; err != nil {
		t.Fatalf("confirmation error = %v", err)
	}
	processed, err := orderService.ExpireDue(context.Background(), 10, "expiry-after-confirmation")
	if err != nil || processed != 1 {
		t.Fatalf("expiry after confirmation = %d, error = %v", processed, err)
	}
	var orderState, paymentState, reservationState string
	var onHand, reconciliations int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,p.state,r.status,s.on_hand FROM orders o JOIN payments p USING(store_id,order_id) JOIN inventory_reservations r USING(store_id,order_id) JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &paymentState, &reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_reconciliation_items WHERE store_id=$1 AND payment_id=$2`, pair.A.Store.ID(), checkout.Order.Payment.ID).Scan(&reconciliations); err != nil {
		t.Fatal(err)
	}
	if orderState != "expired" || paymentState != "reconciliation_required" || reservationState != "expired" || onHand != 1 || reconciliations != 1 {
		t.Fatalf("race state/effects = %s/%s/%s on_hand=%d reconciliation=%d", orderState, paymentState, reservationState, onHand, reconciliations)
	}
}

func TestVietQRConfirmationSerializesWithAdjustment(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "vietqr-adjustment-race"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, _ := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x71}, 32))}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	service := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores))
	if _, err := service.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "adjust-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "ADJUST", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := service.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "adjust-instruction", CorrelationID: "adjust-instruction"})
	if err != nil {
		t.Fatal(err)
	}
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
	confirmationDone := make(chan error, 1)
	go func() {
		_, err := service.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: pair.A.UserID, IdempotencyKey: "adjust-confirmation", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "adjust-confirmation", CorrelationID: "adjust-confirmation"})
		confirmationDone <- err
	}()
	waitForBlockedQueries(t, pool, "inventory_stock_items", 1)
	adjustmentDone := make(chan error, 1)
	go func() {
		_, err := inventoryapplication.NewService(inventory).Adjust(context.Background(), pair.A.Store.ID(), inventorydomain.Adjustment{VariantID: fixture.variantID, QuantityDelta: 1, Reason: "race restock", ExpectedVersion: stockVersion, IdempotencyKey: "adjustment-race", ActorID: pair.A.UserID, RequestID: "adjustment-race"})
		adjustmentDone <- err
	}()
	waitForBlockedQueries(t, pool, "inventory_stock_items", 2)
	if err := blocker.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-confirmationDone; err != nil {
		t.Fatalf("confirmation error = %v", err)
	}
	adjustmentErr := <-adjustmentDone
	if adjustmentErr != nil && !errors.Is(adjustmentErr, inventoryapplication.ErrConflict) {
		t.Fatalf("adjustment error = %v", adjustmentErr)
	}
	var reservationState string
	var onHand, confirmationLedgers int
	if err := pool.QueryRow(context.Background(), `SELECT r.status,s.on_hand FROM inventory_reservations r JOIN inventory_stock_items s ON s.store_id=r.store_id AND s.variant_id=r.variant_id WHERE r.store_id=$1 AND r.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&reservationState, &onHand); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inventory_ledger_entries WHERE store_id=$1 AND business_reference=$2 AND entry_type='reservation_confirmation'`, pair.A.Store.ID(), checkout.Order.ID).Scan(&confirmationLedgers); err != nil {
		t.Fatal(err)
	}
	if reservationState != "confirmed" || (onHand != 1 && onHand != 2) || confirmationLedgers != 1 {
		t.Fatalf("race state/effects = %s on_hand=%d ledgers=%d adjustment_error=%v", reservationState, onHand, confirmationLedgers, adjustmentErr)
	}
}

func TestVietQRConfirmationSerializesPermissionRevocation(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 1).addressID, "vietqr-permission-race"))
	if err != nil {
		t.Fatal(err)
	}
	staffID, _ := ids.NewUserID()
	for _, statement := range []string{
		`INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1,$2,'payment-staff@example.com','payment-staff@example.com','active',now(),now())`,
		`INSERT INTO store_memberships(store_id,user_id,status,role,created_at,updated_at) VALUES($1,$2,'active','staff',now(),now())`,
		`INSERT INTO store_membership_permission_sets(store_id,user_id,version,updated_at) VALUES($1,$2,1,now())`,
		`INSERT INTO store_membership_permissions(store_id,user_id,permission,version,created_at,updated_at) VALUES($1,$2,'payment.confirm',1,now(),now())`,
	} {
		if _, err := pool.Exec(context.Background(), statement, pair.A.Store.ID(), staffID); err != nil {
			t.Fatal(err)
		}
	}
	keyring, err := paymentsecret.New(map[int][32]byte{1: [32]byte(bytes.Repeat([]byte{0x51}, 32))}, 1)
	if err != nil {
		t.Fatal(err)
	}
	inventory := orders.inventory.(*inventorypostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	setupRepository := paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), stores)
	setupService := paymentsapplication.NewService(setupRepository)
	if _, err := setupService.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "permission-config", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "PERMISSION", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instruction, err := setupService.CreateInstruction(context.Background(), paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "permission-instruction", CorrelationID: "permission-instruction"})
	if err != nil {
		t.Fatal(err)
	}
	locked, release := make(chan struct{}), make(chan struct{})
	service := paymentsapplication.NewService(paymentspostgres.NewRepository(orders.db, keyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), blockingPaymentAuthorizer{delegate: stores, locked: locked, release: release}))
	confirmationDone := make(chan error, 1)
	go func() {
		_, err := service.Confirm(context.Background(), paymentsapplication.ConfirmationCommand{StoreID: pair.A.Store.ID(), PaymentID: checkout.Order.Payment.ID, ActorID: staffID, IdempotencyKey: "permission-confirmation", TransferReference: instruction.TransferReference, ObservedAmountMinor: checkout.Order.GrandTotal.AmountMinor, ReceivedAt: time.Now().UTC(), RequestID: "permission-confirmation", CorrelationID: "permission-confirmation"})
		confirmationDone <- err
	}()
	<-locked
	revocationDone := make(chan error, 1)
	go func() {
		_, err := stores.ReplaceStaffPermissions(context.Background(), pair.A.Store.ID(), staffID, nil, 1, pair.A.UserID, "permission-revoke", time.Now().UTC())
		revocationDone <- err
	}()
	waitForBlockedQueries(t, pool, "store_memberships", 1)
	close(release)
	if err := <-confirmationDone; err != nil {
		t.Fatalf("confirmation error = %v", err)
	}
	if err := <-revocationDone; err != nil {
		t.Fatalf("revocation error = %v", err)
	}
	allowed, err := stores.HasPermission(context.Background(), pair.A.Store.ID(), staffID, storesdomain.PermissionPaymentConfirm)
	if err != nil || allowed {
		t.Fatalf("permission after revocation = %v, error = %v", allowed, err)
	}
}
