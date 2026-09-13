//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestCustomersCartsAndCheckoutOrdersMigrationsUpgradePopulatedSchema(t *testing.T) {
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseguard.ValidateConfig(poolConfig.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, _ := ids.NewStoreID()
	user, _ := ids.NewUserID()
	guest, _ := ids.NewGuestID()
	product, _ := ids.NewProductID()
	variant, _ := ids.NewVariantID()
	legacyOrder, _ := ids.NewOrderID()
	schema := "customers_carts_" + strings.ReplaceAll(store.String(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	connectionConfig := poolConfig.ConnConfig.Copy()
	if connectionConfig.RuntimeParams == nil {
		connectionConfig.RuntimeParams = map[string]string{}
	}
	connectionConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*connectionConfig)
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	migrations := filepath.Join("..", "..", "migrations")
	if err := goose.UpToContext(ctx, db, migrations, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1::uuid,'customer-cart','Customer Cart','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, store); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1::uuid,$2::uuid,'a@example.com','a@example.com','active',now(),now())`, store, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identity_guests(store_id,guest_id,created_at,expires_at) VALUES($1::uuid,$2::uuid,now(),now()+interval '30 days')`, store, guest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_products(store_id,product_id,name,slug,description,status,published_at,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,'Product','product','', 'published',now(),1,now(),now())`, store, product); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_variants(store_id,variant_id,product_id,sku,option_values,weight_grams,status,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,'CART','{}',0,'active',1,now(),now())`, store, variant, product); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO inventory_stock_items(store_id,variant_id,on_hand,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,2,1,now(),now())`, store, variant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO inventory_reservations(store_id,reservation_id,order_id,variant_id,quantity,status,expires_at,version,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),$2::uuid,$3::uuid,1,'active',now()+interval '10 minutes',1,now(),now())`, store, legacyOrder, variant); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, migrations, 6); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 6)
	for _, table := range []string{"customers", "customer_addresses", "carts", "cart_lines"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'user',$2::uuid,'VND','active',1,now()+interval '30 days',now(),now())`, store, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO carts(store_id,cart_id,owner_kind,user_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'user',$2::uuid,'VND','active',1,now()+interval '30 days',now(),now())`, store, user); err == nil {
		t.Fatal("duplicate active user cart accepted")
	}
	assertCustomersCartsConstraints(t, ctx, db, store, user, guest, variant)
	if err := goose.UpToContext(ctx, db, migrations, 6); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 6)
	if err := goose.UpToContext(ctx, db, migrations, 7); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 7)
	for _, table := range []string{"shipping_rules", "orders", "order_lines", "order_addresses", "order_guest_grants", "order_idempotency", "order_state_history", "payments"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	assertCheckoutOrdersConstraints(t, ctx, db, store, legacyOrder)
	if err := goose.UpToContext(ctx, db, migrations, 7); err != nil {
		t.Fatal(err)
	}
	assertMigrationVersion(t, ctx, db, 7)
}

