# OSM data coverage — Paris

Measured 2026-09-05 against `ile-de-france-latest.osm.pbf` (2026-09-03 data),
clipped to bbox `2.20,48.79,2.47,48.92`. Regenerate with:

```bash
make ingest-report
```

The plan requires these numbers **before** any UI is built on these attributes.
Designing a "quiet cafe with wifi" filter without knowing the wifi coverage
produces a demo that silently returns nothing.

## Scan

| metric | value |
|---|---|
| Nodes scanned | 5,233,429 |
| Ways scanned | 786,526 |
| POIs from nodes | 24,684 |
| POIs from ways (centroid-resolved) | 3,283 / 3,283 |
| **Total POIs in bbox** | **27,956** |
| Way node-refs tracked → resolved | 70,267 → 70,267 |
| Wall clock | **974 ms** (pass1 760ms, pass2 207ms, pass3 3ms) |
| **Peak heap** | **91 MB** |

The two-pass geometry design is validated: peak heap 91 MB against the ~250 MB+
a `map[NodeID]coord` would have cost. 88% of POIs are plain nodes, so the way
path is a small minority — but it resolved 100% of candidates.

## Category distribution

| category | n | | category | n |
|---|---:|---|---|---:|
| restaurant | 11,834 | | gallery | 549 |
| cafe | 3,079 | | theatre | 365 |
| bar | 2,566 | | attraction | 340 |
| bakery | 2,004 | | library | 265 |
| shop | 1,686 | | market | 182 |
| artwork | 1,533 | | museum | 176 |
| park | 1,396 | | dessert | 140 |
| monument | 996 | | cinema | 123 |
| church | 665 | | viewpoint / castle / zoo / aquarium | 57 |

Food dominates at 19,623 of 27,956 (70%). "Itinerary-worthy" sightseeing
(museum, gallery, attraction, monument, viewpoint, castle, church, park) is
~4,100 — consistent with the plan's 6–15k estimate once cafes are included.

## Tag coverage — the numbers that change the design

| attribute | present | of | coverage |
|---|---:|---:|---:|
| `opening_hours` (all POIs) | 11,038 | 27,956 | **39.5%** |
| `opening_hours` (food only) | 9,076 | 19,623 | **46.3%** |
| `internet_access` (food only) | 751 | 19,623 | **3.8%** |
| `wheelchair` | 7,137 | 27,956 | 25.5% |
| `outdoor_seating` (food) | 7,252 | 19,623 | 37.0% |
| `cuisine` (food) | 8,989 | 19,623 | 45.8% |
| `website` | 6,564 | 27,956 | 23.5% |
| **`quiet`** | — | — | **no such tag exists in OSM** |

### Consequence 1 — wifi is worse than estimated

**3.8%**, against the plan's 5–15% estimate. A hard wifi filter would return
roughly 1 cafe in 26. This confirms the tri-state `has_wifi` design: unknown must
never be coerced to false, and wifi is a soft ranking boost, never a filter.

The honest UI copy is "Wi-Fi unknown for 96% of Paris cafes" — which reads far
better than a filter that quietly returns an empty list.

### Consequence 2 — opening hours are the real problem

**60.5% of POIs have no `opening_hours` tag at all.** This was not anticipated at
this magnitude, and it matters more than wifi because opening hours are a *hard
constraint* in the TOPTW solver: they become the per-node time windows.

Three options, none free:

1. **Treat missing hours as always-open.** Keeps the corpus, but the 100%
   feasibility metric becomes a lie — the solver would happily route someone to a
   museum that is shut.
2. **Exclude POIs without hours.** Honest, but discards 60% of the corpus and
   most of the long tail that makes recommendations interesting.
3. **Category-level inferred defaults**, recorded in `poi_attr_provenance` as
   tier B with confidence < 1 (museum 10:00–18:00, cafe 08:00–19:00, park
   open, ...), with the UI distinguishing known from inferred hours.

Option 3 is the working assumption, with the eval harness reporting feasibility
**separately** for known-hours and inferred-hours itineraries. That keeps the
headline metric truthful while retaining corpus size.

## Retrieval signal — measured after the first full ingest

27,956 POIs embedded with `nomic-embed-text` (196 vectors/sec on Metal) and
loaded. Retrieval is mechanically correct: a Class-1 query returns in **19 ms**.
But the *signal* is currently thin, and it is worth stating precisely.

**Category discrimination works.**

| query | top results |
|---|---|
| "impressionist paintings and fine art" | artwork, gallery, artwork, artwork |
| "a green park to sit outside" | park, park, park, cafe |
| "somewhere to buy fresh bread" | bakery ×4 |

**Within-category discrimination does not, yet.** Median `embed_text` is **91
characters**, and **39% of POIs carry nothing beyond name and category**:

```
search_document: Bar-Tabac L'Européen — a cafe in Paris. Wi-Fi: unknown.
```

The tell: "somewhere to buy fresh bread" returned bakeries literally *named*
"Ten Belles Bread", "Bread and co", "Bread Paradise", "Bread & Co." That is
lexical overlap leaking through the embedding, not semantic understanding.
Similarity scores across the whole cafe result set span 0.705–0.722 — a band too
narrow to rank on.

**This is the case for M2, quantified.** Tier B computed features (quietness,
touristiness, greenness, transit friction) are what give each POI distinguishing
content to embed. Until they exist, vector search is doing little more than
fuzzy name matching, and the RRF fusion has no independent vector signal to fuse.

It is also the case for the eval harness: looking at the cafe query alone, this
looks like it works.

## After Tier B — the same measurement, repeated

Tier B features computed from 185,307 road segments and 1,690 metro entrances,
then the corpus re-embedded. Same probe as above:

| | before Tier B | after Tier B |
|---|---|---|
| median `embed_text` | 91 chars | 178 chars |
| similarity spread across cafes | **0.017** (0.705–0.722) | **0.154** (0.583–0.737) |
| "quiet cafe" top-5 | no quietness signal | **4 of 5 at quietness ≥ 0.70** |
| "lively buzzy cafe" top-5 | no signal | **5 of 5 at quietness ≤ 0.48** |

The two opposing queries now return near-disjoint result sets ordered the right
way round, which is the behaviour the whole Tier B design exists to produce. The
similarity band is ~9x wider, so there is finally room to rank.

**Validation that the formula tracks reality** — quintiles over the full corpus:

| quietness quintile | n | avg dist to major road | avg neighbours ≤100m | avg dist to metro |
|---|---:|---:|---:|---:|
| 1 (loudest) | 5,591 | 18 m | 17.3 | 165 m |
| 2 | 5,591 | 45 m | 17.1 | 213 m |
| 3 | 5,591 | 102 m | 18.7 | 228 m |
| 4 | 5,591 | 170 m | 14.4 | 274 m |
| 5 (quietest) | 5,591 | 259 m | 5.5 | 414 m |

Monotonic on road distance and metro distance, and density collapses in the top
quintile. Spot checks agree: the quietest cafes sit 732–1,236 m from any major
road with zero neighbours; the loudest sit 6–11 m from a boulevard with 16–83.

**Limits, stated plainly.** This is a smoke test on two opposing queries, not an
evaluation. One outlier ("Pause Café", quietness 0.11, ranked 4th for the quiet
query) shows name-lexical leakage persists. Turning this into a number is
exactly what the M2 eval harness with pooled graded judgments is for.
