// Package pbf turns an OSM PBF extract into located, tagged POIs.
//
// Why two passes instead of one:
//
// Most POIs are nodes, but many are ways -- a museum tagged on a building
// outline -- and those need a centroid, which means resolving every node the
// way references. The obvious approach is a map[osm.NodeID]coord built during a
// single pass. On this extract that map would hold millions of entries, and
// Go's map overhead (hashing, bucket slack, pointer chasing) costs 50+ bytes
// per entry: ~250MB+ at 5M nodes, plus sustained GC pressure.
//
// Instead:
//   pass 1  collect POI nodes directly, and the node IDs candidate ways
//           reference, into a plain []int64
//   sort    sort + dedupe that slice
//   pass 2  re-scan nodes, binary-searching each ID against the sorted slice,
//           appending hits to a []nodeCoord of 16 bytes each
//   pass 3  compute way centroids in memory
//
// 5M refs cost 80MB with no pointer chasing and near-zero GC pressure -- the
// difference between fitting in a 4GB container and not.
package pbf

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/sohrab/travel-engine/ingest/internal/poi"
)

// Feature is a located, tagged POI ready for enrichment.
type Feature struct {
	OSMType  byte // 'n' | 'w'
	OSMID    int64
	Lat, Lon float64
	Category string
	Dwell    int16
	Tags     map[string]string
}

type nodeCoord struct {
	ID  int64
	Lat float32 // ~1cm precision at these latitudes; ample for POI centroids
	Lon float32
}

type Stats struct {
	NodesScanned        int64
	WaysScanned         int64
	RelationsSkipped    int64
	NodePOIs            int64
	WayCandidates       int64
	WaysResolved        int64
	WaysUnresolved      int64
	RefsTracked         int
	RefsResolved        int
	Pass1, Pass2, Pass3 time.Duration
	TotalAllocMB        uint64
	PeakHeapMB          uint64
}

func (s Stats) String() string {
	return fmt.Sprintf(
		"nodes=%d ways=%d rel_skipped=%d\n"+
			"node_pois=%d way_candidates=%d resolved=%d unresolved=%d\n"+
			"refs tracked=%d resolved=%d\n"+
			"pass1=%s pass2=%s pass3=%s\n"+
			"peak_heap=%dMB total_alloc=%dMB",
		s.NodesScanned, s.WaysScanned, s.RelationsSkipped,
		s.NodePOIs, s.WayCandidates, s.WaysResolved, s.WaysUnresolved,
		s.RefsTracked, s.RefsResolved,
		s.Pass1.Round(time.Millisecond), s.Pass2.Round(time.Millisecond),
		s.Pass3.Round(time.Millisecond), s.PeakHeapMB, s.TotalAllocMB)
}

type wayCandidate struct {
	id   int64
	refs []int64
	rule poi.Rule
	tags map[string]string
}

// Scan runs the full pipeline against path and returns located features.
func Scan(ctx context.Context, path string, f *poi.Filter) ([]Feature, Stats, error) {
	var st Stats
	procs := runtime.GOMAXPROCS(-1)

	// ---- pass 1: POI nodes + candidate ways ------------------------------
	t0 := time.Now()
	var features []Feature
	var ways []wayCandidate
	var refs []int64

	err := scanFile(ctx, path, procs, false, false, true, func(o osm.Object) {
		switch v := o.(type) {
		case *osm.Node:
			st.NodesScanned++
			tags := v.Tags.Map()
			if r, ok := f.Match(tags); ok {
				st.NodePOIs++
				features = append(features, Feature{
					OSMType: 'n', OSMID: int64(v.ID), Lat: v.Lat, Lon: v.Lon,
					Category: r.Category, Dwell: r.Dwell, Tags: tags,
				})
			}
		case *osm.Way:
			st.WaysScanned++
			tags := v.Tags.Map()
			r, ok := f.Match(tags)
			if !ok {
				return
			}
			st.WayCandidates++
			ids := make([]int64, 0, len(v.Nodes))
			for _, n := range v.Nodes {
				ids = append(ids, int64(n.ID))
			}
			ways = append(ways, wayCandidate{id: int64(v.ID), refs: ids, rule: r, tags: tags})
			refs = append(refs, ids...)
		}
	})
	if err != nil {
		return nil, st, err
	}
	st.Pass1 = time.Since(t0)

	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	refs = dedupe(refs)
	st.RefsTracked = len(refs)

	// ---- pass 2: resolve only the referenced nodes ------------------------
	t1 := time.Now()
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
		return nil, st, err
	}
	sort.Slice(coords, func(i, j int) bool { return coords[i].ID < coords[j].ID })
	st.RefsResolved = len(coords)
	st.Pass2 = time.Since(t1)

	// ---- pass 3: way centroids -------------------------------------------
	t2 := time.Now()
	for _, w := range ways {
		var sumLat, sumLon float64
		var n int
		for _, ref := range w.refs {
			i := sort.Search(len(coords), func(i int) bool { return coords[i].ID >= ref })
			if i < len(coords) && coords[i].ID == ref {
				sumLat += float64(coords[i].Lat)
				sumLon += float64(coords[i].Lon)
				n++
			}
		}
		if n == 0 {
			st.WaysUnresolved++
			continue
		}
		st.WaysResolved++
		features = append(features, Feature{
			OSMType: 'w', OSMID: w.id,
			Lat: sumLat / float64(n), Lon: sumLon / float64(n),
			Category: w.rule.Category, Dwell: w.rule.Dwell, Tags: w.tags,
		})
	}
	st.Pass3 = time.Since(t2)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	st.TotalAllocMB = ms.TotalAlloc / 1024 / 1024
	st.PeakHeapMB = ms.HeapAlloc / 1024 / 1024

	return features, st, nil
}

// scanFile runs fn over every object, skipping whole element types the caller
// does not need -- pass 2 skips ways entirely, avoiding most of the decode cost.
func scanFile(ctx context.Context, path string, procs int, skipNodes, skipWays, skipRelations bool, fn func(osm.Object)) error {
	fh, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open pbf: %w", err)
	}
	defer fh.Close()

	// Scanners are not safe for parallel use: one goroutine drives it while
	// osmpbf parallelises block decompression internally across `procs`.
	s := osmpbf.New(ctx, fh, procs)
	defer s.Close()
	s.SkipNodes, s.SkipWays, s.SkipRelations = skipNodes, skipWays, skipRelations

	for s.Scan() {
		fn(s.Object())
	}
	return s.Err()
}

func dedupe(in []int64) []int64 {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
