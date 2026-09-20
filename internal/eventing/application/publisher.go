package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

type Publisher struct {
	outbox   Outbox
	broker   Broker
	settings PublisherSettings
	now      func() time.Time
}

func NewPublisher(outbox Outbox, broker Broker, settings PublisherSettings, now func() time.Time) (Publisher, error) {
	if outbox == nil || broker == nil || now == nil || settings.Batch < 1 || settings.Batch > 500 || settings.Lease <= 0 || settings.MaxAttempts < 1 || settings.MaxAttempts > 100 || settings.BaseBackoff <= 0 || settings.MaxBackoff < settings.BaseBackoff || settings.PublishTimeout <= 0 {
		return Publisher{}, ErrInvalidMessage
	}
	return Publisher{outbox: outbox, broker: broker, settings: settings, now: now}, nil
}

func (publisher Publisher) ProcessBatch(ctx context.Context) (int, error) {
	claims, err := publisher.outbox.Claim(ctx, publisher.settings.Batch, publisher.settings.Lease)
	if err != nil {
		return 0, fmt.Errorf("claim outbox events: %w", err)
	}
	processed := 0
	for index, claim := range claims {
		if ctx.Err() != nil {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publisher.settings.PublishTimeout)
			err := publisher.outbox.Release(releaseCtx, claims[index:])
			cancel()
			if err != nil {
				return processed, fmt.Errorf("release untouched outbox claims: %w", err)
			}
			return processed, ctx.Err()
		}
		message, messageErr := MessageFor(claim.Event)
		if messageErr != nil {
			digest := sha256.Sum256([]byte(messageErr.Error()))
			if err := publisher.outbox.Fail(ctx, claim, failureClass(messageErr), digest, publisher.now(), true); err != nil {
				return processed, fmt.Errorf("mark terminal outbox event: %w", err)
			}
			processed++
			continue
		}
		publishCtx, cancel := context.WithTimeout(ctx, publisher.settings.PublishTimeout)
		publication, publishErr := publisher.broker.Publish(publishCtx, message)
		cancel()
		if publishErr == nil {
			if err := publisher.outbox.Complete(ctx, claim, message.Topic, publication); err != nil {
				return processed, fmt.Errorf("complete outbox event: %w", err)
			}
			processed++
			continue
		}
		digest := sha256.Sum256([]byte(publishErr.Error()))
		terminal := errors.Is(publishErr, ErrPermanent) || claim.Attempts >= publisher.settings.MaxAttempts
		next := publisher.now().Add(retryDelay(publisher.settings, claim))
		if err := publisher.outbox.Fail(ctx, claim, failureClass(publishErr), digest, next, terminal); err != nil {
			return processed, fmt.Errorf("record outbox failure: %w", err)
		}
		processed++
	}
	return processed, nil
}

func retryDelay(settings PublisherSettings, claim Claim) time.Duration {
	attempt := claim.Attempts
	if attempt < 1 {
		attempt = 1
	}
	delay := settings.BaseBackoff
	for index := 1; index < attempt && delay < settings.MaxBackoff/2; index++ {
		delay *= 2
	}
	if delay > settings.MaxBackoff {
		delay = settings.MaxBackoff
	}
	digest := sha256.Sum256([]byte(claim.Event.ID.String()))
	jitter := time.Duration(int(digest[0]) * int(delay) / 1024)
	if delay+jitter > settings.MaxBackoff {
		return settings.MaxBackoff
	}
	return delay + jitter
}

func failureClass(err error) string {
	if errors.Is(err, ErrUnsupported) {
		return "unsupported"
	}
	if errors.Is(err, ErrPermanent) || errors.Is(err, ErrInvalidMessage) {
		return "permanent"
	}
	return "retryable"
}
