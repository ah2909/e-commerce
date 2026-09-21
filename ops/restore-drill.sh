#!/bin/sh
set -eu

: "${RESTORE_DATABASE_URL:?RESTORE_DATABASE_URL is required}"
: "${BACKUP_INPUT:?BACKUP_INPUT is required}"
: "${BACKUP_AGE_IDENTITY:?BACKUP_AGE_IDENTITY is required}"

case "$RESTORE_DATABASE_URL" in
  postgres://*@localhost:*/*_restore_test*|postgresql://*@localhost:*/*_restore_test*|postgres://*@127.0.0.1:*/*_restore_test*|postgresql://*@127.0.0.1:*/*_restore_test*) ;;
  *) echo "restore target must be a loopback database ending in _restore_test" >&2; exit 2 ;;
esac

sha256sum --check "$BACKUP_INPUT.sha256"
age --decrypt --identity "$BACKUP_AGE_IDENTITY" "$BACKUP_INPUT" | pg_restore --dbname "$RESTORE_DATABASE_URL" --clean --if-exists --no-owner --no-privileges
psql "$RESTORE_DATABASE_URL" -v ON_ERROR_STOP=1 -c "SELECT max(version_id) AS schema_version FROM goose_db_version WHERE is_applied"
psql "$RESTORE_DATABASE_URL" -v ON_ERROR_STOP=1 -c "SELECT count(*) AS invalid_orders FROM orders o LEFT JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id WHERE p.payment_id IS NULL"
psql "$RESTORE_DATABASE_URL" -v ON_ERROR_STOP=1 -c "SELECT count(*) AS negative_stock FROM inventory_stock_items WHERE on_hand < 0 OR reserved < 0 OR reserved > on_hand"
