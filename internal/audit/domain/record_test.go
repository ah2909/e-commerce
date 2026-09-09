package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestRecordRequiresSupportedActor(t *testing.T) {
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	record, err := NewRecord(storeID, ActorPlatformAdmin, "deployment", "store.created", "store", storeID.String(), "request", time.Now())
	if err != nil || record.ActorType != ActorPlatformAdmin || record.ActorID != "deployment" {
		t.Fatalf("NewRecord() = %#v, %v", record, err)
	}
	if _, err := NewRecord(storeID, ActorType("owner"), "actor", "store.created", "store", storeID.String(), "request", time.Now()); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unsupported actor error = %v", err)
	}
}
