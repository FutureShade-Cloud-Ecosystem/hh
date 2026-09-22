package quoteassist

// extract.go, phrases → PROPOSALS, against the real catalogue.
//
// -----------------------------------------------------------------------------------------------
// THE PRODUCT IS ALWAYS THE CATALOGUE'S ANSWER
// -----------------------------------------------------------------------------------------------
// resolve() is the only place a Proposal's ProductID/SKU/Name are ever set, and the only values it
// can set them from are the hits a CatalogueSearch returned. That is invariant 2 from the package
// header, expressed as the shape of one function: whether the phrases arrived from Segment() or
// from a language model, they pass through here, and here there is no branch that trusts a name.
//
// -----------------------------------------------------------------------------------------------
// NOTHING IS DROPPED, AND NOTHING IS ADDED
// -----------------------------------------------------------------------------------------------
// Every phrase leaves this function as exactly one of two things: a Proposal (the catalogue matched)
// or an Unmatched row (it did not, and here is why, in the rep's own words). There is no third
// outcome and no silent path. A rep who pasted eleven items gets eleven rows back, and can count.
//
// -----------------------------------------------------------------------------------------------
// WHY CONFIDENCE IS COMPUTED FROM EVIDENCE AND NOT FROM A SCORE
// -----------------------------------------------------------------------------------------------
// The catalogue search does not return a probability, and inventing one from its rank arm would be
// dressing an ordering as a measurement. What it DOES return is why it matched, and the parser
// knows whether it found a quantity and whether the unit is one the yard quotes in. Those three
// facts are what a rep would check by hand, so they are what the band is built from, and the same
// three facts are written out in words on the row (Proposal.Why), from the same values, so the
// label can never say HIGH while the evidence reads "close match, no quantity".

import (
	"context"
	"sort"
	"strings"
)

// maxHits is how many catalogue rows one phrase asks for: the pick plus two alternates.
//
// Three, not twenty. This is a review list, and a row that offers a rep twenty choices has handed
// the search problem back to them, at which point the product picker they already have is a better
// tool. Two alternates covers the actual failure ("it picked the Blu 60 slab, I meant the Blu 60
// step") without turning every row into a menu.
const maxHits = 3

// maxAlternates is how many runner-ups a proposal carries.
const maxAlternates = maxHits - 1

// Match-reason arms, as the catalogue search spells them (product.matchReasonFor). Named here
// because the confidence bands are defined in terms of them and a typo would silently demote every
// exact match to LOW.
const (
	reasonExact       = "EXACT"
	reasonSKUPrefix   = "SKU_PREFIX"
	reasonNamePrefix  = "NAME_PREFIX"
	reasonName        = "NAME"
	reasonDescription = "DESCRIPTION"
	reasonAlias       = "ALIAS"
	reasonFuzzy       = "FUZZY"
)

// strongMatch is a catalogue match on the product's own identifiers, the rep typed a SKU, or typed
// the name in full. Nothing about these needs a second look.
func strongMatch(reason string) bool {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case reasonExact, reasonSKUPrefix:
		return true
	}
	return false
}

// namedMatch is a match on the product's name or on a synonym the yard itself maintains. Good
// evidence, but a name can be ambiguous in a catalogue with five Techo-Bloc lines in it.
func namedMatch(reason string) bool {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case reasonNamePrefix, reasonName, reasonAlias:
		return true
	}
	return false
}

// resolveInput is one phrase plus everything already known about it, handed to resolve() so the
// function has no hidden inputs and can be tested against a fake search alone.
type resolveInput struct {
	Phrase   Phrase
	BranchID string
}

// resolveAll turns every phrase into a proposal or an unmatched row, in the order the rep wrote
// them. ORDER IS THE REP'S ORDER, always: a list re-sorted by confidence stops corresponding to
// the paragraph above it, and checking it becomes a search rather than a read.
func resolveAll(ctx context.Context, search CatalogueSearch, phrases []Phrase, branchID string) ([]Proposal, []Unmatched, error) {
	proposals := make([]Proposal, 0, len(phrases))
	unmatched := make([]Unmatched, 0)
	n := 0
	for _, p := range phrases {
		prop, un, err := resolve(ctx, search, resolveInput{Phrase: p, BranchID: branchID})
		if err != nil {
			return nil, nil, err
		}
		if un != nil {
			unmatched = append(unmatched, *un)
			continue
		}
		n++
		prop.Ref = refFor(n)
		proposals = append(proposals, *prop)
	}
	return proposals, unmatched, nil
}

