-- +goose Up
CREATE INDEX IF NOT EXISTS outbox_events_published_retention_idx ON outbox_events (published_at, store_id, event_id) WHERE published_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS event_dead_letters_resolved_retention_idx ON event_dead_letters (resolved_at, dead_letter_id) WHERE resolved_at IS NOT NULL;

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'outbox_events_published_retention_idx' AND indexdef LIKE '%(published_at, store_id, event_id)%' AND indexdef LIKE '%WHERE (published_at IS NOT NULL)%') THEN
    RAISE EXCEPTION 'outbox_events_published_retention_idx does not match the approved retention index';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'event_dead_letters_resolved_retention_idx' AND indexdef LIKE '%(resolved_at, dead_letter_id)%' AND indexdef LIKE '%WHERE (resolved_at IS NOT NULL)%') THEN
    RAISE EXCEPTION 'event_dead_letters_resolved_retention_idx does not match the approved retention index';
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS event_dead_letters_resolved_retention_idx;
DROP INDEX IF EXISTS outbox_events_published_retention_idx;
