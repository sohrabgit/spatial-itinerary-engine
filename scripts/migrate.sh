#!/usr/bin/env bash
# Applies db/migrations/*.sql in order, exactly once each.
# No migration framework: numbered files plus a ledger table is enough here, and
# it keeps the SQL readable as the primary artifact.
set -euo pipefail

PG_USER="${POSTGRES_USER:-app}"
PG_DB="${POSTGRES_DB:-itinerary}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

psq() { docker compose exec -T db psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" "$@"; }

psq -q -c "CREATE TABLE IF NOT EXISTS schema_migrations (
             version text PRIMARY KEY,
             applied_at timestamptz NOT NULL DEFAULT now(),
             sha256 text NOT NULL);"

applied=0
for f in "$DIR"/db/migrations/*.sql; do
  v="$(basename "$f")"
  sum="$(shasum -a 256 "$f" | awk '{print $1}')"
  prev="$(psq -tAc "SELECT sha256 FROM schema_migrations WHERE version='${v}'" | tr -d '\r')"

  if [ -z "$prev" ]; then
    echo "applying ${v}"
    psq -q < "$f"
    psq -q -c "INSERT INTO schema_migrations (version, sha256) VALUES ('${v}','${sum}')"
    applied=$((applied + 1))
  elif [ "$prev" != "$sum" ]; then
    # Editing an applied migration means dev and CI silently diverge.
    echo "ERROR: ${v} was already applied but its contents changed." >&2
    echo "       Add a new migration instead of editing this one." >&2
    echo "       (During M1 iteration: 'make nuke' to reset from scratch.)" >&2
    exit 1
  fi
done

[ "$applied" -eq 0 ] && echo "schema up to date" || echo "applied ${applied} migration(s)"
psq -tAc "SELECT count(*)||' tables' FROM information_schema.tables WHERE table_schema='public'"
