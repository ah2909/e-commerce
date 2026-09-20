package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

var ErrClaimLost = errors.New("outbox claim lost")

type Outbox struct{ db *gorm.DB }

func NewOutbox(db *gorm.DB) *Outbox { return &Outbox{db: db} }

type claimRow struct {
	StoreID          string
	EventID          string
	EventType        string
	SchemaVersion    int
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	OccurredAt       time.Time
	CorrelationID    string
	Payload          []byte
	Attempts         int
}

func (outbox *Outbox) Claim(ctx context.Context, limit int, lease time.Duration) ([]eventapplication.Claim, error) {
	var claims []eventapplication.Claim
	err := outbox.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []claimRow
		if err := tx.Raw(`
SELECT store_id,event_id,event_type,schema_version,aggregate_type,aggregate_id,aggregate_version,occurred_at,correlation_id,payload,attempts
FROM outbox_events
WHERE published_at IS NULL AND terminal_at IS NULL AND next_attempt_at <= clock_timestamp()
  AND (claim_token IS NULL OR claimed_until <= clock_timestamp())
ORDER BY next_attempt_at,occurred_at,store_id,event_id
LIMIT ?
FOR UPDATE SKIP LOCKED`, limit).Scan(&rows).Error; err != nil {
			return fmt.Errorf("select due outbox events: %w", err)
		}
		claims = make([]eventapplication.Claim, 0, len(rows))
		for _, row := range rows {
			storeID, err := ids.ParseStoreID(row.StoreID)
			if err != nil {
				return err
			}
			eventID, err := ids.ParseEventID(row.EventID)
			if err != nil {
				return err
			}
			var payload map[string]any
			decoder := json.NewDecoder(bytes.NewReader(row.Payload))
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err != nil {
				return fmt.Errorf("decode outbox payload: %w", err)
			}
			event := eventdomain.Event{StoreID: storeID, ID: eventID, Type: row.EventType, SchemaVersion: row.SchemaVersion, AggregateType: row.AggregateType, AggregateID: row.AggregateID, AggregateVersion: row.AggregateVersion, OccurredAt: row.OccurredAt.UTC(), CorrelationID: row.CorrelationID, Payload: payload}
			token, err := ids.NewEventID()
			if err != nil {
				return fmt.Errorf("generate claim token: %w", err)
			}
			result := tx.Exec(`UPDATE outbox_events SET claim_token=?,claimed_until=clock_timestamp()+(? * interval '1 microsecond') WHERE store_id=? AND event_id=?`, token.String(), lease.Microseconds(), storeID.String(), eventID.String())
			if result.Error != nil {
				return fmt.Errorf("claim outbox event: %w", result.Error)
			}
			claims = append(claims, eventapplication.Claim{Event: event, Token: token, Attempts: row.Attempts + 1})
		}
		return nil
	})
	return claims, err
}

func (outbox *Outbox) Complete(ctx context.Context, claim eventapplication.Claim, topic string, publication eventapplication.Publication) error {
	result := outbox.db.WithContext(ctx).Exec(`
UPDATE outbox_events
SET published_at=clock_timestamp(),published_topic=?,published_partition=?,published_offset=?,claim_token=NULL,claimed_until=NULL,last_failure_class=NULL,last_error_digest=NULL,attempts=attempts+1
WHERE store_id=? AND event_id=? AND claim_token=? AND published_at IS NULL AND terminal_at IS NULL`, topic, publication.Partition, publication.Offset, claim.Event.StoreID.String(), claim.Event.ID.String(), claim.Token.String())
	if result.Error != nil {
		return fmt.Errorf("mark outbox published: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrClaimLost
	}
	return nil
}

func (outbox *Outbox) Fail(ctx context.Context, claim eventapplication.Claim, class string, digest [32]byte, next time.Time, terminal bool) error {
	var result *gorm.DB
	if terminal {
		result = outbox.db.WithContext(ctx).Exec(`
UPDATE outbox_events
SET terminal_at=clock_timestamp(),last_failure_class=?,last_error_digest=?,claim_token=NULL,claimed_until=NULL,attempts=attempts+1
WHERE store_id=? AND event_id=? AND claim_token=? AND published_at IS NULL AND terminal_at IS NULL`, class, digest[:], claim.Event.StoreID.String(), claim.Event.ID.String(), claim.Token.String())
	} else {
		result = outbox.db.WithContext(ctx).Exec(`
UPDATE outbox_events
SET next_attempt_at=?,last_failure_class=?,last_error_digest=?,claim_token=NULL,claimed_until=NULL,attempts=attempts+1
WHERE store_id=? AND event_id=? AND claim_token=? AND published_at IS NULL AND terminal_at IS NULL`, next.UTC(), class, digest[:], claim.Event.StoreID.String(), claim.Event.ID.String(), claim.Token.String())
	}
	if result.Error != nil {
		return fmt.Errorf("mark outbox failure: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrClaimLost
	}
	return nil
}

func (outbox *Outbox) Release(ctx context.Context, claims []eventapplication.Claim) error {
	for _, claim := range claims {
		result := outbox.db.WithContext(ctx).Exec(`UPDATE outbox_events SET claim_token=NULL,claimed_until=NULL WHERE store_id=? AND event_id=? AND claim_token=? AND published_at IS NULL AND terminal_at IS NULL`, claim.Event.StoreID.String(), claim.Event.ID.String(), claim.Token.String())
		if result.Error != nil {
			return fmt.Errorf("release outbox claim: %w", result.Error)
		}
	}
	return nil
}
