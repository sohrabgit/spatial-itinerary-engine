# Status

Last updated 2026-09-06, end of M1.

## Milestones

| | scope | state |
|---|---|---|
| **M0** | Skeleton, arm64 Postgres image, Compose, CI spine | **done** |
| **M1** | Vertical slice: prompt → 3 routed days on a map | **done** |
| **M2** | Data quality, hybrid retrieval, **eval harness** | next |
| **M3** | Go WebSocket hub, Dragonfly hot tier, live re-ranking | |
| **M4** | 5,000-connection benchmark, distributed tracing | |
| **M5** | ADRs finished, demo video, `git clone && make demo` | |

Original estimate ~9 weeks full-time. M0+M1 are complete and demoable, which
was the point of sequencing them first: the project has value at this stopping
point.

## What exists

**Data pipeline** (`services/ingest-go`) — six stages behind `make pipeline`:
PBF scan → context layers → Tier B → re-embed → snap → indexes. Two-pass
geometry resolution using a sorted `[]int64` rather than a map.

**Data layer** (`db/`) — `poi`, `poi_embedding` (halfvec sidecar), `poi_hours`
(row per interval), `poi_attr_provenance`, `llm_trace`, plus `road` and
`transit_entrance` as Tier B inputs. Migrations applied by `scripts/migrate.sh`
(numbered SQL + a checksum ledger that refuses edited migrations).

**Planner** (`services/api-py`) — deterministic pre-pass → schema-constrained
LLM intent → stratified RRF retrieval → linear rerank → OR-Tools TOPTW, plus an
O(n) `repair()` for interactive reordering. 22 tests.

**Web** (`apps/web`) — Next.js 16, MapLibre with day-coloured routes over a
darkened OSM raster basemap, and the three days as parallel columns on a shared
clock axis. Dark theme.

**Not started:** `services/edge-go` (M3), `ops/k6` (M4), evals (M2), OTel (M2–M4).

## Measured, not assumed

Every number here came from running the thing, and several contradict what the
plan predicted.

| fact | value | note |
|---|---|---|
| Paris POIs in corpus | 27,956 | from a 71 MB bbox clip of a 322 MB extract |
| PBF scan | 991 ms, **83 MB peak heap** | a `map[NodeID]coord` would cost ~250 MB |
| Embedding throughput | 196 vectors/sec | nomic-embed-text on Metal |
| `halfvec(768)` size | **1,544 bytes** | under the ~2,000-byte TOAST threshold |
| Dragonfly RSS | **19.8 MiB** | defaults would reserve ~3.5 GB on 14 cores |
| OSRM foot prep | **13.5 s, 755 MB** | plan estimated 20–60 min and 4–8 GB |
| POI snap distance | mean 5.8 m, max 145 m | **0 POIs over 150 m** |
| `opening_hours` coverage | **39.5%** | drives the inferred-hours design |
| `internet_access` (food) | **3.8%** | worse than the 5–15% estimate |
| `quiet` tag | **does not exist in OSM** | computed in PostGIS instead |
| Intent extraction | 3/3 valid, byte-identical at temp 0 | ~1.0 s warm, 15 s cold |
| End-to-end request | **~4.5 s** | intent 1.1s, retrieval 0.2s, matrix 0.2s, solve 3.0s |

### Things that turned out to be wrong

- **The predicted vector-search recall trap did not occur.** At 25k rows the
  planner chose an exact scan under a selective filter and HNSW under none —
  both correct. The real failure is *latency*: a present-but-weak filter yields
  a 42 ms full scan that neither a `MATERIALIZED` barrier nor `iterative_scan`
  fixes. Class-3 partial indexes were the standout at **0.84 ms vs 42 ms**.
  Full write-up in ADR 0004.
- **The snapping risk didn't materialise.** Paris's foot network is dense enough
  that no POI snapped more than 145 m. The mitigation stays because it costs
  3.5 s and would catch it in a less-mapped city, but it saved nothing here.
- **OSRM prep was ~100× cheaper than estimated.** The bbox clip is the
  highest-leverage decision in the project.

### Bugs worth not rediscovering

Each of these produced *plausible-looking output* rather than an error:

1. **Solver dropped every stop** and reported `ROUTING_SUCCESS`. Service time was
   in the arc cost, so skipping always beat visiting.
2. **Then it filled days with commemorative wall plaques.** Removing service
   time made 15-minute stops nearly free. Fixed with a notability floor, heavier
   popularity weight, and service time restored with the penalty rescaled.
3. **`popularity` was 0 for all 27,956 rows** — declared, never computed. The
   RRF prior arm contributed nothing, so ranking collapsed onto quietness and
   surfaced zoo enclosures above Notre-Dame.
4. **A multi-theme prompt collapsed to one theme** — "historic walk with quiet
   cafés" returned 12 cafés and no monuments. Fixed with stratified retrieval.
5. **"cafés" ≠ "cafes"** — the keyword list was unaccented, silently dropping
   the theme. Now folds diacritics.
6. **The map rendered nothing** for reasons covered in `CLAUDE.md`.
7. **CORS was missing entirely** — the page rendered and every request would
   have been blocked. Invisible to curl without an `Origin` header.

## Open questions

- **Day 2 is church-heavy** (7 churches). A per-subcategory cap would fix it;
  M2's eval harness should decide the threshold rather than a guess.
- **Days are uneven** (12/11/8 stops). All three days share one depot, so later
  days get leftovers. Possibly a per-day geographic anchor.
- **Does LLM enrichment beat templates at all?** Untested. M2 ablation decides,
  and a null result is worth publishing.
- **Retrieval recall is unmeasured.** The only probes so far are smoke tests on
  a couple of queries.

## Environment

macOS arm64, 36 GB RAM, Docker VM 18 GB, ~42 GB disk free. Ollama runs natively
on the host for Metal. Repo at `~/Projects/Portfolio/spatial-itinerary-engine`,
pushed to `github.com/sohrabgit/spatial-itinerary-engine` (public, CI green).
