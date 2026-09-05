package sink

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type RoadRow struct {
	OSMID int64
	Class string
	WKT   string
}

type EntranceRow struct {
	OSMID    int64
	Name     *string
	Lat, Lon float64
}

type ContextResult struct {
	Roads, Entrances int64
	Elapsed          time.Duration
}

// LoadContext replaces the Tier B input layers wholesale. These tables are
// derived data with no external references, so truncate-and-reload is simpler
// and faster than a merge, and avoids accumulating stale geometry across runs.
func (s *Sink) LoadContext(ctx context.Context, roads []RoadRow, ents []EntranceRow) (ContextResult, error) {
	var res ContextResult
	t0 := time.Now()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx, `TRUNCATE road, transit_entrance RESTART IDENTITY`); err != nil {
		return res, fmt.Errorf("truncate context: %w", err)
	}

	// Staging as text, cast to geography on insert: keeps WKB encoding out of
	// Go and lets PostGIS validate the geometry.
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE road_stage (osm_id bigint, class text, wkt text) ON COMMIT DROP`); err != nil {
		return res, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"road_stage"},
		[]string{"osm_id", "class", "wkt"},
		pgx.CopyFromSlice(len(roads), func(i int) ([]any, error) {
			return []any{roads[i].OSMID, roads[i].Class, roads[i].WKT}, nil
		})); err != nil {
		return res, fmt.Errorf("copy roads: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO road (osm_id, class, geog)
		SELECT osm_id, class, ST_GeogFromText('SRID=4326;' || wkt) FROM road_stage`)
	if err != nil {
		return res, fmt.Errorf("insert roads: %w", err)
	}
	res.Roads = tag.RowsAffected()

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE ent_stage (osm_id bigint, name text, lat float8, lon float8) ON COMMIT DROP`); err != nil {
		return res, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"ent_stage"},
		[]string{"osm_id", "name", "lat", "lon"},
		pgx.CopyFromSlice(len(ents), func(i int) ([]any, error) {
			return []any{ents[i].OSMID, ents[i].Name, ents[i].Lat, ents[i].Lon}, nil
		})); err != nil {
		return res, fmt.Errorf("copy entrances: %w", err)
	}
	// OSM sometimes carries duplicate entrance nodes; the unique constraint
	// would abort the whole load, so collapse them here.
	tag, err = tx.Exec(ctx, `
		INSERT INTO transit_entrance (osm_id, name, geog)
		SELECT DISTINCT ON (osm_id) osm_id, name,
		       ST_SetSRID(ST_MakePoint(lon, lat), 4326)::geography
		FROM ent_stage ORDER BY osm_id`)
	if err != nil {
		return res, fmt.Errorf("insert entrances: %w", err)
	}
	res.Entrances = tag.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.Elapsed = time.Since(t0)
	return res, nil
}
