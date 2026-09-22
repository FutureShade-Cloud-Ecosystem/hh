package quoteassist

// extract_test.go, DIB-66's named invariants.
//
// THE FOUR THAT ARE LOAD-BEARING, and the mutation each one kills (NOTES-FIX-6667.md):
//
//	TestExtract_NeverAutoAddsALine                 M1, the extractor gains a writer
//	TestExtract_PackageHasNoQuoteWriter            M1, the same, proved from the source
//	TestExtract_UnmatchedTextIsSurfacedNotDropped  M2, a miss is silently swallowed
//	TestExtract_AIPhraseCannotNameAProduct         M5, a model's product name is trusted
//
//	go test ./internal/quoteassist/

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// realJobDescription is the paragraph the walkthrough pastes, kept here so the unit tests and the
// Chromium run describe the same input. Written the way a contractor writes: mixed units, a purpose
// clause per item, one item with no quantity, and one thing this yard does not sell.
const realJobDescription = `Need 480 sq ft of Techo-Bloc Blu 60 for the back patio, 12 tonnes of 3/4 clear stone for the base,
6 bags of polymeric sand and 40 linear feet of Banas coping. Also 2 pallets of Mini-Creta wall block,
some limestone screenings, and a bobcat for the day.`

// -- 1 · the proposal-only invariant -------------------------------------------------------------

// TestExtract_NeverAutoAddsALine is the ticket's central claim, asserted the only way it can be
// asserted from inside this package: the answer is a list of PROPOSALS, and there is no code path
// out of Extract that touches a quote.
//
// The stronger, structural half of the same claim is TestExtract_PackageHasNoQuoteWriter below, and
// the strongest is TestIntegration_Extract_AddsNoLines, which counts quote_lines in a real database
// before and after. Three tests for one property because it is THE property: a feature that puts
// lines on a customer's quote without a person saying yes is not a feature, it is an incident.
func TestExtract_NeverAutoAddsALine(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: realJobDescription})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) == 0 {
		t.Fatal("no proposals, this test is measuring nothing")
	}
	// Every proposal is a PROPOSAL: it carries the rep's own words back, so the row is checkable,
	// and it carries no notion of having been applied.
	for _, p := range got.Proposals {
		if strings.TrimSpace(p.SourceText) == "" {
			t.Errorf("proposal %s carries no source text, a rep cannot check a row they cannot read", p.Ref)
		}
		if strings.TrimSpace(p.Ref) == "" {
			t.Error("a proposal with no ref cannot be confirmed or rejected individually")
		}
		if strings.TrimSpace(p.Confidence) == "" {
			t.Errorf("proposal %s has no confidence band, the review list would be unreadable", p.Ref)
		}
	}
	// The catalogue was READ. It is the only seam this service has, and it has no write method.
	if cat.calls == 0 {
		t.Fatal("the catalogue was never searched, the products would have to have been invented")
	}
}

// TestExtract_PackageHasNoQuoteWriter is the structural half: this package cannot write a quote line
// because there is nothing in it that could.
//
// A SOURCE SCAN, and the house has four of them already (_dib425-sweep, _dib471-surfaces,
// _catalogue, _inventory-lookup-state) for the same reason: some properties are about the ABSENCE of
// code, and absence is not observable from a call. Comments are stripped first, this file's own
// header talks about writers, and a gate that read its own prose would pass vacuously.
func TestExtract_PackageHasNoQuoteWriter(t *testing.T) {
	src := packageSource(t)
	if len(src) < 4000 {
		t.Fatalf("scanned only %d bytes of package source, this gate is measuring nothing", len(src))
	}
	// The write verbs. Each one is a way this package could have grown a path that puts a line on a
	// quote without a person saying yes.
	banned := []struct{ pattern, why string }{
		{`INSERT\s+INTO\s+quote_lines`, "an INSERT into quote_lines"},
		{`UPDATE\s+quote_lines`, "an UPDATE of quote_lines"},
		{`UPDATE\s+quotes`, "an UPDATE of quotes"},
		{`\.AddLine\(`, "a call to the quote module's line writer"},
		{`internal/quote"`, "an import of the quote module (the writer lives there)"},
		{`internal/pricing"`, "an import of pricing (a proposal is not priced here)"},
	}
	for _, b := range banned {
		if regexp.MustCompile(b.pattern).MatchString(src) {
			t.Errorf("internal/quoteassist contains %s, extraction must stay proposal-only", b.why)
		}
	}
	// And the positive half: the only mutation this package has is the admin switch, which changes a
	// rule's visibility and nothing else.
	if !strings.Contains(src, "SetRuleEnabled") {
		t.Fatal("SetRuleEnabled is missing, this gate no longer knows what the package's one write is")
	}
	if strings.Count(src, "UPDATE quote_scope_rules") != 1 {
		t.Errorf("expected exactly one UPDATE statement in this package (the rule switch), found %d",
			strings.Count(src, "UPDATE quote_scope_rules"))
	}
}

