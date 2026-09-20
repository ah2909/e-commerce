package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

const MaxMessageBytes = 64 * 1024

type envelope struct {
	EventID          string         `json:"event_id"`
	EventType        string         `json:"event_type"`
	SchemaVersion    int            `json:"schema_version"`
	OccurredAt       time.Time      `json:"occurred_at"`
	StoreID          string         `json:"store_id"`
	AggregateType    string         `json:"aggregate_type"`
	AggregateID      string         `json:"aggregate_id"`
	AggregateVersion int64          `json:"aggregate_version"`
	CorrelationID    string         `json:"correlation_id"`
	Payload          map[string]any `json:"payload"`
}

type eventContract struct {
	topic         string
	aggregateType string
	identityField string
	required      []string
	optional      []string
}

func Encode(event eventdomain.Event) ([]byte, error) {
	if err := validateContract(event); err != nil {
		return nil, err
	}
	value := envelope{event.ID.String(), event.Type, event.SchemaVersion, event.OccurredAt.UTC(), event.StoreID.String(), event.AggregateType, event.AggregateID, event.AggregateVersion, event.CorrelationID, event.Payload}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode event envelope: %w", err)
	}
	if len(encoded) > MaxMessageBytes {
		return nil, ErrInvalidMessage
	}
	return encoded, nil
}

