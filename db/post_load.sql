-- post_load.sql -- run by the ingester AFTER bulk COPY, never before.
--
-- Building HNSW before the load would mean inserting 150k rows into a live
-- graph index, which is pathological. Building it after costs one pass and
-- gives a better-quality graph.
--
-- CLUSTER is free here because the corpus is static between ingests, and it
-- matters: the Class-1 query path does an exact scan over ~3-8k candidates
-- selected by a spatial predicate. Physically co-locating those rows turns
-- scattered heap fetches into sequential ones.

\timing on

SET maintenance_work_mem = '2GB';
SET max_parallel_maintenance_workers = 4;

-- Primary vector index. m=16/ef_construction=200 trades build time for graph
-- quality -- correct here, since we build once offline and query forever.
DROP INDEX IF EXISTS poi_emb_hnsw;
CREATE INDEX poi_emb_hnsw
  ON poi_embedding USING hnsw (embedding halfvec_cosine_ops)
  WITH (m = 16, ef_construction = 200);

-- Class-3 partial indexes: ANN speed with ZERO recall loss, because every row
-- in the index already satisfies the filter. Worth it for a handful of
-- dominant categories only -- each costs build time and disk.
DROP INDEX IF EXISTS poi_emb_food_hnsw;
CREATE INDEX poi_emb_food_hnsw
  ON poi_embedding USING hnsw (embedding halfvec_cosine_ops)
  WITH (m = 16, ef_construction = 200)
  WHERE category IN ('cafe','restaurant','bar','bakery');

DROP INDEX IF EXISTS poi_emb_sights_hnsw;
CREATE INDEX poi_emb_sights_hnsw
  ON poi_embedding USING hnsw (embedding halfvec_cosine_ops)
  WITH (m = 16, ef_construction = 200)
  WHERE category IN ('museum','gallery','attraction','viewpoint','monument');

RESET maintenance_work_mem;

CLUSTER poi USING poi_geog_gist;

ANALYZE poi;
ANALYZE poi_embedding;
ANALYZE poi_hours;
