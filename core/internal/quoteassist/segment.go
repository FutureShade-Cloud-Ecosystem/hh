package quoteassist

// segment.go, free text → candidate phrases, with quantities read out of them. Deterministic,
// pure, and the DEFAULT path: this is what runs when there is no provider, no key and no network,
// which in this pilot is almost always.
//
// -----------------------------------------------------------------------------------------------
// HOW A CONTRACTOR ACTUALLY WRITES A JOB
// -----------------------------------------------------------------------------------------------
// Not in a form. Three shapes, all of them common, and all three have to work:
//
//	a list        "- 480 sq ft Blu 60\n- 12 t 3/4 clear\n- 6 bags polymeric sand"
//	a sentence    "Need 480 sq ft of Blu 60 for the driveway, 12 tonnes of 3/4 clear stone
//	               for the base and 6 bags of polymeric sand."
//	dictation     the sentence, with no punctuation at all, because it was spoken
//
// The first two split on the marks people use. The third is the reason " and " is a separator: in
// spoken text it is the ONLY separator, and refusing to split on it would turn a whole job into one
// unmatchable phrase. The cost is that "sand and gravel" splits into two phrases, which is
// correct anyway, since they are two lines.
//
// -----------------------------------------------------------------------------------------------
// WHY THE QUANTITY RULES ARE THIS CONSERVATIVE
// -----------------------------------------------------------------------------------------------
// A wrong quantity is the worst thing this feature can produce. A missing one costs the rep two
// keystrokes and is visible: the row says so and cannot be confirmed until they fill it. A wrong
// one is invisible, priced, and goes out to a customer. So:
//
//	1. a number FOLLOWED BY A UNIT is a quantity            "480 sq ft", "12 t", "6 bags"
//	2. a number at the START of the phrase is a quantity    "480 Blu 60"
//	3. a number after 'x' or 'qty' is a quantity            "Blu 60 x 480"
//	4. anything else is NOT a quantity
//
// Rule 4 is what stops "Unilock Series 3000" being quoted three thousand times, and it is why rule
// 2 is anchored to the start rather than "the first number anywhere". Every catalogue in this
// business has numbers in its product names, Blu 60, Series 3000, Porcelain 24x24, and a parser
// that treats a model number as an order quantity is a parser nobody can be allowed to trust.
//
// Fractions are deliberately not numbers here: '3/4' in '3/4 clear stone' is a product name, and
// there is no phrasing in this domain where a fraction on its own is an order quantity.

import (
	"strconv"
	"strings"
	"unicode"
)

// maxPhrases bounds one paste.
//
// A rep pasting an entire spreadsheet is a real thing, and a hundred proposals is not a review
// list, it is a second data-entry job with worse ergonomics. Thirty is roughly two screens, which
// is as much as anyone will actually check line by line before they start clicking through.
// Anything beyond it is reported as one honest unmatched row rather than silently truncated.
const maxPhrases = 30

// segmentSeparators are what a person puts between two items. Newlines and semicolons are handled
// before this list (they are unambiguous); these are the in-sentence ones.
//
// ' and ' and ' plus ' carry spaces on both sides on purpose, 'sand' must not split on 'and', and
// there is no amount of cleverness in this function that would be worth that bug.
var segmentSeparators = []string{";", "•", " and ", " plus ", " also ", " & ", " + "}

// leadingNoise are the words that open a request and say nothing about what is being ordered. They
// are stripped from the FRONT of a phrase only, so a number that follows one still counts as a
// leading quantity under rule 2 ("need 480 Blu 60").
var leadingNoise = map[string]bool{
	"need": true, "needs": true, "needed": true, "want": true, "wants": true, "add": true,
	"also": true, "plus": true, "and": true, "then": true, "get": true, "gimme": true,
	"please": true, "quote": true, "include": true, "put": true, "give": true, "me": true,
	"i": true, "we": true, "us": true, "customer": true, "job": true, "approx": true,
	"about": true, "around": true, "roughly": true, "some": true, "the": true, "a": true,
	"an": true, "for": true, "of": true, "with": true, "another": true, "more": true,
}

