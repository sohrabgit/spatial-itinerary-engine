-- 0001_core.sql -- POI system of record.
--
-- Design notes that are not obvious from the DDL:
--
--  * poi_embedding is a SIDECAR table, not a column on poi. A vector(768) is
--    3076 bytes, above PostgreSQL's ~2000-byte TOAST threshold, so it would be
--    stored out-of-line and every row in the Class-1 brute-force scan would pay
--    a detoast. halfvec(768) measures 1544 bytes and fits inline -- but only if
--    no other column pushes the tuple over. Hence the sidecar + STORAGE PLAIN.
--
--  * has_wifi is deliberately TRI-STATE (true/false/NULL). OSM's
--    internet_access coverage on Paris cafes is roughly 5-15%. Coercing unknown
--    to false makes a wifi filter return almost nothing. Unknown means unknown.
--
--  * Tier B attributes (quietness, touristiness, ...) are COMPUTED percentiles,
--    not tags. There is no quiet=yes in OSM. See docs/adr/0005.
--
--  * poi_hours is one row per (poi, weekday, interval) because a POI may open
--    twice a day ("Mo-Fr 09:00-12:00,14:00-18:00") and OR-Tools allows only one
--    time window per node -- multi-interval is modelled by node duplication.

BEGIN;

-- ---------------------------------------------------------------- system of record

CREATE TABLE poi (
  id                bigserial PRIMARY KEY,

  -- OSM identity. Type+id is unique across the planet.
  osm_type          char(1)     NOT NULL CHECK (osm_type IN ('n','w','r')),
  osm_id            bigint      NOT NULL,

  name              text        NOT NULL CHECK (length(btrim(name)) > 0),
  name_en           text,
  category          text        NOT NULL,   -- normalised: museum|cafe|park|viewpoint|...
  subcategory       text,

  -- Display centroid vs routing point. OSRM snaps to the nearest routable edge;
  -- for large complexes (Louvre, Pere-Lachaise) that can be 200m+ from the
  -- centroid, silently corrupting every duration in the matrix. Route on
  -- snap_geog, display geog.
  geog              geography(Point,4326) NOT NULL,
  snap_geog         geography(Point,4326),
  snap_distance_m   real        CHECK (snap_distance_m IS NULL OR snap_distance_m >= 0),

  arrondissement    smallint    CHECK (arrondissement IS NULL OR arrondissement BETWEEN 1 AND 20),

  -- Tier A: direct OSM tags, confidence 1.0 --------------------------------
  has_wifi          boolean,               -- NULL = unknown, NOT false
  wheelchair        text        CHECK (wheelchair IS NULL OR wheelchair IN ('yes','limited','no','designated')),
  outdoor_seating   boolean,
  cuisine           text[],
  website           text,
  opening_hours_raw text,                  -- verbatim; parsed by the Python worker

  -- Tier B: computed spatial features, percentile-normalised ---------------
  quietness         real CHECK (quietness        IS NULL OR quietness        BETWEEN 0 AND 1),
  touristiness      real CHECK (touristiness     IS NULL OR touristiness     BETWEEN 0 AND 1),
  greenness         real CHECK (greenness        IS NULL OR greenness        BETWEEN 0 AND 1),
  transit_friction  real CHECK (transit_friction IS NULL OR transit_friction BETWEEN 0 AND 1),

  -- Tier C: generated prose, used for embedding text only, never as a filter
  description_llm   text,

  -- Exactly what was embedded, so retrieval results are explainable and the
  -- template-vs-LLM ablation is reproducible.
  embed_text        text        NOT NULL,
  embed_source      text        NOT NULL CHECK (embed_source IN ('template','llm')),

  dwell_minutes     smallint    NOT NULL DEFAULT 30 CHECK (dwell_minutes BETWEEN 5 AND 480),
  popularity        real        NOT NULL DEFAULT 0  CHECK (popularity BETWEEN 0 AND 1),

  tags              jsonb       NOT NULL DEFAULT '{}'::jsonb,
  ingested_at       timestamptz NOT NULL DEFAULT now(),

  UNIQUE (osm_type, osm_id)
);

