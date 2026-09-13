package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	cartsapplication "github.com/ah2909/e-commerce/internal/carts/application"
	cartsdomain "github.com/ah2909/e-commerce/internal/carts/domain"
	catalogapplication "github.com/ah2909/e-commerce/internal/catalog/application"
	customersapplication "github.com/ah2909/e-commerce/internal/customers/application"
	customersdomain "github.com/ah2909/e-commerce/internal/customers/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	inventorydomain "github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	paymentsdomain "github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	shippingapplication "github.com/ah2909/e-commerce/internal/shipping/application"
	shippingdomain "github.com/ah2909/e-commerce/internal/shipping/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Carts interface {
	LockForCheckout(context.Context, *gorm.DB, ids.StoreID, cartsdomain.Owner, int64) (cartsdomain.Cart, error)
	ConvertForCheckout(context.Context, *gorm.DB, ids.StoreID, ids.CartID, ids.OrderID, int64, time.Time) error
}

type Catalog interface {
	ResolveCheckoutVariants(context.Context, *gorm.DB, ids.StoreID, []ids.VariantID) (map[ids.VariantID]catalogapplication.CartVariant, error)
}

type Pricing interface {
	ResolveCheckoutPrices(context.Context, *gorm.DB, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error)
}

type Customers interface {
	ResolveCheckoutAddress(context.Context, *gorm.DB, ids.StoreID, ids.UserID, ids.AddressID) (customersdomain.Profile, customersdomain.Address, error)
}

type Shipping interface {
	QuoteInTransaction(context.Context, *gorm.DB, ids.StoreID) (shippingdomain.Rule, error)
}

type Inventory interface {
	ReserveInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, []inventorydomain.Allocation, string) ([]inventorydomain.Reservation, error)
	ExpireOrderInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, time.Time, string) (bool, error)
}

type Payments interface {
	CreatePendingInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.PaymentID, ids.OrderID, string, money.Money, time.Time) (paymentsdomain.Payment, error)
	ExpireInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, time.Time) (paymentsdomain.Payment, error)
	LoadInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID) (paymentsdomain.Payment, error)
}

type Events interface {
	Append(context.Context, *gorm.DB, eventdomain.Event) error
}

type Repository struct {
	db        *gorm.DB
	carts     Carts
	catalog   Catalog
	pricing   Pricing
	customers Customers
	shipping  Shipping
	inventory Inventory
	payments  Payments
	events    Events
}

func NewRepository(db *gorm.DB, carts Carts, catalog Catalog, pricing Pricing, customers Customers, shipping Shipping, inventory Inventory, payments Payments, events Events) *Repository {
	return &Repository{db: db, carts: carts, catalog: catalog, pricing: pricing, customers: customers, shipping: shipping, inventory: inventory, payments: payments, events: events}
}

