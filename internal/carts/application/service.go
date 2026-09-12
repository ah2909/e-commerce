package application

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/domain"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var (
	ErrNotFound = errors.New("cart not found")
	ErrConflict = errors.New("cart conflict")
)

type Repository interface {
	CreateOrGet(context.Context, ids.StoreID, domain.Owner, ids.CartID) (domain.Cart, bool, error)
	Get(context.Context, ids.StoreID, domain.Owner) (domain.Cart, error)
	SetLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64, int64) (domain.Cart, error)
	RemoveLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64) (domain.Cart, error)
	Abandon(context.Context, ids.StoreID, domain.Owner, int64) error
	ExpireDue(context.Context, int) (int, error)
}

type Catalog interface {
	ResolveCartVariants(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]catalogapplication.CartVariant, error)
}
type Prices interface {
	ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error)
}
type Availability interface {
	ResolveAvailableQuantities(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]int64, error)
}

type LineView struct {
	VariantID    ids.VariantID     `json:"variant_id"`
	Quantity     int64             `json:"quantity"`
	ProductName  string            `json:"product_name,omitempty"`
	SKU          string            `json:"sku,omitempty"`
	OptionValues map[string]string `json:"option_values,omitempty"`
	UnitPrice    *money.Money      `json:"unit_price,omitempty"`
	LineEstimate *money.Money      `json:"line_estimate,omitempty"`
	Sellable     bool              `json:"sellable"`
	InStock      bool              `json:"in_stock"`
}

