package poi

import (
	"fmt"
	"sort"
	"strings"
)

// nomic-embed-text REQUIRES task prefixes. Index-time text must be prefixed
// "search_document: " and query-time text "search_query: ". Getting this wrong
// costs several points of recall and fails SILENTLY -- everything still works,
// just worse.
//
// These constants are the Go half of a cross-language invariant: the Python
// query path must use the identical strings. Both sides have a test asserting
// the exact byte sequence, because this is precisely the kind of thing that
// drifts when two services are edited months apart.
const (
	PrefixDocument = "search_document: "
	PrefixQuery    = "search_query: "
)

// Feature-ish input, kept as an interface-free struct so this package does not
// import pbf (which imports this one).
type EmbedInput struct {
	Name        string
	Category    string
	Cuisine     []string
	HasWifi     *bool
	Outdoor     *bool
	Wheelchair  string
	Quietness   *float64 // 0..1 percentile, nil if not yet computed
	Touristiness *float64
	HoursHuman  string
	HoursKnown  bool
	DescLLM     string
}

// BuildEmbedText renders the text that actually gets embedded.
//
// Percentiles are rendered as WORDS as well as numbers ("very quiet", "lively")
// because embedding models match on vocabulary, and the user types "quiet", not
// "0.82". This template matters more for retrieval quality than model choice.
func BuildEmbedText(in EmbedInput) string {
	var b strings.Builder
	b.WriteString(PrefixDocument)
	fmt.Fprintf(&b, "%s — %s in Paris.", in.Name, humanCategory(in.Category))

	if len(in.Cuisine) > 0 {
		c := append([]string(nil), in.Cuisine...)
		sort.Strings(c) // deterministic: same POI must embed identically each run
		fmt.Fprintf(&b, " Cuisine: %s.", strings.Join(c, ", "))
	}
	if in.Outdoor != nil && *in.Outdoor {
		b.WriteString(" Has outdoor seating.")
	}
	if in.Wheelchair == "yes" || in.Wheelchair == "designated" {
		b.WriteString(" Wheelchair accessible.")
	}

	if in.Quietness != nil {
		fmt.Fprintf(&b, " Atmosphere: %s (%d%% quieter than average).",
			quietWord(*in.Quietness), int(*in.Quietness*100))
	}
	if in.Touristiness != nil {
		fmt.Fprintf(&b, " %s.", touristWord(*in.Touristiness))
	}

	switch {
	case in.HoursHuman != "" && in.HoursKnown:
		fmt.Fprintf(&b, " Open %s.", in.HoursHuman)
	case in.HoursHuman != "":
		fmt.Fprintf(&b, " Typically open %s.", in.HoursHuman)
	}

	// Tri-state. "unknown" is stated explicitly rather than omitted, so the
	// embedding does not imply absence.
	switch {
	case in.HasWifi == nil:
		b.WriteString(" Wi-Fi: unknown.")
	case *in.HasWifi:
		b.WriteString(" Wi-Fi available.")
	default:
		b.WriteString(" No Wi-Fi.")
	}

	if in.DescLLM != "" {
		b.WriteString(" ")
		b.WriteString(in.DescLLM)
	}
	return b.String()
}

func quietWord(q float64) string {
	switch {
	case q >= 0.85:
		return "very quiet and peaceful"
	case q >= 0.65:
		return "quiet"
	case q >= 0.35:
		return "moderately busy"
	case q >= 0.15:
		return "lively"
	default:
		return "very lively and noisy"
	}
}

func touristWord(t float64) string {
	switch {
	case t >= 0.8:
		return "In a major tourist area"
	case t >= 0.5:
		return "Popular with visitors"
	case t >= 0.2:
		return "Mostly frequented by locals"
	default:
		return "Off the tourist track, local neighbourhood"
	}
}

var categoryHuman = map[string]string{
	"cafe": "a cafe", "restaurant": "a restaurant", "bar": "a bar",
	"bakery": "a bakery", "dessert": "an ice cream and dessert shop",
	"museum": "a museum", "gallery": "an art gallery", "artwork": "a public artwork",
	"attraction": "a visitor attraction", "viewpoint": "a scenic viewpoint",
	"monument": "a historic monument", "church": "a historic church",
	"castle": "a castle", "park": "a park or garden", "library": "a library",
	"theatre": "a theatre", "cinema": "a cinema", "market": "a market",
	"shop": "a speciality shop", "zoo": "a zoo", "aquarium": "an aquarium",
}

func humanCategory(c string) string {
	if h, ok := categoryHuman[c]; ok {
		return h
	}
	return "a place"
}
