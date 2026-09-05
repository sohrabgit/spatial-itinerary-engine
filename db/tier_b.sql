-- tier_b.sql -- compute the spatial features OSM does not carry.
--
-- "Quiet" has no OSM tag. Rather than have an LLM assert it (unfalsifiable, and
-- indefensible when wrong), it is CALCULATED from geometry: distance to noisy
-- roads, proximity to pedestrian streets, surrounding POI density, park and
-- metro proximity. The result is reproducible and explainable -- the UI can say
-- "quiet (82nd percentile)" and show this formula. See ADR 0005.
--
-- Raw scores are percentile-normalised at the end, so weights only need to be
-- right in RELATIVE terms; the absolute scale washes out. Weights live here as
-- a single CTE so they are visible in one place and tunable against the M2
-- eval set rather than buried in application code.

\timing on

-- A real table, not TEMP: these raw components are what make the feature
-- explainable ("show the formula") and what the M2 eval harness tunes the
-- weights against. They must outlive the session that computed them.
DROP TABLE IF EXISTS poi_tier_b_raw;
CREATE TABLE poi_tier_b_raw AS
WITH w AS (
  SELECT
    1.00::float8 AS w_road_dist,     -- further from a major road = quieter
    0.60::float8 AS w_density,       -- dense POI cluster = busier
    0.50::float8 AS w_pedestrian,    -- on/near a pedestrian way = calmer
    0.35::float8 AS w_park,          -- near green space = calmer
    0.45::float8 AS w_metro          -- metro entrance = footfall and noise
),
m AS (
  SELECT
    p.id,
    p.geog,
    p.category,
    -- Nearest major road. The <-> KNN operator uses the GiST index; a plain
    -- MIN(ST_Distance) would scan every road for every POI.
    COALESCE(mr.d, 500)  AS road_m,        -- 500m cap = "far enough to not matter"
    COALESCE(pw.d, 300)  AS ped_m,
    COALESCE(mx.d, 1500) AS metro_m,
    COALESCE(pk.d, 1000) AS park_m,
    dens.n               AS density_100m,
    tour.n               AS attractions_200m,
    green.n              AS parks_300m
  FROM poi p
  LEFT JOIN LATERAL (
    SELECT ST_Distance(p.geog, r.geog) AS d FROM road r
    WHERE r.class = 'major' ORDER BY p.geog <-> r.geog LIMIT 1
  ) mr ON true
  LEFT JOIN LATERAL (
    SELECT ST_Distance(p.geog, r.geog) AS d FROM road r
    WHERE r.class = 'pedestrian' ORDER BY p.geog <-> r.geog LIMIT 1
  ) pw ON true
  LEFT JOIN LATERAL (
    SELECT ST_Distance(p.geog, t.geog) AS d FROM transit_entrance t
    ORDER BY p.geog <-> t.geog LIMIT 1
  ) mx ON true
  LEFT JOIN LATERAL (
    SELECT ST_Distance(p.geog, q.geog) AS d FROM poi q
    WHERE q.category = 'park' ORDER BY p.geog <-> q.geog LIMIT 1
  ) pk ON true
  CROSS JOIN LATERAL (
    SELECT count(*)::float8 AS n FROM poi q
    WHERE q.id <> p.id AND ST_DWithin(p.geog, q.geog, 100)
  ) dens
  CROSS JOIN LATERAL (
    SELECT count(*)::float8 AS n FROM poi q
    WHERE q.category IN ('museum','gallery','attraction','monument','viewpoint','castle')
      AND ST_DWithin(p.geog, q.geog, 200)
  ) tour
  CROSS JOIN LATERAL (
    SELECT count(*)::float8 AS n FROM poi q
    WHERE q.category = 'park' AND ST_DWithin(p.geog, q.geog, 300)
  ) green
)
SELECT
  m.id,
  -- log1p compresses distance: the difference between 10m and 60m from a
  -- boulevard matters enormously; 400m vs 450m does not.
    w.w_road_dist * ln(1 + m.road_m)
  - w.w_density   * ln(1 + m.density_100m)
  + w.w_pedestrian * (1.0 / (1.0 + m.ped_m / 50.0))
  + w.w_park       * (1.0 / (1.0 + m.park_m / 200.0))
  - w.w_metro      * (CASE WHEN m.metro_m < 80 THEN 1.0
                           WHEN m.metro_m < 200 THEN 0.4 ELSE 0.0 END)
    AS quietness_raw,
  ln(1 + m.attractions_200m) + 0.5 * ln(1 + m.density_100m) AS touristiness_raw,
  ln(1 + m.parks_300m) + 1.0 / (1.0 + m.park_m / 200.0)     AS greenness_raw,
  m.metro_m                                                  AS transit_m,
  m.road_m, m.ped_m, m.metro_m AS metro_dist, m.park_m, m.density_100m
FROM m CROSS JOIN w;

ALTER TABLE poi_tier_b_raw ADD PRIMARY KEY (id);
ANALYZE poi_tier_b_raw;

-- Percentile-normalise. Corpus-relative by construction: "82nd percentile
-- quiet" means quieter than 82% of Paris POIs, which is the claim the UI makes.
UPDATE poi p SET
  quietness        = r.q,
  touristiness     = r.t,
  greenness        = r.g,
  -- Inverted: friction is HIGH when the nearest metro is far away.
  transit_friction = r.f
FROM (
  SELECT id,
         percent_rank() OVER (ORDER BY quietness_raw)    AS q,
         percent_rank() OVER (ORDER BY touristiness_raw) AS t,
         percent_rank() OVER (ORDER BY greenness_raw)    AS g,
         percent_rank() OVER (ORDER BY transit_m)        AS f
  FROM poi_tier_b_raw
) r
WHERE p.id = r.id;

-- Provenance: tier B, confidence 0.8 -- computed from real geometry, but from
-- a formula with hand-chosen weights, so not the 1.0 of a direct OSM tag.
INSERT INTO poi_attr_provenance (poi_id, attr, tier, source, confidence)
SELECT id, a.attr, 'B', 'calc:tier_b_v1', 0.8
FROM poi CROSS JOIN (VALUES ('quietness'),('touristiness'),('greenness'),('transit_friction')) AS a(attr)
WHERE quietness IS NOT NULL
ON CONFLICT (poi_id, attr) DO UPDATE SET source = EXCLUDED.source, confidence = EXCLUDED.confidence;

ANALYZE poi;
