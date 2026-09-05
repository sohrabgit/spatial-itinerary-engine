# Roadmap

Detailed enough to pick up cold. Each milestone is independently shippable and
ends with something recordable — the project should have value at any stopping
point, which is why M1 came before any hardening.

Sequencing rule carried over from the plan: **each hardening layer lands in the
same milestone as the thing it validates.** Hardening added at the end never
gets adopted.

---

## M2 — Data quality, retrieval, and the eval harness (~2 weeks)

The eval harness is the highest-signal component in the whole project. It is
what turns "retrieval seems good" into a number, and it settles the open
questions in `STATUS.md` rather than leaving them to taste.

### 2.1 Parse real opening hours

Currently **every** POI gets inferred category hours (ADR 0005), including the
39.5% that have a real `opening_hours` tag. Go writes `opening_hours_raw`
verbatim and does not interpret it.

Build a Python enrichment worker using **`opening_hours_py`** (Rust/PyO3
bindings over the mature `opening-hours` crate). The Go ecosystem here is weak —
the only option found was a ~13-star library, and a parse bug sends people to
closed museums.

- Expand real hours into `poi_hours`, replacing inferred rows where a tag exists
- Upgrade provenance from `infer:category/<cat>` @ 0.5 to `osm:opening_hours` @ 1.0
- Handle explicitly: `24/7`; `PH off` (ignore public holidays in v1 and say so
  in the README); `sunrise-sunset` (fixed seasonal approximation); multi-interval
  (`Mo-Fr 09:00-12:00,14:00-18:00` → two rows, which is why `poi_hours` is
  row-per-interval and the solver duplicates nodes)
- Track and publish parse success rate

**Done:** a majority of POIs carry real hours; the eval harness can separate
known-hours from inferred-hours itineraries.

### 2.2 Finish the retrieval design

- **Class 2/3 routing in code.** ADR 0004's rule — *a radius selecting most of
  the corpus is not a filter* — is documented but not implemented. Detect a
  degenerate spatial predicate and route to a partial index or an unfiltered
  HNSW scan instead of emitting it.
- **More partial indexes.** Class 3 was 50× faster (0.84 ms vs 42 ms). Extend
  beyond `food` and `sights` to the dominant query filters.
- **Expose the query class as an OTel span attribute** so routing is observable
  rather than theoretical.
- **Tune the linear rerank weights against the eval set** instead of by hand.

### 2.3 The eval harness — `make eval`

Three suites, none in required CI (too slow), all emitting committed markdown.

**A. Intent extraction.** 60–100 hand-written prompts → gold `TravelIntent` as
YAML fixtures. Metrics: schema-valid rate, per-field accuracy, retry rate,
p50/p95 latency. **Run per model** — this is the table that justifies the model
choice, and it is why `ModelAdapter` exists. A/B `llama3.2:3b` against
`granite4.2:3b` (Apache 2.0, advertises structured JSON output). One extra
`ollama pull`; the comparison table goes in the README.

**B. Retrieval.** 40–60 queries with graded relevance. Build the gold set by
**TREC-style pooling**: take top-20 from each retriever (vector, lexical,
spatial), hand-judge the pooled union 0–3. Metrics: recall@10/@50, nDCG@10, MRR.

Ablation arms — each answers a live question:
- vector-only / lexical-only / spatial-only / RRF / RRF+rerank
- template vs LLM embed text → **does LLM enrichment help at all?**
- **with and without the nomic task prefixes** → quantifies the silent footgun
- stratified vs unstratified → validates the fix for theme collapse

**C. Itinerary quality.** No ground truth for "good", so measure checkable things:
- **Feasibility rate — must be 100%.** Every stop open on arrival, walking within
  budget, nothing past closing. Already asserted in `tests/test_solver_feasibility.py`;
  the harness runs it over the golden query set and reports **known-hours and
  inferred-hours separately** (ADR 0005). Anything under 100% is a solver bug.
- **Objective gap** vs a 60-second reference solve — what the 3-second budget costs.
- **Category diversity** (entropy per day) — this is what should decide the
  church cap, rather than a guessed threshold.

**LLM-as-judge: deliberately skipped.** A 3B model judging a 3B model is noise.
Documenting the omission with the reason is stronger than reporting an unsound
metric.

### 2.4 Testcontainers + the recall assertion