type orderRow struct {
	StoreID              string     `gorm:"column:store_id"`
	OrderID              string     `gorm:"column:order_id"`
	OrderNumber          string     `gorm:"column:order_number"`
	OwnerKind            string     `gorm:"column:owner_kind"`
	UserID               *string    `gorm:"column:user_id"`
	GuestID              *string    `gorm:"column:guest_id"`
	CustomerID           *string    `gorm:"column:customer_id"`
	CartID               string     `gorm:"column:cart_id"`
	Currency             string     `gorm:"column:currency"`
	SubtotalMinor        int64      `gorm:"column:subtotal_minor"`
	ShippingTotalMinor   int64      `gorm:"column:shipping_total_minor"`
	GrandTotalMinor      int64      `gorm:"column:grand_total_minor"`
	TaxPresentation      string     `gorm:"column:tax_presentation"`
	ContactName          string     `gorm:"column:contact_name"`
	ContactEmail         string     `gorm:"column:contact_email"`
	ContactPhone         string     `gorm:"column:contact_phone"`
	ShippingMode         string     `gorm:"column:shipping_mode"`
	ShippingRuleID       string     `gorm:"column:shipping_rule_id"`
	ShippingRuleVersion  int64      `gorm:"column:shipping_rule_version"`
	State                string     `gorm:"column:state"`
	Version              int64      `gorm:"column:version"`
	ReservationExpiresAt time.Time  `gorm:"column:reservation_expires_at"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
	ClosedAt             *time.Time `gorm:"column:closed_at"`
}

func (orderRow) TableName() string { return "orders" }

type lineRow struct {
	StoreID         string    `gorm:"column:store_id"`
	OrderID         string    `gorm:"column:order_id"`
	LineID          string    `gorm:"column:line_id"`
	VariantID       string    `gorm:"column:variant_id"`
	ProductName     string    `gorm:"column:product_name"`
	SKU             string    `gorm:"column:sku"`
	OptionValues    []byte    `gorm:"column:option_values;type:jsonb"`
	Quantity        int64     `gorm:"column:quantity"`
	UnitPriceMinor  int64     `gorm:"column:unit_price_minor"`
	LineTotalMinor  int64     `gorm:"column:line_total_minor"`
	Currency        string    `gorm:"column:currency"`
	TaxPresentation string    `gorm:"column:tax_presentation"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (lineRow) TableName() string { return "order_lines" }

type addressRow struct {
	StoreID       string    `gorm:"column:store_id"`
	OrderID       string    `gorm:"column:order_id"`
	AddressType   string    `gorm:"column:address_type"`
	RecipientName string    `gorm:"column:recipient_name"`
	Phone         string    `gorm:"column:phone"`
	Line1         string    `gorm:"column:line1"`
	Line2         string    `gorm:"column:line2"`
	Ward          string    `gorm:"column:ward"`
	District      string    `gorm:"column:district"`
	Province      string    `gorm:"column:province"`
	PostalCode    string    `gorm:"column:postal_code"`
	CountryCode   string    `gorm:"column:country_code"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (addressRow) TableName() string { return "order_addresses" }

type idempotencyRow struct {
	StoreID            string    `gorm:"column:store_id"`
	IdempotencyKey     string    `gorm:"column:idempotency_key"`
	RequestFingerprint []byte    `gorm:"column:request_fingerprint"`
	OrderID            string    `gorm:"column:order_id"`
	CreatedAt          time.Time `gorm:"column:created_at"`
}

func (idempotencyRow) TableName() string { return "order_idempotency" }

type grantRow struct {
	StoreID     string     `gorm:"column:store_id"`
	TokenDigest []byte     `gorm:"column:token_digest"`
	OrderID     string     `gorm:"column:order_id"`
	ExpiresAt   time.Time  `gorm:"column:expires_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
}

func (grantRow) TableName() string { return "order_guest_grants" }

type historyRow struct {
	StoreID    string    `gorm:"column:store_id"`
	HistoryID  string    `gorm:"column:history_id"`
	OrderID    string    `gorm:"column:order_id"`
	FromState  *string   `gorm:"column:from_state"`
	ToState    string    `gorm:"column:to_state"`
	Reason     string    `gorm:"column:reason"`
	OccurredAt time.Time `gorm:"column:occurred_at"`
}

func (historyRow) TableName() string { return "order_state_history" }

func (repository *Repository) Checkout(ctx context.Context, prepared application.PreparedCheckout) (domain.Order, bool, error) {
	var result domain.Order
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", prepared.StoreID.String()+":checkout:"+prepared.IdempotencyKey).Error; err != nil {
			return err
		}
		var replay idempotencyRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND idempotency_key=?", prepared.StoreID, prepared.IdempotencyKey).First(&replay).Error
		if err == nil {
			if !bytes.Equal(replay.RequestFingerprint, prepared.Fingerprint[:]) {
				return application.ErrConflict
			}
			var loadErr error
			result, loadErr = repository.load(tx, prepared.StoreID, ids.OrderID(replay.OrderID))
			return loadErr
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		cartOwner := cartsdomain.Owner{Kind: cartsdomain.OwnerKind(prepared.Owner.Kind)}
		if prepared.Owner.Kind == domain.OwnerUser {
			cartOwner.ID = prepared.Owner.UserID.String()
		} else {
			cartOwner.ID = prepared.Owner.GuestID.String()
		}
		cart, err := repository.carts.LockForCheckout(ctx, tx, prepared.StoreID, cartOwner, prepared.CartVersion)
		if err != nil {
			return mapParticipantError(err)
		}
		if len(cart.Lines) == 0 || cart.Currency != "VND" {
			return application.ErrUnprocessable
		}
		variantIDs := make([]ids.VariantID, len(cart.Lines))
		for i := range cart.Lines {
			variantIDs[i] = cart.Lines[i].VariantID
		}
		sort.Slice(variantIDs, func(i, j int) bool { return variantIDs[i].String() < variantIDs[j].String() })
		variants, err := repository.catalog.ResolveCheckoutVariants(ctx, tx, prepared.StoreID, variantIDs)
		if err != nil {
			return err
		}
		prices, err := repository.pricing.ResolveCheckoutPrices(ctx, tx, prepared.StoreID, variantIDs)
		if err != nil {
			return err
		}
		if len(variants) != len(variantIDs) || len(prices) != len(variantIDs) {
			return application.ErrUnprocessable
		}
		contact, address, customerID, err := repository.resolveCustomer(ctx, tx, prepared)
		if err != nil {
			return err
		}
		shipping, err := repository.shipping.QuoteInTransaction(ctx, tx, prepared.StoreID)
		if errors.Is(err, shippingapplication.ErrNotFound) {
			return application.ErrUnprocessable
		}
		if err != nil {
			return err
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		orderNumber, err := allocateOrderNumber(tx, prepared.StoreID)
		if err != nil {
			return err
		}
		rows, lines, subtotal, allocations, err := buildLines(prepared.StoreID, prepared.OrderID, cart.Lines, variants, prices, now)
		if err != nil {
			return err
		}
		grand, err := domain.CheckedAdd(subtotal.AmountMinor, shipping.Price.AmountMinor)
		if err != nil {
			return err
		}
		grandTotal, _ := money.New(grand, "VND")
		row := newOrderRow(prepared, cart.ID, orderNumber, customerID, contact, shipping, subtotal, grandTotal, now)
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
		for i := range rows {
			if err := tx.Create(&rows[i]).Error; err != nil {
				return classify(err)
			}
		}
		if err := tx.Create(toAddressRow(prepared.StoreID, prepared.OrderID, address, now)).Error; err != nil {
			return classify(err)
		}
		payment, err := repository.payments.CreatePendingInTransaction(ctx, tx, prepared.StoreID, prepared.PaymentID, prepared.OrderID, prepared.PaymentProvider, grandTotal, now)
		if err != nil {
			return err
		}
		reservations, err := repository.inventory.ReserveInTransaction(ctx, tx, prepared.StoreID, prepared.OrderID, allocations, prepared.CorrelationID)
		if errors.Is(err, inventorydomain.ErrInsufficientStock) {
			return application.ErrUnprocessable
		}
		if err != nil {
			return mapParticipantError(err)
		}
		if len(reservations) != len(allocations) {
			return errors.New("inventory returned incomplete reservations")
		}
		expires := reservations[0].ExpiresAt
		if err := tx.Model(&orderRow{}).Where("store_id=? AND order_id=?", prepared.StoreID, prepared.OrderID).Update("reservation_expires_at", expires).Error; err != nil {
			return err
		}
		row.ReservationExpiresAt = expires
		if err := repository.carts.ConvertForCheckout(ctx, tx, prepared.StoreID, cart.ID, prepared.OrderID, prepared.CartVersion, now); err != nil {
			return mapParticipantError(err)
		}
		if err := tx.Create(&historyRow{StoreID: prepared.StoreID.String(), HistoryID: prepared.HistoryID.String(), OrderID: prepared.OrderID.String(), ToState: string(domain.StatePendingPayment), Reason: "checkout", OccurredAt: now}).Error; err != nil {
			return classify(err)
		}
		if prepared.Owner.Kind == domain.OwnerGuest {
			grant := grantRow{StoreID: prepared.StoreID.String(), TokenDigest: prepared.GrantDigest[:], OrderID: prepared.OrderID.String(), ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now}
			if err := tx.Create(&grant).Error; err != nil {
				return classify(err)
			}
		}
		idempotency := idempotencyRow{StoreID: prepared.StoreID.String(), IdempotencyKey: prepared.IdempotencyKey, RequestFingerprint: prepared.Fingerprint[:], OrderID: prepared.OrderID.String(), CreatedAt: now}
		if err := tx.Create(&idempotency).Error; err != nil {
			return classify(err)
		}
		ownerPayload := map[string]any{"store_id": prepared.StoreID.String(), "order_id": prepared.OrderID.String(), "owner_kind": prepared.Owner.Kind}
		if prepared.Owner.Kind == domain.OwnerUser {
			ownerPayload["customer_id"] = customerID.String()
		} else {
			ownerPayload["guest_id"] = prepared.Owner.GuestID.String()
		}
		if err := repository.appendEvent(ctx, tx, prepared.StoreID, prepared.OrderID, "order.created.v1", 1, now, prepared.CorrelationID, ownerPayload); err != nil {
			return err
		}
		result = domain.Order{StoreID: prepared.StoreID, ID: prepared.OrderID, Number: orderNumber, Owner: prepared.Owner, CartID: cart.ID, Currency: "VND", Subtotal: subtotal, ShippingTotal: shipping.Price, GrandTotal: grandTotal, TaxPresentation: "vat_inclusive", Contact: contact, DeliveryAddress: address, Shipping: domain.ShippingSnapshot{RuleID: shipping.ID, RuleVersion: shipping.Version, Mode: shipping.Mode, Price: shipping.Price}, State: domain.StatePendingPayment, Version: 1, ReservationExpiresAt: expires, CreatedAt: now, UpdatedAt: now, Lines: lines, Payment: toOrderPayment(payment)}
		created = true
		return nil
	})
	return result, created, err
}