func assertCheckoutOrdersConstraints(t *testing.T, ctx context.Context, db *sql.DB, store ids.StoreID, legacyOrder ids.OrderID) {
	t.Helper()
	for _, index := range []string{"shipping_rules_current_uidx", "orders_user_history_idx", "orders_guest_history_idx", "orders_pending_expiry_idx", "order_guest_grants_active_order_uidx", "order_state_history_order_idx"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s exists = %v, error = %v", index, exists, err)
		}
	}
	for table, constraints := range map[string][]string{
		"shipping_rules":     {"shipping_rules_store_fk", "shipping_rules_mode_valid", "shipping_rules_amount_valid", "shipping_rules_currency_valid", "shipping_rules_lifecycle_valid"},
		"orders":             {"orders_user_fk", "orders_guest_fk", "orders_customer_fk", "orders_cart_fk", "orders_shipping_rule_fk", "orders_number_unique", "orders_cart_unique", "orders_owner_valid", "orders_totals_valid", "orders_lifecycle_valid"},
		"order_lines":        {"order_lines_order_fk", "order_lines_variant_fk", "order_lines_quantity_valid", "order_lines_totals_valid"},
		"order_guest_grants": {"order_guest_grants_order_fk", "order_guest_grants_digest_valid", "order_guest_grants_expiry_valid"},
		"order_idempotency":  {"order_idempotency_order_fk", "order_idempotency_key_valid", "order_idempotency_fingerprint_valid"},
		"payments":           {"payments_order_fk", "payments_order_unique", "payments_provider_valid", "payments_amount_valid", "payments_lifecycle_valid"},
		"carts":              {"carts_converted_order_fk", "carts_conversion_valid"},
	} {
		for _, constraint := range constraints {
			var exists bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=$1 AND conrelid=(current_schema()||'.'||$2)::regclass)`, constraint, table).Scan(&exists); err != nil || !exists {
				t.Fatalf("constraint %s exists = %v, error = %v", constraint, exists, err)
			}
		}
	}
	for constraint, expected := range map[string]bool{"carts_converted_order_fk": true, "carts_conversion_valid": false} {
		var validated bool
		if err := db.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint WHERE conname=$1 AND conrelid=(current_schema()||'.carts')::regclass`, constraint).Scan(&validated); err != nil || validated != expected {
			t.Fatalf("constraint %s validated = %v, expected %v, error = %v", constraint, validated, expected, err)
		}
	}
	var legacyCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_reservations WHERE store_id=$1::uuid AND order_id=$2::uuid`, store, legacyOrder).Scan(&legacyCount); err != nil || legacyCount != 1 {
		t.Fatalf("legacy reservation count = %d, error = %v", legacyCount, err)
	}
	expectSQLFailure(t, ctx, db, `INSERT INTO shipping_rules(store_id,shipping_rule_id,mode,flat_amount_minor,currency,status,version,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'free',1,'VND','active',1,now(),now())`, store)
}

func assertCustomersCartsConstraints(t *testing.T, ctx context.Context, db *sql.DB, store ids.StoreID, user ids.UserID, guest ids.GuestID, variant ids.VariantID) {
	t.Helper()
	for _, index := range []string{"customers_contact_idx", "customer_addresses_customer_idx", "carts_active_user_uidx", "carts_active_guest_uidx", "carts_active_expiry_idx", "cart_lines_cart_idx"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass(current_schema()||'.'||$1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s exists = %v, error = %v", index, exists, err)
		}
	}
	for table, constraints := range map[string][]string{
		"customers":          {"customers_user_fk", "customers_user_unique", "customers_phone_valid"},
		"customer_addresses": {"customer_addresses_customer_fk", "customer_addresses_phone_valid", "customer_addresses_country_valid"},
		"carts":              {"carts_user_fk", "carts_guest_fk", "carts_owner_valid", "carts_currency_valid", "carts_status_valid", "carts_expiry_valid", "carts_lifecycle_valid"},
		"cart_lines":         {"cart_lines_cart_fk", "cart_lines_variant_fk", "cart_lines_quantity_valid"},
	} {
		for _, constraint := range constraints {
			var exists bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=$1 AND conrelid=(current_schema()||'.'||$2)::regclass)`, constraint, table).Scan(&exists); err != nil || !exists {
				t.Fatalf("constraint %s exists = %v, error = %v", constraint, exists, err)
			}
		}
	}
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'user','VND','active',1,now()+interval '30 days',now(),now())`, store)
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,user_id,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'user',$2::uuid,$3::uuid,'VND','active',1,now()+interval '30 days',now(),now())`, store, user, guest)
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'guest',$2::uuid,'USD','active',1,now()+interval '30 days',now(),now())`, store, guest)
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at,closed_at) VALUES($1::uuid,gen_random_uuid(),'guest',$2::uuid,'VND','active',1,now()+interval '30 days',now(),now(),now())`, store, guest)
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'guest',$2::uuid,'VND','expired',1,now()+interval '30 days',now(),now())`, store, guest)
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'guest',$2::uuid,'VND','active',1,now(),now(),now())`, store, guest)
	guestCart, _ := ids.NewCartID()
	if _, err := db.ExecContext(ctx, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,$2::uuid,'guest',$3::uuid,'VND','active',1,now()+interval '30 days',now(),now())`, store, guestCart, guest); err != nil {
		t.Fatal(err)
	}
	expectSQLFailure(t, ctx, db, `INSERT INTO carts(store_id,cart_id,owner_kind,guest_id,currency,status,version,expires_at,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),'guest',$2::uuid,'VND','active',1,now()+interval '30 days',now(),now())`, store, guest)
	customer, _ := ids.NewCustomerID()
	if _, err := db.ExecContext(ctx, `INSERT INTO customers(store_id,customer_id,user_id,full_name,email,canonical_email,phone,canonical_phone,version,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,'A','a@example.com','a@example.com','0901234567','+84901234567',1,now(),now())`, store, customer, user); err != nil {
		t.Fatal(err)
	}
	otherStore, _ := ids.NewStoreID()
	otherUser, _ := ids.NewUserID()
	if _, err := db.ExecContext(ctx, `INSERT INTO stores(store_id,subdomain,name,status,default_currency,locale,timezone,settings_version,created_at,updated_at) VALUES($1::uuid,'customer-cart-other','Other','active','VND','vi-VN','Asia/Ho_Chi_Minh',1,now(),now())`, otherStore); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users(store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES($1::uuid,$2::uuid,'other@example.com','other@example.com','active',now(),now())`, otherStore, otherUser); err != nil {
		t.Fatal(err)
	}
	expectSQLFailure(t, ctx, db, `INSERT INTO customers(store_id,customer_id,user_id,full_name,email,canonical_email,phone,canonical_phone,version,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),$2::uuid,'B','b@example.com','b@example.com','0901234567','+84901234567',1,now(),now())`, store, otherUser)
	expectSQLFailure(t, ctx, db, `INSERT INTO customer_addresses(store_id,address_id,customer_id,label,recipient_name,phone,canonical_phone,line1,line2,ward,district,province,postal_code,country_code,version,created_at,updated_at) VALUES($1::uuid,gen_random_uuid(),$2::uuid,'Home','A','0901234567','+84901234567','1 Main','','Ward','District','Province','','VN',1,now(),now())`, otherStore, customer)
	expectSQLFailure(t, ctx, db, `INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,0,now(),now())`, store, guestCart, variant)
	expectSQLFailure(t, ctx, db, `INSERT INTO cart_lines(store_id,cart_id,variant_id,quantity,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,1,now(),now())`, otherStore, guestCart, variant)
}

func expectSQLFailure(t *testing.T, ctx context.Context, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement, args...); err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", statement)
	}
}
