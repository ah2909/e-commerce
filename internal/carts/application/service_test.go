package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/domain"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

type repositoryStub struct{ cart domain.Cart }

func (stub repositoryStub) CreateOrGet(context.Context, ids.StoreID, domain.Owner, ids.CartID) (domain.Cart, bool, error) {
	return stub.cart, true, nil
}
func (stub repositoryStub) Get(context.Context, ids.StoreID, domain.Owner) (domain.Cart, error) {
	return stub.cart, nil
}
func (stub repositoryStub) SetLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64, int64) (domain.Cart, error) {
	return stub.cart, nil
}
func (stub repositoryStub) RemoveLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64) (domain.Cart, error) {
	return stub.cart, nil
}
func (stub repositoryStub) Abandon(context.Context, ids.StoreID, domain.Owner, int64) error {
	return nil
}
func (stub repositoryStub) ExpireDue(context.Context, int) (int, error) { return 0, nil }

type catalogStub map[ids.VariantID]catalogapplication.CartVariant

func (stub catalogStub) ResolveCartVariants(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]catalogapplication.CartVariant, error) {
	return stub, nil
}

type priceStub map[ids.VariantID]money.Money

func (stub priceStub) ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	return stub, nil
}

type availabilityStub map[ids.VariantID]int64

func (stub availabilityStub) ResolveAvailableQuantities(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]int64, error) {
	return stub, nil
}

func TestViewUsesCurrentIntegerEstimates(t *testing.T) {
	store, _ := ids.NewStoreID()
	user, _ := ids.NewUserID()
	cartID, _ := ids.NewCartID()
	variant, _ := ids.NewVariantID()
	now := time.Now().UTC()
	cart := domain.Cart{StoreID: store, ID: cartID, Owner: OwnerFromUser(user), Currency: "VND", Status: domain.StatusActive, Version: 2, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(domain.TTL), Lines: []domain.Line{{VariantID: variant, Quantity: 3}}}
	price, _ := money.New(12000, "VND")
	service := NewService(repositoryStub{cart: cart}, catalogStub{variant: {VariantID: variant, ProductName: "Shirt", SKU: "SHIRT"}}, priceStub{variant: price}, availabilityStub{variant: 3})
	view, err := service.Get(context.Background(), store, OwnerFromUser(user))
	if err != nil || !view.CheckoutReady || view.EstimatedSubtotal == nil || view.EstimatedSubtotal.AmountMinor != 36000 || len(view.Lines) != 1 {
		t.Fatalf("view = %#v, error = %v", view, err)
	}
}

func TestUnavailableLineSuppressesSubtotal(t *testing.T) {
	store, _ := ids.NewStoreID()
	guest, _ := ids.NewGuestID()
	cartID, _ := ids.NewCartID()
	variant, _ := ids.NewVariantID()
	now := time.Now().UTC()
	cart := domain.Cart{StoreID: store, ID: cartID, Owner: OwnerFromGuest(guest), Currency: "VND", Status: domain.StatusActive, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(domain.TTL), Lines: []domain.Line{{VariantID: variant, Quantity: 1}}}
	service := NewService(repositoryStub{cart: cart}, catalogStub{}, priceStub{}, availabilityStub{})
	view, err := service.Get(context.Background(), store, OwnerFromGuest(guest))
	if err != nil || view.CheckoutReady || view.EstimatedSubtotal != nil || view.Lines[0].Sellable {
		t.Fatalf("view = %#v, error = %v", view, err)
	}
	if _, err := service.SetLine(context.Background(), store, OwnerFromGuest(guest), variant, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set unavailable error = %v", err)
	}
}