func (repository *Repository) GetForUser(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, userID ids.UserID) (domain.Order, error) {
	var result domain.Order
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&orderRow{}).Where("store_id=? AND order_id=? AND owner_kind='user' AND user_id=?", storeID, orderID, userID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return application.ErrNotFound
		}
		var err error
		result, err = repository.load(tx, storeID, orderID)
		return err
	})
	return result, err
}

func (repository *Repository) GetForGrant(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, digest [32]byte) (domain.Order, error) {
	var result domain.Order
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var grant grantRow
		err := tx.Where("store_id=? AND order_id=? AND token_digest=? AND revoked_at IS NULL AND expires_at>clock_timestamp()", storeID, orderID, digest[:]).First(&grant).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		}
		if err != nil {
			return err
		}
		result, err = repository.load(tx, storeID, orderID)
		return err
	})
	return result, err
}

func (repository *Repository) ExpireDue(ctx context.Context, limit int, correlationID string) (int, error) {
	type candidate struct{ StoreID, OrderID string }
	var candidates []candidate
	if err := repository.db.WithContext(ctx).Raw("SELECT store_id,order_id FROM orders WHERE state='pending_payment' AND reservation_expires_at<=clock_timestamp() ORDER BY reservation_expires_at,store_id,order_id LIMIT ?", limit).Scan(&candidates).Error; err != nil {
		return 0, err
	}
	processed := 0
	for _, candidate := range candidates {
		changed := false
		err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row orderRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("store_id=? AND order_id=? AND state='pending_payment'", candidate.StoreID, candidate.OrderID).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			now, err := databaseTime(tx)
			if err != nil || row.ReservationExpiresAt.After(now) {
				return err
			}
			storeID, orderID := ids.StoreID(row.StoreID), ids.OrderID(row.OrderID)
			if !domain.CanTransition(domain.State(row.State), domain.StateExpired) {
				return application.ErrConflict
			}
			payment, err := repository.payments.ExpireInTransaction(ctx, tx, storeID, orderID, now)
			if err != nil {
				return err
			}
			if payment.State != "expired" {
				return application.ErrConflict
			}
			expired, err := repository.inventory.ExpireOrderInTransaction(ctx, tx, storeID, orderID, now, correlationID)
			if err != nil {
				return err
			}
			if !expired {
				return application.ErrConflict
			}
			result := tx.Model(&orderRow{}).Where("store_id=? AND order_id=? AND state='pending_payment' AND version=?", storeID, orderID, row.Version).Updates(map[string]any{"state": domain.StateExpired, "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
			if result.Error != nil || result.RowsAffected != 1 {
				if result.Error != nil {
					return result.Error
				}
				return application.ErrConflict
			}
			from := string(domain.StatePendingPayment)
			historyID, err := ids.NewOrderHistoryID()
			if err != nil {
				return err
			}
			if err := tx.Create(&historyRow{StoreID: storeID.String(), HistoryID: historyID.String(), OrderID: orderID.String(), FromState: &from, ToState: string(domain.StateExpired), Reason: "reservation_expired", OccurredAt: now}).Error; err != nil {
				return err
			}
			if err := repository.appendEvent(ctx, tx, storeID, orderID, "order.expired.v1", row.Version+1, now, correlationID, map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "reason": "reservation_expired"}); err != nil {
				return err
			}
			changed = true
			return nil
		})
		if err != nil {
			return processed, err
		}
		if changed {
			processed++
		}
	}
	return processed, nil
}

