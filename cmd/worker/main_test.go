package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type expiryStub struct{ calls atomic.Int64 }

func (stub *expiryStub) ExpireDue(context.Context, int, string) (int, error) {
	stub.calls.Add(1)
	return 0, nil
}

type cartExpiryStub struct{ calls atomic.Int64 }

func (stub *cartExpiryStub) ExpireDue(context.Context, int) (int, error) {
	stub.calls.Add(1)
	return 0, nil
}

func TestRunExpiryStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stub := &expiryStub{}
	done := make(chan struct{})
	go func() {
		runExpiry(ctx, stub, 10, time.Millisecond, time.Second, slog.New(slog.DiscardHandler))
		close(done)
	}()
	deadline := time.After(time.Second)
	for stub.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("expiry processor was not called")
		default:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expiry loop did not stop")
	}
}

func TestRunCartExpiryStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stub := &cartExpiryStub{}
	done := make(chan struct{})
	go func() {
		runCartExpiry(ctx, stub, 10, time.Millisecond, time.Second, slog.New(slog.DiscardHandler))
		close(done)
	}()
	deadline := time.After(time.Second)
	for stub.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("cart expiry processor was not called")
		default:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cart expiry loop did not stop")
	}
}