Python suite against the real custom Postgres image, seeded with ~500 POIs
committed as a ~1 MB `.sql.gz`. **Test the actual SQL**, including a recall
assertion on the hybrid query — that is the test that catches a planner flip
between ANN and exact scan. Wire into CI.

### 2.5 OTel for `api-py`

One service only — cheap, and it establishes the span conventions Go joins in
M3: `llm.model`, `llm.retries`, `retrieval.candidates`, `retrieval.query_class`,
`solver.objective_gap`.

**M2 done:** `docs/EVALUATION.md` committed with real numbers, an ablation table
including the prefix arm, and the model comparison.

---

## M3 — The Go real-time edge (~2.5 weeks, includes the Go learning tax)

Build the WebSocket hub **before** anything else in Go. It is ~600 lines and
forces every concurrency idiom at once — channels, `select`, context, `sync`,
graceful shutdown. The ingester was mostly sequential and taught none of them.

### 3.1 Do the capacity arithmetic first, before writing escalation code

This is the most interesting finding in the design and it inverts the intuition
the architecture was built on:

| tier | trigger | work | latency | demand @5k |
|---|---|---|---|---|
| **T0 re-rank** | every position tick | Dragonfly `GEOSEARCH` + pipelined `HMGET` | <2 ms | ~3,400/sec |
| **T1 repair** | ETA breaches a closing time | incremental recompute, **no OR-Tools** | 1–3 ms | ~10/sec |
| **T2 re-solve** | drift beyond threshold | full OR-Tools TOPTW | 3 s | see below |

```
5,000 users / 600s     =  8.3 re-solves/sec demanded
OR-Tools 3s, ~4 cores  → ~1.3 re-solves/sec available   → 6x over capacity
```

**The cold path is the bottleneck at scale, not the hot path.** T0 runs at ~5%
utilisation. Put this table in the README.

### 3.2 Admission control, not more workers

- **Hysteresis + cooldown.** 400 m radius, 150 m off-route sustained for 60 s,
  120 s minimum between T2s per user. The sustain window is essential — raw GPS
  in central Paris jitters 20–50 m between buildings and would escalate
  constantly on stationary users.
- **A governor that sheds load by raising the trigger, not dropping requests.**
  Queue depth <8 → 1.0×, <16 → 2.0×, ≥16 → 4.0× and answer T2 with T1. Everyone
  still gets a valid itinerary, just re-optimised less eagerly. Export
  `escalation_threshold_multiplier` as a gauge — watching it climb under load is
  a better demonstration of graceful degradation than any latency number.
- **Bounded queue, fast-fail *down* the ladder, never up.** Plus per-user
  cancel-on-supersede via a `context.CancelFunc` in a sharded map.

### 3.3 Connection handling

`github.com/coder/websocket` — context-native throughout, which maps directly
onto .NET's `CancellationToken`. `gobwas/ws` is right above ~100k connections
and wrong here; documenting that you evaluated and rejected it is the signal.

- **Memory target ≤25 KB RSS per connection** → ≤125 MB at 5,000. Set
  `mem_limit: 512m` and let the container OOM-kill on regression.
- **Buffers dominate, not goroutines.** 10k goroutine stacks ≈ 80 MB; naive
  4 KB read+write buffers ≈ 40 MB more. Frames are ~200 B in, ~2 KB out.
- **Backpressure — the most important design point.** `nearby` frames are
  *snapshots*: if the channel is full, drop the oldest. `itinerary.delta` frames
  are *ordered state*: never drop; close 1011 and let the client resync.
- **Positions are lossy by definition** — `atomic.Pointer[Position]` overwritten
  by the read pump, drained by an adaptive ticker (2 Hz moving, 0.2 Hz still).
- **Sharded registry**, `goleak` in tests, graceful shutdown with a 10 s drain.
- **Cold path over HTTP+SSE**, not gRPC. No broker, debuggable with `curl`.

### 3.4 Dragonfly hot tier

Warmed as the ingester's final stage — no lazy fill, no cache-miss path in the
hot loop. `geo:poi` sorted set, `poi:{id}` hashes, `open:{dow}:{hour}` sets so
"open now" is a `SINTERSTORE`. Invalidation is version-and-swap: write
`geo:poi:v{n+1}`, then one atomic `RENAME`.

