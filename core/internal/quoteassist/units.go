package quoteassist

// units.go, the unit VOCABULARY. Words in, catalogue UOM codes out, and nothing else.
//
// -----------------------------------------------------------------------------------------------
// THIS FILE KNOWS ENGLISH. THE CATALOGUE KNOWS THE YARD.
// -----------------------------------------------------------------------------------------------
// The split matters and it was learned the hard way, in the integration test that first ran this
// extractor against the real catalogue. An earlier version of this file decided FOR ITSELF which
// units a product could be quoted in, by comparing the parsed code against the two units a search
// hit happens to carry (base_uom, sold_by). On the real data that rejected "480 sq ft of Techo-Bloc
// Blu 60", the single most realistic sentence in this entire domain, because Blu 60's base unit is
// PC and its sold_by is blank, and the product's actual SF factor lives in a table this file had no
// business having an opinion about.
//
// The yard already keeps that answer: product_uoms holds one row per (product, unit) with the factor
// to base, and it is the same table pricing converts through. So:
//
//	THIS FILE            "sq ft" / "sqft" / "square feet" / "ft²"  →  SF
//	THE CATALOGUE        can THIS product be quoted in SF?          →  CatalogueSearch.QuotableUnit
//
// One consequence is worth naming because it looks like a behaviour change and is really a
// correction: PALLETS ARE QUOTABLE. product_uoms carries PLT for every paver and slab in the seeded
// catalogue (50 pieces to a skid of Antico, 40 of Trevista, 54 of Proma), so "2 pallets of
// Mini-Creta" is a perfectly ordinary line and the extractor now says so, where before it refused a
// unit the business sells in every day.
//
// -----------------------------------------------------------------------------------------------
// NOTHING HERE CONVERTS, AND THAT HAS NOT CHANGED
// -----------------------------------------------------------------------------------------------
// Asking "is there a factor" is not the same as using it. The extractor never multiplies: it hands
// the quantity and the unit to the shipped quote-line writer exactly as the rep's text stated them,
// and pricing does the arithmetic with the factor it owns. A second copy of that multiplication here
// would be a second answer to "how many pieces in 480 square feet", and the two would differ on
// somebody's driveway.

import "strings"

// The catalogue UOM codes this vocabulary can produce. Constants so the parser, the notes and the
// tests cannot disagree about a spelling, and named after the codes the real product_uoms rows use.
const (
	UOMSquareFoot = "SF"
	UOMLinearFoot = "LF"
	UOMTonne      = "T"
	UOMBag        = "BAG"
	UOMEach       = "EA"
	UOMPiece      = "PC"
	UOMPallet     = "PLT"
	UOMKilogram   = "KG"
	UOMCubicYard  = "CYD"
	UOMSquareM    = "M2"
)

// unitCodes maps the words people say onto catalogue UOM codes.
//
// Keys are lower-cased and space-normalised; multi-word keys are matched first (see matchUnit), so
// "sq ft" is never read as the word "sq" plus a stray "ft".
//
// A code appearing here does NOT mean every product can be quoted in it, that is the catalogue's
// answer, per product. What it means is that the word is a UNIT and not part of a product name, which
// is the thing this file has to get right: a unit word left in the search term is noise that costs a
// match, and "2 pallets of Mini-Creta" searched whole is a worse query than "Mini-Creta".
var unitCodes = map[string]string{
	// area
	"sf": UOMSquareFoot, "sqft": UOMSquareFoot, "sq ft": UOMSquareFoot, "sq feet": UOMSquareFoot,
	"sq foot": UOMSquareFoot, "square feet": UOMSquareFoot, "square foot": UOMSquareFoot,
	"ft2": UOMSquareFoot, "ft²": UOMSquareFoot,
	"m2": UOMSquareM, "sq m": UOMSquareM, "square metre": UOMSquareM, "square metres": UOMSquareM,
	"square meter": UOMSquareM, "square meters": UOMSquareM,
	// length
	"lf": UOMLinearFoot, "lft": UOMLinearFoot, "lin ft": UOMLinearFoot, "linft": UOMLinearFoot,
	"linear feet": UOMLinearFoot, "linear foot": UOMLinearFoot, "lineal feet": UOMLinearFoot,
	"lineal foot": UOMLinearFoot, "running feet": UOMLinearFoot,
	// mass, the bulk aggregates are sold by the tonne
	"t": UOMTonne, "ton": UOMTonne, "tons": UOMTonne, "tonne": UOMTonne, "tonnes": UOMTonne,
	"metric ton": UOMTonne, "metric tonne": UOMTonne, "mt": UOMTonne,
	"kg": UOMKilogram, "kgs": UOMKilogram, "kilo": UOMKilogram, "kilos": UOMKilogram,
	// volume
	"yd": UOMCubicYard, "yds": UOMCubicYard, "yard": UOMCubicYard, "yards": UOMCubicYard,
	"cu yd": UOMCubicYard, "cubic yard": UOMCubicYard, "cubic yards": UOMCubicYard,
	// count
	"bag": UOMBag, "bags": UOMBag, "sack": UOMBag, "sacks": UOMBag,
	"ea": UOMEach, "each": UOMEach, "unit": UOMEach, "units": UOMEach,
	"pc": UOMPiece, "pcs": UOMPiece, "piece": UOMPiece, "pieces": UOMPiece,
	// the yard's own bulk handling unit. Every paver and slab in the seeded catalogue has a PLT row.
	"pallet": UOMPallet, "pallets": UOMPallet, "skid": UOMPallet, "skids": UOMPallet,
}

