"""Hybrid retrieval: vector + lexical + prior, fused with RRF, then reranked.

Query-class routing is explicit rather than left to the planner. Measured on
this corpus (docs/adr/0004): with a selective spatial filter the planner picks
an exact scan and is right; with no filter it picks HNSW and is right; with a
present-but-weak filter it picks a 42ms full scan and is wrong. The rule that
falls out: a radius selecting most of the corpus is not a filter, so detect that
case and route it rather than emitting it.
"""

from __future__ import annotations

import time
from dataclasses import dataclass

from app.db import pool
from app.embed import embed_query, to_pgvector
from app.schemas import TravelIntent

# Themes -> the categories the corpus actually contains.
THEME_CATEGORIES: dict[str, list[str]] = {
    "historic": ["monument", "church", "castle", "museum", "attraction"],
    "art": ["museum", "gallery", "artwork"],
    "food": ["restaurant", "bakery", "market", "dessert"],
    "cafe": ["cafe", "bakery"],
    "nature": ["park", "viewpoint"],
    "nightlife": ["bar", "theatre", "cinema"],
    "architecture": ["church", "monument", "castle", "attraction"],
    "shopping": ["shop", "market"],
    "views": ["viewpoint", "attraction", "monument"],
}

SIGHT_CATEGORIES = ["museum", "gallery", "attraction", "monument", "viewpoint",
                    "castle", "church", "park"]

# Retrieval groups. A multi-theme prompt ("historic walk WITH quiet cafes")
# otherwise collapses to whichever group the embedding favours -- observed
# returning 12 cafes and zero monuments for exactly that prompt, which would
# produce three days of coffee. Candidates are allocated per group instead.
CATEGORY_GROUP: dict[str, str] = {
    "museum": "sight", "gallery": "sight", "attraction": "sight", "monument": "sight",
    "viewpoint": "sight", "castle": "sight", "church": "sight", "artwork": "sight",
    "cafe": "food", "restaurant": "food", "bakery": "food", "bar": "food",
    "dessert": "food", "market": "food",
    "park": "nature",
    "shop": "other", "library": "other", "theatre": "other", "cinema": "other",
}

# Sights are the backbone of a day; food punctuates it. A day realistically
# holds 3-4 sights and 1-2 stops to eat, so the candidate pool mirrors that.
GROUP_WEIGHTS = {"sight": 0.55, "food": 0.25, "nature": 0.15, "other": 0.05}

# Radius beyond which a spatial predicate stops being a filter on this corpus.
DEGENERATE_RADIUS_M = 6000

# Minimum notability for something to be a destination rather than street
# furniture. OSM tags every commemorative wall plaque as historic=memorial, so
# without this the solver fills days with "A la memoire des eleves" -- quiet,
# cheap to visit, and not somewhere anyone goes.
SIGHT_POPULARITY_FLOOR = 0.55


@dataclass
class Candidate:
    poi_id: int
    name: str
    category: str
    lat: float
    lon: float
    snap_lat: float
    snap_lon: float
    dwell_minutes: int
    quietness: float | None
    touristiness: float | None
    popularity: float
    has_wifi: bool | None
    hours_known: bool
    score: float
    why: list[str]


@dataclass
class RetrievalResult:
    candidates: list[Candidate]
    query_class: str
    considered: int
    latency_ms: int


def categories_for(intent: TravelIntent) -> list[str]:
    cats: list[str] = []
    for t in intent.themes:
        cats.extend(THEME_CATEGORIES.get(t, []))
    if not cats:
        cats = list(SIGHT_CATEGORIES)
    # Preserve order, drop duplicates.
    return list(dict.fromkeys(cats))


def _query_text(intent: TravelIntent) -> str:
    bits: list[str] = []
    if intent.themes:
        bits.append(" ".join(intent.themes))
    if intent.ambience:
        bits.append(" ".join(intent.ambience))
    if intent.free_text_residual:
        bits.append(intent.free_text_residual)
    return ", ".join(bits) or "interesting places to visit"