func (repository *Repository) resolveCustomer(ctx context.Context, tx *gorm.DB, prepared application.PreparedCheckout) (domain.Contact, domain.Address, ids.CustomerID, error) {
	if prepared.Owner.Kind == domain.OwnerGuest {
		return prepared.GuestContact, prepared.GuestAddress, "", nil
	}
	profile, address, err := repository.customers.ResolveCheckoutAddress(ctx, tx, prepared.StoreID, prepared.Owner.UserID, prepared.AddressID)
	if errors.Is(err, customersapplication.ErrNotFound) {
		return domain.Contact{}, domain.Address{}, "", application.ErrUnprocessable
	}
	if err != nil {
		return domain.Contact{}, domain.Address{}, "", err
	}
	contact, err := domain.ValidateContact(domain.Contact{Name: profile.FullName, Email: profile.Email, Phone: profile.Phone})
	if err != nil {
		return domain.Contact{}, domain.Address{}, "", err
	}
	value, err := domain.ValidateAddress(domain.Address{RecipientName: address.RecipientName, Phone: address.Phone, Line1: address.Line1, Line2: address.Line2, Ward: address.Ward, District: address.District, Province: address.Province, PostalCode: address.PostalCode, CountryCode: address.CountryCode})
	return contact, value, profile.ID, err
}

