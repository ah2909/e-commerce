package domain

import (
	"errors"
	"math"
	"net/mail"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var ErrInvalidOrder = errors.New("invalid order")
var ErrInvalidTransition = errors.New("invalid order transition")

type OwnerKind string
type State string

const (
	OwnerUser  OwnerKind = "user"
	OwnerGuest OwnerKind = "guest"

	StatePendingPayment        State = "pending_payment"
	StatePaid                  State = "paid"
	StateProcessing            State = "processing"
	StateShipped               State = "shipped"
	StateCompleted             State = "completed"
	StateExpired               State = "expired"
	StateCancellationRequested State = "cancellation_requested"
	StateRefundPending         State = "refund_pending"
	StateCancelled             State = "cancelled"
)

type Owner struct {
	Kind       OwnerKind
	UserID     ids.UserID
	GuestID    ids.GuestID
	CustomerID ids.CustomerID
}

type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

type Address struct {
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2"`
	Ward          string `json:"ward"`
	District      string `json:"district"`
	Province      string `json:"province"`
	PostalCode    string `json:"postal_code"`
	CountryCode   string `json:"country_code"`
}

type Line struct {
	ID              ids.OrderLineID   `json:"line_id"`
	VariantID       ids.VariantID     `json:"variant_id"`
	ProductName     string            `json:"product_name"`
	SKU             string            `json:"sku"`
	OptionValues    map[string]string `json:"option_values"`
	Quantity        int64             `json:"quantity"`
	UnitPrice       money.Money       `json:"unit_price"`
	LineTotal       money.Money       `json:"line_total"`
	TaxPresentation string            `json:"tax_presentation"`
}

type ShippingSnapshot struct {
	RuleID      ids.ShippingRuleID `json:"rule_id"`
	RuleVersion int64              `json:"rule_version"`
	Mode        string             `json:"mode"`
	Price       money.Money        `json:"price"`
}

type Payment struct {
	ID       ids.PaymentID `json:"payment_id"`
	Provider string        `json:"provider"`
	Amount   money.Money   `json:"amount"`
	State    string        `json:"state"`
	Version  int64         `json:"version"`
}

type Shipment struct {
	ID             ids.ShipmentID `json:"shipment_id"`
	OrderID        ids.OrderID    `json:"order_id"`
	State          string         `json:"state"`
	Carrier        string         `json:"carrier"`
	TrackingNumber string         `json:"tracking_number"`
	Version        int64          `json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ShippedAt      *time.Time     `json:"shipped_at"`
	CompletedAt    *time.Time     `json:"completed_at"`
}

type Order struct {
	StoreID              ids.StoreID      `json:"-"`
	ID                   ids.OrderID      `json:"order_id"`
	Number               string           `json:"order_number"`
	Owner                Owner            `json:"-"`
	CartID               ids.CartID       `json:"cart_id"`
	Currency             string           `json:"currency"`
	Subtotal             money.Money      `json:"subtotal"`
	ShippingTotal        money.Money      `json:"shipping_total"`
	GrandTotal           money.Money      `json:"grand_total"`
	TaxPresentation      string           `json:"tax_presentation"`
	Contact              Contact          `json:"contact"`
	DeliveryAddress      Address          `json:"delivery_address"`
	Shipping             ShippingSnapshot `json:"shipping"`
	State                State            `json:"state"`
	Version              int64            `json:"version"`
	ReservationExpiresAt time.Time        `json:"reservation_expires_at"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
	ClosedAt             *time.Time       `json:"closed_at"`
	Lines                []Line           `json:"lines"`
	Payment              Payment          `json:"payment"`
	Shipment             *Shipment        `json:"shipment"`
}

func UserOwner(userID ids.UserID) Owner    { return Owner{Kind: OwnerUser, UserID: userID} }
func GuestOwner(guestID ids.GuestID) Owner { return Owner{Kind: OwnerGuest, GuestID: guestID} }

func ValidateOwner(owner Owner) error {
	if owner.Kind == OwnerUser && owner.UserID != "" && owner.GuestID == "" {
		return nil
	}
	if owner.Kind == OwnerGuest && owner.GuestID != "" && owner.UserID == "" && owner.CustomerID == "" {
		return nil
	}
	return ErrInvalidOrder
}

func ValidateContact(value Contact) (Contact, error) {
	value.Name = strings.TrimSpace(value.Name)
	value.Email = strings.TrimSpace(value.Email)
	value.Phone = strings.TrimSpace(value.Phone)
	parsed, err := mail.ParseAddress(value.Email)
	if !bounded(value.Name, 1, 160) || !bounded(value.Email, 3, 254) || !bounded(value.Phone, 8, 20) || !validPhone(value.Phone) || err != nil || parsed.Name != "" || parsed.Address != value.Email {
		return Contact{}, ErrInvalidOrder
	}
	return value, nil
}

func ValidateAddress(value Address) (Address, error) {
	value.RecipientName = strings.TrimSpace(value.RecipientName)
	value.Phone = strings.TrimSpace(value.Phone)
	value.Line1 = strings.TrimSpace(value.Line1)
	value.Line2 = strings.TrimSpace(value.Line2)
	value.Ward = strings.TrimSpace(value.Ward)
	value.District = strings.TrimSpace(value.District)
	value.Province = strings.TrimSpace(value.Province)
	value.PostalCode = strings.TrimSpace(value.PostalCode)
	value.CountryCode = strings.ToUpper(strings.TrimSpace(value.CountryCode))
	if !bounded(value.RecipientName, 1, 160) || !bounded(value.Phone, 8, 20) || !validPhone(value.Phone) || !bounded(value.Line1, 1, 240) || !bounded(value.Line2, 0, 240) || !bounded(value.Ward, 1, 120) || !bounded(value.District, 0, 120) || !bounded(value.Province, 1, 120) || !bounded(value.PostalCode, 0, 20) || value.CountryCode != "VN" {
		return Address{}, ErrInvalidOrder
	}
	return value, nil
}

func CanTransition(from, to State) bool {
	switch from {
	case StatePendingPayment:
		return to == StatePaid || to == StateExpired || to == StateCancelled
	case StatePaid:
		return to == StateProcessing || to == StateCancellationRequested
	case StateProcessing:
		return to == StateShipped || to == StateCancellationRequested
	case StateShipped:
		return to == StateCompleted
	case StateCancellationRequested:
		return to == StateRefundPending
	case StateRefundPending:
		return to == StateCancelled
	default:
		return false
	}
}

func CanShopperCancel(state State) bool { return state == StatePendingPayment }

func CanWorkforceCancel(state State) bool {
	return state == StatePendingPayment || state == StatePaid || state == StateProcessing
}

func CheckedLineTotal(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 1 || quantity > 2147483647 || (unitPrice != 0 && quantity > math.MaxInt64/unitPrice) {
		return 0, ErrInvalidOrder
	}
	return unitPrice * quantity, nil
}

func CheckedAdd(left, right int64) (int64, error) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, ErrInvalidOrder
	}
	return left + right, nil
}

func bounded(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum
}

func validPhone(value string) bool {
	compact := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(value)
	if strings.HasPrefix(compact, "0") {
		compact = "+84" + strings.TrimPrefix(compact, "0")
	}
	if !strings.HasPrefix(compact, "+") || len(compact) < 9 || len(compact) > 16 || compact[1] < '1' || compact[1] > '9' {
		return false
	}
	for _, char := range compact[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