// refFor mints the per-answer handle. "p1", "p2", short enough to read in a test failure and
// stable within one answer, which is all the surface's confirm state needs.
func refFor(n int) string {
	return "p" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// resolve is the whole of one row's decision. Exactly one of (proposal, unmatched) is non-nil.
func resolve(ctx context.Context, search CatalogueSearch, in resolveInput) (*Proposal, *Unmatched, error) {
	phrase := in.Phrase
	terms := searchTerms(phrase)
	if len(terms) == 0 {
		// Nothing to search for: the phrase was a quantity and a unit with no product in it
		// ("another 6 bags"). Reported rather than dropped, because the rep meant something by it
		// and only they know what.
		return nil, &Unmatched{
			SourceText: phrase.Text,
			Reason:     "there's a quantity here but nothing naming a product, say what the " + strings.TrimSpace(phrase.Unit+" ") + "is for and I'll look it up",
		}, nil
	}

	var hits []CatalogueHit
	used := terms[0]
	for _, term := range terms {
		found, err := search.SearchCatalogue(ctx, CatalogueQuery{Text: term, BranchID: in.BranchID, Limit: maxHits})
		if err != nil {
			return nil, nil, err
		}
		if len(found) > 0 {
			hits, used = found, term
			break
		}
	}
	if len(hits) == 0 {
		// THE HONEST EMPTY. It names the term that was actually searched, so a rep can see whether
		// the parser mangled their words or the yard genuinely does not stock the thing, two very
		// different next actions, and a bare "no match" tells them neither.
		return nil, &Unmatched{
			SourceText: phrase.Text,
			Reason:     "nothing in the catalogue matched “" + used + "”",
			Query:      used,
		}, nil
	}

	top := hits[0]
	prop := Proposal{
		SourceText:  phrase.Text,
		ProductID:   top.ProductID,
		SKU:         top.SKU,
		Name:        top.Name,
		SellUOM:     top.SellUOM,
		BaseUOM:     top.BaseUOM,
		QtyEntered:  phrase.Qty,
		MatchReason: strings.ToUpper(strings.TrimSpace(top.MatchReason)),
		Alternates:  alternatesOf(hits),
	}
	prop.NeedsQty = strings.TrimSpace(prop.QtyEntered) == ""
	prop.UOMEntered, prop.UnitNote, prop.NeedsUOM = resolveUnit(ctx, search, phrase.Unit, top)
	prop.Confidence, prop.Why = confidenceFor(prop)
	return &prop, nil, nil
}

// alternatesOf carries the runner-ups. Never the top hit (it is the proposal) and never more than
// maxAlternates.
func alternatesOf(hits []CatalogueHit) []Alternate {
	out := make([]Alternate, 0, maxAlternates)
	for i := 1; i < len(hits) && i <= maxAlternates; i++ {
		out = append(out, Alternate{
			ProductID:   hits[i].ProductID,
			SKU:         hits[i].SKU,
			Name:        hits[i].Name,
			MatchReason: strings.ToUpper(strings.TrimSpace(hits[i].MatchReason)),
			SellUOM:     hits[i].SellUOM,
			BaseUOM:     hits[i].BaseUOM,
		})
	}
	return out
}

// resolveUnit decides what unit this line should be quoted in, and what to say when the text's unit
// is not one of them.
//
// FOUR OUTCOMES, AND THE CATALOGUE DECIDES THREE OF THEM:
//
//	the text named a unit product_uoms carries   → quote in it, no note
//	the text named no unit at all                → quote in the product's own sell/stocking unit,
//	                                               and SAY that it was defaulted rather than read
//	the text named a unit this product has no    → NO unit, a note naming both, needs the rep
//	  factor for ("6 bags of Blu 60")
//	the words are not a unit at all              → NO unit, a note quoting what could not be read
//
// THE THIRD CASE IS WHERE A CONVERSION WOULD BE TEMPTING AND IS REFUSED. product_uoms holds the
// factor; pricing owns the multiplication; this function only ever asks whether a row exists
// (units.go header). "2 pallets of Mini-Creta" now lands as PLT because the yard genuinely keeps a
// skid factor for it, not because anything here multiplied by 50.
//
// AN UNANSWERABLE SEAM IS NOT A REFUSAL. If QuotableUnit errors, the unit is left for the rep to
// confirm and the note says so. Accepting it would put a unit on a quote the catalogue never
// approved; dropping the whole proposal would lose a line the rep asked for.
func resolveUnit(ctx context.Context, search CatalogueSearch, rawUnit string, hit CatalogueHit) (uom, note string, needs bool) {
	fallback := defaultUOM(hit)
	word := strings.ToLower(strings.TrimSpace(rawUnit))

	if word == "" {
		if fallback == "" {
			return "", "no unit in the text, and the catalogue lists no unit for this product, pick one", true
		}
		// The product's own unit, and the row SAYS it is a default rather than a reading. A rep
		// glancing down the list must be able to tell the two apart.
		return fallback, "no unit in the text, quoting in " + fallback + ", this product's " +
			defaultUOMKind(hit) + " unit", false
	}

	m, ok := matchUnit(strings.Fields(word))
	if !ok {
		return "", "couldn't read “" + rawUnit + "” as a unit, " + quotedInPhrase(fallback), true
	}
	if m.Code == "" {
		// A quantity-word that is not a unit of sale: "3 loads of screenings". The number is real and
		// the unit is a conversation.
		return "", "“" + m.Word + "” isn't a unit we quote in, " + quotedInPhrase(fallback), true
	}

	quotable, err := search.QuotableUnit(ctx, hit.ProductID, m.Code)
	switch {
	case err != nil:
		return "", "couldn't check whether this product is quoted in “" + m.Word + "”, confirm the unit", true
	case quotable:
		return m.Code, "", false
	}
	return "", "the catalogue has no " + m.Code + " factor for this product, so “" + m.Word +
		"” needs converting by hand, " + quotedInPhrase(fallback), true
}

// quotedInPhrase renders "it's quoted in SF" or, when the catalogue lists no unit at all, says that
// instead of printing a blank.
func quotedInPhrase(fallback string) string {
	if fallback == "" {
		return "the catalogue lists no unit for it either"
	}
	return "it's quoted in " + fallback
}

// confidenceFor computes the band AND the words that explain it, from one set of facts.
//
// ONE FUNCTION, TWO OUTPUTS, BY CONSTRUCTION. The alternative, a band here and a sentence built
// somewhere in the frontend, is how a row ends up labelled HIGH next to the words "close match".
func confidenceFor(p Proposal) (string, []string) {
	why := make([]string, 0, 3)
	switch {
	case strongMatch(p.MatchReason):
		if p.MatchReason == reasonExact {
			why = append(why, "matched the catalogue exactly on “"+p.SKU+"”")
		} else {
			why = append(why, "matched the SKU “"+p.SKU+"”")
		}
	case namedMatch(p.MatchReason):
		if p.MatchReason == reasonAlias {
			why = append(why, "matched a catalogue synonym for “"+p.Name+"”")
		} else {
			why = append(why, "matched the product name “"+p.Name+"”")
		}
	case p.MatchReason == reasonDescription:
		why = append(why, "matched words in the product description, not its name")
	default:
		why = append(why, "closest catalogue match, the spelling didn't line up exactly")
	}
	if p.NeedsQty {
		why = append(why, "no quantity in the text, you'll need to add one")
	} else {
		why = append(why, "read the quantity "+p.QtyEntered+" from your text")
	}
	switch {
	case p.NeedsUOM:
		why = append(why, "the unit needs your call, see the note")
	case p.UnitNote != "":
		why = append(why, "no unit in the text, so it's quoting in "+p.UOMEntered)
	default:
		why = append(why, "the unit "+p.UOMEntered+" is one this product is sold in")
	}

	complete := !p.NeedsQty && !p.NeedsUOM
	switch {
	case strongMatch(p.MatchReason) && complete && p.UnitNote == "":
		// Everything present, everything read from the text, matched on an identifier.
		return ConfidenceHigh, why
	case strongMatch(p.MatchReason) && complete:
		// An identifier match with the unit defaulted rather than read. Not HIGH: the number on
		// the row is the yard's assumption about a unit, and a rep should look once.
		return ConfidenceMedium, why
	case namedMatch(p.MatchReason) && complete && p.UnitNote == "":
		return ConfidenceMedium, why
	// A NAMED match with an ASSUMED unit is TWO assumptions stacked, which product, and which
	// unit, and two soft spots on one row is not a medium-confidence row. It stays LOW, which is
	// the difference between "check this" and "glance at this". A rep skimming a list of MEDIUMs
	// has to be able to trust that each one has exactly one thing worth a second look.
	default:
		// Fuzzy, description-level, or missing something. Still a row, still one click from being
		// right, and honestly labelled.
		return ConfidenceLow, why
	}
}

// SortRules orders scope rules for presentation: sort_order, then code as the tie-break.
//
// Lives here rather than in the SQL because the pure evaluator must produce the same order when it
// is handed rules from a fake, and a test whose ordering only holds against Postgres is a test that
// stops describing the code.
func SortRules(rules []ScopeRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].SortOrder != rules[j].SortOrder {
			return rules[i].SortOrder < rules[j].SortOrder
		}
		return rules[i].Code < rules[j].Code
	})
}
