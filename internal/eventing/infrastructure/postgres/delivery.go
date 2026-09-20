package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Effect interface {
	Apply(context.Context, *gorm.DB, eventdomain.Event) (bool, error)
}

type Delivery struct {
	db     *gorm.DB
	effect Effect
}

func NewDelivery(db *gorm.DB, effect Effect) *Delivery { return &Delivery{db: db, effect: effect} }

type inboxRow struct {
	StoreID      string `gorm:"column:store_id"`
	ConsumerName string `gorm:"column:consumer_name"`
	EventID      string `gorm:"column:event_id"`
	EventType    string `gorm:"column:event_type"`
	Outcome      string `gorm:"column:outcome"`
}

func (inboxRow) TableName() string { return "consumer_inbox" }

func (delivery *Delivery) Process(ctx context.Context, consumer string, event eventdomain.Event) error {
	return delivery.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := inboxRow{StoreID: event.StoreID.String(), ConsumerName: consumer, EventID: event.ID.String(), EventType: event.Type, Outcome: "applied"}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Exec(`INSERT INTO consumer_inbox (store_id,consumer_name,event_id,event_type,outcome,processed_at) VALUES (?,?,?,?,?,clock_timestamp()) ON CONFLICT DO NOTHING`, row.StoreID, row.ConsumerName, row.EventID, row.EventType, row.Outcome)
		if result.Error != nil {
			return fmt.Errorf("insert consumer inbox: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		applied, err := delivery.effect.Apply(ctx, tx, event)
		if err != nil {
			return err
		}
		if !applied {
			if err := tx.Exec(`UPDATE consumer_inbox SET outcome='ignored' WHERE store_id=? AND consumer_name=? AND event_id=?`, row.StoreID, row.ConsumerName, row.EventID).Error; err != nil {
				return fmt.Errorf("mark inbox ignored: %w", err)
			}
		}
		return nil
	})
}

func (delivery *Delivery) RecordDeadLetter(ctx context.Context, letter eventapplication.DeadLetter) error {
	var envelope any
	if len(letter.Envelope) > 0 {
		if err := json.Unmarshal(letter.Envelope, &envelope); err != nil {
			return eventapplication.ErrInvalidMessage
		}
	}
	result := delivery.db.WithContext(ctx).Exec(`
INSERT INTO event_dead_letters (dead_letter_id,store_id,consumer_name,event_id,topic,partition,message_offset,message_digest,event_type,classification,diagnostic,validated_envelope,attempts,first_failed_at,last_failed_at)
VALUES (?,NULLIF(?, '')::uuid,?,NULLIF(?, '')::uuid,?,?,?,?,?,?,?,?::jsonb,1,?,?)
ON CONFLICT (consumer_name,topic,partition,message_offset) DO UPDATE SET attempts=event_dead_letters.attempts+1,last_failed_at=EXCLUDED.last_failed_at,classification=EXCLUDED.classification,diagnostic=EXCLUDED.diagnostic`,
		letter.ID.String(), letter.StoreID.String(), letter.Consumer, letter.EventID.String(), letter.Topic, letter.Partition, letter.Offset, letter.Digest[:], nullText(letter.EventType), letter.Classification, letter.Diagnostic, nullJSON(envelope), letter.OccurredAt, letter.OccurredAt)
	if result.Error != nil {
		return fmt.Errorf("insert event dead letter: %w", result.Error)
	}
	return nil
}

func (delivery *Delivery) RecordTransient(ctx context.Context, letter eventapplication.DeadLetter, maxAttempts int) (bool, error) {
	var envelope any
	if err := json.Unmarshal(letter.Envelope, &envelope); err != nil {
		return false, eventapplication.ErrInvalidMessage
	}
	var classification string
	err := delivery.db.WithContext(ctx).Raw(`
INSERT INTO event_dead_letters (dead_letter_id,store_id,consumer_name,event_id,topic,partition,message_offset,message_digest,event_type,classification,diagnostic,validated_envelope,attempts,first_failed_at,last_failed_at)
VALUES (?,?,?,?,?,?,?,?,?,CASE WHEN ? <= 1 THEN 'exhausted' ELSE 'retryable' END,?,?::jsonb,1,?,?)
ON CONFLICT (consumer_name,topic,partition,message_offset) DO UPDATE
SET attempts=event_dead_letters.attempts+1,last_failed_at=EXCLUDED.last_failed_at,
    classification=CASE WHEN event_dead_letters.attempts+1 >= ? THEN 'exhausted' ELSE 'retryable' END,
    diagnostic=CASE WHEN event_dead_letters.attempts+1 >= ? THEN 'event retries exhausted' ELSE EXCLUDED.diagnostic END
RETURNING classification`, letter.ID.String(), letter.StoreID.String(), letter.Consumer, letter.EventID.String(), letter.Topic, letter.Partition, letter.Offset, letter.Digest[:], letter.EventType, maxAttempts, letter.Diagnostic, nullJSON(envelope), letter.OccurredAt, letter.OccurredAt, maxAttempts, maxAttempts).Scan(&classification).Error
	if err != nil {
		return false, fmt.Errorf("record transient dead letter: %w", err)
	}
	return classification == "exhausted" || (classification == "retryable" && maxAttempts == 1), nil
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullJSON(value any) any {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