// packageSource concatenates this package's non-test Go source with comments stripped.
func packageSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment := regexp.MustCompile(`(?m)^\s*//.*$`)
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := blockComment.ReplaceAllString(string(raw), "")
		s = lineComment.ReplaceAllString(s, "")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

// -- 2 · unmatched text stays visible ------------------------------------------------------------

// TestExtract_UnmatchedTextIsSurfacedNotDropped: eleven items in, eleven rows out.
//
// The failure this prevents is the quiet one. A rep who pastes a list and is handed a shorter list of
// proposals has no way to know which items went missing, and will find out when the customer does.
// So the count is asserted, not just the presence of an unmatched section.
func TestExtract_UnmatchedTextIsSurfacedNotDropped(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	text := "480 sq ft Blu 60\n12 t clear stone\na bobcat for the day\nporta potty rental"

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: text})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if want := 4; len(got.Proposals)+len(got.Unmatched) != want {
		t.Fatalf("proposals+unmatched = %d, want %d, a phrase was dropped\nproposals: %v\nunmatched: %v",
			len(got.Proposals)+len(got.Unmatched), want, refsOf(got.Proposals), textsOf(got.Unmatched))
	}
	if len(got.Unmatched) != 2 {
		t.Fatalf("unmatched = %d, want 2 (the bobcat and the porta potty)\n%v", len(got.Unmatched), textsOf(got.Unmatched))
	}
	for _, u := range got.Unmatched {
		if strings.TrimSpace(u.SourceText) == "" {
			t.Error(`an unmatched row with no source text is a dropped row wearing a costume`)
		}
		if strings.TrimSpace(u.Reason) == "" {
			t.Errorf("unmatched %q carries no reason, a rep cannot tell a parser mistake from a product we don't stock", u.SourceText)
		}
		// The reason NAMES the term that was searched, which is what makes the two cases
		// distinguishable at a glance.
		if u.Query == "" {
			t.Errorf("unmatched %q names no query, the surface cannot offer 'search the catalogue for this'", u.SourceText)
		}
	}
}

// TestExtract_QuantityWithNoProductIsReportedNotGuessed: "another 6 bags" names no product, and the
// honest answer is to say so rather than to attach the quantity to whatever the last row was.
func TestExtract_QuantityWithNoProductIsReportedNotGuessed(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "6 bags"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 0 {
		t.Fatalf("a bare quantity produced %d proposals, it named no product", len(got.Proposals))
	}
	if len(got.Unmatched) != 1 {
		t.Fatalf("unmatched = %d, want 1", len(got.Unmatched))
	}
	if !strings.Contains(got.Unmatched[0].Reason, "quantity") {
		t.Errorf("reason = %q, want it to say a quantity was found with nothing to attach it to", got.Unmatched[0].Reason)
	}
}

// -- 3 · empty is an answer ----------------------------------------------------------------------

// TestExtract_EmptyTextIsAnEmptyAnswerNotAnError, the DIB-443 rule, one surface further on. A rep
// who presses Read before typing must not get a red box.
func TestExtract_EmptyTextIsAnEmptyAnswerNotAnError(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	for _, in := range []string{"", "   ", "\n\t "} {
		got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: in})
		if err != nil {
			t.Fatalf("Extract(%q): %v, empty is an answer, never a failure", in, err)
		}
		if got.Proposals == nil || got.Unmatched == nil {
			t.Errorf("Extract(%q) returned nil slices, the wire must carry [] and not null", in)
		}
		if len(got.Proposals) != 0 {
			t.Errorf("Extract(%q) invented %d proposals", in, len(got.Proposals))
		}
	}
}

