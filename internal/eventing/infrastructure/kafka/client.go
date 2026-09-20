package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

type Producer struct{ client *kgo.Client }

func NewProducer(brokers []string, timeout time.Duration) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(int32(eventapplication.MaxMessageBytes+4096)),
		kgo.ProduceRequestTimeout(timeout),
		kgo.RecordRetries(3),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Kafka producer: %w", err)
	}
	return &Producer{client: client}, nil
}

func (producer *Producer) Close() { producer.client.Close() }

func (producer *Producer) Publish(ctx context.Context, message eventapplication.Message) (eventapplication.Publication, error) {
	headers := make([]kgo.RecordHeader, 0, len(message.Headers))
	for _, header := range message.Headers {
		headers = append(headers, kgo.RecordHeader{Key: header.Key, Value: header.Value})
	}
	record := &kgo.Record{Topic: message.Topic, Key: message.Key, Value: message.Value, Headers: headers}
	result := producer.client.ProduceSync(ctx, record)[0]
	if result.Err != nil {
		var kafkaError *kerr.Error
		if errors.As(result.Err, &kafkaError) && !kafkaError.Retriable {
			return eventapplication.Publication{}, fmt.Errorf("%w: %w", eventapplication.ErrPermanent, result.Err)
		}
		return eventapplication.Publication{}, fmt.Errorf("publish Kafka event: %w", result.Err)
	}
	return eventapplication.Publication{Partition: result.Record.Partition, Offset: result.Record.Offset}, nil
}

type Processor interface {
	Process(context.Context, eventapplication.Record) error
}

type ConsumerSettings struct {
	Brokers []string
	Group   string
	Topics  []string
	Timeout time.Duration
}

func RunConsumer(ctx context.Context, settings ConsumerSettings, processor Processor) error {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ConsumerGroup(settings.Group),
		kgo.ConsumeTopics(settings.Topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchMaxBytes(int32(eventapplication.MaxMessageBytes+4096)),
		kgo.FetchMaxPartitionBytes(int32(eventapplication.MaxMessageBytes+4096)),
	)
	if err != nil {
		return fmt.Errorf("initialize Kafka consumer %s: %w", settings.Group, err)
	}
	defer client.CloseAllowingRebalance()
	for {
		fetches := client.PollRecords(ctx, 1)
		if ctx.Err() != nil {
			return nil
		}
		if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
			return fmt.Errorf("poll Kafka group %s: %w", settings.Group, fetchErrors[0].Err)
		}
		var processErr error
		fetches.EachRecord(func(record *kgo.Record) {
			if processErr != nil {
				return
			}
			operationCtx, cancel := context.WithTimeout(ctx, settings.Timeout)
			headers := make([]eventapplication.Header, 0, len(record.Headers))
			for _, header := range record.Headers {
				headers = append(headers, eventapplication.Header{Key: header.Key, Value: header.Value})
			}
			processErr = processor.Process(operationCtx, eventapplication.Record{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, Key: record.Key, Headers: headers, Value: record.Value})
			cancel()
			if processErr == nil {
				processErr = client.CommitRecords(ctx, record)
			}
		})
		client.AllowRebalance()
		if processErr != nil {
			return fmt.Errorf("process Kafka group %s: %w", settings.Group, processErr)
		}
	}
}
