package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type RetentionCutoffs struct {
	PublishedBefore  time.Time
	ProcessedBefore  time.Time
	DeadLetterBefore time.Time
}

type RetentionStatus struct {
	PublishedOutbox     int64
	ProcessedInbox      int64
	ResolvedDeadLetters int64
}

type RetentionResult struct {
	PublishedOutbox     int64
	ProcessedInbox      int64
	ResolvedDeadLetters int64
}

type Retention struct{ db *gorm.DB }

const (
	publishedRetentionWindow  = 30 * 24 * time.Hour
	deadLetterRetentionWindow = 90 * 24 * time.Hour
)

func NewRetention(db *gorm.DB) *Retention { return &Retention{db: db} }

func (retention *Retention) Status(ctx context.Context, cutoffs RetentionCutoffs) (RetentionStatus, error) {
	if err := retention.validateCutoffs(ctx, retention.db, cutoffs, false); err != nil {
		return RetentionStatus{}, err
	}
	var status RetentionStatus
	query := `SELECT
(SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL AND published_at < ?) AS published_outbox,
(SELECT count(*) FROM consumer_inbox WHERE processed_at < ?) AS processed_inbox,
(SELECT count(*) FROM event_dead_letters WHERE resolved_at IS NOT NULL AND resolved_at < ?) AS resolved_dead_letters`
	if err := retention.db.WithContext(ctx).Raw(query, cutoffs.PublishedBefore, cutoffs.ProcessedBefore, cutoffs.DeadLetterBefore).Scan(&status).Error; err != nil {
		return RetentionStatus{}, fmt.Errorf("read event retention status: %w", err)
	}
	return status, nil
}

func (retention *Retention) DeleteBatch(ctx context.Context, cutoffs RetentionCutoffs, limit int) (RetentionResult, error) {
	if limit < 1 || limit > 1000 {
		return RetentionResult{}, errors.New("invalid event retention request")
	}
	var result RetentionResult
	err := retention.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := retention.validateCutoffs(ctx, tx, cutoffs, true); err != nil {
			return err
		}
		queries := []struct {
			query  string
			cutoff time.Time
			count  *int64
		}{
			{`WITH eligible AS (SELECT store_id,event_id FROM outbox_events WHERE published_at IS NOT NULL AND published_at < ? ORDER BY published_at,store_id,event_id FOR UPDATE SKIP LOCKED LIMIT ?) DELETE FROM outbox_events target USING eligible WHERE target.store_id=eligible.store_id AND target.event_id=eligible.event_id`, cutoffs.PublishedBefore, &result.PublishedOutbox},
			{`WITH eligible AS (SELECT store_id,consumer_name,event_id FROM consumer_inbox WHERE processed_at < ? ORDER BY processed_at,store_id,consumer_name,event_id FOR UPDATE SKIP LOCKED LIMIT ?) DELETE FROM consumer_inbox target USING eligible WHERE target.store_id=eligible.store_id AND target.consumer_name=eligible.consumer_name AND target.event_id=eligible.event_id`, cutoffs.ProcessedBefore, &result.ProcessedInbox},
			{`WITH eligible AS (SELECT dead_letter_id FROM event_dead_letters WHERE resolved_at IS NOT NULL AND resolved_at < ? ORDER BY resolved_at,dead_letter_id FOR UPDATE SKIP LOCKED LIMIT ?) DELETE FROM event_dead_letters target USING eligible WHERE target.dead_letter_id=eligible.dead_letter_id`, cutoffs.DeadLetterBefore, &result.ResolvedDeadLetters},
		}
		for _, item := range queries {
			deleted := tx.Exec(item.query, item.cutoff, limit)
			if deleted.Error != nil {
				return fmt.Errorf("delete retained event records: %w", deleted.Error)
			}
			*item.count = deleted.RowsAffected
		}
		return nil
	})
	if err != nil {
		return RetentionResult{}, err
	}
	return result, nil
}

func (retention *Retention) validateCutoffs(ctx context.Context, db *gorm.DB, cutoffs RetentionCutoffs, enforceRetentionWindow bool) error {
	var now time.Time
	if err := db.WithContext(ctx).Raw(`SELECT clock_timestamp()`).Scan(&now).Error; err != nil {
		return fmt.Errorf("read database time for event retention: %w", err)
	}
	for _, cutoff := range []time.Time{cutoffs.PublishedBefore, cutoffs.ProcessedBefore, cutoffs.DeadLetterBefore} {
		if cutoff.IsZero() || !cutoff.Before(now.Add(-time.Hour)) {
			return errors.New("retention cutoffs require at least a one-hour safety delay")
		}
	}
	if enforceRetentionWindow && (!cutoffs.PublishedBefore.Before(now.Add(-publishedRetentionWindow)) || !cutoffs.ProcessedBefore.Before(now.Add(-publishedRetentionWindow)) || !cutoffs.DeadLetterBefore.Before(now.Add(-deadLetterRetentionWindow))) {
		return errors.New("retention cutoffs are younger than their approved retention windows")
	}
	return nil
}
