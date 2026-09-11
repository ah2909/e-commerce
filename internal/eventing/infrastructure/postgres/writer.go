package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"gorm.io/gorm"
)

type Writer struct{}

func NewWriter() Writer { return Writer{} }

type eventRow struct {
	StoreID          string    `gorm:"column:store_id"`
	EventID          string    `gorm:"column:event_id"`
	EventType        string    `gorm:"column:event_type"`
	SchemaVersion    int       `gorm:"column:schema_version"`
	AggregateType    string    `gorm:"column:aggregate_type"`
	AggregateID      string    `gorm:"column:aggregate_id"`
	AggregateVersion int64     `gorm:"column:aggregate_version"`
	OccurredAt       time.Time `gorm:"column:occurred_at"`
	CorrelationID    string    `gorm:"column:correlation_id"`
	Payload          []byte    `gorm:"column:payload;type:jsonb"`
	NextAttemptAt    time.Time `gorm:"column:next_attempt_at"`
}

func (eventRow) TableName() string { return "outbox_events" }

func (Writer) Append(ctx context.Context, tx *gorm.DB, event eventdomain.Event) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("encode outbox payload: %w", err)
	}
	row := eventRow{StoreID: event.StoreID.String(), EventID: event.ID.String(), EventType: event.Type, SchemaVersion: event.SchemaVersion, AggregateType: event.AggregateType, AggregateID: event.AggregateID, AggregateVersion: event.AggregateVersion, OccurredAt: event.OccurredAt, CorrelationID: event.CorrelationID, Payload: payload, NextAttemptAt: event.OccurredAt}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("append outbox event: %w", err)
	}
	return nil
}
