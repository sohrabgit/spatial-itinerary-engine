package pbf

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/paulmach/osm"
)

// Road is a way retained purely as an input to the Tier B computation.
type Road struct {
	OSMID int64
	Class string // "major" | "pedestrian"
	// WKT LINESTRING. Built here rather than in SQL so the sink stays a dumb
	// writer and the geometry decisions live next to the geometry code.
	WKT string
}

// TransitEntrance is a metro/RER entrance node.
type TransitEntrance struct {
	OSMID int64
	Name  string
	Lat   float64
	Lon   float64
}

// Noise sources. *_link included: slip roads carry the same traffic.
var majorHighway = map[string]bool{
	"motorway": true, "motorway_link": true,
	"trunk": true, "trunk_link": true,
	"primary": true, "primary_link": true,
	"secondary": true, "secondary_link": true,
}

// Calm indicators.
var pedestrianHighway = map[string]bool{
	"pedestrian": true, "footway": true, "living_street": true,
	"path": true, "steps": true, "track": true,
}

type ContextStats struct {
	MajorRoads   int
	PedRoads     int
	Entrances    int
	RefsTracked  int
	RefsResolved int
	Elapsed      time.Duration
	PeakHeapMB   uint64
}

func (s ContextStats) String() string {
	return fmt.Sprintf("major_roads=%d pedestrian_ways=%d transit_entrances=%d\nrefs tracked=%d resolved=%d | %s | peak_heap=%dMB",
		s.MajorRoads, s.PedRoads, s.Entrances, s.RefsTracked, s.RefsResolved,
		s.Elapsed.Round(time.Millisecond), s.PeakHeapMB)
}

// ScanContext extracts the geometry Tier B is computed from.
//
// Same two-pass shape as Scan: collect way refs first, resolve coordinates
// second. Road ways reference far more nodes than POI ways do, so the
// sorted-slice approach matters more here, not less.
func ScanContext(ctx context.Context, path string) ([]Road, []TransitEntrance, ContextStats, error) {
	var st ContextStats
	t0 := time.Now()
	procs := runtime.GOMAXPROCS(-1)

	type wayRef struct {
		id    int64
		class string
		refs  []int64
	}
	var ways []wayRef
	var entrances []TransitEntrance
	var refs []int64

	err := scanFile(ctx, path, procs, false, false, true, func(o osm.Object) {
		switch v := o.(type) {
		case *osm.Node:
			t := v.Tags.Map()
			// subway_entrance is the precise tag; station nodes are the
			// fallback for networks that do not map entrances individually.
			if t["railway"] == "subway_entrance" || t["railway"] == "station" {
				entrances = append(entrances, TransitEntrance{
					OSMID: int64(v.ID), Name: t["name"], Lat: v.Lat, Lon: v.Lon,
				})
			}
		case *osm.Way:
			hw := v.Tags.Find("highway")
			if hw == "" {
				return
			}
			var class string
			switch {
			case majorHighway[hw]:
				class = "major"
			case pedestrianHighway[hw]:
				class = "pedestrian"
			default:
				return
			}
			if len(v.Nodes) < 2 {
				return // a LINESTRING needs two points
			}
			ids := make([]int64, 0, len(v.Nodes))
			for _, n := range v.Nodes {
				ids = append(ids, int64(n.ID))
			}
			ways = append(ways, wayRef{id: int64(v.ID), class: class, refs: ids})
			refs = append(refs, ids...)
		}
	})
	if err != nil {
		return nil, nil, st, err
	}
	st.Entrances = len(entrances)

	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	refs = dedupe(refs)
	st.RefsTracked = len(refs)

	coords := make([]nodeCoord, 0, len(refs))
	err = scanFile(ctx, path, procs, false, true, true, func(o osm.Object) {
		n, ok := o.(*osm.Node)
		if !ok {
			return
		}
		id := int64(n.ID)
		i := sort.Search(len(refs), func(i int) bool { return refs[i] >= id })
		if i < len(refs) && refs[i] == id {
			coords = append(coords, nodeCoord{ID: id, Lat: float32(n.Lat), Lon: float32(n.Lon)})
		}
	})
	if err != nil {
		return nil, nil, st, err
	}
	sort.Slice(coords, func(i, j int) bool { return coords[i].ID < coords[j].ID })
	st.RefsResolved = len(coords)

	roads := make([]Road, 0, len(ways))
	for _, w := range ways {
		var b strings.Builder
		b.WriteString("LINESTRING(")
		n := 0
		for _, ref := range w.refs {
			i := sort.Search(len(coords), func(i int) bool { return coords[i].ID >= ref })
			if i >= len(coords) || coords[i].ID != ref {
				continue // node outside the extract; skip the vertex
			}
			if n > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%.7f %.7f", coords[i].Lon, coords[i].Lat)
			n++
		}
		b.WriteByte(')')
		if n < 2 {
			continue
		}
		roads = append(roads, Road{OSMID: w.id, Class: w.class, WKT: b.String()})
		if w.class == "major" {
			st.MajorRoads++
		} else {
			st.PedRoads++
		}
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	st.PeakHeapMB = ms.HeapAlloc / 1024 / 1024
	st.Elapsed = time.Since(t0)
	return roads, entrances, st, nil
}
