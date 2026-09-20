package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventingkafka "github.com/ah2909/e-commerce/internal/eventing/infrastructure/kafka"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/platform/database"
	"github.com/ah2909/e-commerce/internal/platform/logging"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.LookupEnv); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("event reconciliation failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup func(string) (string, bool)) error {
	if len(args) < 1 {
		return usage()
	}
	databaseURL, ok := lookup("DATABASE_URL")
	if !ok || strings.TrimSpace(databaseURL) == "" {
		return errors.New("DATABASE_URL is required")
	}
	logger := logging.New(os.Stdout, slog.LevelInfo)
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, err := database.Open(operationCtx, databaseURL, logger, database.DefaultPoolSettings())
	if err != nil {
		return err
	}
	defer func() { _ = connection.SQL.Close() }()
	if _, err := database.VerifySchemaRange(operationCtx, connection.GORM, 12, database.CurrentSchemaVersion); err != nil {
		return err
	}
	outbox := eventingpostgres.NewOutbox(connection.GORM)
	delivery := eventingpostgres.NewDelivery(connection.GORM, nil)
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return usage()
		}
		backlog, err := outbox.Backlog(operationCtx)
		if err != nil {
			return err
		}
		deadLetters, err := delivery.DeadLetterBacklog(operationCtx)
		if err != nil {
			return err
		}
		logger.Info("event backlog", "due", backlog.Due, "retrying", backlog.Retrying, "terminal", backlog.Terminal, "oldest_due", backlog.OldestDue, "dead_letters_open", deadLetters.Open, "dead_letters_retryable", deadLetters.Retryable, "dead_letters_terminal", deadLetters.Terminal)
		return nil
	case "list-outbox":
		if len(args) != 3 {
			return usage()
		}
		storeID, err := ids.ParseStoreID(args[1])
		if err != nil {
			return err
		}
		limit, err := parseLimit(args[2])
		if err != nil {
			return err
		}
		items, err := outbox.List(operationCtx, storeID, limit)
		if err != nil {
			return err
		}
		for _, item := range items {
			logger.Info("outbox event", "event_id", item.EventID, "event_type", item.EventType, "attempts", item.Attempts, "next_attempt_at", item.NextAttemptAt, "terminal_at", item.TerminalAt, "failure_class", item.LastFailureClass)
		}
		return nil
	case "list-dead-letters":
		if len(args) != 3 {
			return usage()
		}
		limit, err := parseLimit(args[2])
		if err != nil {
			return err
		}
		var storeID *ids.StoreID
		if args[1] != "unscoped" {
			parsed, parseErr := ids.ParseStoreID(args[1])
			if parseErr != nil {
				return parseErr
			}
			storeID = &parsed
		}
		items, err := delivery.ListDeadLetters(operationCtx, storeID, limit)
		if err != nil {
			return err
		}
		for _, item := range items {
			logger.Info("event dead letter", "dead_letter_id", item.DeadLetterID, "consumer", item.ConsumerName, "event_id", item.EventID, "topic", item.Topic, "partition", item.Partition, "offset", item.MessageOffset, "event_type", item.EventType, "classification", item.Classification, "attempts", item.Attempts, "last_failed_at", item.LastFailedAt)
		}
		return nil
	case "requeue-outbox":
		if len(args) != 4 {
			return usage()
		}
		storeID, eventID, err := parseIDs(args[1], args[2])
		if err != nil {
			return err
		}
		return outbox.Requeue(operationCtx, storeID, eventID, args[3])
	case "replay-dead-letter":
		if len(args) != 4 {
			return usage()
		}
		storeID, deadLetterID, err := parseIDs(args[1], args[2])
		if err != nil {
			return err
		}
		brokers, err := brokerAddresses(lookup)
		if err != nil {
			return err
		}
		letter, err := delivery.LoadReplayable(operationCtx, storeID, deadLetterID)
		if err != nil {
			return err
		}
		message, err := replayMessage(letter, storeID)
		if err != nil {
			return err
		}
		producer, err := eventingkafka.NewProducer(brokers, 10*time.Second)
		if err != nil {
			return err
		}
		defer producer.Close()
		if _, err := producer.Publish(operationCtx, message); err != nil {
			return err
		}
		return delivery.ResolveReplay(operationCtx, storeID, deadLetterID, args[3])
	default:
		return usage()
	}
}

func replayMessage(letter eventingpostgres.ReplayableDeadLetter, storeID ids.StoreID) (eventapplication.Message, error) {
	event, err := eventapplication.Decode(letter.Envelope)
	if err != nil || event.StoreID != storeID {
		return eventapplication.Message{}, errors.New("dead letter no longer has a valid tenant envelope")
	}
	message, err := eventapplication.MessageFor(event)
	if err != nil {
		return eventapplication.Message{}, errors.New("dead letter no longer matches the event contract")
	}
	return message, nil
}

func parseLimit(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return value, nil
}

func parseIDs(storeText, eventText string) (ids.StoreID, ids.EventID, error) {
	storeID, err := ids.ParseStoreID(storeText)
	if err != nil {
		return "", "", err
	}
	eventID, err := ids.ParseEventID(eventText)
	return storeID, eventID, err
}

func brokerAddresses(lookup func(string) (string, bool)) ([]string, error) {
	raw, ok := lookup("KAFKA_BROKERS")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, errors.New("KAFKA_BROKERS is required")
	}
	parts := strings.Split(raw, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if _, _, err := net.SplitHostPort(parts[index]); err != nil {
			return nil, errors.New("KAFKA_BROKERS contains an invalid address")
		}
	}
	return parts, nil
}

func usage() error {
	return fmt.Errorf("usage: event-reconcile status | list-outbox <store-id> <limit> | list-dead-letters <store-id|unscoped> <limit> | requeue-outbox <store-id> <event-id> <reason> | replay-dead-letter <store-id> <dead-letter-id> <reason>")
}
