//go:build integration

package kafka

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type captureProcessor struct {
	eventID ids.EventID
	done    chan eventapplication.Record
}

type failTargetProcessor struct{ eventID ids.EventID }

func (processor failTargetProcessor) Process(_ context.Context, record eventapplication.Record) error {
	event, err := eventapplication.Decode(record.Value)
	if err == nil && event.ID == processor.eventID {
		return errors.New("injected effect failure")
	}
	return nil
}

type terminalProgressDelivery struct {
	processed chan eventdomain.Event
	letters   chan eventapplication.DeadLetter
}

type rebalanceProcessor struct {
	role       string
	first      string
	second     string
	entered    chan struct{}
	release    chan struct{}
	secondSeen chan struct{}
}

func (processor rebalanceProcessor) Process(_ context.Context, record eventapplication.Record) error {
	event, err := eventapplication.Decode(record.Value)
	if err != nil {
		return nil
	}
	if event.CorrelationID == processor.first && processor.role == "first" {
		select {
		case processor.entered <- struct{}{}:
		default:
		}
		<-processor.release
		return nil
	}
	if event.CorrelationID == processor.second {
		if processor.role == "first" {
			return errors.New("injected member departure")
		}
		select {
		case processor.secondSeen <- struct{}{}:
		default:
		}
	}
	return nil
}

func (delivery terminalProgressDelivery) Process(_ context.Context, _ string, event eventdomain.Event) error {
	delivery.processed <- event
	return nil
}

func (delivery terminalProgressDelivery) RecordDeadLetter(_ context.Context, letter eventapplication.DeadLetter) error {
	delivery.letters <- letter
	return nil
}

func (terminalProgressDelivery) RecordTransient(context.Context, eventapplication.DeadLetter, int) (bool, error) {
	return false, nil
}

func (processor captureProcessor) Process(_ context.Context, record eventapplication.Record) error {
	event, err := eventapplication.Decode(record.Value)
	if err == nil && event.ID == processor.eventID {
		select {
		case processor.done <- record:
		default:
		}
	}
	return nil
}