// queryStopWords are dropped when building the CATALOGUE QUERY (not from the phrase the rep reads
// back, SourceText is never edited). They are the words that carry no evidence about which
// product is meant, so leaving them in only dilutes the trigram similarity the ranked search
// leans on.
var queryStopWords = map[string]bool{
	"of": true, "the": true, "for": true, "a": true, "an": true, "and": true, "with": true,
	"some": true, "need": true, "needs": true, "want": true, "add": true, "also": true,
	"plus": true, "approx": true, "about": true, "around": true, "roughly": true, "total": true,
	"in": true, "on": true, "at": true, "to": true, "x": true, "×": true, "qty": true,
	"job": true, "site": true, "customer": true, "please": true, "include": true, "we": true,
	"i": true, "me": true, "us": true, "them": true, "it": true, "this": true, "that": true,
	"my": true, "our": true, "their": true, "his": true, "her": true, "is": true, "are": true,
	"be": true, "will": true, "would": true, "like": true, "get": true, "getting": true,
}

// purposeMarkers introduce the part of a phrase that says WHERE the material is going rather than
// WHAT it is: "…for the driveway", "…around the patio", "…behind the wall".
//
// Stripped from the query because a purpose clause is pure noise to a product search AND actively
// harmful: "3/4 clear stone for base" searched whole ranks worse than "3/4 clear stone", because
// 'base' is a real application word that pulls in the screenings. The clause stays in SourceText,
// where the rep can still read it.
var purposeMarkers = []string{
	" for the ", " for ", " around the ", " around ", " along the ", " along ",
	" behind the ", " behind ", " under the ", " under ", " on the ", " to the ",
	" at the ", " in the ", " going ", " to do the ", " to do ",
}

// Segment splits free text into candidate phrases and reads a quantity out of each.
//
// It is the deterministic half of DIB-66 and it is exported so the AI path can be compared against
// it, the adapter's tests and the service's degradation path both call it directly.
func Segment(text string) []Phrase {
	raw := splitPhrases(text)
	out := make([]Phrase, 0, len(raw))
	for _, r := range raw {
		if len(out) >= maxPhrases {
			break
		}
		p, ok := readPhrase(r)
		if !ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Truncated reports whether a text would produce more phrases than maxPhrases, so the caller can
// SAY so instead of silently dropping the tail. Separate from Segment because "there were more"
// is a fact about the request, not a phrase.
func Truncated(text string) bool { return len(splitPhrases(text)) > maxPhrases }

// splitPhrases breaks the text on the marks people separate items with.
func splitPhrases(text string) []string {
	// Newlines first and unconditionally: a list is a list, and a line break in a pasted
	// description is the least ambiguous separator there is.
	chunks := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	// Then commas. Handled with the same standing as newlines because "480 sq ft Blu 60, 12 t
	// clear stone" is how the sentence form is written, and a comma between two items is never
	// anything but a separator in this domain, EXCEPT when it is a thousands separator, which is
	// why this is splitCommas and not strings.Split. "1,200 sq ft" split naively becomes a phrase
	// with no product and a phrase quoting two hundred square feet, and the second one is priced.
	next := make([]string, 0, len(chunks))
	for _, c := range chunks {
		next = append(next, splitCommas(c)...)
	}
	chunks = next
	for _, sep := range segmentSeparators {
		next = make([]string, 0, len(chunks))
		for _, c := range chunks {
			next = append(next, splitFold(c, sep)...)
		}
		chunks = next
	}
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if s := cleanPhrase(c); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitCommas splits on commas that separate ITEMS, leaving the ones that separate DIGITS alone.
//
// "1,200 sq ft of Blu 60" is one item. Splitting it produced two, a fragment with no product and a
// perfectly plausible-looking proposal for 200 square feet, and the second one would have been
// priced and sent. Found by TestExtract_ReadsQuantityAndUnitFromRealPhrasing/1,200_sqft, which is
// the reason that case is in the table.
func splitCommas(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != ',' {
			continue
		}
		if i > 0 && i+1 < len(s) && isDigitByte(s[i-1]) && isDigitByte(s[i+1]) {
			continue // a thousands separator, not a separator between items
		}
		out = append(out, s[start:i])
		start = i + 1
	}
	return append(out, s[start:])
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// splitFold splits case-insensitively on sep, keeping the original casing of the pieces. Product
// names are capitalised and the separators are not, so a case-sensitive split would miss "…Blu 60
// And 12 tonnes…", which is exactly what dictation software produces.
func splitFold(s, sep string) []string {
	low, lowSep := strings.ToLower(s), strings.ToLower(sep)
	out := []string{}
	for {
		i := strings.Index(low, lowSep)
		if i < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:i])
		s, low = s[i+len(sep):], low[i+len(lowSep):]
	}
}

// cleanPhrase trims list markers, stray punctuation and the whitespace around what is left. It
// does NOT reword anything: SourceText has to be recognisable as the rep's own text or the review
// list is not checkable.
func cleanPhrase(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "-*•·-->»\t ")
	// A leading "1." / "2)" list marker is a marker, not a quantity. Numbered lists are common in
	// pasted job descriptions and reading the marker as an order quantity would be the single most
	// embarrassing failure this parser could have.
	s = trimListNumber(s)
	s = strings.TrimRight(s, " .;:--\t")
	return strings.TrimSpace(s)
}