func buildLines(storeID ids.StoreID, orderID ids.OrderID, cartLines []cartsdomain.Line, variants map[ids.VariantID]catalogapplication.CartVariant, prices map[ids.VariantID]money.Money, now time.Time) ([]lineRow, []domain.Line, money.Money, []inventorydomain.Allocation, error) {
	rows := make([]lineRow, 0, len(cartLines))
	lines := make([]domain.Line, 0, len(cartLines))
	allocations := make([]inventorydomain.Allocation, 0, len(cartLines))
	subtotal := int64(0)
	for _, cartLine := range cartLines {
		variant, ok := variants[cartLine.VariantID]
		price, priced := prices[cartLine.VariantID]
		if !ok || !priced || price.Currency != "VND" {
			return nil, nil, money.Money{}, nil, application.ErrUnprocessable
		}
		total, err := domain.CheckedLineTotal(price.AmountMinor, cartLine.Quantity)
		if err != nil {
			return nil, nil, money.Money{}, nil, application.ErrUnprocessable
		}
		subtotal, err = domain.CheckedAdd(subtotal, total)
		if err != nil {
			return nil, nil, money.Money{}, nil, application.ErrUnprocessable
		}
		lineID, err := ids.NewOrderLineID()
		if err != nil {
			return nil, nil, money.Money{}, nil, err
		}
		options, err := json.Marshal(variant.OptionValues)
		if err != nil {
			return nil, nil, money.Money{}, nil, err
		}
		lineTotal, _ := money.New(total, "VND")
		rows = append(rows, lineRow{StoreID: storeID.String(), OrderID: orderID.String(), LineID: lineID.String(), VariantID: cartLine.VariantID.String(), ProductName: variant.ProductName, SKU: variant.SKU, OptionValues: options, Quantity: cartLine.Quantity, UnitPriceMinor: price.AmountMinor, LineTotalMinor: total, Currency: "VND", TaxPresentation: "vat_inclusive", CreatedAt: now})
		lines = append(lines, domain.Line{ID: lineID, VariantID: cartLine.VariantID, ProductName: variant.ProductName, SKU: variant.SKU, OptionValues: variant.OptionValues, Quantity: cartLine.Quantity, UnitPrice: price, LineTotal: lineTotal, TaxPresentation: "vat_inclusive"})
		allocations = append(allocations, inventorydomain.Allocation{VariantID: cartLine.VariantID, Quantity: cartLine.Quantity})
	}
	value, _ := money.New(subtotal, "VND")
	return rows, lines, value, allocations, nil
}

