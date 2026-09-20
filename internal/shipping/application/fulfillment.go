package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
)

var ErrForbidden = errors.New("fulfillment forbidden")
var ErrUnprocessable = errors.New("fulfillment transition unavailable")

type Mutation string

const (
	MutationCreate   Mutation = "create"
	MutationUpdate   Mutation = "update"
	MutationDispatch Mutation = "dispatch"
	MutationComplete Mutation = "complete"
)

type Command struct {
	StoreID         ids.StoreID
	OrderID         ids.OrderID
	ShipmentID      ids.ShipmentID
	ActorID         ids.UserID
	Mutation        Mutation
	Details         domain.Details
	ExpectedVersion int64
	IdempotencyKey  string
	RequestID       string
}

type OrderLock struct {
	State        ordersdomain.State
	Version      int64
	PaymentState string
}

type RepositoryFulfillment interface {
	Mutate(context.Context, Command, [32]byte) (domain.Shipment, bool, error)
	GetOrder(context.Context, ids.StoreID, ids.OrderID) (ordersdomain.Order, error)
	ListOrders(context.Context, ids.StoreID, ordersdomain.State, int, string) ([]ordersdomain.Order, string, error)
}

type FulfillmentService struct{ repository RepositoryFulfillment }

func NewFulfillmentService(repository RepositoryFulfillment) FulfillmentService {
	return FulfillmentService{repository: repository}
}

func (service FulfillmentService) Mutate(ctx context.Context, command Command) (domain.Shipment, bool, error) {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	if command.StoreID == "" || command.ActorID == "" || command.IdempotencyKey == "" || len(command.IdempotencyKey) > 128 || command.RequestID == "" || len(command.RequestID) > 200 {
		return domain.Shipment{}, false, domain.ErrInvalidShipment
	}
	switch command.Mutation {
	case MutationCreate:
		if command.OrderID == "" || command.ShipmentID != "" || command.ExpectedVersion != 0 {
			return domain.Shipment{}, false, domain.ErrInvalidShipment
		}
		details, err := domain.ValidateDetails(command.Details)
		if err != nil {
			return domain.Shipment{}, false, err
		}
		command.Details = details
	case MutationUpdate:
		if command.ShipmentID == "" || command.OrderID != "" || command.ExpectedVersion < 1 {
			return domain.Shipment{}, false, domain.ErrInvalidShipment
		}
		details, err := domain.ValidateDetails(command.Details)
		if err != nil {
			return domain.Shipment{}, false, err
		}
		command.Details = details
	case MutationDispatch, MutationComplete:
		if command.ShipmentID == "" || command.OrderID != "" || command.ExpectedVersion < 1 || command.Details != (domain.Details{}) {
			return domain.Shipment{}, false, domain.ErrInvalidShipment
		}
	default:
		return domain.Shipment{}, false, domain.ErrInvalidShipment
	}
	raw, err := json.Marshal(struct {
		OrderID         ids.OrderID
		ShipmentID      ids.ShipmentID
		Mutation        Mutation
		Details         domain.Details
		ExpectedVersion int64
	}{command.OrderID, command.ShipmentID, command.Mutation, command.Details, command.ExpectedVersion})
	if err != nil {
		return domain.Shipment{}, false, err
	}
	return service.repository.Mutate(ctx, command, sha256.Sum256(raw))
}

func (service FulfillmentService) GetOrder(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID) (ordersdomain.Order, error) {
	if storeID == "" || orderID == "" {
		return ordersdomain.Order{}, domain.ErrInvalidShipment
	}
	return service.repository.GetOrder(ctx, storeID, orderID)
}

func (service FulfillmentService) ListOrders(ctx context.Context, storeID ids.StoreID, state string, limit int, cursor string) ([]ordersdomain.Order, string, error) {
	parsed := ordersdomain.State(state)
	if storeID == "" || limit < 1 || limit > 100 || len(cursor) > 512 || (state != "" && parsed != ordersdomain.StatePaid && parsed != ordersdomain.StateProcessing && parsed != ordersdomain.StateShipped) {
		return nil, "", domain.ErrInvalidShipment
	}
	return service.repository.ListOrders(ctx, storeID, parsed, limit, cursor)
}
