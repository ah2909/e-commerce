package application

import (
	"bytes"
	"errors"
	"testing"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

const (
	testStore       = ids.StoreID("11111111-1111-4111-8111-111111111111")
	testEvent       = ids.EventID("22222222-2222-4222-8222-222222222222")
	testSecondEvent = ids.EventID("55555555-5555-4555-8555-555555555555")
)

func TestEnvelopeRoundTripAndRouting(t *testing.T) {
	event, err := eventdomain.New(testStore, testEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, time.Date(2026, 9, 20, 1, 2, 3, 0, time.FixedZone("ICT", 7*60*60)), "request-1", validOrderPayload())
	if err != nil {
		t.Fatal(err)
	}
	message, err := MessageFor(event)
	if err != nil {
		t.Fatal(err)
	}
	if message.Topic != TopicOrders || string(message.Key) != testStore.String()+":"+testEvent.String() {
		t.Fatalf("message = %#v", message)
	}
	decoded, err := Decode(message.Value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.StoreID != event.StoreID || decoded.ID != event.ID || !decoded.OccurredAt.Equal(event.OccurredAt) {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDecodeRejectsUnsafeMessages(t *testing.T) {
	valid := []byte(`{"event_id":"22222222-2222-4222-8222-222222222222","event_type":"order.created.v1","schema_version":1,"occurred_at":"2026-09-20T00:00:00Z","store_id":"11111111-1111-4111-8111-111111111111","aggregate_type":"order","aggregate_id":"33333333-3333-4333-8333-333333333333","aggregate_version":1,"correlation_id":"request-1","payload":{"store_id":"11111111-1111-4111-8111-111111111111","order_id":"33333333-3333-4333-8333-333333333333","owner_kind":"guest","guest_id":"44444444-4444-4444-8444-444444444444"}}`)
	tests := map[string][]byte{
		"empty":                 nil,
		"oversized":             bytes.Repeat([]byte("x"), MaxMessageBytes+1),
		"trailing":              append(append([]byte{}, valid...), []byte(` {}`)...),
		"unknown field":         bytes.Replace(valid, []byte(`"payload"`), []byte(`"unexpected":1,"payload"`), 1),
		"tenant mismatch":       bytes.Replace(valid, []byte(`"payload":{"store_id":"11111111-1111-4111-8111-111111111111"`), []byte(`"payload":{"store_id":"44444444-4444-4444-8444-444444444444"`), 1),
		"wrong aggregate":       bytes.Replace(valid, []byte(`"order_id":"33333333-3333-4333-8333-333333333333"`), []byte(`"order_id":"55555555-5555-4555-8555-555555555555"`), 1),
		"wrong aggregate type":  bytes.Replace(valid, []byte(`"aggregate_type":"order"`), []byte(`"aggregate_type":"shipment"`), 1),
		"missing payload field": bytes.Replace(valid, []byte(`,"owner_kind":"guest"`), nil, 1),
		"extra payload":         bytes.Replace(valid, []byte(`"owner_kind"`), []byte(`"secret":"do-not-retain","owner_kind"`), 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func FuzzDecodeRejectsUntrustedInput(f *testing.F) {
	f.Add([]byte(`{"event_id":"22222222-2222-4222-8222-222222222222"}`))
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = Decode(raw) })
}

func TestDecodeRejectsUnsupportedEnvelopeWithoutTrustingItsIdentity(t *testing.T) {
	raw := []byte(`{"event_id":"22222222-2222-4222-8222-222222222222","event_type":"future.event.v2","schema_version":1,"occurred_at":"2026-09-20T00:00:00Z","store_id":"11111111-1111-4111-8111-111111111111","aggregate_type":"order","aggregate_id":"33333333-3333-4333-8333-333333333333","aggregate_version":1,"correlation_id":"request-1","payload":{}}`)
	event, err := Decode(raw)
	if !errors.Is(err, ErrUnsupported) || event.StoreID != "" || event.ID != "" {
		t.Fatalf("event = %#v, error = %v", event, err)
	}
}

func TestShipmentLifecycleContractBindsOrderAndRequiresIdentifiers(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	orderID := "33333333-3333-4333-8333-333333333333"
	payload := map[string]any{"store_id": testStore.String(), "order_id": "55555555-5555-4555-8555-555555555555", "shipment_id": "44444444-4444-4444-8444-444444444444", "state": "shipped"}
	event, _ := eventdomain.New(testStore, testEvent, "order.shipped.v1", "order", orderID, 2, now, "shipment", payload)
	if _, err := Encode(event); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("mismatched order error=%v", err)
	}
	payload["order_id"] = orderID
	payload["shipment_id"] = ""
	event, _ = eventdomain.New(testStore, testEvent, "order.shipped.v1", "order", orderID, 2, now, "shipment", payload)
	if _, err := Encode(event); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("empty shipment error=%v", err)
	}
}

func validOrderPayload() map[string]any {
	return map[string]any{"store_id": testStore.String(), "order_id": "33333333-3333-4333-8333-333333333333", "owner_kind": "guest", "guest_id": "44444444-4444-4444-8444-444444444444"}
}