// TestExtract_UnwiredCatalogueIsA503Seam, a seam nobody connected is named, not crashed through.
func TestExtract_UnwiredCatalogueIsA503Seam(t *testing.T) {
	svc := NewService(nil, nil, nil, nil)
	if _, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"}); err != ErrCatalogueUnwired {
		t.Fatalf("err = %v, want ErrCatalogueUnwired", err)
	}
}

// -- 4 · the product is always the catalogue's answer --------------------------------------------

// TestExtract_AIPhraseCannotNameAProduct is invariant 2 from the package header.
//
// The fake AI returns a phrase whose TEXT is a fabricated product ("Techo-Bloc Quantum 9000") and
// whose words include a fabricated SKU. The extractor must not put either on a proposal: the only
// products that can appear are ones the catalogue returned, and this phrase resolves to nothing.
//
// Under mutation M5, Phrase grows a ProductID and resolve() trusts it, this test fails by name.
func TestExtract_AIPhraseCannotNameAProduct(t *testing.T) {
	cat := seededCatalogue()
	ai := &fakeAI{seg: Segmentation{
		Provider: "test", Model: "test-1",
		Phrases: []Phrase{
			{Text: "300 sq ft of Techo-Bloc Quantum 9000 (SKU DBS-TB-QUANTUM9000)", Qty: "300", Unit: "sq ft"},
			{Text: "12 tonnes of 3/4 clear stone", Qty: "12", Unit: "tonnes"},
		},
	}}
	svc := NewService(cat, nil, nil, nil).WithAI(ai)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "anything", UseAI: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if ai.call != 1 {
		t.Fatalf("the AI seam was called %d times, want 1", ai.call)
	}
	if got.Source != SourceAIAssisted {
		t.Fatalf("source = %q, want %q", got.Source, SourceAIAssisted)
	}
	// The invented product is NOT a proposal. It is a "couldn't match" row.
	for _, p := range got.Proposals {
		if strings.Contains(strings.ToUpper(p.SKU), "QUANTUM") || strings.Contains(p.Name, "Quantum") {
			t.Fatalf("a model's invented product reached a proposal: %+v", p)
		}
	}
	if len(got.Unmatched) != 1 {
		t.Fatalf("unmatched = %d, want 1 (the invented product)\n%v", len(got.Unmatched), textsOf(got.Unmatched))
	}
	// And the real one still resolved, through the catalogue, from the catalogue's own row.
	if len(got.Proposals) != 1 || got.Proposals[0].ProductID != "prod-clear" {
		t.Fatalf("proposals = %+v, want exactly the catalogue's 3/4 clear stone", got.Proposals)
	}
	// Every query the extractor ran came from the PHRASE TEXT, through searchTerms, never from a
	// field the provider filled with a product.
	if len(cat.gotQueries) == 0 {
		t.Fatal("the catalogue was never searched on the AI path")
	}
}

// TestExtract_ProposalProductAlwaysComesFromAHit: the fields that identify a product on a proposal
// are byte-equal to the catalogue row they came from. Nothing is normalised, prettified or filled in.
func TestExtract_ProposalProductAlwaysComesFromAHit(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(got.Proposals))
	}
	p := got.Proposals[0]
	var row *CatalogueHit
	for i := range cat.rows {
		if cat.rows[i].ProductID == p.ProductID {
			row = &cat.rows[i]
		}
	}
	if row == nil {
		t.Fatalf("proposal names product %q, which is not in the catalogue at all", p.ProductID)
	}
	if p.SKU != row.SKU || p.Name != row.Name || p.BaseUOM != row.BaseUOM || p.SellUOM != row.SellUOM {
		t.Errorf("proposal identity drifted from the catalogue row:\nproposal %+v\nrow      %+v", p, *row)
	}
}

// -- 5 · quantities and units --------------------------------------------------------------------

