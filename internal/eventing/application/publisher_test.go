package application

import (
	"context"
	"errors"
	"testing"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
)

type fakeOutbox struct {
	claims    []Claim
	completed int
	failed    int
	terminal  bool
	released  int
}

func (outbox *fakeOutbox) Release(_ context.Context, claims []Claim) error {
	outbox.released += len(claims)
	return nil
}

func (outbox *fakeOutbox) Claim(context.Context, int, time.Duration) ([]Claim, error) {
	return outbox.claims, nil
}
func (outbox *fakeOutbox) Complete(context.Context, Claim, string, Publication) error {
	outbox.completed++
	return nil
}
func (outbox *fakeOutbox) Fail(_ context.Context, _ Claim, _ string, _ [32]byte, _ time.Time, terminal bool) error {
	outbox.failed++
	outbox.terminal = terminal
	return nil
}

type fakeBroker struct{ err error }

func (broker fakeBroker) Publish(context.Context, Message) (Publication, error) {
	return Publication{Partition: 1, Offset: 2}, broker.err
}

type timeoutThenSuccessBroker struct{ calls int }

func (broker *timeoutThenSuccessBroker) Publish(ctx context.Context, _ Message) (Publication, error) {
	broker.calls++
	if broker.calls == 1 {
		<-ctx.Done()
		return Publication{}, ctx.Err()
	}
	return Publication{Partition: 1, Offset: 2}, nil
}

func TestPublisherCompletesAndClassifiesFailures(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	event, err := eventdomain.New(testStore, testEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, now, "request-1", validOrderPayload())
	if err != nil {
		t.Fatal(err)
	}
	settings := PublisherSettings{Batch: 10, Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute, PublishTimeout: time.Second}
	tests := []struct {
		name      string
		brokerErr error
		attempts  int
		complete  int
		failed    int
		terminal  bool
	}{
		{name: "success", complete: 1, attempts: 1},
		{name: "retryable", brokerErr: errors.New("offline"), attempts: 1, failed: 1},
		{name: "exhausted", brokerErr: errors.New("offline"), attempts: 3, failed: 1, terminal: true},
		{name: "permanent", brokerErr: ErrPermanent, attempts: 1, failed: 1, terminal: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outbox := &fakeOutbox{claims: []Claim{{Event: event, Token: testEvent, Attempts: test.attempts}}}
			publisher, err := NewPublisher(outbox, fakeBroker{err: test.brokerErr}, settings, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			processed, err := publisher.ProcessBatch(context.Background())
			if err != nil || processed != 1 || outbox.completed != test.complete || outbox.failed != test.failed || outbox.terminal != test.terminal {
				t.Fatalf("processed=%d completed=%d failed=%d terminal=%t error=%v", processed, outbox.completed, outbox.failed, outbox.terminal, err)
			}
		})
	}
}

func TestPublisherUsesIndependentAttemptDeadlines(t *testing.T) {
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	first, _ := eventdomain.New(testStore, testEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, now, "request-1", validOrderPayload())
	second, _ := eventdomain.New(testStore, testSecondEvent, "order.created.v1", "order", "33333333-3333-4333-8333-333333333333", 1, now, "request-2", validOrderPayload())
	outbox := &fakeOutbox{claims: []Claim{{Event: first, Token: testEvent, Attempts: 1}, {Event: second, Token: testSecondEvent, Attempts: 1}}}
	broker := &timeoutThenSuccessBroker{}
	settings := PublisherSettings{Batch: 10, Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute, PublishTimeout: 10 * time.Millisecond}
	publisher, _ := NewPublisher(outbox, broker, settings, func() time.Time { return now })
	processed, err := publisher.ProcessBatch(context.Background())
	if err != nil || processed != 2 || broker.calls != 2 || outbox.failed != 1 || outbox.completed != 1 || outbox.terminal {
		t.Fatalf("processed=%d calls=%d failed=%d completed=%d terminal=%t error=%v", processed, broker.calls, outbox.failed, outbox.completed, outbox.terminal, err)
	}
}
