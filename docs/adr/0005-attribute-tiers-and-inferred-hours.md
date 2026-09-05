# 5. Three-tier attributes, and inferring the opening hours OSM does not have

Date: 2026-09-05 · Status: accepted

## Context

The product pitch rests on attributes OSM largely does not carry. Measured on
the real Paris extract (`docs/DATA_COVERAGE.md`, 27,956 POIs):

| attribute | coverage |
|---|---:|
| `opening_hours` | **39.5%** |
| `internet_access` (food) | **3.8%** |
| `wheelchair` | 25.5% |
| **`quiet`** | **no such tag exists** |

Building a "quiet cafe with wifi near historic sites" feature on this naively
produces a demo that returns nothing.

## Decision

**Three tiers, with per-attribute provenance recorded in `poi_attr_provenance`.**

**Tier A — direct OSM tags, confidence 1.0.** `has_wifi` is tri-state
(`true`/`false`/`NULL`). At 3.8% coverage, coercing unknown to false would make a
wifi filter return roughly 1 cafe in 26. Wifi is therefore a **soft ranking
boost, never a hard filter**, and the UI says "Wi-Fi unknown for 96% of Paris
cafes" rather than silently returning an empty list.

**Tier B — computed spatial features, confidence ~0.8.** "Quiet" is *calculated*
in PostGIS from distance to major roads, POI density, pedestrian-way adjacency,
park proximity and metro-entrance distance, then percentile-normalised. This is
computable, reproducible and explainable: the UI can say "quiet (82nd
percentile)" and show the formula. An LLM asserting "this cafe is quiet" would be
unfalsifiable and, when wrong, indefensible.

**Tier C — LLM-generated prose, embedding text only, never a filter.**

## The opening-hours decision

60.5% of POIs have no hours, and unlike wifi this is a **hard constraint**: hours
become per-node time windows in the TOPTW solver. Three options were weighed:

1. *Treat missing as always-open* — rejected. The solver would confidently route
   someone to a closed museum, and the 100% feasibility metric would become
   meaningless, measuring the solver only against constraints we invented.
2. *Exclude POIs without hours* — rejected. Costs 60% of the corpus, and skews
   hard toward chains and major sights, which are the best-tagged.
3. **Category-level inferred defaults — chosen.**

Defaults live in `poi-filter.yaml` under `default_hours`, applied only when no
tag exists, and recorded as tier B, confidence 0.5, source
`infer:category/<category>`.

**The metric discipline that makes this honest:** the eval harness reports
feasibility **separately** for known-hours and inferred-hours itineraries. The
headline claim stays "100% feasible against real opening hours"; the
inferred-hours number is reported alongside as the weaker claim it is. The UI
marks estimated hours visibly.

## Consequences

- Every derived attribute is auditable: which tier, from what source, how
  confident. This is what lets the UI show its work.
- Inferred hours are a product-visible approximation, disclosed rather than
  hidden.
- Public holidays are ignored in v1 (`PH off` unparsed) and the README says so.
- The Tier B `quietness` weights are config, tuned against the M2 eval set — not
  constants baked into code.