// TestExtract_ReadsQuantityAndUnitFromRealPhrasing walks the sentence forms a contractor writes.
func TestExtract_ReadsQuantityAndUnitFromRealPhrasing(t *testing.T) {
	cases := []struct {
		in       string
		wantQty  string
		wantUOM  string
		wantSKU  string
		wantBand string
	}{
		{"480 sq ft of Techo-Bloc Blu 60 for the back patio", "480", "SF", "DBS-TB-BLU60", ConfidenceMedium},
		{"12 tonnes of 3/4 clear stone", "12", "T", "DBS-AGG-34CLEAR", ConfidenceMedium},
		{"6 bags of polymeric sand", "6", "BAG", "DBS-GATOR-SAND", ConfidenceMedium},
		{"1,200 sqft Brussels Block", "1200", "SF", "DBS-UL-BRUSSELS", ConfidenceMedium},
		{"12.5 t limestone screenings", "12.5", "T", "DBS-AGG-SCREEN", ConfidenceMedium},
		{"Blu 60 x 300", "300", "PC", "DBS-TB-BLU60", ConfidenceLow},
		{"300 Brussels Block", "300", "SF", "DBS-UL-BRUSSELS", ConfidenceLow},
		{"8 pieces of Banas Natural Step", "8", "PC", "DBS-BANAS-STEP", ConfidenceMedium},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			svc := NewService(seededCatalogue(), nil, nil, nil)
			got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: tc.in})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(got.Proposals) != 1 {
				t.Fatalf("proposals = %d, want 1 (unmatched: %v)", len(got.Proposals), textsOf(got.Unmatched))
			}
			p := got.Proposals[0]
			if p.SKU != tc.wantSKU {
				t.Errorf("sku = %q, want %q", p.SKU, tc.wantSKU)
			}
			if p.QtyEntered != tc.wantQty {
				t.Errorf("qty = %q, want %q", p.QtyEntered, tc.wantQty)
			}
			if p.UOMEntered != tc.wantUOM {
				t.Errorf("uom = %q, want %q", p.UOMEntered, tc.wantUOM)
			}
			if p.Confidence != tc.wantBand {
				t.Errorf("confidence = %q, want %q (why: %v)", p.Confidence, tc.wantBand, p.Why)
			}
		})
	}
}

// TestExtract_ModelNumbersAreNotQuantities is the one that stops the embarrassing failure.
//
// "Unilock Series 3000" is a product name with a number in it, and every catalogue in this business
// has several. A parser that read 3000 as an order quantity would quote three thousand somethings to
// a customer, and it would look exactly like a working feature until it did.
func TestExtract_ModelNumbersAreNotQuantities(t *testing.T) {
	cat := &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "prod-3000", SKU: "DBS-UL-SERIES3000", Name: "Unilock Series 3000", BaseUOM: "PC", SellUOM: "SF", MatchReason: "NAME"},
		},
		quotable: map[string]map[string]bool{"prod-3000": {"SF": true, "PC": true}},
	}
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "Unilock Series 3000 for the walkway"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(got.Proposals))
	}
	p := got.Proposals[0]
	if p.QtyEntered != "" {
		t.Fatalf("qty = %q, 3000 is the product's model number, not an order quantity", p.QtyEntered)
	}
	if !p.NeedsQty {
		t.Error("needs_qty must be true so the surface refuses to confirm the row until a rep types a quantity")
	}
	if p.Confidence != ConfidenceLow {
		t.Errorf("confidence = %q, want LOW, a row with no quantity is not a confident row", p.Confidence)
	}
}

// TestExtract_NumberedListMarkersAreNotQuantities, a pasted "1. 480 sq ft Blu 60" must read 480,
// not 1.
func TestExtract_NumberedListMarkersAreNotQuantities(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "1. 480 sq ft Blu 60\n2. 12 t clear stone"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 2 {
		t.Fatalf("proposals = %d, want 2 (unmatched: %v)", len(got.Proposals), textsOf(got.Unmatched))
	}
	if got.Proposals[0].QtyEntered != "480" {
		t.Errorf("qty = %q, want 480, '1.' is a list marker", got.Proposals[0].QtyEntered)
	}
	if got.Proposals[1].QtyEntered != "12" {
		t.Errorf("qty = %q, want 12", got.Proposals[1].QtyEntered)
	}
}

