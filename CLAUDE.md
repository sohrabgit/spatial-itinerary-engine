# Working on this repo

Read this first. It is loaded automatically at the start of every session.

**What this is:** a local-only engine that turns a sentence — *"3-day historic
walk in Paris with quiet cafés and minimal transit"* — into an optimized, routed
walking itinerary. Self-hosted inference (Ollama), self-hosted routing (OSRM),
raw OpenStreetMap data. **No billed external APIs.** It is a portfolio piece:
the judgment on display matters as much as the features.

- **Where things stand:** [`docs/STATUS.md`](docs/STATUS.md)
- **What to build next:** [`docs/ROADMAP.md`](docs/ROADMAP.md)
- **Why things are the way they are:** [`docs/adr/`](docs/adr/)
- **What we deliberately are not building:** [`docs/NON-GOALS.md`](docs/NON-GOALS.md)

## Orientation

```
services/ingest-go/   OSM ingestion: PBF parse, embeddings, COPY, Tier B inputs
services/api-py/      intent -> retrieval -> OR-Tools solve   (FastAPI, :8000)
services/edge-go/     WebSocket hub                            (empty until M3)
apps/web/             Next.js 16 map + timetable               (:3002)
db/                   migrations + the SQL that computes derived attributes
infra/                custom Postgres and osmium images
docs/adr/             architecture decision records
```

**Go owns the concurrent I/O edge. Python owns the compute.** Split by workload
character, not by feature (ADR 0002). Don't move work across that line without
a reason worth writing down.

## Commands

```bash
make up            # db, cache, osrm-foot
make verify        # asserts the environment is actually correct -- run when confused
make migrate       # apply pending schema
make pipeline      # full data build (~9 min); only after a schema/corpus change
make api           # planner on :8000
make web           # app on :3002   (:3000 is another project of the user's)
make test lint     # everything
```

`make verify` checks the things that fail *silently*: image architecture,
extension presence, halfvec staying under the TOAST threshold, embedding
dimension agreement, container→host Ollama reachability, Dragonfly GEO commands.

## Invariants — breaking these breaks the system quietly

1. **`TravelIntent` stays flat.** No nested models, `$ref`, `anyOf`, or arrays
   of objects. Sub-4B models go from ~3% to ~68% structured-output failure once
   `$defs` appear. `tests/test_schema_flatness.py` enforces this.
2. **Embedding prefixes must match across languages.** `search_document: ` at
   index time (Go, `internal/poi/embedtext.go`), `search_query: ` at query time
   (Python, `app/config.py`). A mismatch costs recall and fails silently.
3. **Route on `snap_geog`, display `geog`.** POI coordinates are building
   centroids; OSRM snaps to the street. Routing from centroids corrupts the
   matrix.
4. **The solver's arc cost includes service time, and the penalty is in
   seconds.** Remove service time and it packs days with 15-minute wall plaques;
   make the penalty too small and it drops every stop and still reports
   `ROUTING_SUCCESS`. Both happened.
5. **`has_wifi` is tri-state.** `NULL` means unknown, and 96% of Paris cafés are
   unknown. Coercing to false empties the filter.
6. **The deterministic pre-pass beats the LLM.** Dates, durations and times are
   regex-owned. Never let a 3B model do arithmetic.
7. **No external runtime dependencies.** Map tiles are the one documented
   exception, behind a config seam. Don't add a glyphs/font/sprite URL.

## Gotchas that have already cost hours

- **maplibre-gl v6 loads its worker as a separate ES module** that Next's
  bundler doesn't emit. Symptom: raster tiles render fine, every GeoJSON source
  hangs `isSourceLoaded() === false` forever, no console error, nothing paints.
  Fixed by `scripts/copy-maplibre-worker.mjs` + `setWorkerUrl()`.
- **No `glyphs` in the map style, so no symbol/text layers.** Adding one leaves
  the style permanently unloaded and nothing renders — same silent failure.
- **OSRM healthcheck must use `127.0.0.1`, not `localhost`.** It binds IPv4
  only; `localhost` resolves to `::1` first and the check fails against a
  working service.
- **Port 5000 is macOS AirPlay Receiver.** OSRM is on 5001.
- **`pg_stat_statements` needs `shared_preload_libraries`**; `CREATE EXTENSION`
  alone leaves the view erroring.
- **`shm_size: 4g` on Postgres** or parallel HNSW builds fail with
  `could not resize shared memory segment`, which doesn't point at shm.
- **Moving the checkout breaks the Python venv** (uv bakes absolute paths):
  `rm -rf services/api-py/.venv && (cd services/api-py && uv sync)`.
- **The Compose project name is pinned** in `docker-compose.yml` so renaming the
  directory doesn't orphan the data volumes.
- **`next lint` was removed in Next 16.** Use the ESLint CLI; `eslint-config-next`
  ships a native flat config that must not be wrapped in `FlatCompat`.

## How to work here

Run the exact CI commands locally before pushing — `make lint && make test`,
plus `cd apps/web && npm run build`. The first CI run caught three failures that
local spot-checks had missed.

Prefer measuring to assuming. Several of this project's load-bearing beliefs
turned out to be wrong when tested (ADR 0004 is the clearest case), and the
measurements are more valuable than the original plan. When something surprises
you, write it down in `docs/STATUS.md` or an ADR.
