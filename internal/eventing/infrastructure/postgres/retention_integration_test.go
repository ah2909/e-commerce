//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/gormtest"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
)

func TestRetentionDeletesOnlyEligibleTerminalRecords(t *testing.T) {
	pool := eventingPool(t)
	pair := storefixture.NewPair(t, pool)
	db := gormtest.FromPool(t, pool)
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-time.Minute)
	oldOutbox, recentOutbox := mustEventID(t), mustEventID(t)
	oldInbox, recentInbox := mustEventID(t), mustEventID(t)
	resolvedDead, openDead := mustEventID(t), mustEventID(t)
	for _, row := range []struct {
		id        string
		published time.Time
	}{{oldOutbox.String(), old}, {recentOutbox.String(), recent}} {
		if err := db.Exec(`INSERT INTO outbox_events(store_id,event_id,event_type,schema_version,aggregate_type,aggregate_id,aggregate_version,occurred_at,correlation_id,payload,next_attempt_at,published_at,published_topic,published_partition,published_offset) VALUES(?,?,'order.created.v1',1,'order',?,1,?,'retention','{}',?,?, 'commerce.orders.v1',0,1)`, pair.A.Store.ID(), row.id, mustEventID(t), row.published, row.published, row.published).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id        string
		processed time.Time
	}{{oldInbox.String(), old}, {recentInbox.String(), recent}} {
		if err := db.Exec(`INSERT INTO consumer_inbox(store_id,consumer_name,event_id,event_type,outcome,processed_at) VALUES(?,'retention-test',?,'order.created.v1','applied',?)`, pair.A.Store.ID(), row.id, row.processed).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id       string
		resolved *time.Time
	}{{resolvedDead.String(), &old}, {openDead.String(), nil}} {
		if err := db.Exec(`INSERT INTO event_dead_letters(dead_letter_id,store_id,consumer_name,topic,partition,message_offset,message_digest,classification,diagnostic,attempts,first_failed_at,last_failed_at,resolved_at) VALUES(?,?,'retention-test','commerce.orders.v1',0,?,decode(repeat('01',32),'hex'),'permanent','retention test',1,?,?,?)`, row.id, pair.A.Store.ID(), map[bool]int{true: 1, false: 2}[row.resolved != nil], old, old, row.resolved).Error; err != nil {
			t.Fatal(err)
		}
	}
	retention := eventingpostgres.NewRetention(db)
	cutoffs := eventingpostgres.RetentionCutoffs{PublishedBefore: old.Add(24 * time.Hour), ProcessedBefore: old.Add(24 * time.Hour), DeadLetterBefore: old.Add(24 * time.Hour)}
	status, err := retention.Status(context.Background(), cutoffs)
	if err != nil || status.PublishedOutbox != 1 || status.ProcessedInbox != 1 || status.ResolvedDeadLetters != 1 {
		t.Fatalf("status=%#v error=%v", status, err)
	}
	result, err := retention.DeleteBatch(context.Background(), cutoffs, 10)
	if err != nil || result.PublishedOutbox != 1 || result.ProcessedInbox != 1 || result.ResolvedDeadLetters != 1 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	for table, want := range map[string]int64{"outbox_events": 1, "consumer_inbox": 1, "event_dead_letters": 1} {
		var count int64
		if err := db.Table(table).Where("store_id=?", pair.A.Store.ID()).Count(&count).Error; err != nil || count != want {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
}

func TestRetentionRejectsCutoffsYoungerThanApprovedWindows(t *testing.T) {
	db := gormtest.FromPool(t, eventingPool(t))
	retention := eventingpostgres.NewRetention(db)
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)
	for _, cutoffs := range []eventingpostgres.RetentionCutoffs{
		{PublishedBefore: now.Add(-29 * 24 * time.Hour), ProcessedBefore: old, DeadLetterBefore: old},
		{PublishedBefore: old, ProcessedBefore: now.Add(-29 * 24 * time.Hour), DeadLetterBefore: old},
		{PublishedBefore: old, ProcessedBefore: old, DeadLetterBefore: now.Add(-89 * 24 * time.Hour)},
	} {
		if _, err := retention.DeleteBatch(context.Background(), cutoffs, 1); err == nil {
			t.Fatal("DeleteBatch() accepted cutoffs younger than approved retention windows")
		}
	}
}
