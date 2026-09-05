-- popularity.sql -- notability, derived without any external API.
--
-- The zero-cloud mandate rules out Wikipedia pageviews, Google popularity or a
-- reviews API. But OSM carries its own notability cross-references, and their
-- coverage tracks importance closely: 88.9% of castles and 74.4% of museums
-- have a wikidata tag, against 2.9% of galleries.
--
-- Without this, popularity is 0 everywhere, the RRF "prior" arm contributes
-- nothing, and ranking collapses onto quietness alone -- which surfaced zoo
-- enclosures ("Chevres du Senegal") above Notre-Dame for a historic-walk query.
--
-- Signals, strongest first:
--   wikipedia  an actual article exists
--   wikidata   a structured entity exists
--   heritage   officially listed/protected
--   name:*     mapped in several languages = international attention
--   tag count  richly mapped features get more mapper attention

\timing on

UPDATE poi p SET popularity = r.pct
FROM (
  SELECT id, percent_rank() OVER (ORDER BY raw) AS pct
  FROM (
    SELECT id,
        2.0 * (CASE WHEN tags ? 'wikipedia' THEN 1 ELSE 0 END)
      + 1.5 * (CASE WHEN tags ? 'wikidata'  THEN 1 ELSE 0 END)
      + 1.0 * (CASE WHEN tags ? 'heritage'  THEN 1 ELSE 0 END)
      + 0.5 * ln(1 + (SELECT count(*) FROM jsonb_object_keys(tags) k WHERE k LIKE 'name:%'))
      + 0.3 * ln(1 + (SELECT count(*) FROM jsonb_object_keys(tags) k))
      AS raw
    FROM poi
  ) s
) r
WHERE p.id = r.id;

INSERT INTO poi_attr_provenance (poi_id, attr, tier, source, confidence)
SELECT id, 'popularity', 'B', 'calc:osm_notability_v1', 0.7 FROM poi
ON CONFLICT (poi_id, attr) DO UPDATE SET source = EXCLUDED.source, confidence = EXCLUDED.confidence;

ANALYZE poi;