SQL = """
WITH cand AS MATERIALIZED (
  -- Class 1: the spatial+attribute prefilter. MATERIALIZED so the shape is
  -- deterministic rather than dependent on ANALYZE statistics that drift.
  SELECT p.id, p.name, p.category, p.dwell_minutes, p.quietness, p.touristiness,
         p.popularity, p.has_wifi,
         ST_Y(p.geog::geometry) AS lat, ST_X(p.geog::geometry) AS lon,
         ST_Y(COALESCE(p.snap_geog, p.geog)::geometry) AS snap_lat,
         ST_X(COALESCE(p.snap_geog, p.geog)::geometry) AS snap_lon,
         (p.opening_hours_raw IS NOT NULL) AS hours_known,
         to_tsvector('french', coalesce(p.name,'') || ' ' || coalesce(p.embed_text,'')) AS tsv,
         p.name AS lex_name
  FROM poi p
  WHERE ST_DWithin(p.geog, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4)
    AND p.category = ANY($5::text[])
    AND (NOT $6::boolean OR p.has_wifi IS NOT FALSE)
    AND (NOT $7::boolean OR p.wheelchair IN ('yes','designated'))
    -- Notability floor for sights only; a good cafe need not be famous.
    AND (p.category NOT IN ('monument','artwork','attraction')
         OR p.popularity >= $15::real)
    AND EXISTS (
      SELECT 1 FROM poi_hours h
      WHERE h.poi_id = p.id AND h.dow = $8 AND h.open_m <= $9 AND h.close_m >= $9
    )
),
vec AS (
  SELECT c.id, row_number() OVER (ORDER BY e.embedding <=> $1::halfvec) AS rk
  FROM cand c JOIN poi_embedding e ON e.poi_id = c.id
  ORDER BY e.embedding <=> $1::halfvec LIMIT 100
),
lex AS (
  SELECT c.id, row_number() OVER (
           ORDER BY (ts_rank(c.tsv, plainto_tsquery('french', $10))
                     + similarity(c.lex_name, $10)) DESC) AS rk
  FROM cand c LIMIT 100
),
prior AS (
  SELECT c.id, row_number() OVER (ORDER BY c.popularity DESC, c.touristiness DESC NULLS LAST) AS rk
  FROM cand c LIMIT 100
),
-- Reciprocal Rank Fusion: no score normalisation needed across cosine,
-- ts_rank and popularity, which are on incomparable scales.
fused AS (
  SELECT id, SUM(w)::float8 AS rrf FROM (
    SELECT id, 1.0::float8/(60 + rk) AS w FROM vec
    UNION ALL SELECT id, 0.7::float8/(60 + rk) FROM lex
    UNION ALL SELECT id, 0.5::float8/(60 + rk) FROM prior
  ) s GROUP BY id
),
-- Stratified selection: rank within each group, then take that group's quota.
-- Without this a multi-theme prompt returns a single category.
ranked AS (
  SELECT c.id, c.name, c.category, c.lat, c.lon, c.snap_lat, c.snap_lon,
         c.dwell_minutes, c.quietness, c.touristiness, c.popularity, c.has_wifi,
         c.hours_known, f.rrf, g.grp,
         row_number() OVER (PARTITION BY g.grp ORDER BY f.rrf DESC) AS grp_rank
  FROM fused f
  JOIN cand c ON c.id = f.id
  JOIN unnest($11::text[], $12::text[]) AS g(cat, grp) ON g.cat = c.category
)
SELECT r.*, (SELECT count(*) FROM cand) AS considered
FROM ranked r
JOIN unnest($13::text[], $14::int[]) AS q(grp, quota) ON q.grp = r.grp
WHERE r.grp_rank <= q.quota
ORDER BY r.rrf DESC
"""


