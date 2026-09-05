-- Runs once, on first init of an empty data directory.
-- The healthcheck asserts 'vector' is present, which catches the failure mode
-- where the container is up but these scripts silently did not run.

CREATE EXTENSION IF NOT EXISTS postgis;          -- geography/geometry, GiST, ST_DWithin
CREATE EXTENSION IF NOT EXISTS vector;           -- halfvec, HNSW, iterative index scans
CREATE EXTENSION IF NOT EXISTS pg_trgm;          -- fuzzy POI name matching ("saint chapelle")
CREATE EXTENSION IF NOT EXISTS btree_gist;       -- mixed btree+gist constraints
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Recorded so drift is visible in logs and in `make verify`.
DO $$
BEGIN
  RAISE NOTICE 'postgis=% pgvector=%',
    (SELECT extversion FROM pg_extension WHERE extname = 'postgis'),
    (SELECT extversion FROM pg_extension WHERE extname = 'vector');
END $$;
