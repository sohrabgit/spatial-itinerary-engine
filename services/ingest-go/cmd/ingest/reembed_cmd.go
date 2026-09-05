package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sohrab/travel-engine/ingest/internal/embed"
	"github.com/sohrab/travel-engine/ingest/internal/poi"
	"github.com/sohrab/travel-engine/ingest/internal/sink"
)

// runReembed rebuilds embed_text using Tier B features and re-embeds.
//
// Must run AFTER `make tier-b`: before that, quietness is NULL and the text has
// no discriminating content beyond name and category.
func runReembed(ctx context.Context, batchSize, workers int) error {
	start := time.Now()

	dim, err := strconv.Atoi(env("OLLAMA_EMBED_DIM", "768"))
	if err != nil {
		return err
	}
	sk, err := sink.New(ctx, env("DATABASE_URL", "postgres://app:app@localhost:5432/itinerary"))
	if err != nil {
		return err
	}
	defer sk.Close()

	rows, err := sk.LoadForReembed(ctx)
	if err != nil {
		return err
	}
	withTierB := 0
	for i := range rows {
		if rows[i].Quietness != nil {
			withTierB++
		}
		hoursKnown := rows[i].OpeningHoursRaw != nil
		human := ""
		if hoursKnown {
			human = *rows[i].OpeningHoursRaw
		}
		rows[i].EmbedText = poi.BuildEmbedText(poi.EmbedInput{
			Name: rows[i].Name, Category: rows[i].Category, Cuisine: rows[i].Cuisine,
			HasWifi: rows[i].HasWifi, Outdoor: rows[i].OutdoorSeating,
			Wheelchair: deref(rows[i].Wheelchair),
			Quietness:  rows[i].Quietness, Touristiness: rows[i].Touristiness,
			HoursHuman: human, HoursKnown: hoursKnown,
		})
	}
	if withTierB == 0 {
		return fmt.Errorf("no POI has Tier B features -- run `make tier-b` first")
	}
	fmt.Printf("== reembed ==\n%d POIs, %d with Tier B features\nexample: %s\n\n",
		len(rows), withTierB, rows[0].EmbedText)

	ec := embed.New(env("OLLAMA_BASE_URL", "http://localhost:11434"),
		env("OLLAMA_EMBED_MODEL", "nomic-embed-text"), dim)
	if err := ec.Health(ctx); err != nil {
		return err
	}

	t0 := time.Now()
	type job struct{ lo, hi int }
	jobs := make(chan job)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for lo := 0; lo < len(rows); lo += batchSize {
			select {
			case jobs <- job{lo, min(lo+batchSize, len(rows))}:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		return nil
	})
	var done atomic64
	for w := 0; w < workers; w++ {
		g.Go(func() error {
			texts := make([]string, 0, batchSize)
			for j := range jobs {
				texts = texts[:0]
				for i := j.lo; i < j.hi; i++ {
					texts = append(texts, rows[i].EmbedText)
				}
				vecs, err := ec.Embed(gctx, texts)
				if err != nil {
					return err
				}
				for i := j.lo; i < j.hi; i++ {
					rows[i].Embedding = vecs[i-j.lo]
				}
				if n := done.add(int64(j.hi - j.lo)); n%5120 == 0 {
					fmt.Printf("  embedded %d/%d\n", n, len(rows))
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	fmt.Printf("%d vectors in %s (%.0f/sec)\n", len(rows), since(t0),
		float64(len(rows))/time.Since(t0).Seconds())

	n, dur, err := sk.UpdateEmbeddings(ctx, rows)
	if err != nil {
		return err
	}
	fmt.Printf("updated %d embeddings in %s\ntotal %s -- now run: make post-load\n",
		n, dur.Round(time.Millisecond), since(start))
	return nil
}
