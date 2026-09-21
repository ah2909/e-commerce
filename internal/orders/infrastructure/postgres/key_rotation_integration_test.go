//go:build integration

package postgres

import (
	"bytes"
	"context"
	"testing"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	paymentsapplication "github.com/ah2909/e-commerce/internal/payments/application"
	paymentsdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	paymentspostgres "github.com/ah2909/e-commerce/internal/payments/infrastructure/postgres"
	paymentsecret "github.com/ah2909/e-commerce/internal/payments/infrastructure/secret"
	paymentvietqr "github.com/ah2909/e-commerce/internal/payments/infrastructure/vietqr"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
)

func TestPaymentKeyRotationCoversEveryCiphertextShapeAndPermitsRetirement(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := ordersapplication.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "rotation-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := [32]byte(bytes.Repeat([]byte{0x31}, 32))
	newKey := [32]byte(bytes.Repeat([]byte{0x32}, 32))
	oldKeyring, _ := paymentsecret.New(map[int][32]byte{1: oldKey}, 1)
	inventory := orders.inventory.(*inventorypostgres.Repository)
	oldRepository := paymentspostgres.NewRepository(orders.db, oldKeyring, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	service := paymentsapplication.NewService(oldRepository)
	if _, err := service.ReplaceConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "rotation-vietqr", 0, paymentsdomain.VietQRConfigDraft{BankBIN: "970436", AccountNumber: "0017040702738", AccountName: "NGUYEN VAN A", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	instructionCommand := paymentsapplication.InstructionCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, PaymentID: checkout.Order.Payment.ID, IdempotencyKey: "rotation-attempt", CorrelationID: "rotation-attempt"}
	if _, err := service.CreateInstruction(context.Background(), instructionCommand); err != nil {
		t.Fatal(err)
	}
	momoService := service.WithMoMo(oldRepository, &momoGatewayFixture{})
	if _, err := momoService.ReplaceMoMoConfig(context.Background(), pair.A.Store.ID(), pair.A.UserID, "rotation-momo", 0, paymentsdomain.MoMoConfigDraft{PartnerCode: "MOMOTESTPARTNER", AccessKey: "sandbox-access-key", SecretKey: "sandbox-secret-key-123456", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	newKeyring, _ := paymentsecret.New(map[int][32]byte{1: oldKey, 2: newKey}, 2)
	rotator := paymentspostgres.NewKeyRotator(orders.db, newKeyring)
	for {
		processed, err := rotator.RotateBatch(context.Background(), 1, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if processed == 0 {
			break
		}
	}
	if references, err := rotator.ReferenceCount(context.Background(), 1); err != nil || references != 0 {
		t.Fatalf("old references=%d error=%v", references, err)
	}
	newOnly, _ := paymentsecret.New(map[int][32]byte{2: newKey}, 2)
	newRepository := paymentspostgres.NewRepository(orders.db, newOnly, paymentvietqr.Encoder{}, orders, inventory, auditpostgres.NewWriter(), eventingpostgres.NewWriter(), storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()))
	if _, err := paymentsapplication.NewService(newRepository).GetConfig(context.Background(), pair.A.Store.ID()); err != nil {
		t.Fatalf("rotated config unreadable: %v", err)
	}
	if replay, err := paymentsapplication.NewService(newRepository).CreateInstruction(context.Background(), instructionCommand); err != nil || replay.Created {
		t.Fatalf("rotated attempt replay=%#v error=%v", replay, err)
	}
	var callbackRow struct {
		Digest []byte `gorm:"column:token_digest"`
	}
	if err := orders.db.Raw(`SELECT token_digest FROM payment_callback_routes WHERE store_id=?`, pair.A.Store.ID()).Scan(&callbackRow).Error; err != nil || len(callbackRow.Digest) != 32 {
		t.Fatalf("callback digest length=%d error=%v", len(callbackRow.Digest), err)
	}
	var callbackDigest [32]byte
	copy(callbackDigest[:], callbackRow.Digest)
	if _, err := newRepository.ResolveMoMoCallback(context.Background(), callbackDigest); err != nil {
		t.Fatalf("rotated callback unreadable: %v", err)
	}
}