func newOrderRow(prepared application.PreparedCheckout, cartID ids.CartID, number string, customerID ids.CustomerID, contact domain.Contact, shipping shippingdomain.Rule, subtotal, grand money.Money, now time.Time) orderRow {
	row := orderRow{StoreID: prepared.StoreID.String(), OrderID: prepared.OrderID.String(), OrderNumber: number, OwnerKind: string(prepared.Owner.Kind), CartID: cartID.String(), Currency: "VND", SubtotalMinor: subtotal.AmountMinor, ShippingTotalMinor: shipping.Price.AmountMinor, GrandTotalMinor: grand.AmountMinor, TaxPresentation: "vat_inclusive", ContactName: contact.Name, ContactEmail: contact.Email, ContactPhone: contact.Phone, ShippingMode: shipping.Mode, ShippingRuleID: shipping.ID.String(), ShippingRuleVersion: shipping.Version, State: string(domain.StatePendingPayment), Version: 1, ReservationExpiresAt: now.Add(inventorydomain.ReservationTTL), CreatedAt: now, UpdatedAt: now}
	if prepared.Owner.Kind == domain.OwnerUser {
		user, customer := prepared.Owner.UserID.String(), customerID.String()
		row.UserID, row.CustomerID = &user, &customer
	} else {
		guest := prepared.Owner.GuestID.String()
		row.GuestID = &guest
	}
	return row
}

func toAddressRow(storeID ids.StoreID, orderID ids.OrderID, value domain.Address, now time.Time) *addressRow {
	return &addressRow{StoreID: storeID.String(), OrderID: orderID.String(), AddressType: "delivery", RecipientName: value.RecipientName, Phone: value.Phone, Line1: value.Line1, Line2: value.Line2, Ward: value.Ward, District: value.District, Province: value.Province, PostalCode: value.PostalCode, CountryCode: value.CountryCode, CreatedAt: now}
}

func allocateOrderNumber(tx *gorm.DB, storeID ids.StoreID) (string, error) {
	var sequence int64
	err := tx.Raw(`INSERT INTO order_number_counters(store_id,next_value) VALUES(?,2) ON CONFLICT(store_id) DO UPDATE SET next_value=order_number_counters.next_value+1 RETURNING next_value-1`, storeID).Scan(&sequence).Error
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ORD-%012d", sequence), nil
}

func (repository *Repository) appendEvent(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, eventType string, version int64, now time.Time, correlationID string, payload map[string]any) error {
	eventID, err := ids.NewEventID()
	if err != nil {
		return err
	}
	event, err := eventdomain.New(storeID, eventID, eventType, "order", orderID.String(), version, now, correlationID, payload)
	if err != nil {
		return err
	}
	return repository.events.Append(ctx, tx, event)
}

func (repository *Repository) load(tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (domain.Order, error) {
	var row orderRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND order_id=?", storeID, orderID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Order{}, application.ErrNotFound
	} else if err != nil {
		return domain.Order{}, err
	}
	var rawLines []lineRow
	if err := tx.Where("store_id=? AND order_id=?", storeID, orderID).Order("line_id").Find(&rawLines).Error; err != nil {
		return domain.Order{}, err
	}
	var rawAddress addressRow
	if err := tx.Where("store_id=? AND order_id=? AND address_type='delivery'", storeID, orderID).First(&rawAddress).Error; err != nil {
		return domain.Order{}, err
	}
	payment, err := repository.payments.LoadInTransaction(tx.Statement.Context, tx, storeID, orderID)
	if err != nil {
		return domain.Order{}, err
	}
	return mapOrder(row, rawLines, rawAddress, payment)
}

