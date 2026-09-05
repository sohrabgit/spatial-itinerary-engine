# 4. Route vector queries into three explicit classes

Date: 2026-09-05 · Status: accepted · Supersedes the assumption in the plan's §2.3

## Context

Retrieval combines three filters at once: geospatial proximity, vector
similarity, and scalar attributes. The design assumption going in was that a
single naive query would let the planner take the HNSW index, walk it in
*global* similarity order, post-filter the results, and silently collapse recall
-- a non-deterministic correctness bug.

That assumption was tested rather than trusted.

## What was actually measured

25,000 synthetic POIs over the Paris bbox, `halfvec(768)`, HNSW `m=16
ef_construction=200`, plus two partial indexes. PostgreSQL 17.11 / pgvector
0.8.6 / PostGIS 3.6.4, warm cache.

| query shape | plan chosen | time |
|---|---|---|
| 2 km radius + category (211 candidates) | BitmapAnd (`poi_cat_btree` ∩ `poi_geog_gist`) → exact sort | **5.7 ms** |
| no filter at all | `poi_emb_hnsw` | **1.8 ms** |
| 15 km radius (matches ~all rows) | Parallel Seq Scan → sort 25k | **42 ms** |
| 15 km radius, `hnsw.iterative_scan=relaxed_order` | Parallel Seq Scan (unchanged) | 27 ms |
| category filter matching a partial index | `poi_emb_food_hnsw` | **0.84 ms** |

**The predicted recall trap did not occur.** With a selective filter the planner
correctly chose the exact path; with no filter it correctly chose HNSW. At this
scale its cost model got both right.

**The real failure mode is latency, not recall.** A filter that is present but
weakly selective (row 3) produces a full parallel scan at 42 ms -- 20-50x slower
than necessary -- and neither a `MATERIALIZED` barrier nor `hnsw.iterative_scan`
changes it. Those settings only take effect once the index is *already* chosen;
they cannot force the planner into it.

## Decision

Keep the three query classes, routed explicitly in application code, but for
revised reasons:

- **Class 1, localised** (tight radius, few thousand candidates): exact scan
  inside a `MATERIALIZED` CTE. The barrier is now justified as *plan determinism
  insurance* -- making the shape independent of `ANALYZE` statistics that drift
  between dev, CI and a larger corpus -- not as a fix for an observed bug.
- **Class 2, unfiltered / city-wide**: HNSW, with `iterative_scan` and
  `max_scan_tuples` as latency guard rails once the index is in play.
- **Class 3, filter matching a partial index**: the standout result. ANN speed
  with zero recall loss, because every indexed row already satisfies the
  predicate. **0.84 ms vs 42 ms.** Worth extending beyond the current two
  (`food`, `sights`) to cover the dominant query filters.

The new rule this measurement produces: **a radius that selects most of the
corpus is not a filter.** The application must detect a degenerate spatial
predicate and route to Class 2 or Class 3 rather than emitting it, since that is
the one case the planner handles badly.

## Confidence and limits

- Plan *shapes* are the reliable output here. Absolute timings will shift at the
  real 150k corpus.
- The corpus used uniform-random 768-dim vectors. In high dimensions random
  vectors are near-equidistant, which is degenerate for ANN. **Recall was
  therefore not measurable** -- the M2 eval harness with real embeddings is what
  settles recall, and the ablation must re-check these plan choices there.
- 25k rows is small. The cost-model crossover between exact scan and HNSW will
  move; the class-routing thresholds must be re-derived, not assumed.

## Consequences

- `db/post_load.sql` builds the partial indexes as first-class, not as an
  optimisation to add later.
- Retrieval code must expose which class a query took, as an OTel span attribute,
  so the routing is observable rather than theoretical.
- The plan's §2.3 claim about silent recall collapse is downgraded to "not
  observed at 25k; re-test at full corpus with real embeddings".