func TestKafkaPublishesAndCommitsConsumerDelivery(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	if len(brokers) == 1 && strings.TrimSpace(brokers[0]) == "" {
		t.Fatal("KAFKA_BROKERS is required")
	}
	producer, err := NewProducer(brokers, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	storeID, _ := ids.NewStoreID()
	eventID, _ := ids.NewEventID()
	aggregateID, _ := ids.NewOrderID()
	guestID, _ := ids.NewGuestID()
	event, err := eventdomain.New(storeID, eventID, "order.created.v1", "order", aggregateID.String(), 1, time.Now().UTC(), "kafka-integration", map[string]any{"store_id": storeID.String(), "order_id": aggregateID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	if err != nil {
		t.Fatal(err)
	}
	message, err := eventapplication.MessageFor(event)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := producer.Publish(context.Background(), message)
	if err != nil || publication.Partition < 0 || publication.Offset < 0 {
		t.Fatalf("publication=%#v error=%v", publication, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan eventapplication.Record, 1)
	returned := make(chan error, 1)
	go func() {
		returned <- RunConsumer(ctx, ConsumerSettings{Brokers: brokers, Group: "eventing-integration-" + eventID.String(), Topics: []string{eventapplication.TopicOrders}, Timeout: 5 * time.Second}, captureProcessor{eventID: eventID, done: done})
	}()
	select {
	case record := <-done:
		if record.Topic != eventapplication.TopicOrders || record.Partition != publication.Partition || record.Offset != publication.Offset {
			t.Fatalf("record=%#v publication=%#v", record, publication)
		}
		if string(record.Key) != string(message.Key) || len(record.Headers) != len(message.Headers) {
			t.Fatalf("transport contract not preserved: %#v", record)
		}
		time.Sleep(250 * time.Millisecond)
		cancel()
	case err := <-returned:
		t.Fatalf("consumer returned early: %v", err)
	case <-ctx.Done():
		t.Fatal("consumer did not receive published event")
	}
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
}

func TestKafkaRedeliversAfterProcessingFailureAndIsolatesGroups(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	if len(brokers) == 1 && strings.TrimSpace(brokers[0]) == "" {
		t.Fatal("KAFKA_BROKERS is required")
	}
	producer, err := NewProducer(brokers, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	storeID, _ := ids.NewStoreID()
	eventID, _ := ids.NewEventID()
	orderID, _ := ids.NewOrderID()
	guestID, _ := ids.NewGuestID()
	event, _ := eventdomain.New(storeID, eventID, "order.created.v1", "order", orderID.String(), 1, time.Now().UTC(), "kafka-redelivery", map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	message, _ := eventapplication.MessageFor(event)
	if _, err := producer.Publish(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	group := "eventing-redelivery-" + eventID.String()
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = RunConsumer(firstCtx, ConsumerSettings{Brokers: brokers, Group: group, Topics: []string{eventapplication.TopicOrders}, Timeout: 5 * time.Second}, failTargetProcessor{eventID: eventID})
	firstCancel()
	if err == nil {
		t.Fatal("injected processing failure did not stop consumer")
	}
	assertGroupReceives(t, brokers, group, eventID)
	assertGroupReceives(t, brokers, "eventing-isolated-"+eventID.String(), eventID)
}

func assertGroupReceives(t *testing.T, brokers []string, group string, eventID ids.EventID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan eventapplication.Record, 1)
	returned := make(chan error, 1)
	go func() {
		returned <- RunConsumer(ctx, ConsumerSettings{Brokers: brokers, Group: group, Topics: []string{eventapplication.TopicOrders}, Timeout: 5 * time.Second}, captureProcessor{eventID: eventID, done: done})
	}()
	select {
	case <-done:
		time.Sleep(250 * time.Millisecond)
		cancel()
	case err := <-returned:
		t.Fatalf("consumer returned before target: %v", err)
	case <-ctx.Done():
		t.Fatal("consumer did not receive target event")
	}
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
}

func TestKafkaTerminalRecordDoesNotStarveFollowingRecord(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	producer, err := NewProducer(brokers, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	storeID, _ := ids.NewStoreID()
	eventID, _ := ids.NewEventID()
	productID, _ := ids.NewProductID()
	product, _ := eventdomain.New(storeID, eventID, "product.published.v1", "product", productID.String(), 1, time.Now().UTC(), "wrong-topic", map[string]any{"store_id": storeID.String(), "product_id": productID.String(), "version": 1})
	wrong, _ := eventapplication.MessageFor(product)
	wrong.Topic = eventapplication.TopicOrders
	if _, err := producer.Publish(context.Background(), wrong); err != nil {
		t.Fatal(err)
	}
	orderID, _ := ids.NewOrderID()
	guestID, _ := ids.NewGuestID()
	order, _ := eventdomain.New(storeID, eventID, "order.created.v1", "order", orderID.String(), 1, time.Now().UTC(), "after-terminal", map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	valid, _ := eventapplication.MessageFor(order)
	if _, err := producer.Publish(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	delivery := terminalProgressDelivery{processed: make(chan eventdomain.Event, 100), letters: make(chan eventapplication.DeadLetter, 100)}
	consumer, _ := eventapplication.NewConsumer("terminal-progress", delivery, 2, time.Now)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	returned := make(chan error, 1)
	go func() {
		returned <- RunConsumer(ctx, ConsumerSettings{Brokers: brokers, Group: "eventing-terminal-" + eventID.String(), Topics: []string{eventapplication.TopicOrders}, Timeout: 5 * time.Second}, consumer)
	}()
	for {
		select {
		case letter := <-delivery.letters:
			if letter.EventID == eventID {
				if letter.Classification != "permanent" {
					t.Fatalf("letter=%#v", letter)
				}
				goto classified
			}
		case <-ctx.Done():
			t.Fatal("wrong-topic record was not classified")
		}
	}
classified:
	for {
		select {
		case got := <-delivery.processed:
			if got.ID == eventID && got.CorrelationID == "after-terminal" {
				goto progressed
			}
		case <-ctx.Done():
			t.Fatal("valid record after terminal record was starved")
		}
	}
progressed:
	time.Sleep(250 * time.Millisecond)
	cancel()
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
}

func TestKafkaProducerRecoversWhenBootstrapAccessReturns(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := reservation.Addr().String()
	_ = reservation.Close()
	producer, err := NewProducer([]string{proxyAddress}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	storeID, _ := ids.NewStoreID()
	eventID, _ := ids.NewEventID()
	orderID, _ := ids.NewOrderID()
	guestID, _ := ids.NewGuestID()
	event, _ := eventdomain.New(storeID, eventID, "order.created.v1", "order", orderID.String(), 1, time.Now().UTC(), "broker-recovery", map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
	message, _ := eventapplication.MessageFor(event)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, publishErr := producer.Publish(ctx, message); result <- publishErr }()
	select {
	case err := <-result:
		t.Fatalf("publish returned before recovery: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	stopProxy := startTCPProxy(t, proxyAddress, brokers[0])
	defer stopProxy()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("publish did not recover after broker access returned")
	}
}

func TestKafkaRebalancesSafelyWhileRecordIsInFlight(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	producer, err := NewProducer(brokers, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	storeID, _ := ids.NewStoreID()
	firstID, _ := ids.NewEventID()
	secondID, _ := ids.NewEventID()
	orderID, _ := ids.NewOrderID()
	guestID, _ := ids.NewGuestID()
	makeMessage := func(eventID ids.EventID, correlation string) eventapplication.Message {
		event, _ := eventdomain.New(storeID, eventID, "order.created.v1", "order", orderID.String(), 1, time.Now().UTC(), correlation, map[string]any{"store_id": storeID.String(), "order_id": orderID.String(), "owner_kind": "guest", "guest_id": guestID.String()})
		message, _ := eventapplication.MessageFor(event)
		return message
	}
	firstCorrelation := "rebalance-first-" + firstID.String()
	secondCorrelation := "rebalance-second-" + secondID.String()
	first := makeMessage(firstID, firstCorrelation)
	second := makeMessage(secondID, secondCorrelation)
	second.Key = first.Key
	if _, err := producer.Publish(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Publish(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	entered, release, secondSeen := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	group := "eventing-rebalance-" + firstID.String()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	firstReturned := make(chan error, 1)
	go func() {
		firstReturned <- RunConsumer(ctx, ConsumerSettings{Brokers: brokers, Group: group, Topics: []string{eventapplication.TopicOrders}, Timeout: 10 * time.Second}, rebalanceProcessor{role: "first", first: firstCorrelation, second: secondCorrelation, entered: entered, release: release, secondSeen: secondSeen})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first member did not start target record")
	}
	secondReturned := make(chan error, 1)
	go func() {
		secondReturned <- RunConsumer(ctx, ConsumerSettings{Brokers: brokers, Group: group, Topics: []string{eventapplication.TopicOrders}, Timeout: 10 * time.Second}, rebalanceProcessor{role: "second", first: firstCorrelation, second: secondCorrelation, entered: entered, release: release, secondSeen: secondSeen})
	}()
	time.Sleep(time.Second)
	close(release)
	select {
	case <-secondSeen:
	case <-ctx.Done():
		t.Fatal("replacement member did not resume the partition")
	}
	time.Sleep(250 * time.Millisecond)
	cancel()
	firstErr, secondErr := <-firstReturned, <-secondReturned
	if firstErr != nil && !strings.Contains(firstErr.Error(), "injected member departure") {
		t.Fatal(firstErr)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
}

func startTCPProxy(t *testing.T, address, upstreamAddress string) func() {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			downstream, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				upstream, dialErr := net.Dial("tcp", upstreamAddress)
				if dialErr != nil {
					_ = downstream.Close()
					return
				}
				defer upstream.Close()
				defer downstream.Close()
				go func() { _, _ = io.Copy(upstream, downstream) }()
				_, _ = io.Copy(downstream, upstream)
			}()
		}
	}()
	return func() { _ = listener.Close(); <-done }
}
