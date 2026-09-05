#!/usr/bin/env bash
# Asserts the environment is actually correct, not merely running.
# Every check here corresponds to a failure mode that is silent if unchecked.
set -uo pipefail

PG_IMAGE="${PG_IMAGE:-travel-postgres:17-3.6}"
PG_USER="${POSTGRES_USER:-app}"
PG_DB="${POSTGRES_DB:-itinerary}"
OLLAMA_BASE_URL="${OLLAMA_BASE_URL:-http://host.docker.internal:11434}"
OLLAMA_EMBED_DIM="${OLLAMA_EMBED_DIM:-768}"
OLLAMA_EMBED_MODEL="${OLLAMA_EMBED_MODEL:-nomic-embed-text}"

fail=0
pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; }
warn() { printf '  \033[33mWARN\033[0m  %s\n' "$1"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; fail=1; }
psq()  { docker compose exec -T db psql -U "$PG_USER" -d "$PG_DB" -tAc "$1" 2>/dev/null | tr -d '\r'; }

echo "== image =="
arch=$(docker image inspect "$PG_IMAGE" --format '{{.Architecture}}' 2>/dev/null)
host_arch=$(uname -m); [ "$host_arch" = "x86_64" ] && host_arch=amd64
[ "$host_arch" = "arm64" ] && want=arm64 || want=amd64
if [ "$arch" = "$want" ]; then pass "postgres image is ${arch}-native"
else bad "postgres image is '${arch}', host is '${want}' -- it will run under emulation"; fi

echo "== postgres =="
if [ -n "$(psq 'select 1')" ]; then
  pass "reachable (PostgreSQL $(psq 'show server_version'))"
  for ext in postgis vector pg_trgm btree_gist pg_stat_statements; do
    v=$(psq "select extversion from pg_extension where extname='${ext}'")
    [ -n "$v" ] && pass "extension ${ext} ${v}" || bad "extension ${ext} MISSING"
  done

  # halfvec must stay under the ~2000-byte TOAST threshold, or every brute-force
  # scan in the Class-1 query path pays a detoast per row.
  sz=$(psq "select pg_column_size(array_fill(0.1::real,ARRAY[${OLLAMA_EMBED_DIM}])::vector::halfvec)")
  if [ -n "$sz" ] && [ "$sz" -lt 2000 ]; then pass "halfvec(${OLLAMA_EMBED_DIM}) = ${sz} bytes, inline (TOAST threshold ~2000)"
  else bad "halfvec(${OLLAMA_EMBED_DIM}) = ${sz:-?} bytes -- will be TOASTed out of line"; fi

  # Embedding dim drift produces garbage similarity scores, never an error.
  dim=$(psq "select atttypmod from pg_attribute a join pg_class c on c.oid=a.attrelid where c.relname='poi_embedding' and a.attname='embedding'")
  if [ -z "$dim" ]; then warn "poi_embedding not created yet (expected before M1)"
  elif [ "$dim" = "$OLLAMA_EMBED_DIM" ]; then pass "poi_embedding dim ${dim} matches OLLAMA_EMBED_DIM"
  else bad "poi_embedding dim ${dim} != OLLAMA_EMBED_DIM ${OLLAMA_EMBED_DIM} -- similarity scores would be garbage"; fi
else
  bad "postgres unreachable (is 'make up' running?)"
fi

echo "== dragonfly =="
if [ "$(docker compose exec -T cache redis-cli ping 2>/dev/null | tr -d '\r')" = "PONG" ]; then
  pass "reachable"
  mem=$(docker compose exec -T cache redis-cli INFO memory 2>/dev/null | awk -F: '/^used_memory_human/{print $2}' | tr -d '\r')
  pass "used_memory ${mem} (defaults would reserve ~3.5GB without --proactor_threads)"
  docker compose exec -T cache redis-cli GEOADD __verify 2.3364 48.8606 x >/dev/null 2>&1
  hit=$(docker compose exec -T cache redis-cli GEOSEARCH __verify FROMLONLAT 2.3364 48.8606 BYRADIUS 10 m ASC 2>/dev/null | tr -d '\r')
  docker compose exec -T cache redis-cli DEL __verify >/dev/null 2>&1
  [ "$hit" = "x" ] && pass "GEOADD/GEOSEARCH work" || bad "GEO commands not working"
else
  bad "dragonfly unreachable"
fi

echo "== ollama (host-native) =="
probe=$(docker compose exec -T db bash -c "getent hosts host.docker.internal >/dev/null && echo ok" 2>/dev/null | tr -d '\r')
if curl -sf --max-time 3 "${OLLAMA_BASE_URL/host.docker.internal/localhost}/api/tags" >/dev/null 2>&1; then
  models=$(curl -s --max-time 3 "${OLLAMA_BASE_URL/host.docker.internal/localhost}/api/tags" | jq -r '.models[].name' 2>/dev/null | tr '\n' ' ')
  pass "reachable from host; models: ${models:-<none pulled>}"
  # Resolving is not the same as reachable. ADR 0003 depends on containers
  # actually talking to the host daemon, so assert the HTTP round trip.
  if [ "$probe" = "ok" ] && docker compose exec -T db bash -c \
       'exec 3<>/dev/tcp/host.docker.internal/11434 && printf "GET /api/tags HTTP/1.0\r\n\r\n" >&3 && head -1 <&3' \
       2>/dev/null | grep -q '200 OK'; then
    pass "containers reach host Ollama over host.docker.internal (HTTP 200)"
  else
    bad "containers cannot reach host Ollama -- ADR 0003 assumption broken"
  fi
else
  warn "ollama not reachable at ${OLLAMA_BASE_URL} (not installed yet -- required from M1)"
fi

echo
[ "$fail" -eq 0 ] && echo -e "\033[32mverify: OK\033[0m" || { echo -e "\033[31mverify: FAILED\033[0m"; exit 1; }