type View struct {
	CartID            ids.CartID    `json:"cart_id"`
	Currency          string        `json:"currency"`
	Status            domain.Status `json:"status"`
	Version           int64         `json:"version"`
	ExpiresAt         time.Time     `json:"expires_at"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Lines             []LineView    `json:"lines"`
	EstimatedSubtotal *money.Money  `json:"estimated_subtotal"`
	CheckoutReady     bool          `json:"checkout_ready"`
}

type Service struct {
	repository   Repository
	catalog      Catalog
	prices       Prices
	availability Availability
}

func NewService(repository Repository, catalog Catalog, prices Prices, availability Availability) Service {
	return Service{repository: repository, catalog: catalog, prices: prices, availability: availability}
}

func OwnerFromUser(id ids.UserID) domain.Owner {
	return domain.Owner{Kind: domain.OwnerUser, ID: id.String()}
}
func OwnerFromGuest(id ids.GuestID) domain.Owner {
	return domain.Owner{Kind: domain.OwnerGuest, ID: id.String()}
}

func (service Service) CreateOrGet(ctx context.Context, storeID ids.StoreID, owner domain.Owner) (View, bool, error) {
	if storeID == "" || domain.ValidateOwner(owner) != nil {
		return View{}, false, domain.ErrInvalidCart
	}
	id, err := ids.NewCartID()
	if err != nil {
		return View{}, false, err
	}
	cart, created, err := service.repository.CreateOrGet(ctx, storeID, owner, id)
	if err != nil {
		return View{}, false, err
	}
	view, err := service.view(ctx, cart)
	return view, created, err
}

func (service Service) Get(ctx context.Context, storeID ids.StoreID, owner domain.Owner) (View, error) {
	if storeID == "" || domain.ValidateOwner(owner) != nil {
		return View{}, domain.ErrInvalidCart
	}
	cart, err := service.repository.Get(ctx, storeID, owner)
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, cart)
}

func (service Service) SetLine(ctx context.Context, storeID ids.StoreID, owner domain.Owner, variantID ids.VariantID, quantity, expected int64) (View, error) {
	if storeID == "" || variantID == "" || expected < 1 || domain.ValidateOwner(owner) != nil || domain.ValidateQuantity(quantity) != nil {
		return View{}, domain.ErrInvalidCart
	}
	variants, err := service.catalog.ResolveCartVariants(ctx, storeID, []ids.VariantID{variantID})
	if err != nil {
		return View{}, err
	}
	prices, err := service.prices.ResolveCurrent(ctx, storeID, []ids.VariantID{variantID})
	if err != nil {
		return View{}, err
	}
	if _, ok := variants[variantID]; !ok {
		return View{}, ErrNotFound
	}
	if _, ok := prices[variantID]; !ok {
		return View{}, ErrNotFound
	}
	cart, err := service.repository.SetLine(ctx, storeID, owner, variantID, quantity, expected)
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, cart)
}

func (service Service) RemoveLine(ctx context.Context, storeID ids.StoreID, owner domain.Owner, variantID ids.VariantID, expected int64) (View, error) {
	if storeID == "" || variantID == "" || expected < 1 || domain.ValidateOwner(owner) != nil {
		return View{}, domain.ErrInvalidCart
	}
	cart, err := service.repository.RemoveLine(ctx, storeID, owner, variantID, expected)
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, cart)
}

func (service Service) Abandon(ctx context.Context, storeID ids.StoreID, owner domain.Owner, expected int64) error {
	if storeID == "" || expected < 1 || domain.ValidateOwner(owner) != nil {
		return domain.ErrInvalidCart
	}
	return service.repository.Abandon(ctx, storeID, owner, expected)
}

func (service Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, domain.ErrInvalidCart
	}
	return service.repository.ExpireDue(ctx, limit)
}

func (service Service) view(ctx context.Context, cart domain.Cart) (View, error) {
	view := View{CartID: cart.ID, Currency: cart.Currency, Status: cart.Status, Version: cart.Version, ExpiresAt: cart.ExpiresAt, CreatedAt: cart.CreatedAt, UpdatedAt: cart.UpdatedAt, Lines: make([]LineView, len(cart.Lines))}
	if len(cart.Lines) == 0 {
		zero, _ := money.New(0, "VND")
		view.EstimatedSubtotal = &zero
		view.CheckoutReady = false
		return view, nil
	}
	variantIDs := make([]ids.VariantID, len(cart.Lines))
	for i := range cart.Lines {
		variantIDs[i] = cart.Lines[i].VariantID
	}
	variants, err := service.catalog.ResolveCartVariants(ctx, cart.StoreID, variantIDs)
	if err != nil {
		return View{}, err
	}
	prices, err := service.prices.ResolveCurrent(ctx, cart.StoreID, variantIDs)
	if err != nil {
		return View{}, err
	}
	stock, err := service.availability.ResolveAvailableQuantities(ctx, cart.StoreID, variantIDs)
	if err != nil {
		return View{}, err
	}
	ready, total := true, int64(0)
	for i, line := range cart.Lines {
		entry := LineView{VariantID: line.VariantID, Quantity: line.Quantity, InStock: stock[line.VariantID] >= line.Quantity}
		variant, hasVariant := variants[line.VariantID]
		price, hasPrice := prices[line.VariantID]
		if hasVariant {
			entry.ProductName, entry.SKU, entry.OptionValues = variant.ProductName, variant.SKU, variant.OptionValues
		}
		if hasVariant && hasPrice && price.Valid() && price.Currency == "VND" && (price.AmountMinor == 0 || line.Quantity <= math.MaxInt64/price.AmountMinor) {
			entry.Sellable = true
			entry.UnitPrice = &price
			estimate, _ := money.New(price.AmountMinor*line.Quantity, "VND")
			entry.LineEstimate = &estimate
			if total <= math.MaxInt64-estimate.AmountMinor {
				total += estimate.AmountMinor
			} else {
				ready = false
			}
		} else {
			ready = false
		}
		if !entry.InStock {
			ready = false
		}
		view.Lines[i] = entry
	}
	sort.Slice(view.Lines, func(i, j int) bool { return view.Lines[i].VariantID < view.Lines[j].VariantID })
	if ready {
		subtotal, _ := money.New(total, "VND")
		view.EstimatedSubtotal = &subtotal
	}
	view.CheckoutReady = ready
	return view, nil
}
