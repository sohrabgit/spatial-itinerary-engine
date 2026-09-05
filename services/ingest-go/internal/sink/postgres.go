// Package sink writes POIs to Postgres.
//
// COPY into an UNLOGGED staging table, then a single merge, rather than batched
// INSERTs: COPY is 5-20x faster and is what pgx is genuinely good at. Indexes
// are built afterwards by db/post_load.sql -- inserting 28k rows into a live
// HNSW graph is pathological.
package sink

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Row struct {
	OSMType         byte
	OSMID           int64
	Name            string
	Category        string
	Lat, Lon        float64
	HasWifi         *bool
	Wheelchair      *string
	OutdoorSeating  *bool
	Cuisine         []string
	Website         *string
	OpeningHoursRaw *string
	DwellMinutes    int16
	EmbedText       string
	EmbedSource     string
	Tags            []byte    // json
	Embedding       []float32
}

type Sink struct{ pool *pgxpool.Pool }

func New(ctx context.Context, dsn string) (*Sink, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Sink{pool: pool}, nil
}

func (s *Sink) Close() { s.pool.Close() }

// AssertEmbeddingDim fails loudly if the column and the model disagree. A
// silent mismatch yields garbage similarity scores, never an error.
func (s *Sink) AssertEmbeddingDim(ctx context.Context, want int) error {
	var got int
	err := s.pool.QueryRow(ctx, `
		SELECT atttypmod FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = 'poi_embedding' AND a.attname = 'embedding'`).Scan(&got)
	if err != nil {
		return fmt.Errorf("read embedding dim (did migrations run?): %w", err)
	}
	if got != want {
		return fmt.Errorf("poi_embedding is halfvec(%d) but OLLAMA_EMBED_DIM=%d", got, want)
	}
	return nil
}

const stageDDL = `
DROP TABLE IF EXISTS poi_stage;
CREATE UNLOGGED TABLE poi_stage (
  osm_type char(1), osm_id bigint, name text, category text,
  lat double precision, lon double precision,
  has_wifi boolean, wheelchair text, outdoor_seating boolean,
  cuisine text[], website text, opening_hours_raw text,
  dwell_minutes smallint, embed_text text, embed_source text,
  tags jsonb, embedding text
);`

type Result struct {
	Staged, Merged, Embedded, Hours, Provenance int64
	Copy, Merge                                 time.Duration
}

// Load stages every row, then merges in one transaction.
func (s *Sink) Load(ctx context.Context, rows []Row, defaults HoursProvider) (Result, error) {
	var res Result

	if _, err := s.pool.Exec(ctx, stageDDL); err != nil {
		return res, fmt.Errorf("create staging: %w", err)
	}

	t0 := time.Now()
	n, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"poi_stage"},
		[]string{"osm_type", "osm_id", "name", "category", "lat", "lon",
			"has_wifi", "wheelchair", "outdoor_seating", "cuisine", "website",
			"opening_hours_raw", "dwell_minutes", "embed_text", "embed_source",
			"tags", "embedding"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			return []any{
				string(r.OSMType), r.OSMID, r.Name, r.Category, r.Lat, r.Lon,
				r.HasWifi, r.Wheelchair, r.OutdoorSeating, r.Cuisine, r.Website,
				r.OpeningHoursRaw, r.DwellMinutes, r.EmbedText, r.EmbedSource,
				string(r.Tags), vectorLiteral(r.Embedding),
			}, nil
		}))
	if err != nil {
		return res, fmt.Errorf("copy: %w", err)
	}
	res.Staged = n
	res.Copy = time.Since(t0)

	t1 := time.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	tag, err := tx.Exec(ctx, `
		INSERT INTO poi (osm_type, osm_id, name, category, geog,
		                 has_wifi, wheelchair, outdoor_seating, cuisine, website,
		                 opening_hours_raw, dwell_minutes, embed_text, embed_source, tags)
		SELECT osm_type, osm_id, name, category,
		       ST_SetSRID(ST_MakePoint(lon, lat), 4326)::geography,
		       has_wifi, wheelchair, outdoor_seating, cuisine, website,
		       opening_hours_raw, dwell_minutes, embed_text, embed_source, tags
		FROM poi_stage
		ON CONFLICT (osm_type, osm_id) DO UPDATE SET
		  name = EXCLUDED.name, category = EXCLUDED.category, geog = EXCLUDED.geog,
		  has_wifi = EXCLUDED.has_wifi, wheelchair = EXCLUDED.wheelchair,
		  outdoor_seating = EXCLUDED.outdoor_seating, cuisine = EXCLUDED.cuisine,
		  website = EXCLUDED.website, opening_hours_raw = EXCLUDED.opening_hours_raw,
		  dwell_minutes = EXCLUDED.dwell_minutes, embed_text = EXCLUDED.embed_text,
		  embed_source = EXCLUDED.embed_source, tags = EXCLUDED.tags,
		  ingested_at = now()`)
	if err != nil {
		return res, fmt.Errorf("merge poi: %w", err)
	}
	res.Merged = tag.RowsAffected()

	tag, err = tx.Exec(ctx, `
		INSERT INTO poi_embedding (poi_id, category, embedding)
		SELECT p.id, p.category, s.embedding::halfvec
		FROM poi_stage s JOIN poi p ON p.osm_type = s.osm_type AND p.osm_id = s.osm_id
		ON CONFLICT (poi_id) DO UPDATE SET
		  embedding = EXCLUDED.embedding, category = EXCLUDED.category`)
	if err != nil {
		return res, fmt.Errorf("merge embeddings: %w", err)
	}
	res.Embedded = tag.RowsAffected()

	// Tier A provenance for attributes that came straight from OSM tags.
	tag, err = tx.Exec(ctx, `
		INSERT INTO poi_attr_provenance (poi_id, attr, tier, source, confidence)
		SELECT p.id, 'has_wifi', 'A', 'osm:internet_access', 1.0
		FROM poi_stage s JOIN poi p ON p.osm_type = s.osm_type AND p.osm_id = s.osm_id
		WHERE s.has_wifi IS NOT NULL
		ON CONFLICT (poi_id, attr) DO UPDATE SET
		  source = EXCLUDED.source, confidence = EXCLUDED.confidence`)
	if err != nil {
		return res, fmt.Errorf("provenance: %w", err)
	}
	res.Provenance = tag.RowsAffected()

	hoursSQL, err := defaults.SQL()
	if err != nil {
		return res, err
	}
	tag, err = tx.Exec(ctx, hoursSQL)
	if err != nil {
		return res, fmt.Errorf("hours: %w", err)
	}
	res.Hours = tag.RowsAffected()

	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS poi_stage`); err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	res.Merge = time.Since(t1)
	return res, nil
}

// HoursProvider emits the SQL that seeds poi_hours.
type HoursProvider interface{ SQL() (string, error) }

// vectorLiteral renders pgvector's text input format. Cheaper than registering
// a custom pgx type for a value that is written once and read as halfvec.
func vectorLiteral(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v) * 9)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}
