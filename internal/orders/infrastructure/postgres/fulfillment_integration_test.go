//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	auditpostgres "github.com/ah2909/e-commerce/internal/audit/infrastructure/postgres"
	eventingpostgres "github.com/ah2909/e-commerce/internal/eventing/infrastructure/postgres"
	inventorypostgres "github.com/ah2909/e-commerce/internal/inventory/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingapplication "github.com/ah2909/e-commerce/internal/shipping/application"
	shippingdomain "github.com/ah2909/e-commerce/internal/shipping/domain"
	shippingpostgres "github.com/ah2909/e-commerce/internal/shipping/infrastructure/postgres"
	storespostgres "github.com/ah2909/e-commerce/internal/stores/infrastructure/postgres"
	"github.com/ah2909/e-commerce/internal/testsupport/storefixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFulfillmentLifecycleIsIdempotentAtomicAndTenantScoped(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := application.NewService(orders)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "fulfillment-checkout"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	shipping := orders.shipping.(*shippingpostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	shipping.WithFulfillment(orders, stores, auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := shippingapplication.NewFulfillmentService(shipping)
	if detail, err := service.GetOrder(context.Background(), pair.A.Store.ID(), checkout.Order.ID); err != nil || detail.State != domain.StatePaid {
		t.Fatalf("paid detail = %#v, error=%v", detail, err)
	}
	create := shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "  Viettel   Post ", TrackingNumber: "VP123"}, IdempotencyKey: "prepare-shipment", RequestID: "prepare-shipment"}
	shipment, created, err := service.Mutate(context.Background(), create)
	if err != nil || !created || shipment.State != shippingdomain.ShipmentProcessing || shipment.Carrier != "Viettel Post" || shipment.Version != 1 {
		t.Fatalf("create shipment = %#v, created=%v, error=%v", shipment, created, err)
	}
	detail, err := service.GetOrder(context.Background(), pair.A.Store.ID(), checkout.Order.ID)
	if err != nil || detail.State != domain.StateProcessing || detail.Shipment == nil || detail.Shipment.OrderID != checkout.Order.ID {
		t.Fatalf("processing detail = %#v, error=%v", detail, err)
	}
	if _, err := service.GetOrder(context.Background(), pair.B.Store.ID(), checkout.Order.ID); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("foreign detail error = %v", err)
	}
	if _, err := orderService.GetForUser(context.Background(), pair.A.Store.ID(), checkout.Order.ID, pair.B.UserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("foreign shopper error = %v", err)
	}
	listed, next, err := service.ListOrders(context.Background(), pair.A.Store.ID(), "processing", 1, "")
	if err != nil || len(listed) != 1 || listed[0].ID != checkout.Order.ID || next != "" {
		t.Fatalf("processing queue = %#v, next=%q, error=%v", listed, next, err)
	}
	listed, _, err = service.ListOrders(context.Background(), pair.B.Store.ID(), "", 25, "")
	if err != nil || len(listed) != 0 {
		t.Fatalf("foreign queue = %#v, error=%v", listed, err)
	}
	replay, created, err := service.Mutate(context.Background(), create)
	if err != nil || created || replay.ID != shipment.ID || replay.State != shipment.State || replay.Version != shipment.Version || !replay.CreatedAt.Equal(shipment.CreatedAt) {
		t.Fatalf("create replay = %#v, created=%v, error=%v", replay, created, err)
	}
	changed := create
	changed.Details.TrackingNumber = "CHANGED"
	if _, _, err := service.Mutate(context.Background(), changed); !errors.Is(err, shippingapplication.ErrConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	foreign := create
	foreign.StoreID, foreign.ActorID, foreign.IdempotencyKey = pair.B.Store.ID(), pair.B.UserID, "foreign-shipment"
	if _, _, err := service.Mutate(context.Background(), foreign); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("foreign shipment error = %v", err)
	}
	update := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationUpdate, Details: shippingdomain.Details{Carrier: "VNPost", TrackingNumber: "VN456"}, ExpectedVersion: 1, IdempotencyKey: "update-shipment", RequestID: "update-shipment"}
	shipment, _, err = service.Mutate(context.Background(), update)
	if err != nil || shipment.Version != 2 || shipment.TrackingNumber != "VN456" {
		t.Fatalf("update shipment = %#v, error=%v", shipment, err)
	}
	foreignUpdate := update
	foreignUpdate.StoreID, foreignUpdate.ActorID, foreignUpdate.IdempotencyKey = pair.B.Store.ID(), pair.B.UserID, "foreign-update"
	if _, _, err := service.Mutate(context.Background(), foreignUpdate); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("foreign update error = %v", err)
	}
	if _, _, err := service.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationDispatch, ExpectedVersion: 1, IdempotencyKey: "stale-dispatch", RequestID: "stale-dispatch"}); !errors.Is(err, shippingapplication.ErrConflict) {
		t.Fatalf("stale dispatch error = %v", err)
	}
	dispatch := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationDispatch, ExpectedVersion: 2, IdempotencyKey: "dispatch-shipment", RequestID: "dispatch-shipment"}
	foreignDispatch := dispatch
	foreignDispatch.StoreID, foreignDispatch.ActorID, foreignDispatch.IdempotencyKey = pair.B.Store.ID(), pair.B.UserID, "foreign-dispatch"
	if _, _, err := service.Mutate(context.Background(), foreignDispatch); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("foreign dispatch error = %v", err)
	}
	shipment, _, err = service.Mutate(context.Background(), dispatch)
	if err != nil || shipment.State != shippingdomain.ShipmentShipped || shipment.Version != 3 || shipment.ShippedAt == nil {
		t.Fatalf("dispatch shipment = %#v, error=%v", shipment, err)
	}
	if detail, err := service.GetOrder(context.Background(), pair.A.Store.ID(), checkout.Order.ID); err != nil || detail.State != domain.StateShipped {
		t.Fatalf("shipped detail = %#v, error=%v", detail, err)
	}
	complete := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationComplete, ExpectedVersion: 3, IdempotencyKey: "complete-shipment", RequestID: "complete-shipment"}
	foreignComplete := complete
	foreignComplete.StoreID, foreignComplete.ActorID, foreignComplete.IdempotencyKey = pair.B.Store.ID(), pair.B.UserID, "foreign-complete"
	if _, _, err := service.Mutate(context.Background(), foreignComplete); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("foreign complete error = %v", err)
	}
	shipment, _, err = service.Mutate(context.Background(), complete)
	if err != nil || shipment.State != shippingdomain.ShipmentCompleted || shipment.Version != 4 || shipment.CompletedAt == nil {
		t.Fatalf("complete shipment = %#v, error=%v", shipment, err)
	}
	order, err := orderService.GetForUser(context.Background(), pair.A.Store.ID(), checkout.Order.ID, pair.A.UserID)
	if err != nil || order.State != domain.StateCompleted || order.Shipment == nil || order.Shipment.ID != shipment.ID {
		t.Fatalf("completed order = %#v, error=%v", order, err)
	}
	if _, err := service.GetOrder(context.Background(), pair.A.Store.ID(), checkout.Order.ID); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("completed fulfillment detail error = %v", err)
	}
	listed, next, err = service.ListOrders(context.Background(), pair.A.Store.ID(), "shipped", 25, "")
	if err != nil || len(listed) != 0 || next != "" {
		t.Fatalf("shipped queue = %d, next=%q, error=%v", len(listed), next, err)
	}
	var commands, audits, shipmentEvents, shippedEvents, completedEvents int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM shipment_commands WHERE store_id=$1 AND shipment_id=$2),(SELECT count(*) FROM audit_records WHERE store_id=$1 AND subject_id=$2),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND payload->>'shipment_id'=$2::text),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_type='order.shipped.v1' AND payload->>'order_id'=$3),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_type='order.completed.v1' AND payload->>'order_id'=$3)`, pair.A.Store.ID(), shipment.ID, checkout.Order.ID).Scan(&commands, &audits, &shipmentEvents, &shippedEvents, &completedEvents); err != nil {
		t.Fatal(err)
	}
	if commands != 4 || audits != 4 || shipmentEvents != 4 || shippedEvents != 1 || completedEvents != 1 {
		t.Fatalf("commands/audits/events/shipped/completed = %d/%d/%d/%d/%d", commands, audits, shipmentEvents, shippedEvents, completedEvents)
	}
	otherFixture := insertAdditionalCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	other, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, otherFixture.addressID, "fulfillment-command-binding"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO shipment_commands(store_id,idempotency_key,operation,request_fingerprint,shipment_id,order_id,result_state,result_carrier,result_tracking_number,result_version,result_created_at,result_updated_at,result_shipped_at,result_completed_at,created_at) VALUES($1,'mismatched-command','complete',decode(repeat('42',32),'hex'),$2,$3,'completed','VNPost','VN456',4,now(),now(),now(),now(),now())`, pair.A.Store.ID(), shipment.ID, other.Order.ID)
	if err == nil {
		t.Fatal("mismatched shipment/order command was accepted")
	}
	if _, err := service.GetOrder(context.Background(), pair.A.Store.ID(), other.Order.ID); !errors.Is(err, shippingapplication.ErrNotFound) {
		t.Fatalf("pending fulfillment detail error = %v", err)
	}
}

