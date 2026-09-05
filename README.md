# Spatial Travel Recommendation Engine

Turns an unstructured prompt — *"3-day historic walk in Paris with quiet cafes and
minimal transit"* — into an optimized, time-bounded daily itinerary with real
turn-by-turn walking routes, then keeps it live, re-ranking suggestions as the
traveller moves.

**Runs entirely locally. No billed external APIs.** Self-hosted inference
(Ollama), self-hosted routing (OSRM), raw OpenStreetMap data, $0/month.

> **Status: M0 — skeleton.** The data layer, ingestion, AI and real-time paths
> land in M1–M4. See [the milestone plan](#milestones).

## Architecture

```
                    ┌───── HOST (macOS, Metal GPU) ─────┐
                    │  ollama  :11434                   │
                    └──────────▲──────────▲─────────────┘
                               │          │  host.docker.internal
  ┌──────────┐  WS + REST  ┌───┴────┐ SSE ├─────────┐
  │ web      │◄───────────►│ edge-go│◄───►│ api-py  │
  │ Next.js  │             │  :8080 │     │  :8000  │
  └──────────┘             └─┬──────┘     └─┬─────┬─┘
                             │ GEOSEARCH    │     │
                        ┌────▼────┐  ┌──────▼─┐ ┌─▼──────────┐
                        │dragonfly│  │postgres│ │ osrm-foot  │
                        │  :6379  │  │ PostGIS│ │ MLD :5000  │
                        └─────────┘  │pgvector│ └────────────┘
                                     └────────┘
```

**Go owns the concurrent I/O edge. Python owns the compute.** Split by workload
character, not by feature — see [ADR 0002](docs/adr/0002-go-python-workload-split.md).

## Quick start

```bash
cp .env.example .env
make up          # start Postgres, Dragonfly, OSRM; wait for health
make verify      # assert the environment is actually correct
make migrate     # apply the schema
make pipeline    # OSM -> POIs -> Tier B -> embeddings -> snap -> indexes
make api         # planner on :8000
make web         # app on :3002
```

If you move or rename the checkout, rebuild the Python venv — `uv` bakes
absolute paths into its console scripts, and the failure is an unhelpful
`Failed to spawn: uvicorn`:

```bash
rm -rf services/api-py/.venv && (cd services/api-py && uv sync)
```

The pipeline is a one-time ~9 minutes: 28k POIs scanned in 1s, embedded on the
host GPU in ~2.5min, Tier B computed in 17s, OSRM foot graph built in 15s.

`make verify` checks things that fail *silently* if unchecked: image architecture,
extension presence, `halfvec` staying under the TOAST threshold, embedding
dimension agreement, and Dragonfly's GEO commands.

Run `make help` for all targets.

## Platform note: Ollama runs on the host

Docker Desktop on macOS has no Metal GPU passthrough, so a containerised model
would be CPU-only and roughly an order of magnitude slower — which matters when
embedding 25k+ POIs. Ollama therefore runs natively:

```bash
ollama pull nomic-embed-text
ollama pull llama3.2:3b
launchctl setenv OLLAMA_HOST "0.0.0.0:11434"   # must rebind off loopback
```

Services reach it via `OLLAMA_BASE_URL`. Point that at a containerised
(`--profile gpu`) or remote instance on Linux — no code changes.
See [ADR 0003](docs/adr/0003-host-native-ollama.md).

## Map tiles

The default basemap uses OpenStreetMap's public raster tiles, which are free and
satisfy this project's "no paid/billed external APIs" mandate. OSM's tile usage
policy is intended for development and low-volume use, so this is appropriate for
a demo but not for production traffic. The tile source sits behind
`NEXT_PUBLIC_TILE_*`; swapping to self-hosted Protomaps PMTiles is a config
change, not a code change. The load test exercises only the WebSocket path and
never requests tiles.

## Milestones

| | scope | status |
|---|---|---|
| **M0** | Skeleton, arm64 Postgres image, compose, CI spine | done |
| **M1** | Vertical slice: prompt → 3 routed days on a map | **done** |
| **M2** | Data quality, hybrid retrieval, eval harness | |
| **M3** | Go WebSocket hub, Dragonfly hot tier, live re-ranking | |
| **M4** | 5,000-connection benchmark, distributed tracing | |
| **M5** | ADRs, demo video, `git clone && make demo` | |

## Design decisions

Architecture decision records live in [`docs/adr/`](docs/adr/). Deliberate
exclusions are in [`docs/NON-GOALS.md`](docs/NON-GOALS.md).

## Verified environment facts

Measured on the development machine rather than assumed:

| fact | value | why it matters |
|---|---|---|
| Postgres image architecture | `arm64` | `postgis/postgis` is amd64-only; Rosetta costs 2–5x on index builds |
| `halfvec(768)` column size | **1,544 bytes** | Under the ~2,000-byte TOAST threshold, so the hot query path avoids a per-row detoast |
| Dragonfly RSS with `--proactor_threads=2` | **19.8 MiB** | Defaults would reserve ~3.5 GB on a 14-core VM |
| Île-de-France OSM extract | 337 MB | Clipped to a Paris bbox before use |
| `nomic-embed-text` output dims | **768** | Must match `OLLAMA_EMBED_DIM`; `make verify` asserts it against the column |
| Flat-schema structured output, `llama3.2:3b` | **3/3 valid, byte-identical at temp 0** | The schema stays flat — sub-4B models fail ~68% of the time once `$defs` appear |
| Intent extraction latency (warm) | **~1.0 s** (15 s cold) | Why `OLLAMA_KEEP_ALIVE=30m` is set — model reload dominates otherwise |
