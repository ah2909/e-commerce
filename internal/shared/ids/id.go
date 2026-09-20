package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalid = errors.New("invalid identifier")

type StoreID string
type UserID string
type GuestID string
type ProductID string
type VariantID string
type CategoryID string
type MediaID string
type PriceListID string
type EventID string
type ReservationID string
type LedgerEntryID string
type OrderID string
type CustomerID string
type AddressID string
type CartID string
type OrderLineID string
type OrderHistoryID string
type PaymentID string
type PaymentAttemptID string
type ReconciliationID string
type ShippingRuleID string
type RefundID string
type CancellationID string
type ShipmentID string

func ParseStoreID(value string) (StoreID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse store ID: %w", err)
	}
	return StoreID(value), nil
}

func ParseUserID(value string) (UserID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse user ID: %w", err)
	}
	return UserID(value), nil
}

func ParseGuestID(value string) (GuestID, error) {
	value, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse guest ID: %w", err)
	}
	return GuestID(value), nil
}

func ParseProductID(value string) (ProductID, error) {
	return parseTypedID[ProductID](value, "product")
}
func ParseVariantID(value string) (VariantID, error) {
	return parseTypedID[VariantID](value, "variant")
}
func ParseCategoryID(value string) (CategoryID, error) {
	return parseTypedID[CategoryID](value, "category")
}
func ParseMediaID(value string) (MediaID, error) { return parseTypedID[MediaID](value, "media") }
func ParsePriceListID(value string) (PriceListID, error) {
	return parseTypedID[PriceListID](value, "price list")
}
func ParseEventID(value string) (EventID, error) { return parseTypedID[EventID](value, "event") }
func ParseReservationID(value string) (ReservationID, error) {
	return parseTypedID[ReservationID](value, "reservation")
}
func ParseLedgerEntryID(value string) (LedgerEntryID, error) {
	return parseTypedID[LedgerEntryID](value, "ledger entry")
}
func ParseOrderID(value string) (OrderID, error) { return parseTypedID[OrderID](value, "order") }
func ParseCustomerID(value string) (CustomerID, error) {
	return parseTypedID[CustomerID](value, "customer")
}
func ParseAddressID(value string) (AddressID, error) {
	return parseTypedID[AddressID](value, "address")
}
func ParseCartID(value string) (CartID, error) { return parseTypedID[CartID](value, "cart") }
func ParsePaymentID(value string) (PaymentID, error) {
	return parseTypedID[PaymentID](value, "payment")
}
func ParsePaymentAttemptID(value string) (PaymentAttemptID, error) {
	return parseTypedID[PaymentAttemptID](value, "payment attempt")
}
func ParseReconciliationID(value string) (ReconciliationID, error) {
	return parseTypedID[ReconciliationID](value, "reconciliation")
}
func ParseShippingRuleID(value string) (ShippingRuleID, error) {
	return parseTypedID[ShippingRuleID](value, "shipping rule")
}
func ParseRefundID(value string) (RefundID, error) { return parseTypedID[RefundID](value, "refund") }
func ParseCancellationID(value string) (CancellationID, error) {
	return parseTypedID[CancellationID](value, "cancellation")
}
func ParseShipmentID(value string) (ShipmentID, error) {
	return parseTypedID[ShipmentID](value, "shipment")
}

func NewStoreID() (StoreID, error)         { value, err := newUUID(); return StoreID(value), err }
func NewUserID() (UserID, error)           { value, err := newUUID(); return UserID(value), err }
func NewGuestID() (GuestID, error)         { value, err := newUUID(); return GuestID(value), err }
func NewProductID() (ProductID, error)     { value, err := newUUID(); return ProductID(value), err }
func NewVariantID() (VariantID, error)     { value, err := newUUID(); return VariantID(value), err }
func NewCategoryID() (CategoryID, error)   { value, err := newUUID(); return CategoryID(value), err }
func NewMediaID() (MediaID, error)         { value, err := newUUID(); return MediaID(value), err }
func NewPriceListID() (PriceListID, error) { value, err := newUUID(); return PriceListID(value), err }
func NewEventID() (EventID, error)         { value, err := newUUID(); return EventID(value), err }
func NewReservationID() (ReservationID, error) {
	value, err := newUUID()
	return ReservationID(value), err
}
func NewLedgerEntryID() (LedgerEntryID, error) {
	value, err := newUUID()
	return LedgerEntryID(value), err
}
func NewOrderID() (OrderID, error)         { value, err := newUUID(); return OrderID(value), err }
func NewCustomerID() (CustomerID, error)   { value, err := newUUID(); return CustomerID(value), err }
func NewAddressID() (AddressID, error)     { value, err := newUUID(); return AddressID(value), err }
func NewCartID() (CartID, error)           { value, err := newUUID(); return CartID(value), err }
func NewOrderLineID() (OrderLineID, error) { value, err := newUUID(); return OrderLineID(value), err }
func NewOrderHistoryID() (OrderHistoryID, error) {
	value, err := newUUID()
	return OrderHistoryID(value), err
}
func NewPaymentID() (PaymentID, error) { value, err := newUUID(); return PaymentID(value), err }
func NewPaymentAttemptID() (PaymentAttemptID, error) {
	value, err := newUUID()
	return PaymentAttemptID(value), err
}
func NewReconciliationID() (ReconciliationID, error) {
	value, err := newUUID()
	return ReconciliationID(value), err
}
func NewShippingRuleID() (ShippingRuleID, error) {
	value, err := newUUID()
	return ShippingRuleID(value), err
}
func NewRefundID() (RefundID, error) { value, err := newUUID(); return RefundID(value), err }
func NewCancellationID() (CancellationID, error) {
	value, err := newUUID()
	return CancellationID(value), err
}
func NewShipmentID() (ShipmentID, error) { value, err := newUUID(); return ShipmentID(value), err }

func (id StoreID) String() string          { return string(id) }
func (id UserID) String() string           { return string(id) }
func (id GuestID) String() string          { return string(id) }
func (id ProductID) String() string        { return string(id) }
func (id VariantID) String() string        { return string(id) }
func (id CategoryID) String() string       { return string(id) }
func (id MediaID) String() string          { return string(id) }
func (id PriceListID) String() string      { return string(id) }
func (id EventID) String() string          { return string(id) }
func (id ReservationID) String() string    { return string(id) }
func (id LedgerEntryID) String() string    { return string(id) }
func (id OrderID) String() string          { return string(id) }
func (id CustomerID) String() string       { return string(id) }
func (id AddressID) String() string        { return string(id) }
func (id CartID) String() string           { return string(id) }
func (id OrderLineID) String() string      { return string(id) }
func (id OrderHistoryID) String() string   { return string(id) }
func (id PaymentID) String() string        { return string(id) }
func (id PaymentAttemptID) String() string { return string(id) }
func (id ReconciliationID) String() string { return string(id) }
func (id ShippingRuleID) String() string   { return string(id) }
func (id RefundID) String() string         { return string(id) }
func (id CancellationID) String() string   { return string(id) }
func (id ShipmentID) String() string       { return string(id) }

func parseTypedID[T ~string](value, name string) (T, error) {
	parsed, err := parseUUID(value)
	if err != nil {
		return "", fmt.Errorf("parse %s ID: %w", name, err)
	}
	return T(parsed), nil
}

func parseUUID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", ErrInvalid
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != 16 {
		return "", ErrInvalid
	}
	var nonzero bool
	for _, part := range decoded {
		nonzero = nonzero || part != 0
	}
	if !nonzero {
		return "", ErrInvalid
	}
	return value, nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
