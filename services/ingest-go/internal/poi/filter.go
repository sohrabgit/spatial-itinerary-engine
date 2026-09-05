// Package poi holds the tag taxonomy and the in-flight POI representation.
package poi

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Rule is the normalised outcome of matching one OSM tag pair.
type Rule struct {
	Category string `yaml:"category"`
	Dwell    int16  `yaml:"dwell"`
}

// DefaultHours is applied only when a POI carries no opening_hours tag.
// 60.5% of Paris POIs do not. Recorded as tier B, confidence 0.5. See ADR 0005.
type DefaultHours struct {
	Open  int16   `yaml:"open"`  // minutes from midnight
	Close int16   `yaml:"close"`
	Days  []int16 `yaml:"days"`  // 0=Mon .. 6=Sun
}

type Filter struct {
	Require struct {
		Name bool `yaml:"name"`
	} `yaml:"require"`
	Include      map[string]map[string]Rule `yaml:"include"`
	ExcludeKeys  []string                   `yaml:"exclude_keys"`
	DefaultHours map[string]DefaultHours    `yaml:"default_hours"`

	excludeSet map[string]struct{}
}

func LoadFilter(path string) (*Filter, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read filter: %w", err)
	}
	var f Filter
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse filter: %w", err)
	}
	if len(f.Include) == 0 {
		return nil, fmt.Errorf("filter %s has no include rules", path)
	}
	f.excludeSet = make(map[string]struct{}, len(f.ExcludeKeys))
	for _, k := range f.ExcludeKeys {
		f.excludeSet[k] = struct{}{}
	}
	return &f, nil
}

// priority fixes the order in which OSM keys are consulted. Without it, match
// results would depend on Go's randomised map iteration order and the same
// input could yield different categories between runs. Most specific first: a
// place_of_worship that is also historic=church is a church.
var priority = []string{"tourism", "historic", "leisure", "amenity", "shop"}

// Match reports whether the tags select this feature, and the rule that did.
func (f *Filter) Match(tags map[string]string) (Rule, bool) {
	for k := range f.excludeSet {
		if _, bad := tags[k]; bad {
			return Rule{}, false
		}
	}
	if f.Require.Name && tags["name"] == "" {
		return Rule{}, false
	}
	for _, key := range priority {
		vals, ok := f.Include[key]
		if !ok {
			continue
		}
		if r, ok := vals[tags[key]]; ok {
			return r, true
		}
	}
	return Rule{}, false
}

// Categories returns every normalised category the taxonomy can emit.
func (f *Filter) Categories() map[string]struct{} {
	out := make(map[string]struct{})
	for _, vals := range f.Include {
		for _, r := range vals {
			out[r.Category] = struct{}{}
		}
	}
	return out
}