**Be first to say the tier isn't justified on latency.** PostGIS answers
proximity in well under 1 ms; Dragonfly in ~50–100 µs. The real justification is
**workload isolation** — stopping a burst of walkers from degrading itinerary
generation. Write `docs/adr/0007-dragonfly-hot-tier-and-when-it-is-not-worth-it.md`.

### 3.5 Frontend

WS provider, "worth a detour" bottom sheet, drag-to-reorder → T1 repair in
<150 ms with an infeasibility badge. **Build a GPX replay dev mode**
(`?simulate=louvre-walk.gpx`) — without it the best feature is undemoable at a
desk, and the same tracks feed the load test.

Watch for HMR + React 19 StrictMode producing duplicate connections: module-scope
singleton guarded against double-mount, backoff with jitter, explicit
`readyState` in the UI.

**M3 done:** GPX replay drives live cards; drag-reorder repairs in <150 ms; k6
sustains 1,000 connections; one trace spans Go → Python → Postgres.

---

## M4 — Prove it (~1.5 weeks)

### The k6 benchmark

**Multi-socket VUs are mandatory and must be designed in from the first script** —
retrofitting means a rewrite. `k6/websockets` uses a global event loop, so one VU
holds many sockets: 250 VUs × 20 sockets = 5,000 connections at ~750 MB, versus
~15 GB for the naive 1-VU-per-connection approach.

Four arms, all serving *given a position, return the 10 best open POIs within
800 m*:

| arm | setup | isolates |
|---|---|---|
| A | Go → Dragonfly `GEOSEARCH` | the target architecture |
| B | Go → PostGIS | the cache's actual contribution |
| C | FastAPI → PostGIS, 1 uvicorn worker | the naive comparison |
| **D** | FastAPI → PostGIS, **4 workers** | **do not skip** |

**Arm D is what makes the benchmark trustworthy.** Beating single-worker Python
proves nothing, and it is the first objection a reviewer raises. If D closes the
throughput gap, report that — memory-per-connection and p99 stability are still
real claims.

- Every socket replays a **real GPX track**, not random coordinates; random
  points defeat the coalescer and the drift policy and measure nothing.
- **Pin the SUT** (`cpus: '2'` on `edge-go`) so the server saturates before the
  generator, and the run reproduces on a reviewer's laptop.
- Run k6 **natively**, raise `ulimit -n`; macOS defaults cap ~256 and present as
  an inexplicable plateau.
- **Write the methodology and your predictions before running it.**

Three artifacts: p50/p95/p99 and **KB RSS per connection** across arms; **p99
itinerary-generation latency vs connections, cache on/off** (the more
interesting chart — workload isolation made visible); and the governor gauge
climbing with zero user-visible errors.

**If the results contradict the architecture, publish that.** A benchmark that
falsifies your own prior is the strongest signal in the project.

### Observability

OTel across all three languages including browser spans, `grafana/otel-lgtm`,
Loki↔Tempo correlation via `trace_id` in `slog`/`structlog`. **A distributed-trace
screenshot in the README is worth more than 1,000 lines of code.**

Eval suite C joins the harness here. This milestone has deliberate slack:
tuning under load always finds something.

---

## M5 — Narrative (~1 week)

ADRs finished, architecture diagram, demo video, an honest "what I'd do
differently". Optional: JWT + Postgres RLS auth slice (~150 lines, stop there);
self-hosted PMTiles basemap (the seam already exists — `pmtiles extract --bbox`
pulls a Paris subset from the public daily build in minutes); Dragonfly Streams
for multiple `edge-go` replicas.

**Done:** a stranger runs `git clone && make demo` and has a working system in
under 20 minutes. The most under-rated portfolio quality bar, and the one most
projects fail.

---

## Standing risks

1. **Scope.** Four hardening layers on an already-large project. Mitigated
   entirely by sequencing: every milestone ends recordable.
2. **The T2 capacity gap** is structural, not a tuning problem. Do the
   arithmetic in M3 before writing the escalation code, not in M4 when the
   benchmark surprises you.
3. **Docker VM RAM.** Currently 18 GB. Adding `edge-go`, `api-py` and the
   observability stack will use it.
4. **Full-corpus LLM enrichment is infeasible** (~55 h for 150k POIs). Tiered
   enrichment only, and let the eval decide whether it helps.
