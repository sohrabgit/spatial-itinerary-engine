package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sohrab/travel-engine/ingest/internal/osrm"
	"github.com/sohrab/travel-engine/ingest/internal/sink"
)

// runSnap stores each POI's routable point alongside its display point.
func runSnap(ctx context.Context, workers int) error {
	start := time.Now()

	sk, err := sink.New(ctx, env("DATABASE_URL", "postgres://app:app@localhost:5432/itinerary"))
	if err != nil {
		return err
	}
	defer sk.Close()

	oc := osrm.New(env("OSRM_BASE_URL", "http://localhost:5001"))
	if err := oc.Health(ctx); err != nil {
		return err
	}

	pts, err := sk.LoadForSnap(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("== snap ==\nsnapping %d POIs via OSRM /nearest (%d workers)\n", len(pts), workers)

	jobs := make(chan int)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for i := range pts {
			select {
			case jobs <- i:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		return nil
	})
	var done atomic64
	for w := 0; w < workers; w++ {
		g.Go(func() error {
			for i := range jobs {
				s, err := oc.Nearest(gctx, pts[i].Lon, pts[i].Lat)
				if err != nil {
					return err
				}
				pts[i].SnapLon, pts[i].SnapLat, pts[i].SnapM = s.Lon, s.Lat, s.DistM
				if n := done.add(1); n%5000 == 0 {
					fmt.Printf("  snapped %d/%d\n", n, len(pts))
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("snap: %w", err)
	}

	res, err := sk.UpdateSnaps(ctx, pts)
	if err != nil {
		return err
	}
	fmt.Printf("updated %d in %s\n", res.Updated, res.Elapsed.Round(time.Millisecond))
	fmt.Printf("\nsnap distance distribution:\n%s\n", res.Histogram)
	fmt.Printf("total %s\n", since(start))
	return nil
}
