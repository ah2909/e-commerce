package application

import (
	"context"
	"errors"
	"testing"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
)

type failingDelivery struct {
	attempts  int
	processed int
	letters   []DeadLetter
}

func (delivery *failingDelivery) Process(context.Context, string, eventdomain.Event) error {
	delivery.processed++
	return errors.New("temporary database outage")
}
func (delivery *failingDelivery) RecordDeadLetter(_ context.Context, letter DeadLetter) error {
	delivery.letters = append(delivery.letters, letter)
	return nil
}
func (delivery *failingDelivery) RecordTransient(context.Context, DeadLetter, int) (bool, error) {
	delivery.attempts++
	return delivery.attempts >= 2, nil
}

func TestConsumerRejectsTransportMismatchBeforeDispatch(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	event, _ := eventdomain.New(testStore, testEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, now, "request-1", validOrderPayload())
	message, _ := MessageFor(event)
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "wrong topic", mutate: func(record *Record) { record.Topic = TopicShipping }},
		{name: "wrong key", mutate: func(record *Record) { record.Key = []byte("wrong") }},
		{name: "wrong header", mutate: func(record *Record) { record.Headers[0].Value = []byte("wrong") }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &failingDelivery{}
			consumer, _ := NewConsumer("test-consumer", delivery, 2, func() time.Time { return now })
			record := Record{Topic: message.Topic, Partition: 1, Offset: int64(index), Key: append([]byte(nil), message.Key...), Headers: append([]Header(nil), message.Headers...), Value: message.Value}
			record.Headers[0].Value = append([]byte(nil), record.Headers[0].Value...)
			test.mutate(&record)
			if err := consumer.Process(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if delivery.processed != 0 || len(delivery.letters) != 1 || delivery.letters[0].Classification != "permanent" || len(delivery.letters[0].Envelope) == 0 {
				t.Fatalf("processed=%d letters=%#v", delivery.processed, delivery.letters)
			}
		})
	}
}

func TestConsumerDoesNotRetainInvalidPayload(t *testing.T) {
	delivery := &failingDelivery{}
	consumer, _ := NewConsumer("test-consumer", delivery, 2, time.Now)
	raw := []byte(`{"event_id":"22222222-2222-4222-8222-222222222222","event_type":"order.created.v1","schema_version":1,"occurred_at":"2026-09-20T00:00:00Z","store_id":"11111111-1111-4111-8111-111111111111","aggregate_type":"order","aggregate_id":"33333333-3333-4333-8333-333333333333","aggregate_version":1,"correlation_id":"request-1","payload":{"store_id":"11111111-1111-4111-8111-111111111111","order_id":"33333333-3333-4333-8333-333333333333","owner_kind":"guest","guest_id":"44444444-4444-4444-8444-444444444444","token":"secret"}}`)
	if err := consumer.Process(context.Background(), Record{Topic: TopicOrders, Partition: 1, Offset: 9, Value: raw}); err != nil {
		t.Fatal(err)
	}
	if len(delivery.letters) != 1 || delivery.letters[0].StoreID != "" || len(delivery.letters[0].Envelope) != 0 {
		t.Fatalf("letter=%#v", delivery.letters)
	}
}

func TestConsumerBoundsTransientRetries(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	event, err := eventdomain.New(testStore, testEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, now, "request-1", validOrderPayload())
	if err != nil {
		t.Fatal(err)
	}
	message, err := MessageFor(event)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &failingDelivery{}
	consumer, err := NewConsumer("test-consumer", delivery, 2, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Topic: message.Topic, Partition: 1, Offset: 2, Key: message.Key, Headers: message.Headers, Value: message.Value}
	if err := consumer.Process(context.Background(), record); err == nil {
		t.Fatal("first retry unexpectedly became terminal")
	}
	if err := consumer.Process(context.Background(), record); err != nil {
		t.Fatalf("exhausted retry error = %v", err)
	}
}
