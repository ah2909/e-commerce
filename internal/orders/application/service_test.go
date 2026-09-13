package application

import (
	"context"
	"errors"
	"testing"

	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type repositoryStub struct {
	prepared PreparedCheckout
	created  bool
	err      error
}

func (stub *repositoryStub) Checkout(_ context.Context, value PreparedCheckout) (domain.Order, bool, error) {
	stub.prepared = value
	return domain.Order{ID: value.OrderID}, stub.created, stub.err
}
func (*repositoryStub) GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (domain.Order, error) {
	return domain.Order{}, nil
}
func (*repositoryStub) GetForGrant(context.Context, ids.StoreID, ids.OrderID, [32]byte) (domain.Order, error) {
	return domain.Order{}, nil
}
func (*repositoryStub) ExpireDue(context.Context, int, string) (int, error) { return 0, nil }

func TestGuestCheckoutGeneratesDigestAndReturnsGrantOnlyOnCreation(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	guestID, _ := ids.NewGuestID()
	command := CheckoutCommand{StoreID: storeID, Owner: domain.GuestOwner(guestID), CartVersion: 1, PaymentProvider: "vietqr", GuestContact: domain.Contact{Name: "Guest", Email: "guest@example.com", Phone: "+84901234567"}, GuestAddress: domain.Address{RecipientName: "Guest", Phone: "+84901234567", Line1: "1 Street", Ward: "Ward", District: "District", Province: "Province", CountryCode: "VN"}, IdempotencyKey: "key", CorrelationID: "request"}
	stub := &repositoryStub{created: true}
	result, err := NewService(stub).Checkout(context.Background(), command)
	if err != nil || result.OrderAccessToken == "" || stub.prepared.GrantDigest == ([32]byte{}) {
		t.Fatalf("result = %#v, prepared = %#v, error = %v", result, stub.prepared, err)
	}
	stub.created = false
	result, err = NewService(stub).Checkout(context.Background(), command)
	if err != nil || result.OrderAccessToken != "" {
		t.Fatalf("replay result = %#v, error = %v", result, err)
	}
}

func TestCheckoutValidationRejectsOwnerAndProviderConfusion(t *testing.T) {
	storeID, _ := ids.NewStoreID()
	userID, _ := ids.NewUserID()
	addressID, _ := ids.NewAddressID()
	valid := CheckoutCommand{StoreID: storeID, Owner: domain.UserOwner(userID), CartVersion: 1, PaymentProvider: "vietqr", AddressID: addressID, IdempotencyKey: "key", CorrelationID: "request"}
	for name, mutate := range map[string]func(*CheckoutCommand){
		"missing key":          func(value *CheckoutCommand) { value.IdempotencyKey = "" },
		"unsupported provider": func(value *CheckoutCommand) { value.PaymentProvider = "cash" },
		"guest fields on user": func(value *CheckoutCommand) { value.GuestContact.Name = "confused" },
		"stale cart":           func(value *CheckoutCommand) { value.CartVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			command := valid
			mutate(&command)
			_, err := NewService(&repositoryStub{}).Checkout(context.Background(), command)
			if !errors.Is(err, domain.ErrInvalidOrder) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