func mapOrder(row orderRow, rows []lineRow, address addressRow, payment paymentsdomain.Payment) (domain.Order, error) {
	owner := domain.Owner{Kind: domain.OwnerKind(row.OwnerKind)}
	if row.UserID != nil {
		owner.UserID = ids.UserID(*row.UserID)
	}
	if row.GuestID != nil {
		owner.GuestID = ids.GuestID(*row.GuestID)
	}
	if row.CustomerID != nil {
		owner.CustomerID = ids.CustomerID(*row.CustomerID)
	}
	subtotal, err := money.New(row.SubtotalMinor, row.Currency)
	if err != nil {
		return domain.Order{}, err
	}
	shippingPrice, err := money.New(row.ShippingTotalMinor, row.Currency)
	if err != nil {
		return domain.Order{}, err
	}
	grand, err := money.New(row.GrandTotalMinor, row.Currency)
	if err != nil {
		return domain.Order{}, err
	}
	lines := make([]domain.Line, len(rows))
	for i, value := range rows {
		options := map[string]string{}
		if err := json.Unmarshal(value.OptionValues, &options); err != nil {
			return domain.Order{}, err
		}
		unit, _ := money.New(value.UnitPriceMinor, value.Currency)
		total, _ := money.New(value.LineTotalMinor, value.Currency)
		lines[i] = domain.Line{ID: ids.OrderLineID(value.LineID), VariantID: ids.VariantID(value.VariantID), ProductName: value.ProductName, SKU: value.SKU, OptionValues: options, Quantity: value.Quantity, UnitPrice: unit, LineTotal: total, TaxPresentation: value.TaxPresentation}
	}
	return domain.Order{StoreID: ids.StoreID(row.StoreID), ID: ids.OrderID(row.OrderID), Number: row.OrderNumber, Owner: owner, CartID: ids.CartID(row.CartID), Currency: row.Currency, Subtotal: subtotal, ShippingTotal: shippingPrice, GrandTotal: grand, TaxPresentation: row.TaxPresentation, Contact: domain.Contact{Name: row.ContactName, Email: row.ContactEmail, Phone: row.ContactPhone}, DeliveryAddress: domain.Address{RecipientName: address.RecipientName, Phone: address.Phone, Line1: address.Line1, Line2: address.Line2, Ward: address.Ward, District: address.District, Province: address.Province, PostalCode: address.PostalCode, CountryCode: address.CountryCode}, Shipping: domain.ShippingSnapshot{RuleID: ids.ShippingRuleID(row.ShippingRuleID), RuleVersion: row.ShippingRuleVersion, Mode: row.ShippingMode, Price: shippingPrice}, State: domain.State(row.State), Version: row.Version, ReservationExpiresAt: row.ReservationExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ClosedAt: row.ClosedAt, Lines: lines, Payment: toOrderPayment(payment)}, nil
}

func toOrderPayment(value paymentsdomain.Payment) domain.Payment {
	return domain.Payment{ID: value.ID, Provider: value.Provider, Amount: value.Amount, State: value.State, Version: value.Version}
}

func databaseTime(tx *gorm.DB) (time.Time, error) {
	var value time.Time
	err := tx.Raw("SELECT clock_timestamp()").Scan(&value).Error
	return value.UTC(), err
}

func mapParticipantError(err error) error {
	if errors.Is(err, cartsapplication.ErrNotFound) {
		return application.ErrNotFound
	}
	if errors.Is(err, cartsapplication.ErrConflict) {
		return application.ErrConflict
	}
	return err
}

type stateError interface{ SQLState() string }

func classify(err error) error {
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23514" || state.SQLState() == "23503") {
		return application.ErrConflict
	}
	return err
}
