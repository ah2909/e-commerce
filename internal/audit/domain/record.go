package domain

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrInvalidRecord = errors.New("invalid audit record")

type Record struct {
	StoreID     ids.StoreID
	AuditID     AuditID
	ActorType   ActorType
	ActorID     string
	Action      string
	SubjectType string
	SubjectID   string
	RequestID   string
	OccurredAt  time.Time
}

type ActorType string

const (
	ActorPlatformAdmin ActorType = "platform_admin"
	ActorUser          ActorType = "user"
)

type AuditID string

func (id AuditID) String() string { return string(id) }

func NewRecord(storeID ids.StoreID, actorType ActorType, actorID, action, subjectType, subjectID, requestID string, occurredAt time.Time) (Record, error) {
	auditID, err := newAuditID()
	if err != nil {
		return Record{}, err
	}
	if storeID == "" || (actorType != ActorPlatformAdmin && actorType != ActorUser) || actorID == "" || subjectID == "" || requestID == "" || occurredAt.IsZero() {
		return Record{}, ErrInvalidRecord
	}
	validSubject := (action == "store.created" && subjectType == "store") || ((action == "membership.created" || action == "membership.updated") && subjectType == "membership") || (action == "inventory.adjusted" && subjectType == "inventory_stock_item")
	if !validSubject {
		return Record{}, ErrInvalidRecord
	}
	return Record{StoreID: storeID, AuditID: auditID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: subjectType, SubjectID: subjectID, RequestID: requestID, OccurredAt: occurredAt.UTC()}, nil
}

func newAuditID() (AuditID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate audit ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return AuditID(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])), nil
}