-- ---------------------------------------------------------------- embeddings

CREATE TABLE poi_embedding (
  poi_id    bigint PRIMARY KEY REFERENCES poi(id) ON DELETE CASCADE,
  -- Denormalised so Class-3 partial HNSW indexes can express their predicate
  -- locally, without a join back to poi.
  category  text        NOT NULL,
  embedding halfvec(768) NOT NULL
);
-- Keep the vector inline; see the header note on TOAST.
ALTER TABLE poi_embedding ALTER COLUMN embedding SET STORAGE PLAIN;

-- ---------------------------------------------------------------- opening hours

CREATE TABLE poi_hours (
  poi_id  bigint   NOT NULL REFERENCES poi(id) ON DELETE CASCADE,
  dow     smallint NOT NULL CHECK (dow BETWEEN 0 AND 6),   -- 0 = Monday
  open_m  smallint NOT NULL CHECK (open_m  BETWEEN 0 AND 1440),
  close_m smallint NOT NULL CHECK (close_m BETWEEN 0 AND 1440),
  CHECK (close_m > open_m),
  PRIMARY KEY (poi_id, dow, open_m)
);

-- ---------------------------------------------------------------- provenance

-- Every derived attribute is auditable: which tier produced it, from what, and
-- how confident. This is what lets the UI say "quiet (82nd percentile)" and
-- show its work, rather than asserting an unfalsifiable claim.
CREATE TABLE poi_attr_provenance (
  poi_id     bigint  NOT NULL REFERENCES poi(id) ON DELETE CASCADE,
  attr       text    NOT NULL,
  tier       char(1) NOT NULL CHECK (tier IN ('A','B','C')),
  source     text    NOT NULL,   -- 'osm:internet_access' | 'calc:quietness_v2' | 'llm:llama3.2:3b'
  confidence real    NOT NULL CHECK (confidence BETWEEN 0 AND 1),
  PRIMARY KEY (poi_id, attr)
);

-- ---------------------------------------------------------------- LLM audit trail

-- Every structured-output call. Doubles as the M2 eval dataset and as the
-- source of OTel span attributes.
CREATE TABLE llm_trace (
  id            bigserial PRIMARY KEY,
  created_at    timestamptz NOT NULL DEFAULT now(),
  model         text        NOT NULL,
  purpose       text        NOT NULL,   -- 'intent' | 'narration' | 'enrich'
  prompt        text        NOT NULL,
  raw_output    text,
  parsed        jsonb,
  valid         boolean     NOT NULL,
  retries       smallint    NOT NULL DEFAULT 0,
  latency_ms    integer,
  trace_id      text                    -- correlates to Tempo
);

-- ---------------------------------------------------------------- indexes
-- Cheap indexes only. HNSW and CLUSTER are built AFTER bulk load by
-- db/post_load.sql -- inserting 150k rows into an existing HNSW is pathological.

CREATE INDEX poi_geog_gist   ON poi USING gist (geog);
CREATE INDEX poi_snap_gist   ON poi USING gist (snap_geog);
CREATE INDEX poi_cat_btree   ON poi (category);
CREATE INDEX poi_name_trgm   ON poi USING gin (name gin_trgm_ops);
CREATE INDEX poi_tags_gin    ON poi USING gin (tags jsonb_path_ops);

-- French stemming does little for proper nouns, so pg_trgm above carries most
-- of the lexical load. FTS still helps on the descriptive embed_text.
CREATE INDEX poi_fts_gin ON poi USING gin (
  to_tsvector('french', coalesce(name,'') || ' ' || coalesce(embed_text,''))
);

CREATE INDEX poi_hours_lookup ON poi_hours (dow, open_m, close_m);
CREATE INDEX llm_trace_purpose ON llm_trace (purpose, created_at DESC);

COMMIT;