func TestFulfillmentRollsBackWhenEventAppendFails(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := application.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "fulfillment-rollback"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	shipping := orders.shipping.(*shippingpostgres.Repository)
	shipping.WithFulfillment(orders, storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()), auditpostgres.NewWriter(), failingEvents{})
	_, _, err = shippingapplication.NewFulfillmentService(shipping).Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "ROLLBACK1"}, IdempotencyKey: "rollback-shipment", RequestID: "rollback-shipment"})
	if !errors.Is(err, errInjectedEvent) {
		t.Fatalf("rollback error = %v", err)
	}
	var state string
	var shipments, commands, audits int
	if err := pool.QueryRow(context.Background(), `SELECT state,(SELECT count(*) FROM shipments WHERE store_id=$1 AND order_id=$2),(SELECT count(*) FROM shipment_commands WHERE store_id=$1 AND order_id=$2),(SELECT count(*) FROM audit_records WHERE store_id=$1 AND action='shipment.prepared' AND subject_id=$2) FROM orders WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&state, &shipments, &commands, &audits); err != nil {
		t.Fatal(err)
	}
	if state != "paid" || shipments != 0 || commands != 0 || audits != 0 {
		t.Fatalf("rollback state/effects = %s/%d/%d/%d", state, shipments, commands, audits)
	}
}

func TestFulfillmentRollsBackWhenCommandResultInsertFails(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := application.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "command-insert-rollback"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	if _, err := pool.Exec(context.Background(), `
		CREATE OR REPLACE FUNCTION reject_test_shipment_command() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.idempotency_key = 'rollback-command-insert' THEN
				RAISE EXCEPTION 'injected shipment command failure';
			END IF;
			RETURN NEW;
		END $$;
		DROP TRIGGER IF EXISTS reject_test_shipment_command_trigger ON shipment_commands;
		CREATE TRIGGER reject_test_shipment_command_trigger BEFORE INSERT ON shipment_commands FOR EACH ROW EXECUTE FUNCTION reject_test_shipment_command()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS reject_test_shipment_command_trigger ON shipment_commands; DROP FUNCTION IF EXISTS reject_test_shipment_command()`)
	})
	shipping := orders.shipping.(*shippingpostgres.Repository)
	shipping.WithFulfillment(orders, storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	_, _, err = shippingapplication.NewFulfillmentService(shipping).Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "COMMAND-FAIL"}, IdempotencyKey: "rollback-command-insert", RequestID: "rollback-command-insert"})
	if err == nil {
		t.Fatal("command insert failure was not returned")
	}
	var state string
	var shipments, commands, history, audits, events int
	if err := pool.QueryRow(context.Background(), `SELECT state,(SELECT count(*) FROM shipments WHERE store_id=$1 AND order_id=$2),(SELECT count(*) FROM shipment_commands WHERE store_id=$1 AND order_id=$2),(SELECT count(*) FROM order_state_history WHERE store_id=$1 AND order_id=$2 AND reason='shipment_prepared'),(SELECT count(*) FROM audit_records WHERE store_id=$1 AND request_id='rollback-command-insert'),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND correlation_id='rollback-command-insert') FROM orders WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&state, &shipments, &commands, &history, &audits, &events); err != nil {
		t.Fatal(err)
	}
	if state != "paid" || shipments != 0 || commands != 0 || history != 0 || audits != 0 || events != 0 {
		t.Fatalf("command failure rollback = %s shipments=%d commands=%d history=%d audits=%d events=%d", state, shipments, commands, history, audits, events)
	}
}