// trimListNumber removes a leading "12." or "3)" ordinal marker.
//
// The distinguishing mark is the PUNCTUATION: an order quantity is never written "480." with a full
// stop before the product. So the marker is only stripped when a '.' or ')' immediately follows the
// digits AND is not itself followed by another digit, "12.5 t of screenings" is a DECIMAL, and an
// earlier version of this function turned it into "5 t of screenings", which is the same class of
// bug as the thousands-separator split and was found by the same table.
func trimListNumber(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return s
	}
	if s[i] == ')' {
		return strings.TrimSpace(s[i+1:])
	}
	if s[i] == '.' && (i+1 >= len(s) || !isDigitByte(s[i+1])) {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// readPhrase turns one cleaned chunk into a Phrase, applying the four quantity rules.
//
// ok=false only for a chunk with no letters in it at all, a stray "12" on its own line, or a
// leftover "and". Those are not items and are not reported as unmatched either, because there is
// nothing there to tell a rep about.
func readPhrase(chunk string) (Phrase, bool) {
	if !hasLetter(chunk) {
		return Phrase{}, false
	}
	toks := tokenise(chunk)
	if len(toks) == 0 {
		return Phrase{}, false
	}
	// Rule 1, a number followed by a unit, anywhere in the phrase. Strongest evidence, so it wins
	// over the positional rules even when the phrase also starts with a number.
	for i, t := range toks {
		if !isNumberToken(t.low) {
			continue
		}
		if u, ok := matchUnit(lows(toks[i+1:])); ok {
			return Phrase{Text: chunk, Qty: normaliseNumber(t.low), Unit: u.Word}, true
		}
	}
	// Rule 2, a number at the START, after any opening noise words ("need 480 Blu 60").
	for _, t := range toks {
		if leadingNoise[t.low] {
			continue
		}
		if isNumberToken(t.low) {
			return Phrase{Text: chunk, Qty: normaliseNumber(t.low)}, true
		}
		break // the first meaningful token is not a number: rule 2 does not apply
	}
	// Rule 3, a number introduced by 'x' or 'qty' ("Blu 60 x 480", "Brussels qty 300").
	for i := 1; i < len(toks); i++ {
		prev := toks[i-1].low
		if prev != "x" && prev != "×" && prev != "qty" {
			continue
		}
		if isNumberToken(toks[i].low) {
			return Phrase{Text: chunk, Qty: normaliseNumber(toks[i].low)}, true
		}
	}
	// Rule 4, no quantity. The phrase still becomes a Phrase: a product with no quantity is a
	// proposal the rep completes, which is far more useful than a silent omission.
	return Phrase{Text: chunk}, true
}

// token is one word, kept alongside its lower-cased form so nothing lower-cases twice and the
// original casing survives for the query.
type token struct {
	raw string
	low string
}

func lows(ts []token) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.low)
	}
	return out
}

