package main

import (
	"testing"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestParseLimitBoundsInspection(t *testing.T) {
	for _, value := range []string{"0", "101", "invalid"} {
		if _, err := parseLimit(value); err == nil {
			t.Fatalf("parseLimit(%q) succeeded", value)
		}
	}
	if value, err := parseLimit("100"); err != nil || value != 100 {
		t.Fatalf("value=%d error=%v", value, err)
	}
}

func TestReplayUsesCanonicalRouteInsteadOfOriginalBadTopic(t *testing.T) {
	storeID := ids.StoreID("11111111-1111-4111-8111-111111111111")
	eventID := ids.EventID("22222222-2222-4222-8222-222222222222")
	productID := "33333333-3333-4333-8333-333333333333"
	event, _ := eventdomain.New(storeID, eventID, "product.published.v1", "product", productID, 1, time.Now().UTC(), "replay", map[string]any{"store_id": storeID.String(), "product_id": productID, "version": 1})
	envelope, _ := eventapplication.Encode(event)
	message, err := replayMessage(eventingpostgres.ReplayableDeadLetter{ID: eventID, StoreID: storeID, Envelope: envelope, Topic: eventapplication.TopicOrders}, storeID)
	if err != nil || message.Topic != eventapplication.TopicCatalog {
		t.Fatalf("topic=%q error=%v", message.Topic, err)
	}
}