func Decode(raw []byte) (eventdomain.Event, error) {
	if len(raw) == 0 || len(raw) > MaxMessageBytes {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var value envelope
	if err := decoder.Decode(&value); err != nil {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || value.SchemaVersion != 1 || value.Payload == nil {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	storeID, err := ids.ParseStoreID(value.StoreID)
	if err != nil {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	eventID, err := ids.ParseEventID(value.EventID)
	if err != nil {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	event, err := eventdomain.New(storeID, eventID, value.EventType, value.AggregateType, value.AggregateID, value.AggregateVersion, value.OccurredAt, value.CorrelationID, value.Payload)
	if err != nil {
		return eventdomain.Event{}, ErrInvalidMessage
	}
	if err := validateContract(event); err != nil {
		return eventdomain.Event{}, err
	}
	return event, nil
}

func validateContract(event eventdomain.Event) error {
	contract, ok := eventContracts[event.Type]
	if !ok {
		return ErrUnsupported
	}
	if event.SchemaVersion != 1 || event.AggregateType != contract.aggregateType {
		return ErrInvalidMessage
	}
	if _, err := ids.ParseEventID(event.AggregateID); err != nil {
		return ErrInvalidMessage
	}
	allowed := make(map[string]struct{}, len(contract.required)+len(contract.optional))
	required := make(map[string]struct{}, len(contract.required))
	for _, field := range contract.required {
		allowed[field] = struct{}{}
		required[field] = struct{}{}
		if _, ok := event.Payload[field]; !ok {
			return ErrInvalidMessage
		}
	}
	for _, field := range contract.optional {
		allowed[field] = struct{}{}
	}
	for field := range event.Payload {
		if _, ok := allowed[field]; !ok {
			return ErrInvalidMessage
		}
	}
	if textField(event.Payload, "store_id") != event.StoreID.String() || textField(event.Payload, contract.identityField) != event.AggregateID {
		return ErrInvalidMessage
	}
	for field, value := range event.Payload {
		if strings.HasSuffix(field, "_id") && field != "store_id" {
			text, ok := value.(string)
			if !ok {
				return ErrInvalidMessage
			}
			if strings.TrimSpace(text) == "" {
				if _, required := required[field]; required {
					return ErrInvalidMessage
				}
				continue
			}
			if _, err := ids.ParseEventID(text); err != nil {
				return ErrInvalidMessage
			}
			continue
		}
		if field == "store_id" || field == "version" {
			continue
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 200 {
			return ErrInvalidMessage
		}
		if field == "expires_at" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return ErrInvalidMessage
			}
		}
	}
	if version, ok := event.Payload["version"]; ok {
		var number json.Number
		switch value := version.(type) {
		case json.Number:
			number = value
		case int64:
			number = json.Number(strconv.FormatInt(value, 10))
		case int:
			number = json.Number(strconv.Itoa(value))
		default:
			return ErrInvalidMessage
		}
		parsed, err := number.Int64()
		if err != nil || parsed != event.AggregateVersion {
			return ErrInvalidMessage
		}
	}
	if event.Type == "order.created.v1" {
		owner := textField(event.Payload, "owner_kind")
		customer, guest := textField(event.Payload, "customer_id"), textField(event.Payload, "guest_id")
		if (owner == "user" && (customer == "" || guest != "")) || (owner == "guest" && (guest == "" || customer != "")) || (owner != "user" && owner != "guest") {
			return ErrInvalidMessage
		}
	}
	return nil
}

func textField(payload map[string]any, field string) string {
	value, _ := payload[field].(string)
	return strings.TrimSpace(value)
}

func TopicFor(eventType string) (string, bool) {
	contract, ok := eventContracts[eventType]
	return contract.topic, ok
}

var eventContracts = map[string]eventContract{
	"product.published.v1":               {TopicCatalog, "product", "product_id", []string{"store_id", "product_id", "version"}, nil},
	"product.updated.v1":                 {TopicCatalog, "product", "product_id", []string{"store_id", "product_id", "version"}, nil},
	"product.archived.v1":                {TopicCatalog, "product", "product_id", []string{"store_id", "product_id", "version"}, nil},
	"price.changed.v1":                   {TopicCatalog, "price", "price_id", []string{"store_id", "price_id", "price_list_id", "variant_id", "version"}, nil},
	"inventory.changed.v1":               {TopicInventory, "inventory_stock_item", "variant_id", []string{"store_id", "variant_id", "ledger_entry_id", "reason"}, []string{"refund_id"}},
	"inventory.reserved.v1":              {TopicInventory, "inventory_reservation", "reservation_id", []string{"store_id", "reservation_id", "order_id", "variant_id", "expires_at"}, nil},
	"inventory.reservation_expired.v1":   {TopicInventory, "inventory_reservation", "reservation_id", []string{"store_id", "reservation_id", "order_id", "variant_id"}, []string{"reason"}},
	"inventory.reservation_released.v1":  {TopicInventory, "inventory_reservation", "reservation_id", []string{"store_id", "reservation_id", "order_id", "variant_id", "reason"}, nil},
	"inventory.reservation_confirmed.v1": {TopicInventory, "inventory_reservation", "reservation_id", []string{"store_id", "reservation_id", "order_id", "variant_id"}, []string{"reason"}},
	"order.created.v1":                   {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "owner_kind"}, []string{"customer_id", "guest_id"}},
	"order.expired.v1":                   {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "reason"}, nil},
	"order.paid.v1":                      {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "payment_id"}, nil},
	"order.cancellation_requested.v1":    {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "reason", "actor_type"}, nil},
	"order.cancelled.v1":                 {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "reason"}, []string{"actor_type", "refund_id"}},
	"order.shipped.v1":                   {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "shipment_id", "state"}, nil},
	"order.completed.v1":                 {TopicOrders, "order", "order_id", []string{"store_id", "order_id", "shipment_id", "state"}, nil},
	"payment.action_required.v1":         {TopicPayments, "payment_attempt", "attempt_id", []string{"store_id", "order_id", "payment_id", "attempt_id", "provider", "expires_at"}, nil},
	"payment.confirmed.v1":               {TopicPayments, "payment", "payment_id", []string{"store_id", "order_id", "payment_id", "attempt_id", "provider"}, nil},
	"payment.failed.v1":                  {TopicPayments, "payment_attempt", "attempt_id", []string{"store_id", "order_id", "payment_id", "attempt_id", "provider", "failure_class"}, nil},
	"payment.reconciliation_required.v1": {TopicPayments, "payment_reconciliation", "reconciliation_id", []string{"store_id", "payment_id", "reconciliation_id", "reason"}, []string{"attempt_id"}},
	"refund.requested.v1":                {TopicPayments, "refund", "refund_id", []string{"store_id", "order_id", "payment_id", "refund_id"}, nil},
	"refund.succeeded.v1":                {TopicPayments, "refund", "refund_id", []string{"store_id", "order_id", "payment_id", "refund_id"}, nil},
	"refund.failed.v1":                   {TopicPayments, "refund", "refund_id", []string{"store_id", "order_id", "payment_id", "refund_id", "failure_class"}, nil},
	"shipment.prepared.v1":               {TopicShipping, "shipment", "shipment_id", []string{"store_id", "order_id", "shipment_id", "state"}, nil},
	"shipment.updated.v1":                {TopicShipping, "shipment", "shipment_id", []string{"store_id", "order_id", "shipment_id", "state"}, nil},
}

func MessageFor(event eventdomain.Event) (Message, error) {
	topic, ok := TopicFor(event.Type)
	if !ok {
		return Message{}, ErrUnsupported
	}
	encoded, err := Encode(event)
	if err != nil {
		return Message{}, err
	}
	headers := []Header{{"event_id", []byte(event.ID.String())}, {"event_type", []byte(event.Type)}, {"schema_version", []byte(strconv.Itoa(event.SchemaVersion))}, {"store_id", []byte(event.StoreID.String())}, {"aggregate_type", []byte(event.AggregateType)}, {"aggregate_id", []byte(event.AggregateID)}, {"correlation_id", []byte(event.CorrelationID)}}
	return Message{Topic: topic, Key: []byte(event.StoreID.String() + ":" + event.ID.String()), Value: encoded, Headers: headers}, nil
}
