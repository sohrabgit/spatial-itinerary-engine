package sink

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultHours mirrors poi.DefaultHours without importing that package.
type DefaultHours struct {
	Open, Close int16
	Days        []int16
}

// CategoryDefaults seeds poi_hours from per-category inferred windows.
//
// M1 scope: applied to EVERY POI, because 60.5% have no opening_hours tag and
// the solver needs a time window for all of them (ADR 0005). The M2 Python
// worker parses opening_hours_raw with opening_hours_py and REPLACES these
// rows where it can, upgrading provenance from confidence 0.5 to 1.0.
//
// Provenance is written per POI so the eval harness can report feasibility
// separately for known vs inferred hours -- the discipline that keeps the
// headline metric honest.
type CategoryDefaults struct{ ByCategory map[string]DefaultHours }

func (c CategoryDefaults) SQL() (string, error) {
	if len(c.ByCategory) == 0 {
		return "", fmt.Errorf("no default hours configured")
	}
	cats := make([]string, 0, len(c.ByCategory))
	for k := range c.ByCategory {
		cats = append(cats, k)
	}
	sort.Strings(cats) // deterministic SQL, so migrations diff cleanly

	var vals []string
	for _, cat := range cats {
		d := c.ByCategory[cat]
		if d.Close <= d.Open {
			return "", fmt.Errorf("category %q: close (%d) must exceed open (%d)", cat, d.Close, d.Open)
		}
		for _, day := range d.Days {
			vals = append(vals, fmt.Sprintf("('%s',%d,%d,%d)", cat, day, d.Open, d.Close))
		}
	}

	return fmt.Sprintf(`
WITH defaults(category, dow, open_m, close_m) AS (VALUES %s),
ins AS (
  INSERT INTO poi_hours (poi_id, dow, open_m, close_m)
  SELECT p.id, d.dow, d.open_m, d.close_m
  FROM poi_stage s
  JOIN poi p ON p.osm_type = s.osm_type AND p.osm_id = s.osm_id
  JOIN defaults d ON d.category = p.category
  ON CONFLICT (poi_id, dow, open_m) DO NOTHING
  RETURNING poi_id
)
INSERT INTO poi_attr_provenance (poi_id, attr, tier, source, confidence)
SELECT p.id, 'opening_hours', 'B',
       'infer:category/' || p.category,
       0.5
FROM poi_stage s
JOIN poi p ON p.osm_type = s.osm_type AND p.osm_id = s.osm_id
JOIN (SELECT DISTINCT category FROM defaults) d ON d.category = p.category
ON CONFLICT (poi_id, attr) DO UPDATE SET
  source = EXCLUDED.source, confidence = EXCLUDED.confidence`,
		strings.Join(vals, ",")), nil
}