// TestExtract_PalletsAreQuotableBecauseTheCatalogueKeepsASkidFactor.
//
// THIS TEST RECORDS A CORRECTION. An earlier version of this extractor decided for itself which units
// a product could be quoted in, by comparing the parsed code against the two units a search hit
// carries, and it refused "2 pallets of Mini-Creta" as an unquotable unit. On the real data that was
// simply wrong: product_uoms carries a PLT row for every paver and slab in the catalogue (50 pieces to
// a skid of Antico, 40 of Trevista, 54 of Proma), so a pallet is an ordinary line the yard writes
// every day. The authority moved to where it belongs, CatalogueSearch.QuotableUnit, over the yard's
// own table, and the refusal became an acceptance.
//
// NOTHING HERE CONVERTS. The quantity stays 2 and the unit stays PLT; pricing multiplies by the
// factor it owns. Asking whether a factor EXISTS is not the same as using it.
func TestExtract_PalletsAreQuotableBecauseTheCatalogueKeepsASkidFactor(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "2 pallets of Mini-Creta"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 (unmatched: %v)", len(got.Proposals), textsOf(got.Unmatched))
	}
	p := got.Proposals[0]
	if p.QtyEntered != "2" || p.UOMEntered != "PLT" {
		t.Fatalf("proposal = %s %s, want 2 PLT", p.QtyEntered, p.UOMEntered)
	}
	if p.NeedsUOM {
		t.Error("needs_uom is true for a unit the catalogue keeps a factor for")
	}
	if p.UnitNote != "" {
		t.Errorf("unit_note = %q, there is nothing to explain about a unit that was read and accepted", p.UnitNote)
	}
	// THE CATALOGUE decided, and the assertion is on what it was ASKED.
	if len(cat.gotUnitChecks) != 1 || cat.gotUnitChecks[0] != "prod-minicreta:PLT" {
		t.Fatalf("unit checks = %v, want exactly one ask of the catalogue for prod-minicreta:PLT", cat.gotUnitChecks)
	}
}

// TestExtract_AUnitTheCatalogueHasNoFactorForIsFlaggedNeverConverted, the aggregates are quotable in
// tonnes and in nothing else, so "3 pallets of clear stone" is a question for the rep.
func TestExtract_AUnitTheCatalogueHasNoFactorForIsFlaggedNeverConverted(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "3 pallets of 3/4 clear stone"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 (unmatched: %v)", len(got.Proposals), textsOf(got.Unmatched))
	}
	p := got.Proposals[0]
	if p.QtyEntered != "3" {
		t.Errorf("qty = %q, want 3, the quantity is still read", p.QtyEntered)
	}
	if p.UOMEntered != "" || !p.NeedsUOM {
		t.Fatalf("uom = %q needs_uom = %v, the catalogue has no PLT factor for a bulk aggregate", p.UOMEntered, p.NeedsUOM)
	}
	if !strings.Contains(p.UnitNote, "PLT") || !strings.Contains(p.UnitNote, "T") {
		t.Errorf("unit_note = %q, want it to name both the unit read and the unit it IS quoted in", p.UnitNote)
	}
	if p.Confidence != ConfidenceLow {
		t.Errorf("confidence = %q, want LOW", p.Confidence)
	}
}

// TestExtract_ANonUnitQuantityWordLeavesTheUnitToTheRep, "3 loads of screenings". A truck load is a
// delivery arrangement, not a unit of sale; the number is real and the unit is a conversation.
func TestExtract_ANonUnitQuantityWordLeavesTheUnitToTheRep(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "3 loads of limestone screenings"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 (unmatched: %v)", len(got.Proposals), textsOf(got.Unmatched))
	}
	p := got.Proposals[0]
	if p.SKU != "DBS-AGG-SCREEN" {
		t.Fatalf("sku = %q, 'loads' must not have poisoned the search", p.SKU)
	}
	if p.QtyEntered != "3" {
		t.Errorf("qty = %q, want 3", p.QtyEntered)
	}
	if p.UOMEntered != "" || !p.NeedsUOM {
		t.Errorf("uom = %q needs_uom = %v, a load is not a unit of sale", p.UOMEntered, p.NeedsUOM)
	}
	// The catalogue was never asked, because there was no unit code to ask about.
	if len(cat.gotUnitChecks) != 0 {
		t.Errorf("the catalogue was asked %v about a word that maps to no unit code", cat.gotUnitChecks)
	}
}

