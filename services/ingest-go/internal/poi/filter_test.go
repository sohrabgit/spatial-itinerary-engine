package poi

import "testing"

func load(t *testing.T) *Filter {
	t.Helper()
	f, err := LoadFilter("../../poi-filter.yaml")
	if err != nil {
		t.Fatalf("LoadFilter: %v", err)
	}
	return f
}

func TestMatch(t *testing.T) {
	f := load(t)
	cases := []struct {
		name string
		tags map[string]string
		want string // "" means no match
	}{
		{"named cafe", map[string]string{"amenity": "cafe", "name": "Cafe de Flore"}, "cafe"},
		{"unnamed cafe dropped", map[string]string{"amenity": "cafe"}, ""},
		{"museum", map[string]string{"tourism": "museum", "name": "Louvre"}, "museum"},
		{"disused dropped", map[string]string{"amenity": "cafe", "name": "X", "disused": "yes"}, ""},
		{"untagged", map[string]string{"name": "Nothing"}, ""},
		{"bench not in taxonomy", map[string]string{"amenity": "bench", "name": "B"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := f.Match(c.tags)
			if c.want == "" {
				if ok {
					t.Fatalf("expected no match, got %q", r.Category)
				}
				return
			}
			if !ok || r.Category != c.want {
				t.Fatalf("got (%q,%v), want %q", r.Category, ok, c.want)
			}
		})
	}
}

// A feature carrying several taxonomy keys must resolve identically every run.
// Go randomises map iteration, so this would flake if priority order were not
// fixed explicitly.
func TestMatchIsDeterministic(t *testing.T) {
	f := load(t)
	tags := map[string]string{
		"name": "Saint-Germain", "historic": "church",
		"amenity": "place_of_worship", "tourism": "attraction",
	}
	first, ok := f.Match(tags)
	if !ok {
		t.Fatal("expected a match")
	}
	if first.Category != "attraction" {
		t.Fatalf("priority order broken: got %q, want tourism to win", first.Category)
	}
	for i := 0; i < 200; i++ {
		if r, _ := f.Match(tags); r.Category != first.Category {
			t.Fatalf("nondeterministic: %q then %q", first.Category, r.Category)
		}
	}
}

func TestDwellPopulated(t *testing.T) {
	f := load(t)
	for key, vals := range f.Include {
		for val, r := range vals {
			if r.Dwell <= 0 {
				t.Errorf("%s=%s has no dwell time", key, val)
			}
			if r.Category == "" {
				t.Errorf("%s=%s has no category", key, val)
			}
		}
	}
}
