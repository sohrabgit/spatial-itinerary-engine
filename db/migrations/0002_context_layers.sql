-- 0002_context_layers.sql -- geometry that Tier B attributes are computed FROM.
--
-- "Quiet" has no OSM tag (ADR 0005), so it is calculated: distance to noisy
-- roads, proximity to pedestrian streets, POI density, park and metro
-- proximity. That needs road and transit geometry, which the POI extract does
-- not carry. These tables exist purely as inputs to the Tier B computation and
-- are never served directly.

BEGIN;

CREATE TABLE road (
  id     bigserial PRIMARY KEY,
  osm_id bigint NOT NULL,
  class  text   NOT NULL CHECK (class IN ('major','pedestrian')),
  geog   geography(LineString,4326) NOT NULL
);
COMMENT ON TABLE road IS 'Tier B input only: major roads are a noise source, pedestrian ways a calm indicator.';

CREATE TABLE transit_entrance (
  id     bigserial PRIMARY KEY,
  osm_id bigint NOT NULL UNIQUE,
  name   text,
  geog   geography(Point,4326) NOT NULL
);
COMMENT ON TABLE transit_entrance IS 'Tier B input only: metro/RER entrances. Near = convenient but noisy.';

CREATE INDEX road_geog_gist     ON road USING gist (geog);
CREATE INDEX road_class         ON road (class);
CREATE INDEX transit_geog_gist  ON transit_entrance USING gist (geog);

COMMIT;