// TestExtract_AnUnanswerableUnitCheckLeavesItToTheRep, a seam that cannot answer must not accept
// (that puts a unit on a quote the catalogue never approved) and must not drop the row (that loses a
// line the rep asked for).
func TestExtract_AnUnanswerableUnitCheckLeavesItToTheRep(t *testing.T) {
	cat := seededCatalogue()
	cat.unitErr = errBoomQA
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v, an unanswerable unit check is not a failed extraction", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(got.Proposals))
	}
	p := got.Proposals[0]
	if p.UOMEntered != "" || !p.NeedsUOM {
		t.Errorf("uom = %q needs_uom = %v", p.UOMEntered, p.NeedsUOM)
	}
	if !strings.Contains(p.UnitNote, "confirm") {
		t.Errorf("unit_note = %q, want it to ask the rep to confirm", p.UnitNote)
	}
}

// TestExtract_BagsOfPaversIsFlagged, a unit the yard DOES sell in, but not for this product.
func TestExtract_BagsOfPaversIsFlagged(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "6 bags of Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(got.Proposals))
	}
	p := got.Proposals[0]
	if p.UOMEntered != "" || !p.NeedsUOM {
		t.Errorf("uom = %q needs_uom = %v, BAG is not how a slab is sold", p.UOMEntered, p.NeedsUOM)
	}
}

// TestExtract_NoUnitFallsBackToThePriceUnitAndSaysSo, the defaulted-unit case is MEDIUM, never
// HIGH, because the number on the row rests on the yard's assumption rather than on the rep's words.
func TestExtract_NoUnitFallsBackToThePriceUnitAndSaysSo(t *testing.T) {
	cat := &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "prod-gator", SKU: "DBS-GATOR-SAND", Name: "Alliance Gator Polymeric Sand", BaseUOM: "BAG", SellUOM: "BAG", MatchReason: "EXACT"},
		},
		quotable: map[string]map[string]bool{"prod-gator": {"BAG": true}},
	}
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "6 Gator Polymeric Sand"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	p := got.Proposals[0]
	if p.UOMEntered != "BAG" {
		t.Errorf("uom = %q, want BAG (the product's own price unit)", p.UOMEntered)
	}
	if p.NeedsUOM {
		t.Error("a product with a price unit does not need the rep to pick one")
	}
	if !strings.Contains(p.UnitNote, "BAG") {
		t.Errorf("unit_note = %q, want it to say the unit was defaulted", p.UnitNote)
	}
	if p.Confidence != ConfidenceMedium {
		t.Errorf("confidence = %q, want MEDIUM, the unit was assumed, not read", p.Confidence)
	}
}

// TestExtract_ExactSKUWithEverythingReadIsHigh, the one shape that earns HIGH.
func TestExtract_ExactSKUWithEverythingReadIsHigh(t *testing.T) {
	cat := &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "prod-blu60", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", BaseUOM: "PC", SellUOM: "SF", MatchReason: "EXACT"},
		},
		quotable: map[string]map[string]bool{"prod-blu60": {"SF": true, "PC": true}},
	}
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft DBS-TB-BLU60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	p := got.Proposals[0]
	if p.Confidence != ConfidenceHigh {
		t.Fatalf("confidence = %q, want HIGH (why: %v)", p.Confidence, p.Why)
	}
	// The band and the explanation are built from the same facts, so the words must agree with it.
	joined := strings.Join(p.Why, " | ")
	if !strings.Contains(joined, "480") {
		t.Errorf("why = %q, want it to name the quantity that was read", joined)
	}
	if strings.Contains(strings.ToLower(joined), "no quantity") {
		t.Errorf("why = %q says no quantity on a HIGH row, the band and the evidence have drifted", joined)
	}
}

