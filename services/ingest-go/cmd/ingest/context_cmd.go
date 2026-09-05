package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sohrab/travel-engine/ingest/internal/pbf"
	"github.com/sohrab/travel-engine/ingest/internal/sink"
)

// runContext extracts and loads the geometry Tier B is computed from.
func runContext(ctx context.Context, pbfPath string) error {
	start := time.Now()
	roads, ents, st, err := pbf.ScanContext(ctx, pbfPath)
	if err != nil {
		return err
	}
	fmt.Printf("== context scan ==\n%s\n\n", st)

	sk, err := sink.New(ctx, env("DATABASE_URL", "postgres://app:app@localhost:5432/itinerary"))
	if err != nil {
		return err
	}
	defer sk.Close()

	rows := make([]sink.RoadRow, len(roads))
	for i, r := range roads {
		rows[i] = sink.RoadRow{OSMID: r.OSMID, Class: r.Class, WKT: r.WKT}
	}
	erows := make([]sink.EntranceRow, len(ents))
	for i, e := range ents {
		var name *string
		if e.Name != "" {
			n := e.Name
			name = &n
		}
		erows[i] = sink.EntranceRow{OSMID: e.OSMID, Name: name, Lat: e.Lat, Lon: e.Lon}
	}

	res, err := sk.LoadContext(ctx, rows, erows)
	if err != nil {
		return err
	}
	fmt.Printf("== context load ==\nroads=%d entrances=%d in %s\ntotal %s -- now run: make tier-b\n",
		res.Roads, res.Entrances, res.Elapsed.Round(time.Millisecond), since(start))
	return nil
}