// nonUnitQuantityWords are words that follow a number, are NOT units, and must still be kept out of
// the product query, a truck load is a delivery arrangement, not a unit of sale, and no yard prices
// in "courses".
//
// They are recognised (so the word does not poison the search) and mapped to NO code, which means the
// proposal comes back with the quantity read and the unit left for the rep. That is the honest answer
// for "3 loads of screenings": the number is real and the unit is a conversation.
var nonUnitQuantityWords = map[string]string{
	"load": "load", "loads": "load", "truckload": "truckload", "truckloads": "truckload",
	"truck": "truckload", "trucks": "truckload", "box": "box", "boxes": "box",
	"roll": "roll", "rolls": "roll", "course": "course", "courses": "course",
	"layer": "layer", "layers": "layer", "bundle": "bundle", "bundles": "bundle",
	"lb": "pound", "lbs": "pound", "pound": "pound", "pounds": "pound",
}

// maxUnitWords is the longest phrase in either table ("square metres", "metric tonne"). The matcher
// tries this many words first and walks down, so a two-word unit always beats the one-word prefix of
// itself.
const maxUnitWords = 2

// unitMatch is one recognised quantity-word: the catalogue code it maps to (empty for a recognised
// non-unit), the words as they should be quoted back, and how many tokens it consumed.
type unitMatch struct {
	// Code is a catalogue UOM code, or "" for a word that is a quantity-word but not a unit of sale.
	Code string
	// Word is the phrase as the reader should see it quoted back ("sq ft", "load").
	Word string
	// Tokens is how many whitespace tokens it occupied, so the caller can remove exactly those from
	// the product query and no more.
	Tokens int
}

// matchUnit reads a unit phrase starting at tokens[0], longest phrase first.
//
// ok=false rather than a zero unitMatch-with-a-flag because "there is no unit here" is by far the
// common case, most words are not units, and a bool at the call site reads as the question being
// asked.
func matchUnit(tokens []string) (unitMatch, bool) {
	for n := maxUnitWords; n >= 1; n-- {
		if n > len(tokens) {
			continue
		}
		phrase := strings.Join(tokens[:n], " ")
		if code, ok := unitCodes[phrase]; ok {
			return unitMatch{Code: code, Word: phrase, Tokens: n}, true
		}
		if word, ok := nonUnitQuantityWords[phrase]; ok {
			return unitMatch{Code: "", Word: word, Tokens: n}, true
		}
	}
	return unitMatch{}, false
}

// isUnitWord reports whether a single token is a quantity-word in either table. Used to keep such
// words out of a product query even when they were not attached to a number, "sq ft" left in the
// search term is noise that costs a match.
func isUnitWord(tok string) bool {
	if _, ok := unitCodes[tok]; ok {
		return true
	}
	_, ok := nonUnitQuantityWords[tok]
	return ok
}

// defaultUOM is the unit to quote in when the text named none: the counter's declared sell unit if the
// catalogue set one, otherwise the stocking unit.
//
// NEVER INVENTED. A product with neither is reported as needing the rep's call, because a quantity
// printed against a unit nobody chose is a quantity in the wrong unit.
func defaultUOM(hit CatalogueHit) string {
	if s := strings.ToUpper(strings.TrimSpace(hit.SellUOM)); s != "" {
		return s
	}
	return strings.ToUpper(strings.TrimSpace(hit.BaseUOM))
}

// defaultUOMKind names WHERE the defaulted unit came from, so the row can say "this product's sell
// unit" rather than leaving a rep to wonder which of the yard's units they are looking at.
func defaultUOMKind(hit CatalogueHit) string {
	if strings.TrimSpace(hit.SellUOM) != "" {
		return "sell"
	}
	return "stocking"
}
