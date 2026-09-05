// Command ingest turns an OSM PBF extract into embedded, queryable POIs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/sohrab/travel-engine/ingest/internal/embed"
	"github.com/sohrab/travel-engine/ingest/internal/pbf"
	"github.com/sohrab/travel-engine/ingest/internal/poi"
	"github.com/sohrab/travel-engine/ingest/internal/sink"
)

func main() {
	var (
		pbfPath    = flag.String("pbf", "data/paris.osm.pbf", "OSM PBF extract")
		filterPath = flag.String("filter", "poi-filter.yaml", "POI taxonomy")
		report     = flag.Bool("report", false, "print tag coverage and exit without loading")
		limit      = flag.Int("limit", 0, "cap features (0 = all); for fast dev iteration")
		batch      = flag.Int("batch", 64, "embedding batch size")
		workers    = flag.Int("workers", 3, "concurrent embed workers")
		ctxLayers  = flag.Bool("context", false, "extract road/transit geometry for Tier B, then exit")
		reembed    = flag.Bool("reembed", false, "rebuild embed_text with Tier B features and re-embed")
		snap       = flag.Bool("snap", false, "store OSRM-snapped routing points for every POI")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch {
	case *ctxLayers:
		err = runContext(ctx, *pbfPath)
	case *reembed:
		err = runReembed(ctx, *batch, *workers)
	case *snap:
		err = runSnap(ctx, 8)
	default:
		err = run(ctx, *pbfPath, *filterPath, *report, *limit, *batch, *workers)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\ningest: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, pbfPath, filterPath string, report bool, limit, batchSize, workers int) error {
	f, err := poi.LoadFilter(filterPath)
	if err != nil {
		return err
	}

	// ---- scan ------------------------------------------------------------
	start := time.Now()
	features, st, err := pbf.Scan(ctx, pbfPath, f)
	if err != nil {
		return err
	}
	features = clipBBox(features, env("OSM_BBOX", ""))
	fmt.Printf("== scan ==\n%s\ntotal=%d features in %s\n\n", st, len(features), since(start))

	if report {
		printCoverage(features)
		return nil
	}
	if limit > 0 && limit < len(features) {
		features = features[:limit]
		fmt.Printf("-limit: capped to %d features\n\n", len(features))
	}

	// ---- connect + preflight ---------------------------------------------
	dim, err := strconv.Atoi(env("OLLAMA_EMBED_DIM", "768"))
	if err != nil {
		return fmt.Errorf("OLLAMA_EMBED_DIM: %w", err)
	}
	sk, err := sink.New(ctx, env("DATABASE_URL", "postgres://app:app@localhost:5432/itinerary"))
	if err != nil {
		return err
	}
	defer sk.Close()
	if err := sk.AssertEmbeddingDim(ctx, dim); err != nil {
		return err
	}

	ec := embed.New(env("OLLAMA_BASE_URL", "http://localhost:11434"),
		env("OLLAMA_EMBED_MODEL", "nomic-embed-text"), dim)
	if err := ec.Health(ctx); err != nil {
		return err
	}

	// ---- build rows ------------------------------------------------------
	rows := make([]sink.Row, len(features))
	for i, ft := range features {
		rows[i] = toRow(ft)
	}

	// ---- embed -----------------------------------------------------------
	// 3 workers, not more: the host GPU serialises anyway, so extra concurrency
	// only adds queueing and makes failures harder to attribute.
	t0 := time.Now()
	type job struct{ lo, hi int }
	jobs := make(chan job)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for lo := 0; lo < len(rows); lo += batchSize {
			hi := min(lo+batchSize, len(rows))
			select {
			case jobs <- job{lo, hi}:
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
				if n := done.add(int64(j.hi - j.lo)); n%2560 == 0 {
					fmt.Printf("  embedded %d/%d\n", n, len(rows))
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	embedDur := time.Since(t0)
	fmt.Printf("== embed ==\n%d vectors in %s (%.0f/sec)\n\n",
		len(rows), since(t0), float64(len(rows))/embedDur.Seconds())

	// ---- load ------------------------------------------------------------
	res, err := sk.Load(ctx, rows, sink.CategoryDefaults{ByCategory: hoursFromFilter(f)})
	if err != nil {
		return err
	}
	fmt.Printf("== load ==\nstaged=%d merged=%d embedded=%d hours=%d provenance=%d\ncopy=%s merge=%s\n\n",
		res.Staged, res.Merged, res.Embedded, res.Hours, res.Provenance,
		res.Copy.Round(time.Millisecond), res.Merge.Round(time.Millisecond))
	fmt.Printf("total %s -- now run: make post-load\n", since(start))
	return nil
}

func toRow(ft pbf.Feature) sink.Row {
	t := ft.Tags
	r := sink.Row{
		OSMType: ft.OSMType, OSMID: ft.OSMID, Name: t["name"],
		Category: ft.Category, Lat: ft.Lat, Lon: ft.Lon,
		DwellMinutes: ft.Dwell, EmbedSource: "template",
	}
	// Tri-state: absent means unknown, never false. At 3.8% coverage this
	// distinction is the difference between a usable filter and an empty one.
	if v, ok := t["internet_access"]; ok {
		b := v == "wlan" || v == "yes" || v == "wired"
		r.HasWifi = &b
	}
	if v, ok := t["wheelchair"]; ok && isWheelchair(v) {
		r.Wheelchair = &v
	}
	if v, ok := t["outdoor_seating"]; ok {
		b := v == "yes"
		r.OutdoorSeating = &b
	}
	if v, ok := t["website"]; ok {
		r.Website = &v
	}
	if v, ok := t["opening_hours"]; ok {
		r.OpeningHoursRaw = &v
	}
	if v, ok := t["cuisine"]; ok {
		r.Cuisine = strings.Split(v, ";")
	}

	hoursKnown := r.OpeningHoursRaw != nil
	human := ""
	if hoursKnown {
		human = *r.OpeningHoursRaw
	}
	r.EmbedText = poi.BuildEmbedText(poi.EmbedInput{
		Name: r.Name, Category: r.Category, Cuisine: r.Cuisine,
		HasWifi: r.HasWifi, Outdoor: r.OutdoorSeating,
		Wheelchair: deref(r.Wheelchair), HoursHuman: human, HoursKnown: hoursKnown,
	})
	r.Tags, _ = json.Marshal(t)
	return r
}

func hoursFromFilter(f *poi.Filter) map[string]sink.DefaultHours {
	out := make(map[string]sink.DefaultHours, len(f.DefaultHours))
	for cat, d := range f.DefaultHours {
		out[cat] = sink.DefaultHours{Open: d.Open, Close: d.Close, Days: d.Days}
	}
	return out
}

// clipBBox drops features outside the window. osmium's extract uses a
// complete-ways strategy, so the .pbf legitimately holds geometry well outside
// the requested box; this is the precise cut.
func clipBBox(in []pbf.Feature, spec string) []pbf.Feature {
	var minLon, minLat, maxLon, maxLat float64
	if _, err := fmt.Sscanf(spec, "%f,%f,%f,%f", &minLon, &minLat, &maxLon, &maxLat); err != nil {
		return in
	}
	out := in[:0]
	for _, ft := range in {
		if ft.Lon >= minLon && ft.Lon <= maxLon && ft.Lat >= minLat && ft.Lat <= maxLat {
			out = append(out, ft)
		}
	}
	return out
}

func printCoverage(fs []pbf.Feature) {
	byCat := map[string]int{}
	for _, f := range fs {
		byCat[f.Category]++
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return byCat[cats[i]] > byCat[cats[j]] })
	fmt.Println("== category distribution ==")
	for _, c := range cats {
		fmt.Printf("  %-12s %6d\n", c, byCat[c])
	}

	isFood := func(f pbf.Feature) bool {
		switch f.Category {
		case "cafe", "restaurant", "bar", "bakery", "dessert":
			return true
		}
		return false
	}
	any := func(pbf.Feature) bool { return true }
	probes := []struct {
		label string
		of    func(pbf.Feature) bool
		has   func(pbf.Feature) bool
	}{
		{"opening_hours (all)", any, tagSet("opening_hours")},
		{"opening_hours (food)", isFood, tagSet("opening_hours")},
		{"internet_access (food)", isFood, tagSet("internet_access")},
		{"wheelchair (all)", any, tagSet("wheelchair")},
		{"outdoor_seating (food)", isFood, tagSet("outdoor_seating")},
		{"website (all)", any, tagSet("website")},
		{"cuisine (food)", isFood, tagSet("cuisine")},
	}
	fmt.Println("\n== tag coverage ==")
	for _, p := range probes {
		var denom, num int
		for _, f := range fs {
			if !p.of(f) {
				continue
			}
			denom++
			if p.has(f) {
				num++
			}
		}
		pct := 0.0
		if denom > 0 {
			pct = 100 * float64(num) / float64(denom)
		}
		fmt.Printf("  %-24s %6d / %-6d  %5.1f%%\n", p.label, num, denom, pct)
	}
	fmt.Println("\n  quiet: no OSM tag exists. Computed in PostGIS (Tier B) -- see ADR 0005.")
}

func tagSet(k string) func(pbf.Feature) bool {
	return func(f pbf.Feature) bool { return f.Tags[k] != "" }
}

func isWheelchair(v string) bool {
	switch v {
	case "yes", "limited", "no", "designated":
		return true
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func since(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }
