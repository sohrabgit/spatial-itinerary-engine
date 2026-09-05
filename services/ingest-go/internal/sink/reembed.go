package sink

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReembedRow carries everything BuildEmbedText needs, read back AFTER Tier B
// has been computed.
//
// The pipeline is necessarily two-phase: Tier B features are computed from the
// geometry of the loaded corpus, so they cannot exist when the corpus is first
// embedded. Re-embedding is what turns a 91-character template into text with
// actual discriminating content.
type ReembedRow struct {
	ID              int64
	Name            string
	Category        string
	Cuisine         []string
	HasWifi         *bool
	OutdoorSeating  *bool
	Wheelchair      *string
	Quietness       *float64
	Touristiness    *float64
	OpeningHoursRaw *string
	EmbedText       string
	Embedding       []float32
}

func (s *Sink) LoadForReembed(ctx context.Context) ([]ReembedRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, category, cuisine, has_wifi, outdoor_seating, wheelchair,
		       quietness, touristiness, opening_hours_raw
		FROM poi ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load for reembed: %w", err)
	}
	defer rows.Close()

	var out []ReembedRow
	for rows.Next() {
		var r ReembedRow
		var q, t *float64
		if err := rows.Scan(&r.ID, &r.Name, &r.Category, &r.Cuisine, &r.HasWifi,
			&r.OutdoorSeating, &r.Wheelchair, &q, &t, &r.OpeningHoursRaw); err != nil {
			return nil, err
		}
		r.Quietness, r.Touristiness = q, t
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateEmbeddings rewrites embed_text and the vector for every row.
func (s *Sink) UpdateEmbeddings(ctx context.Context, rows []ReembedRow) (int64, time.Duration, error) {
	t0 := time.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE reembed_stage (poi_id bigint, embed_text text, embedding text)
		ON COMMIT DROP`); err != nil {
		return 0, 0, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"reembed_stage"},
		[]string{"poi_id", "embed_text", "embedding"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			return []any{rows[i].ID, rows[i].EmbedText, vectorLiteral(rows[i].Embedding)}, nil
		})); err != nil {
		return 0, 0, fmt.Errorf("copy reembed: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE poi p SET embed_text = s.embed_text
		FROM reembed_stage s WHERE p.id = s.poi_id`); err != nil {
		return 0, 0, fmt.Errorf("update embed_text: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE poi_embedding e SET embedding = s.embedding::halfvec
		FROM reembed_stage s WHERE e.poi_id = s.poi_id`)
	if err != nil {
		return 0, 0, fmt.Errorf("update embeddings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return tag.RowsAffected(), time.Since(t0), nil
}