def _rerank(rows, intent: TravelIntent) -> list[Candidate]:
    """Feature-weighted linear scorer.

    Deliberately not a cross-encoder: at ~60 candidates a linear model is enough,
    and every term is nameable, which is what lets the UI say WHY a stop was
    chosen. Weights are config, tuned against the eval set.
    """
    want_quiet = "quiet" in intent.ambience
    want_lively = "lively" in intent.ambience
    want_local = "local" in intent.ambience
    theme_cats = set(categories_for(intent))

    max_rrf = max((r["rrf"] for r in rows), default=1.0) or 1.0
    out: list[Candidate] = []
    for r in rows:
        why: list[str] = []
        score = 0.30 * (r["rrf"] / max_rrf)

        if r["category"] in theme_cats:
            score += 0.20
            why.append(f"matches {r['category']}")

        q = r["quietness"]
        if q is not None:
            if want_quiet:
                score += 0.15 * q
                if q >= 0.65:
                    why.append(f"quiet ({int(q * 100)}th pct)")
            elif want_lively:
                score += 0.15 * (1 - q)
                if q <= 0.35:
                    why.append("lively spot")

        t = r["touristiness"]
        if t is not None and want_local:
            score += 0.10 * (1 - t)
            if t <= 0.3:
                why.append("off the tourist track")

        # Notability decides whether somewhere is a destination at all;
        # ambience only shades the ranking among real destinations.
        score += 0.30 * float(r["popularity"] or 0)
        if r["has_wifi"] is True:
            score += 0.05
            why.append("Wi-Fi")
        if not r["hours_known"]:
            why.append("hours estimated")

        out.append(Candidate(
            poi_id=r["id"], name=r["name"], category=r["category"],
            lat=r["lat"], lon=r["lon"], snap_lat=r["snap_lat"], snap_lon=r["snap_lon"],
            dwell_minutes=r["dwell_minutes"], quietness=q, touristiness=t,
            popularity=float(r["popularity"] or 0), has_wifi=r["has_wifi"],
            hours_known=r["hours_known"], score=score, why=why,
        ))
    out.sort(key=lambda c: c.score, reverse=True)
    return out


async def retrieve(
    intent: TravelIntent, lat: float, lon: float, radius_m: int = 3000, limit: int = 40,
    dow: int = 2, at_minute: int = 720,
) -> RetrievalResult:
    t0 = time.perf_counter()
    qtext = _query_text(intent)
    vec = to_pgvector(await embed_query(qtext))
    cats = categories_for(intent)

    # ADR 0004: a radius selecting most of the corpus is not a filter. Report it
    # so routing is observable rather than theoretical.
    query_class = "class1_localised" if radius_m < DEGENERATE_RADIUS_M else "class2_broad"

    # Group quotas, allocated over the categories this intent actually selects.
    groups_present = {CATEGORY_GROUP.get(c, "other") for c in cats}
    total_w = sum(GROUP_WEIGHTS[g] for g in groups_present) or 1.0
    quota_names, quota_values = [], []
    for g in sorted(groups_present):
        quota_names.append(g)
        quota_values.append(max(3, round(limit * GROUP_WEIGHTS[g] / total_w)))

    p = await pool()
    rows = await p.fetch(
        SQL, vec, lon, lat, radius_m, cats,
        intent.requires_wifi, intent.accessibility_required,
        dow, at_minute, qtext,
        cats, [CATEGORY_GROUP.get(c, "other") for c in cats],
        quota_names, quota_values, SIGHT_POPULARITY_FLOOR,
    )
    considered = rows[0]["considered"] if rows else 0
    return RetrievalResult(
        candidates=_rerank(rows, intent),
        query_class=query_class,
        considered=considered,
        latency_ms=int((time.perf_counter() - t0) * 1000),
    )


async def fetch_hours(poi_ids: list[int], dows: list[int]) -> dict[int, list[tuple[int, int]]]:
    """Opening windows for the trip's weekdays, unioned per POI.

    Multi-interval days ("Mo-Fr 09:00-12:00,14:00-18:00") arrive as separate
    rows, which is why poi_hours is row-per-interval.
    """
    if not poi_ids:
        return {}
    p = await pool()
    rows = await p.fetch(
        """SELECT poi_id, open_m, close_m FROM poi_hours
           WHERE poi_id = ANY($1::bigint[]) AND dow = ANY($2::smallint[])""",
        poi_ids, dows,
    )
    out: dict[int, list[tuple[int, int]]] = {}
    for r in rows:
        out.setdefault(r["poi_id"], []).append((r["open_m"], r["close_m"]))
    return out