func TestConcurrentFulfillmentCommandsProduceOneLifecycle(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := application.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 3).addressID, "concurrent-fulfillment"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	shipping := orders.shipping.(*shippingpostgres.Repository)
	shipping.WithFulfillment(orders, storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := shippingapplication.NewFulfillmentService(shipping)
	blocker := orders.db.Begin()
	if err := blocker.Exec(`SELECT 1 FROM orders WHERE store_id=? AND order_id=? FOR UPDATE`, pair.A.Store.ID(), checkout.Order.ID).Error; err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		shipment shippingdomain.Shipment
		err      error
	}
	results := make([]outcome, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			command := shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "RACE" + string(rune('1'+index))}, IdempotencyKey: "race-create-" + string(rune('1'+index)), RequestID: "race-create"}
			results[index].shipment, _, results[index].err = service.Mutate(context.Background(), command)
		}(index)
	}
	waitForBlockedQueries(t, pool, "orders", 2)
	if err := blocker.Commit().Error; err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	successes := 0
	var shipment shippingdomain.Shipment
	for _, result := range results {
		if result.err == nil {
			successes++
			shipment = result.shipment
		} else if !errors.Is(result.err, shippingapplication.ErrUnprocessable) && !errors.Is(result.err, shippingapplication.ErrConflict) {
			t.Fatalf("concurrent create error = %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent create successes = %d", successes)
	}
	for _, transition := range []struct {
		mutation shippingapplication.Mutation
		version  int64
		state    shippingdomain.ShipmentState
	}{{shippingapplication.MutationDispatch, 1, shippingdomain.ShipmentShipped}, {shippingapplication.MutationComplete, 2, shippingdomain.ShipmentCompleted}} {
		blocker = orders.db.Begin()
		if err := blocker.Exec(`SELECT 1 FROM orders WHERE store_id=? AND order_id=? FOR UPDATE`, pair.A.Store.ID(), checkout.Order.ID).Error; err != nil {
			t.Fatal(err)
		}
		results = make([]outcome, 2)
		for index := range results {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				command := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: transition.mutation, ExpectedVersion: transition.version, IdempotencyKey: string(transition.mutation) + "-race-" + string(rune('1'+index)), RequestID: "transition-race"}
				results[index].shipment, _, results[index].err = service.Mutate(context.Background(), command)
			}(index)
		}
		waitForBlockedQueries(t, pool, "orders", 2)
		if err := blocker.Commit().Error; err != nil {
			t.Fatal(err)
		}
		wait.Wait()
		successes = 0
		for _, result := range results {
			if result.err == nil {
				successes++
				shipment = result.shipment
			} else if !errors.Is(result.err, shippingapplication.ErrConflict) && !errors.Is(result.err, shippingapplication.ErrUnprocessable) {
				t.Fatalf("concurrent %s error = %v", transition.mutation, result.err)
			}
		}
		if successes != 1 || shipment.State != transition.state {
			t.Fatalf("concurrent %s successes/state = %d/%s", transition.mutation, successes, shipment.State)
		}
	}
	var shipments, commands, shipped, completed int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM shipments WHERE store_id=$1 AND order_id=$2),(SELECT count(*) FROM shipment_commands WHERE store_id=$1 AND shipment_id=$3),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_type='order.shipped.v1' AND payload->>'order_id'=$2::text),(SELECT count(*) FROM outbox_events WHERE store_id=$1 AND event_type='order.completed.v1' AND payload->>'order_id'=$2::text)`, pair.A.Store.ID(), checkout.Order.ID, shipment.ID).Scan(&shipments, &commands, &shipped, &completed); err != nil {
		t.Fatal(err)
	}
	if shipments != 1 || commands != 3 || shipped != 1 || completed != 1 {
		t.Fatalf("concurrent effects = shipments=%d commands=%d shipped=%d completed=%d", shipments, commands, shipped, completed)
	}
}

func TestDispatchAndCancellationSerializeToOneTerminalPath(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := application.NewService(orders)
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
	checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "dispatch-cancel-race"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	shipping := orders.shipping.(*shippingpostgres.Repository)
	shipping.WithFulfillment(orders, storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	fulfillment := shippingapplication.NewFulfillmentService(shipping)
	shipment, _, err := fulfillment.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "RACE-CANCEL"}, IdempotencyKey: "race-cancel-create", RequestID: "race-cancel-create"})
	if err != nil {
		t.Fatal(err)
	}
	blocker := orders.db.Begin()
	if err := blocker.Exec(`SELECT 1 FROM orders WHERE store_id=? AND order_id=? FOR UPDATE`, pair.A.Store.ID(), checkout.Order.ID).Error; err != nil {
		t.Fatal(err)
	}
	dispatchDone := make(chan error, 1)
	cancelDone := make(chan error, 1)
	go func() {
		_, _, err := fulfillment.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationDispatch, ExpectedVersion: 1, IdempotencyKey: "dispatch-cancel-dispatch", RequestID: "dispatch-cancel-dispatch"})
		dispatchDone <- err
	}()
	go func() {
		_, err := orderService.Cancel(context.Background(), application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorType: application.CancellationWorkforce, ActorID: pair.A.UserID.String(), IdempotencyKey: "dispatch-cancel-cancel", Reason: "Customer request", RequestID: "dispatch-cancel-cancel"})
		cancelDone <- err
	}()
	waitForBlockedQueries(t, pool, "orders", 2)
	if err := blocker.Commit().Error; err != nil {
		t.Fatal(err)
	}
	dispatchErr, cancelErr := <-dispatchDone, <-cancelDone
	if (dispatchErr == nil) == (cancelErr == nil) {
		t.Fatalf("dispatch/cancel errors = %v/%v", dispatchErr, cancelErr)
	}
	if dispatchErr != nil && !errors.Is(dispatchErr, shippingapplication.ErrUnprocessable) {
		t.Fatalf("dispatch race error = %v", dispatchErr)
	}
	if cancelErr != nil && !errors.Is(cancelErr, application.ErrUnprocessable) {
		t.Fatalf("cancellation race error = %v", cancelErr)
	}
	var orderState, shipmentState string
	var refunds, shippedEvents int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,s.state,(SELECT count(*) FROM refunds WHERE store_id=o.store_id AND order_id=o.order_id),(SELECT count(*) FROM outbox_events WHERE store_id=o.store_id AND event_type='order.shipped.v1' AND payload->>'order_id'=o.order_id::text) FROM orders o JOIN shipments s USING(store_id,order_id) WHERE o.store_id=$1 AND o.order_id=$2`, pair.A.Store.ID(), checkout.Order.ID).Scan(&orderState, &shipmentState, &refunds, &shippedEvents); err != nil {
		t.Fatal(err)
	}
	if dispatchErr == nil && (orderState != "shipped" || shipmentState != "shipped" || refunds != 0 || shippedEvents != 1) {
		t.Fatalf("dispatch outcome = %s/%s refunds=%d shipped_events=%d", orderState, shipmentState, refunds, shippedEvents)
	}
	if cancelErr == nil && (orderState != "refund_pending" || shipmentState != "processing" || refunds != 1 || shippedEvents != 0) {
		t.Fatalf("cancel outcome = %s/%s refunds=%d shipped_events=%d", orderState, shipmentState, refunds, shippedEvents)
	}
}

