package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type Consumer struct {
	name        string
	delivery    Delivery
	maxAttempts int
	now         func() time.Time
}

func NewConsumer(name string, delivery Delivery, maxAttempts int, now func() time.Time) (Consumer, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || delivery == nil || maxAttempts < 1 || maxAttempts > 100 || now == nil {
		return Consumer{}, ErrInvalidMessage
	}
	return Consumer{name: name, delivery: delivery, maxAttempts: maxAttempts, now: now}, nil
}

func (consumer Consumer) Process(ctx context.Context, record Record) error {
	event, err := Decode(record.Value)
	if err == nil {
		err = validateRecord(record, event)
	}
	if err == nil {
		err = consumer.delivery.Process(ctx, consumer.name, event)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrPermanent) {
			letter, letterErr := consumer.deadLetter(record, event, "retryable", "retryable event failure")
			if letterErr != nil {
				return letterErr
			}
			terminal, recordErr := consumer.delivery.RecordTransient(ctx, letter, consumer.maxAttempts)
			if recordErr != nil {
				return fmt.Errorf("record transient event failure: %w", recordErr)
			}
			if terminal {
				return nil
			}
			return err
		}
	}
	classification := "malformed"
	if errors.Is(err, ErrUnsupported) {
		classification = "unsupported"
	} else if errors.Is(err, ErrPermanent) {
		classification = "permanent"
	}
	letter, letterErr := consumer.deadLetter(record, event, classification, classification+" event message")
	if letterErr != nil {
		return letterErr
	}
	if err := consumer.delivery.RecordDeadLetter(ctx, letter); err != nil {
		return fmt.Errorf("record event dead letter: %w", err)
	}
	return nil
}

func validateRecord(record Record, event eventdomain.Event) error {
	topic, ok := TopicFor(event.Type)
	if !ok || record.Topic != topic || string(record.Key) != event.StoreID.String()+":"+event.ID.String() {
		return ErrPermanent
	}
	expected := map[string]string{
		"event_id": event.ID.String(), "event_type": event.Type, "schema_version": "1",
		"store_id": event.StoreID.String(), "aggregate_type": event.AggregateType,
		"aggregate_id": event.AggregateID, "correlation_id": event.CorrelationID,
	}
	if len(record.Headers) != len(expected) {
		return ErrPermanent
	}
	seen := make(map[string]struct{}, len(record.Headers))
	for _, header := range record.Headers {
		value, exists := expected[header.Key]
		if !exists || value != string(header.Value) {
			return ErrPermanent
		}
		if _, duplicate := seen[header.Key]; duplicate {
			return ErrPermanent
		}
		seen[header.Key] = struct{}{}
	}
	return nil
}

func (consumer Consumer) deadLetter(record Record, event eventdomain.Event, classification, diagnostic string) (DeadLetter, error) {
	digest := sha256.Sum256(record.Value)
	id, err := ids.NewEventID()
	if err != nil {
		return DeadLetter{}, fmt.Errorf("generate dead-letter ID: %w", err)
	}
	letter := DeadLetter{ID: id, Consumer: consumer.name, Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, Digest: digest, Classification: classification, Diagnostic: diagnostic, OccurredAt: consumer.now().UTC()}
	if event.StoreID != "" {
		letter.StoreID, letter.EventID, letter.EventType = event.StoreID, event.ID, event.Type
		if encoded, encodeErr := Encode(event); encodeErr == nil {
			letter.Envelope = encoded
		}
	}
	return letter, nil
}
