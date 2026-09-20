package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrNotFound = errors.New("order not found")
var ErrConflict = errors.New("order conflict")
var ErrForbidden = errors.New("order operation forbidden")
var ErrUnprocessable = errors.New("checkout unavailable")

type CheckoutCommand struct {
	StoreID         ids.StoreID
	Owner           domain.Owner
	CartVersion     int64
	PaymentProvider string
	AddressID       ids.AddressID
	GuestContact    domain.Contact
	GuestAddress    domain.Address
	IdempotencyKey  string
	CorrelationID   string
}

type PreparedCheckout struct {
	CheckoutCommand
	OrderID     ids.OrderID
	PaymentID   ids.PaymentID
	HistoryID   ids.OrderHistoryID
	Fingerprint [32]byte
	GrantDigest [32]byte
}

type CheckoutResult struct {
	Order            domain.Order `json:"order"`
	OrderAccessToken string       `json:"order_access_token,omitempty"`
	Created          bool         `json:"-"`
}

type CancellationActor string

const (
	CancellationUser      CancellationActor = "user"
	CancellationGuest     CancellationActor = "guest"
	CancellationWorkforce CancellationActor = "workforce"
)

type CancellationCommand struct {
	StoreID        ids.StoreID
	OrderID        ids.OrderID
	ActorType      CancellationActor
	ActorID        string
	IdempotencyKey string
	Reason         string
	RequestID      string
}

type CancellationResult struct {
	OrderID  ids.OrderID  `json:"order_id"`
	State    domain.State `json:"state"`
	RefundID ids.RefundID `json:"refund_id,omitempty"`
	Created  bool         `json:"created"`
}

type Repository interface {
	Checkout(context.Context, PreparedCheckout) (domain.Order, bool, error)
	GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (domain.Order, error)
	GetForGrant(context.Context, ids.StoreID, ids.OrderID, [32]byte) (domain.Order, error)
	ExpireDue(context.Context, int, string) (int, error)
}

type NotificationContact struct {
	OrderID ids.OrderID
	Name    string
	Email   string
}

type NotificationResolver interface {
	ResolveNotificationContact(context.Context, ids.StoreID, ids.OrderID) (NotificationContact, error)
}

type CancellationRepository interface {
	Cancel(context.Context, CancellationCommand, [32]byte) (CancellationResult, error)
}

func (service Service) Cancel(ctx context.Context, command CancellationCommand) (CancellationResult, error) {
	command.Reason = strings.Join(strings.Fields(command.Reason), " ")
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.StoreID == "" || command.OrderID == "" || command.ActorID == "" || len(command.IdempotencyKey) < 1 || len(command.IdempotencyKey) > 128 || len(command.Reason) < 1 || len(command.Reason) > 240 || strings.TrimSpace(command.RequestID) == "" || (command.ActorType != CancellationUser && command.ActorType != CancellationGuest && command.ActorType != CancellationWorkforce) {
		return CancellationResult{}, domain.ErrInvalidOrder
	}
	fingerprint := sha256.Sum256([]byte(command.OrderID.String() + "\x00" + string(command.ActorType) + "\x00" + command.ActorID + "\x00" + command.Reason))
	repository, ok := service.repository.(CancellationRepository)
	if !ok {
		return CancellationResult{}, ErrUnprocessable
	}
	return repository.Cancel(ctx, command, fingerprint)
}

