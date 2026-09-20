package application

import (
	"context"
	"errors"
	"time"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrInvalidMessage = errors.New("invalid event message")
	ErrUnsupported    = errors.New("unsupported event")
	ErrPermanent      = errors.New("permanent event failure")
)

const (
	TopicCatalog   = "commerce.catalog.v1"
	TopicInventory = "commerce.inventory.v1"
	TopicOrders    = "commerce.orders.v1"
	TopicPayments  = "commerce.payments.v1"
	TopicShipping  = "commerce.shipping.v1"
)

type Header struct {
	Key   string
	Value []byte
}

type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers []Header
}

type Publication struct {
	Partition int32
	Offset    int64
}

type Broker interface {
	Publish(context.Context, Message) (Publication, error)
}

type Claim struct {
	Event    eventdomain.Event
	Token    ids.EventID
	Attempts int
}

type Outbox interface {
	Claim(context.Context, int, time.Duration) ([]Claim, error)
	Complete(context.Context, Claim, string, Publication) error
	Fail(context.Context, Claim, string, [32]byte, time.Time, bool) error
	Release(context.Context, []Claim) error
}

type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Headers   []Header
	Value     []byte
}

type Delivery interface {
	Process(context.Context, string, eventdomain.Event) error
	RecordDeadLetter(context.Context, DeadLetter) error
	RecordTransient(context.Context, DeadLetter, int) (bool, error)
}

type DeadLetter struct {
	ID             ids.EventID
	StoreID        ids.StoreID
	EventID        ids.EventID
	Consumer       string
	Topic          string
	Partition      int32
	Offset         int64
	Digest         [32]byte
	EventType      string
	Classification string
	Diagnostic     string
	Envelope       []byte
	OccurredAt     time.Time
}

type PublisherSettings struct {
	Batch          int
	Lease          time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	PublishTimeout time.Duration
}
