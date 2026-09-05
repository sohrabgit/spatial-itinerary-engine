package sink

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type SnapPoint struct {
	ID                        int64
	Lon, Lat                  float64
	SnapLon, SnapLat, SnapM   float64
}

type SnapResult struct {
	Updated   int64
	Elapsed   time.Duration
	Histogram string
}

func (s *Sink) LoadForSnap(ctx context.Context) ([]SnapPoint, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, ST_X(geog::geometry), ST_Y(geog::geometry) FROM poi ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load for snap: %w", err)
	}
	defer rows.Close()
	var out []SnapPoint
	for rows.Next() {
		var p SnapPoint
		if err := rows.Scan(&p.ID, &p.Lon, &p.Lat); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Sink) UpdateSnaps(ctx context.Context, pts []SnapPoint) (SnapResult, error) {
	var res SnapResult
	t0 := time.Now()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE snap_stage (poi_id bigint, lon float8, lat float8, dist real)
		ON COMMIT DROP`); err != nil {
		return res, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"snap_stage"},
		[]string{"poi_id", "lon", "lat", "dist"},
		pgx.CopyFromSlice(len(pts), func(i int) ([]any, error) {
			return []any{pts[i].ID, pts[i].SnapLon, pts[i].SnapLat, float32(pts[i].SnapM)}, nil
		})); err != nil {
		return res, fmt.Errorf("copy snaps: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE poi p SET
		  snap_geog = ST_SetSRID(ST_MakePoint(s.lon, s.lat), 4326)::geography,
		  snap_distance_m = s.dist
		FROM snap_stage s WHERE p.id = s.poi_id`)
	if err != nil {
		return res, fmt.Errorf("update snaps: %w", err)
	}
	res.Updated = tag.RowsAffected()

	// Surfaced as an ingest metric: a long tail here means routing durations
	// are being computed from the wrong points.
	var b strings.Builder
	hrows, err := tx.Query(ctx, `
		SELECT CASE
		         WHEN dist <   10 THEN 'a. <10m   (on the street)'
		         WHEN dist <   50 THEN 'b. 10-50m'
		         WHEN dist <  150 THEN 'c. 50-150m'
		         WHEN dist <  400 THEN 'd. 150-400m  REVIEW'
		         ELSE                  'e. >400m     SUSPECT'
		       END AS bucket, count(*)
		FROM snap_stage GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return res, err
	}
	for hrows.Next() {
		var bucket string
		var n int64
		if err := hrows.Scan(&bucket, &n); err != nil {
			hrows.Close()
			return res, err
		}
		fmt.Fprintf(&b, "  %-26s %6d\n", bucket, n)
	}
	hrows.Close()
	res.Histogram = b.String()

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.Elapsed = time.Since(t0)
	return res, nil
}