type Service struct{ repository Repository }

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) Checkout(ctx context.Context, command CheckoutCommand) (CheckoutResult, error) {
	if err := validateCommand(&command); err != nil {
		return CheckoutResult{}, err
	}
	fingerprint, err := requestFingerprint(command)
	if err != nil {
		return CheckoutResult{}, err
	}
	orderID, err := ids.NewOrderID()
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("generate order ID: %w", err)
	}
	paymentIDValue, err := newUUID()
	if err != nil {
		return CheckoutResult{}, err
	}
	historyIDValue, err := newUUID()
	if err != nil {
		return CheckoutResult{}, err
	}
	prepared := PreparedCheckout{CheckoutCommand: command, OrderID: orderID, PaymentID: ids.PaymentID(paymentIDValue), HistoryID: ids.OrderHistoryID(historyIDValue), Fingerprint: fingerprint}
	var token string
	if command.Owner.Kind == domain.OwnerGuest {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return CheckoutResult{}, fmt.Errorf("generate order grant: %w", err)
		}
		token = base64.RawURLEncoding.EncodeToString(raw)
		prepared.GrantDigest = sha256.Sum256([]byte(token))
	}
	order, created, err := service.repository.Checkout(ctx, prepared)
	if err != nil {
		return CheckoutResult{}, err
	}
	if !created {
		token = ""
	}
	return CheckoutResult{Order: order, OrderAccessToken: token, Created: created}, nil
}

func (service Service) GetForUser(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, userID ids.UserID) (domain.Order, error) {
	if storeID == "" || orderID == "" || userID == "" {
		return domain.Order{}, domain.ErrInvalidOrder
	}
	return service.repository.GetForUser(ctx, storeID, orderID, userID)
}

func (service Service) GetForGrant(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, token string) (domain.Order, error) {
	if storeID == "" || orderID == "" || len(token) < 32 || len(token) > 256 {
		return domain.Order{}, ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	return service.repository.GetForGrant(ctx, storeID, orderID, digest)
}

func (service Service) ExpireDue(ctx context.Context, limit int, correlationID string) (int, error) {
	if limit < 1 || limit > 500 || strings.TrimSpace(correlationID) == "" || len(correlationID) > 200 {
		return 0, domain.ErrInvalidOrder
	}
	return service.repository.ExpireDue(ctx, limit, correlationID)
}

func (service Service) ResolveNotificationContact(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID) (NotificationContact, error) {
	if storeID == "" || orderID == "" {
		return NotificationContact{}, domain.ErrInvalidOrder
	}
	resolver, ok := service.repository.(NotificationResolver)
	if !ok {
		return NotificationContact{}, ErrUnprocessable
	}
	return resolver.ResolveNotificationContact(ctx, storeID, orderID)
}

func validateCommand(command *CheckoutCommand) error {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.CorrelationID = strings.TrimSpace(command.CorrelationID)
	if command.StoreID == "" || domain.ValidateOwner(command.Owner) != nil || command.CartVersion < 1 || len(command.IdempotencyKey) < 1 || len(command.IdempotencyKey) > 128 || command.CorrelationID == "" || len(command.CorrelationID) > 200 || (command.PaymentProvider != "vietqr" && command.PaymentProvider != "momo") {
		return domain.ErrInvalidOrder
	}
	if command.Owner.Kind == domain.OwnerUser {
		if command.AddressID == "" || command.GuestContact != (domain.Contact{}) || command.GuestAddress != (domain.Address{}) {
			return domain.ErrInvalidOrder
		}
		return nil
	}
	contact, err := domain.ValidateContact(command.GuestContact)
	if err != nil {
		return err
	}
	address, err := domain.ValidateAddress(command.GuestAddress)
	if err != nil {
		return err
	}
	command.GuestContact, command.GuestAddress = contact, address
	return nil
}

func requestFingerprint(command CheckoutCommand) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Owner       domain.Owner
		CartVersion int64
		Provider    string
		AddressID   ids.AddressID
		Contact     domain.Contact
		Address     domain.Address
	}{command.Owner, command.CartVersion, command.PaymentProvider, command.AddressID, command.GuestContact, command.GuestAddress})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode checkout fingerprint: %w", err)
	}
	return sha256.Sum256(raw), nil
}

func newUUID() (string, error) {
	id, err := ids.NewOrderID()
	if err != nil {
		return "", fmt.Errorf("generate checkout ID: %w", err)
	}
	return id.String(), nil
}
