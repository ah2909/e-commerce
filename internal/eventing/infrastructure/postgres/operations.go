package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

type Backlog struct {
	Due       int64
	Retrying  int64
	Terminal  int64
	OldestDue *time.Time
}

func (outbox *Outbox) Backlog(ctx context.Context) (Backlog, error) {
	var value Backlog
	if err := outbox.db.WithContext(ctx).Raw(`
SELECT count(*) FILTER (WHERE published_at IS NULL AND terminal_at IS NULL AND next_attempt_at <= clock_timestamp()) AS due,
       count(*) FILTER (WHERE published_at IS NULL AND terminal_at IS NULL AND next_attempt_at > clock_timestamp()) AS retrying,
       count(*) FILTER (WHERE terminal_at IS NOT NULL) AS terminal,
       min(occurred_at) FILTER (WHERE published_at IS NULL AND terminal_at IS NULL AND next_attempt_at <= clock_timestamp()) AS oldest_due
FROM outbox_events`).Scan(&value).Error; err != nil {
		return Backlog{}, fmt.Errorf("read outbox backlog: %w", err)
	}
	return value, nil
}

type OutboxItem struct {
	EventID          string
	EventType        string
	Attempts         int
	NextAttemptAt    time.Time
	TerminalAt       *time.Time
	LastFailureClass *string
}

func (outbox *Outbox) List(ctx context.Context, storeID ids.StoreID, limit int) ([]OutboxItem, error) {
	if storeID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("invalid outbox listing")
	}
	var values []OutboxItem
	if err := outbox.db.WithContext(ctx).Raw(`
SELECT event_id,event_type,attempts,next_attempt_at,terminal_at,last_failure_class
FROM outbox_events
WHERE store_id=? AND published_at IS NULL
ORDER BY COALESCE(terminal_at,next_attempt_at),event_id
LIMIT ?`, storeID.String(), limit).Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("list outbox events: %w", err)
	}
	return values, nil
}

type DeadLetterBacklog struct {
	Open      int64
	Retryable int64
	Terminal  int64
}

func (delivery *Delivery) DeadLetterBacklog(ctx context.Context) (DeadLetterBacklog, error) {
	var value DeadLetterBacklog
	if err := delivery.db.WithContext(ctx).Raw(`
SELECT count(*) FILTER (WHERE resolved_at IS NULL) AS open,
       count(*) FILTER (WHERE resolved_at IS NULL AND classification='retryable') AS retryable,
       count(*) FILTER (WHERE resolved_at IS NULL AND classification<>'retryable') AS terminal
FROM event_dead_letters`).Scan(&value).Error; err != nil {
		return DeadLetterBacklog{}, fmt.Errorf("read dead-letter backlog: %w", err)
	}
	return value, nil
}

type DeadLetterItem struct {
	DeadLetterID   string
	ConsumerName   string
	EventID        *string
	Topic          string
	Partition      int32
	MessageOffset  int64
	EventType      *string
	Classification string
	Attempts       int
	LastFailedAt   time.Time
}

func (delivery *Delivery) ListDeadLetters(ctx context.Context, storeID *ids.StoreID, limit int) ([]DeadLetterItem, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid dead-letter listing")
	}
	query := `SELECT dead_letter_id,consumer_name,event_id,topic,partition,message_offset,event_type,classification,attempts,last_failed_at FROM event_dead_letters WHERE resolved_at IS NULL AND store_id IS NULL ORDER BY last_failed_at DESC,dead_letter_id LIMIT ?`
	args := []any{limit}
	if storeID != nil {
		if *storeID == "" {
			return nil, errors.New("invalid dead-letter store")
		}
		query = `SELECT dead_letter_id,consumer_name,event_id,topic,partition,message_offset,event_type,classification,attempts,last_failed_at FROM event_dead_letters WHERE resolved_at IS NULL AND store_id=? ORDER BY last_failed_at DESC,dead_letter_id LIMIT ?`
		args = []any{storeID.String(), limit}
	}
	var values []DeadLetterItem
	if err := delivery.db.WithContext(ctx).Raw(query, args...).Scan(&values).Error; err != nil {
		return nil, fmt.Errorf("list event dead letters: %w", err)
	}
	return values, nil
}

func (outbox *Outbox) Requeue(ctx context.Context, storeID ids.StoreID, eventID ids.EventID, reason string) error {
	reason = strings.Join(strings.Fields(reason), " ")
	if storeID == "" || eventID == "" || len(reason) < 1 || len(reason) > 240 {
		return errors.New("invalid outbox requeue")
	}
	result := outbox.db.WithContext(ctx).Exec(`
UPDATE outbox_events
SET terminal_at=NULL,attempts=0,next_attempt_at=clock_timestamp(),last_failure_class=NULL,last_error_digest=NULL,claim_token=NULL,claimed_until=NULL,requeued_at=clock_timestamp(),requeue_reason=?
WHERE store_id=? AND event_id=? AND published_at IS NULL AND terminal_at IS NOT NULL`, reason, storeID.String(), eventID.String())
	if result.Error != nil {
		return fmt.Errorf("requeue outbox event: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type ReplayableDeadLetter struct {
	ID       ids.EventID
	StoreID  ids.StoreID
	Envelope []byte
	Topic    string
}

func (delivery *Delivery) LoadReplayable(ctx context.Context, storeID ids.StoreID, id ids.EventID) (ReplayableDeadLetter, error) {
	var row struct {
		DeadLetterID      string
		StoreID           string
		ValidatedEnvelope []byte
		Topic             string
	}
	err := delivery.db.WithContext(ctx).Raw(`SELECT dead_letter_id,store_id,validated_envelope,topic FROM event_dead_letters WHERE dead_letter_id=? AND store_id=? AND validated_envelope IS NOT NULL AND resolved_at IS NULL`, id.String(), storeID.String()).Take(&row).Error
	if err != nil {
		return ReplayableDeadLetter{}, err
	}
	return ReplayableDeadLetter{ID: ids.EventID(row.DeadLetterID), StoreID: ids.StoreID(row.StoreID), Envelope: row.ValidatedEnvelope, Topic: row.Topic}, nil
}

func (delivery *Delivery) ResolveReplay(ctx context.Context, storeID ids.StoreID, id ids.EventID, reason string) error {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) < 1 || len(reason) > 240 {
		return errors.New("invalid dead-letter replay reason")
	}
	result := delivery.db.WithContext(ctx).Exec(`UPDATE event_dead_letters SET replay_requested_at=clock_timestamp(),replay_reason=?,resolved_at=clock_timestamp() WHERE dead_letter_id=? AND store_id=? AND validated_envelope IS NOT NULL AND resolved_at IS NULL`, reason, id.String(), storeID.String())
	if result.Error != nil {
		return fmt.Errorf("resolve dead-letter replay: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
