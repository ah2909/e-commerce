#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${BACKUP_OUTPUT:?BACKUP_OUTPUT is required}"
: "${BACKUP_AGE_RECIPIENT:?BACKUP_AGE_RECIPIENT is required}"

umask 077
case "$BACKUP_OUTPUT" in
  *.age) ;;
  *) echo "BACKUP_OUTPUT must end in .age" >&2; exit 2 ;;
esac

pg_dump --dbname "$DATABASE_URL" --format=custom --no-owner --no-privileges | age --recipient "$BACKUP_AGE_RECIPIENT" --output "$BACKUP_OUTPUT"
sha256sum "$BACKUP_OUTPUT" > "$BACKUP_OUTPUT.sha256"