// TestExtract_FuzzyMatchIsLowEvenWithEverythingElsePresent, a close-match row is never confident,
// because the thing in doubt is WHICH PRODUCT, and that is the expensive mistake.
func TestExtract_FuzzyMatchIsLowEvenWithEverythingElsePresent(t *testing.T) {
	cat := &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "prod-blu60", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", BaseUOM: "PC", SellUOM: "SF", MatchReason: "FUZZY"},
		},
		quotable: map[string]map[string]bool{"prod-blu60": {"SF": true, "PC": true}},
	}
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if got.Proposals[0].Confidence != ConfidenceLow {
		t.Fatalf("confidence = %q, want LOW for a FUZZY match", got.Proposals[0].Confidence)
	}
}

// -- 6 · the two-attempt search rule -------------------------------------------------------------

// TestExtract_SecondAttemptDropsTheTrailingCategoryWord, "Blu 60 pavers" fails as written (the
// catalogue calls it a Slab) and succeeds without the trailing word. Bounded at two attempts.
func TestExtract_SecondAttemptDropsTheTrailingCategoryWord(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Techo-Bloc Blu 60 pavers"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 (queries: %v)", len(got.Proposals), cat.gotQueries)
	}
	if len(cat.gotQueries) != 2 {
		t.Fatalf("queries = %v, want exactly two attempts", cat.gotQueries)
	}
	if !strings.Contains(cat.gotQueries[0], "pavers") {
		t.Errorf("first attempt %q should be the whole phrase", cat.gotQueries[0])
	}
	if strings.Contains(cat.gotQueries[1], "pavers") {
		t.Errorf("second attempt %q should have dropped the trailing word", cat.gotQueries[1])
	}
}

// TestExtract_TwoWordMissIsNotRetried, a short phrase that matched nothing is a genuine miss, and
// shortening it to one word would start returning confident nonsense.
func TestExtract_TwoWordMissIsNotRetried(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	if _, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "bobcat rental"}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(cat.gotQueries) != 1 {
		t.Fatalf("queries = %v, want one attempt for a two-word phrase", cat.gotQueries)
	}
}

// TestExtract_PurposeClauseIsStrippedFromTheQueryButNotFromTheRow, "for the base" would pull the
// screenings into a 3/4 clear stone search; the rep still sees their own words.
func TestExtract_PurposeClauseIsStrippedFromTheQueryButNotFromTheRow(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: `12 tonnes of 3/4" clear stone for the base`})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(cat.gotQueries) == 0 || strings.Contains(strings.ToLower(cat.gotQueries[0]), "base") {
		t.Errorf("query %v still carries the purpose clause", cat.gotQueries)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(got.Proposals))
	}
	if !strings.Contains(got.Proposals[0].SourceText, "for the base") {
		t.Errorf("source_text = %q, the rep's own words must come back unedited", got.Proposals[0].SourceText)
	}
}

// -- 7 · alternates, refs and ordering -----------------------------------------------------------

// TestExtract_AlternatesLetAWrongFirstPickBeFixedInOneClick.
func TestExtract_AlternatesLetAWrongFirstPickBeFixedInOneClick(t *testing.T) {
	cat := &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "a", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", BaseUOM: "PC", SellUOM: "SF", MatchReason: "NAME"},
			{ProductID: "b", SKU: "DBS-TB-BLU60-STEP", Name: "Techo-Bloc Blu 60 Step", BaseUOM: "PC", SellUOM: "EA", MatchReason: "NAME"},
			{ProductID: "c", SKU: "DBS-TB-BLU60-COPE", Name: "Techo-Bloc Blu 60 Coping", BaseUOM: "PC", SellUOM: "LF", MatchReason: "NAME"},
			{ProductID: "d", SKU: "DBS-TB-BLU60-WALL", Name: "Techo-Bloc Blu 60 Wall", BaseUOM: "PC", SellUOM: "SF", MatchReason: "NAME"},
		},
		quotable: map[string]map[string]bool{"a": {"SF": true}},
	}
	svc := NewService(cat, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	p := got.Proposals[0]
	if p.ProductID != "a" {
		t.Fatalf("the pick is %q, want the top hit", p.ProductID)
	}
	if len(p.Alternates) != maxAlternates {
		t.Fatalf("alternates = %d, want %d, a row is a review list, not a menu", len(p.Alternates), maxAlternates)
	}
	for _, alt := range p.Alternates {
		if alt.ProductID == p.ProductID {
			t.Error("the pick must not also be offered as its own alternate")
		}
	}
}