func TestFulfillmentQueuePaginatesAndLocksCandidates(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	orderService := application.NewService(orders)
	created := make(map[ids.OrderID]bool, 3)
	var lockedOrder domain.Order
	for index := range 3 {
		var fixture checkoutFixture
		if index == 0 {
			fixture = insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
		} else {
			fixture = insertAdditionalCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2)
		}
		checkout, err := orderService.Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, "queue-page-"+string(rune('1'+index))))
		if err != nil {
			t.Fatal(err)
		}
		markOrderPaidForFulfillment(t, orders, checkout.Order)
		created[checkout.Order.ID] = true
		lockedOrder = checkout.Order
	}
	shipping := orders.shipping.(*shippingpostgres.Repository)
	shipping.WithFulfillment(orders, storespostgres.NewRepository(orders.db, auditpostgres.NewWriter()), auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := shippingapplication.NewFulfillmentService(shipping)
	first, cursor, err := service.ListOrders(context.Background(), pair.A.Store.ID(), "paid", 2, "")
	if err != nil || len(first) != 2 || cursor == "" {
		t.Fatalf("first page = %d cursor=%q error=%v", len(first), cursor, err)
	}
	second, next, err := service.ListOrders(context.Background(), pair.A.Store.ID(), "paid", 2, cursor)
	if err != nil || len(second) != 1 || next != "" {
		t.Fatalf("second page = %d cursor=%q error=%v", len(second), next, err)
	}
	seen := map[ids.OrderID]bool{}
	for _, order := range append(first, second...) {
		if seen[order.ID] || !created[order.ID] {
			t.Fatalf("unexpected or duplicate paged order %s", order.ID)
		}
		seen[order.ID] = true
	}
	tx := orders.db.Begin()
	rows, err := orders.lockFulfillmentOrderIDs(tx, pair.A.Store.ID(), domain.StatePaid, 10, time.Time{}, "")
	if err != nil || len(rows) != 3 {
		_ = tx.Rollback().Error
		t.Fatalf("locked candidates = %#v error=%v", rows, err)
	}
	cancelDone := make(chan error, 1)
	go func() {
		_, err := orderService.Cancel(context.Background(), application.CancellationCommand{StoreID: pair.A.Store.ID(), OrderID: lockedOrder.ID, ActorType: application.CancellationWorkforce, ActorID: pair.A.UserID.String(), IdempotencyKey: "queue-lock-cancel", Reason: "Customer request", RequestID: "queue-lock-cancel"})
		cancelDone <- err
	}()
	waitForBlockedQueries(t, pool, "orders", 1)
	loaded, err := orders.load(tx, pair.A.Store.ID(), lockedOrder.ID)
	if err != nil || loaded.State != domain.StatePaid {
		_ = tx.Rollback().Error
		t.Fatalf("locked projection = %#v error=%v", loaded, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("post-read cancellation error = %v", err)
	}
}

func TestFulfillmentRevalidatesStaffPermissionMembershipAndStore(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	shipping := orders.shipping.(*shippingpostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	shipping.WithFulfillment(orders, stores, auditpostgres.NewWriter(), eventingpostgres.NewWriter())
	service := shippingapplication.NewFulfillmentService(shipping)
	staffID, _ := ids.NewUserID()
	for _, statement := range []string{
		`INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1,$2,'fulfillment-staff@example.com','fulfillment-staff@example.com','active',now(),now())`,
		`INSERT INTO store_memberships(store_id,user_id,status,role,created_at,updated_at) VALUES($1,$2,'active','staff',now(),now())`,
		`INSERT INTO store_membership_permission_sets(store_id,user_id,version,updated_at) VALUES($1,$2,1,now())`,
		`INSERT INTO store_membership_permissions(store_id,user_id,permission,version,created_at,updated_at) VALUES($1,$2,'order.fulfill',1,now(),now())`,
	} {
		if _, err := pool.Exec(context.Background(), statement, pair.A.Store.ID(), staffID); err != nil {
			t.Fatal(err)
		}
	}
	fixture := insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 4)
	prepare := func(key string, fixture checkoutFixture) domain.Order {
		checkout, err := application.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, fixture.addressID, key))
		if err != nil {
			t.Fatal(err)
		}
		markOrderPaidForFulfillment(t, orders, checkout.Order)
		return checkout.Order
	}
	first := prepare("staff-allowed", fixture)
	if _, _, err := service.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: first.ID, ActorID: staffID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "STAFF1"}, IdempotencyKey: "staff-allowed", RequestID: "staff-allowed"}); err != nil {
		t.Fatalf("granted staff error = %v", err)
	}
	if _, err := stores.ReplaceStaffPermissions(context.Background(), pair.A.Store.ID(), staffID, nil, 1, pair.A.UserID, "revoke-fulfillment", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second := prepare("staff-revoked", insertAdditionalCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2))
	if _, _, err := service.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: second.ID, ActorID: staffID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "STAFF2"}, IdempotencyKey: "staff-revoked", RequestID: "staff-revoked"}); !errors.Is(err, shippingapplication.ErrForbidden) {
		t.Fatalf("revoked staff error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE store_memberships SET status='inactive' WHERE store_id=$1 AND user_id=$2`, pair.A.Store.ID(), staffID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: second.ID, ActorID: staffID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "STAFF3"}, IdempotencyKey: "staff-inactive", RequestID: "staff-inactive"}); !errors.Is(err, shippingapplication.ErrForbidden) {
		t.Fatalf("inactive staff error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='suspended' WHERE store_id=$1`, pair.A.Store.ID()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: second.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "SUSPENDED"}, IdempotencyKey: "store-suspended", RequestID: "store-suspended"}); !errors.Is(err, shippingapplication.ErrForbidden) {
		t.Fatalf("suspended store error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stores SET status='active' WHERE store_id=$1`, pair.A.Store.ID()); err != nil {
		t.Fatal(err)
	}
	var effects int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM shipments WHERE store_id=$1 AND order_id=$2`, pair.A.Store.ID(), second.ID).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("denied shipment effects = %d, error=%v", effects, err)
	}
}

func TestFulfillmentUpdateDispatchAndCompletionRollBackAtEventBoundary(t *testing.T) {
	pool := orderPool(t)
	pair := storefixture.NewPair(t, pool)
	orders := orderRepository(t, pool, eventingpostgres.NewWriter())
	checkout, err := application.NewService(orders).Checkout(context.Background(), userCheckoutCommand(pair.A.Store.ID(), pair.A.UserID, insertCheckoutFixture(t, pool, pair.A.Store.ID(), pair.A.UserID, 2).addressID, "fulfillment-transition-rollback"))
	if err != nil {
		t.Fatal(err)
	}
	markOrderPaidForFulfillment(t, orders, checkout.Order)
	shipping := orders.shipping.(*shippingpostgres.Repository)
	stores := storespostgres.NewRepository(orders.db, auditpostgres.NewWriter())
	good := func() shippingapplication.FulfillmentService {
		shipping.WithFulfillment(orders, stores, auditpostgres.NewWriter(), eventingpostgres.NewWriter())
		return shippingapplication.NewFulfillmentService(shipping)
	}
	failing := func() shippingapplication.FulfillmentService {
		shipping.WithFulfillment(orders, stores, auditpostgres.NewWriter(), failingEvents{})
		return shippingapplication.NewFulfillmentService(shipping)
	}
	shipment, _, err := good().Mutate(context.Background(), shippingapplication.Command{StoreID: pair.A.Store.ID(), OrderID: checkout.Order.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationCreate, Details: shippingdomain.Details{Carrier: "Carrier", TrackingNumber: "ROLLBACK2"}, IdempotencyKey: "rollback-create-good", RequestID: "rollback-create-good"})
	if err != nil {
		t.Fatal(err)
	}
	update := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationUpdate, Details: shippingdomain.Details{Carrier: "Changed", TrackingNumber: "ROLLBACK3"}, ExpectedVersion: 1, IdempotencyKey: "rollback-update", RequestID: "rollback-update"}
	if _, _, err := failing().Mutate(context.Background(), update); !errors.Is(err, errInjectedEvent) {
		t.Fatalf("update rollback error = %v", err)
	}
	assertFulfillmentState(t, pool, pair.A.Store.ID(), checkout.Order.ID, shipment.ID, "processing", "processing", 1, 1)
	update.IdempotencyKey = "update-good"
	shipment, _, err = good().Mutate(context.Background(), update)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationDispatch, ExpectedVersion: 2, IdempotencyKey: "rollback-dispatch", RequestID: "rollback-dispatch"}
	if _, _, err := failing().Mutate(context.Background(), dispatch); !errors.Is(err, errInjectedEvent) {
		t.Fatalf("dispatch rollback error = %v", err)
	}
	assertFulfillmentState(t, pool, pair.A.Store.ID(), checkout.Order.ID, shipment.ID, "processing", "processing", 2, 2)
	dispatch.IdempotencyKey = "dispatch-good"
	shipment, _, err = good().Mutate(context.Background(), dispatch)
	if err != nil {
		t.Fatal(err)
	}
	complete := shippingapplication.Command{StoreID: pair.A.Store.ID(), ShipmentID: shipment.ID, ActorID: pair.A.UserID, Mutation: shippingapplication.MutationComplete, ExpectedVersion: 3, IdempotencyKey: "rollback-complete", RequestID: "rollback-complete"}
	if _, _, err := failing().Mutate(context.Background(), complete); !errors.Is(err, errInjectedEvent) {
		t.Fatalf("completion rollback error = %v", err)
	}
	assertFulfillmentState(t, pool, pair.A.Store.ID(), checkout.Order.ID, shipment.ID, "shipped", "shipped", 3, 3)
}

func assertFulfillmentState(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, orderID ids.OrderID, shipmentID ids.ShipmentID, orderState, shipmentState string, version int64, commands int) {
	t.Helper()
	var gotOrder, gotShipment string
	var gotVersion int64
	var gotCommands int
	if err := pool.QueryRow(context.Background(), `SELECT o.state,s.state,s.version,(SELECT count(*) FROM shipment_commands c WHERE c.store_id=o.store_id AND c.shipment_id=s.shipment_id) FROM orders o JOIN shipments s USING(store_id,order_id) WHERE o.store_id=$1 AND o.order_id=$2 AND s.shipment_id=$3`, storeID, orderID, shipmentID).Scan(&gotOrder, &gotShipment, &gotVersion, &gotCommands); err != nil {
		t.Fatal(err)
	}
	if gotOrder != orderState || gotShipment != shipmentState || gotVersion != version || gotCommands != commands {
		t.Fatalf("state/version/commands = %s/%s/%d/%d", gotOrder, gotShipment, gotVersion, gotCommands)
	}
}

func markOrderPaidForFulfillment(t *testing.T, repository *Repository, order domain.Order) {
	t.Helper()
	tx := repository.db.Begin()
	locked, err := repository.LockForPaymentInTransaction(context.Background(), tx, order.StoreID, order.ID)
	if err == nil {
		err = repository.inventory.(*inventorypostgres.Repository).ConfirmOrderInTransaction(context.Background(), tx, order.StoreID, order.ID, time.Now().UTC(), "fulfillment-payment")
	}
	if err == nil {
		err = tx.Exec(`UPDATE payments SET state='succeeded',version=version+1,updated_at=clock_timestamp(),closed_at=clock_timestamp() WHERE store_id=? AND payment_id=? AND state='pending'`, order.StoreID, order.Payment.ID).Error
	}
	if err == nil {
		err = repository.MarkPaidInTransaction(context.Background(), tx, order.StoreID, locked, order.Payment.ID, time.Now().UTC(), "fulfillment-payment")
	}
	if err != nil {
		_ = tx.Rollback().Error
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
}

func insertAdditionalCheckoutFixture(t *testing.T, pool *pgxpool.Pool, storeID ids.StoreID, userID ids.UserID, stock int64) checkoutFixture {
	t.Helper()
	productID, _ := ids.NewProductID()
	variantID, _ := ids.NewVariantID()
	priceListID, _ := ids.NewPriceListID()
	cartID, _ := ids.NewCartID()
	var addressID string
	if err := pool.QueryRow(context.Background(), `SELECT a.address_id FROM customer_addresses a JOIN customers c USING(store_id,customer_id) WHERE a.store_id=$1 AND c.user_id=$2 AND a.archived_at IS NULL LIMIT 1`, storeID, userID).Scan(&addressID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,published_at,version,created_at,updated_at) VALUES($1,$2,'Fulfillment product',$3,'','published',$4,1,$4,$4)`, []any{storeID, productID, "fulfillment-" + productID.String(), now}},
		{`INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES($1,$2,$3,$4,'{}',0,'active',1,$5,$5)`, []any{storeID, variantID, productID, "FULFILL-" + strings.ToUpper(variantID.String()), now}},
		{`INSERT INTO pricing_price_lists(store_id,price_list_id,name,channel,currency,priority,status,version,created_at,updated_at) VALUES($1,$2,'Fulfillment','storefront','VND',1,'active',1,$3,$3)`, []any{storeID, priceListID, now}},
		{`INSERT INTO pricing_prices(store_id,price_list_id,variant_id,amount_minor,version,created_at,updated_at) VALUES($1,$2,$3,10000,1,$4,$4)`, []any{storeID, priceListID, variantID, now}},
		{`INSERT INTO inventory_stock_items(store_id,variant_id,on_hand,version,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, []any{storeID, variantID, stock, now}},
		{`INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1,$2,'user',$3,'VND','active',2,$4::timestamptz+interval '1 day',$4,$4)`, []any{storeID, cartID, userID, now}},
		{`INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, []any{storeID, cartID, variantID, now}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(context.Background(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return checkoutFixture{productID: productID, variantID: variantID, addressID: ids.AddressID(addressID)}
}