// tokenise splits on whitespace and strips the punctuation that clings to words, keeping the
// characters that are part of a product's identity: '/' ('3/4'), '-' ('Techo-Bloc'), '.' inside a
// number, '"' after a dimension ('24"'), and '²'.
func tokenise(s string) []token {
	fields := strings.Fields(s)
	out := make([]token, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "(),;:!?“”\"'`")
		f = strings.TrimRight(f, ".")
		if f == "" {
			continue
		}
		out = append(out, token{raw: f, low: strings.ToLower(f)})
	}
	return out
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// isNumberToken reports whether a token is a plain decimal quantity: digits, optional thousands
// commas, optional single decimal point. '3/4' is NOT one, see the file header.
func isNumberToken(t string) bool {
	if t == "" {
		return false
	}
	stripped := strings.ReplaceAll(t, ",", "")
	if stripped == "" {
		return false
	}
	_, err := strconv.ParseFloat(stripped, 64)
	if err != nil {
		return false
	}
	// Reject a leading sign or an exponent: neither is how anybody writes an order quantity, and
	// accepting them would let '1e9' through as a number.
	for _, r := range stripped {
		if !(r >= '0' && r <= '9') && r != '.' {
			return false
		}
	}
	return true
}

// normaliseNumber strips thousands separators and returns the digits as written.
//
// It does NOT reformat: "12.5" stays "12.5" and "480" stays "480". The quote line writer parses a
// decimal string and re-formats it to its own precision (quote.formatQty), so a second formatter
// here would be a second opinion about how many decimal places a quantity has.
func normaliseNumber(t string) string {
	return strings.ReplaceAll(t, ",", "")
}

// searchTerms builds the catalogue queries for a phrase, in the order they should be tried.
//
// TWO ATTEMPTS, NEVER MORE. The first is the phrase's content words: quantity gone, unit words
// gone, purpose clause gone, stop words gone. The second drops the LAST content word, and exists
// for one specific, common failure, people append a category word the catalogue spells
// differently ("Blu 60 pavers" where the catalogue says "Slab", "Mini-Creta blocks" where it says
// "Wall"). Below three content words there is no second attempt: a two-word phrase that matched
// nothing is a genuine miss, and shortening it to one word would start returning confident
// nonsense.
//
// Returning a slice rather than searching in here keeps this function pure and testable, and keeps
// the number of catalogue round-trips visible at the call site where it is paid for.
func searchTerms(p Phrase) []string {
	body := stripPurpose(p.Text)
	toks := tokenise(body)
	words := make([]string, 0, len(toks))
	skip := 0
	for i, t := range toks {
		if skip > 0 {
			skip--
			continue
		}
		// The quantity token and the unit phrase attached to it come out together.
		if isNumberToken(t.low) {
			if u, ok := matchUnit(lows(toks[i+1:])); ok {
				skip = u.Tokens
			}
			continue
		}
		if isUnitWord(t.low) || queryStopWords[t.low] {
			continue
		}
		words = append(words, t.raw)
	}
	if len(words) == 0 {
		return nil
	}
	terms := []string{strings.Join(words, " ")}
	if len(words) >= 3 {
		terms = append(terms, strings.Join(words[:len(words)-1], " "))
	}
	return terms
}

// stripPurpose removes a trailing "for the driveway" style clause. The FIRST marker wins, because
// everything after it is the destination rather than the material.
func stripPurpose(s string) string {
	low := strings.ToLower(s)
	cut := -1
	for _, m := range purposeMarkers {
		if i := strings.Index(low, m); i >= 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut <= 0 {
		return s
	}
	head := strings.TrimSpace(s[:cut])
	// Never strip away everything: a phrase that IS a purpose clause ("for the driveway") keeps its
	// words, so the unmatched row can quote something back to the rep.
	if head == "" {
		return s
	}
	return head
}
