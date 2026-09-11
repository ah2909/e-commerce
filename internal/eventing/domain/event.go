package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrInvalidEvent = errors.New("invalid event")

type Event struct {
	StoreID          ids.StoreID
	ID               ids.EventID
	Type             string
	SchemaVersion    int
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	OccurredAt       time.Time
	CorrelationID    string
	Payload          map[string]any
}

func New(storeID ids.StoreID, id ids.EventID, eventType, aggregateType, aggregateID string, aggregateVersion int64, occurredAt time.Time, correlationID string, payload map[string]any) (Event, error) {
	eventType, aggregateType, aggregateID, correlationID = strings.TrimSpace(eventType), strings.TrimSpace(aggregateType), strings.TrimSpace(aggregateID), strings.TrimSpace(correlationID)
	if storeID == "" || id == "" || eventType == "" || len(eventType) > 160 || aggregateType == "" || len(aggregateType) > 80 || aggregateID == "" || aggregateVersion < 1 || occurredAt.IsZero() || correlationID == "" || len(correlationID) > 200 || payload == nil {
		return Event{}, ErrInvalidEvent
	}
	return Event{StoreID: storeID, ID: id, Type: eventType, SchemaVersion: 1, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: aggregateVersion, OccurredAt: occurredAt.UTC(), CorrelationID: correlationID, Payload: payload}, nil
}