// TestExtract_RefsAreStableAndPerRowSoConfirmingOneDoesNotConfirmTwo: the same product twice in one
// description is two rows with two refs.
func TestExtract_RefsAreStableAndPerRowSoConfirmingOneDoesNotConfirmTwo(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(),
		ExtractRequest{Text: "120 sq ft Blu 60 for the patio\n40 sq ft Blu 60 for the walk"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 2 {
		t.Fatalf("proposals = %d, want 2, the same product twice is two lines", len(got.Proposals))
	}
	if got.Proposals[0].Ref == got.Proposals[1].Ref {
		t.Fatal("two rows share a ref, confirming one would confirm both")
	}
	if got.Proposals[0].ProductID != got.Proposals[1].ProductID {
		t.Fatal("this test needs both rows to be the same product to mean anything")
	}
	if got.Proposals[0].QtyEntered != "120" || got.Proposals[1].QtyEntered != "40" {
		t.Errorf("quantities = %q/%q, want 120/40", got.Proposals[0].QtyEntered, got.Proposals[1].QtyEntered)
	}
}

// TestExtract_RowsStayInTheRepsOwnOrder, a list re-sorted by confidence stops corresponding to the
// paragraph above it, and checking it becomes a search rather than a read.
func TestExtract_RowsStayInTheRepsOwnOrder(t *testing.T) {
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(),
		ExtractRequest{Text: "Unilock Brussels Block\n480 sq ft DBS-TB-BLU60\n6 bags polymeric sand"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 3 {
		t.Fatalf("proposals = %d, want 3", len(got.Proposals))
	}
	wantSKUs := []string{"DBS-UL-BRUSSELS", "DBS-TB-BLU60", "DBS-GATOR-SAND"}
	for i, want := range wantSKUs {
		if got.Proposals[i].SKU != want {
			t.Errorf("row %d = %q, want %q, the rep's order is the order", i, got.Proposals[i].SKU, want)
		}
	}
}

// TestExtract_LongPasteIsCappedAndSaysSo, silently reading the first thirty of eighty items is the
// exact failure this feature must not have.
func TestExtract_LongPasteIsCappedAndSaysSo(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxPhrases+5; i++ {
		b.WriteString("480 sq ft Blu 60\n")
	}
	svc := NewService(seededCatalogue(), nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: b.String()})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != maxPhrases {
		t.Fatalf("proposals = %d, want the cap of %d", len(got.Proposals), maxPhrases)
	}
	found := false
	for _, u := range got.Unmatched {
		if u.SourceText == NoteTruncated {
			found = true
		}
	}
	if !found {
		t.Fatalf("the cap was applied silently, unmatched = %v", textsOf(got.Unmatched))
	}
}

// TestExtract_CatalogueFailureIsAnError, a broken catalogue read is NOT an empty answer. Empty
// means "nothing matched"; a failed read means nothing was looked at, and dressing that as an empty
// list would tell a rep the yard does not stock things it does.
func TestExtract_CatalogueFailureIsAnError(t *testing.T) {
	cat := seededCatalogue()
	cat.err = errBoomQA
	svc := NewService(cat, nil, nil, nil)
	if _, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft Blu 60"}); err == nil {
		t.Fatal("a failed catalogue read must not be reported as an empty result")
	}
}

// TestExtract_BranchTravelsIntoTheCatalogueRead, the on-hand figures are scoped to the quote's
// branch, and the assertion is on what the SEAM RECEIVED rather than on what came back.
func TestExtract_BranchTravelsIntoTheCatalogueRead(t *testing.T) {
	cat := seededCatalogue()
	svc := NewService(cat, nil, nil, nil)
	if _, err := svc.Extract(context.Background(), staffCaller(),
		ExtractRequest{Text: "480 sq ft Blu 60", BranchID: "branch-trenton"}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if cat.gotBranch != "branch-trenton" {
		t.Fatalf("branch reaching the catalogue = %q, want branch-trenton", cat.gotBranch)
	}
}

// -- helpers ------------------------------------------------------------------------------------

func refsOf(ps []Proposal) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Ref+":"+p.SKU)
	}
	return out
}

func textsOf(us []Unmatched) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.SourceText+" ("+u.Reason+")")
	}
	return out
}
