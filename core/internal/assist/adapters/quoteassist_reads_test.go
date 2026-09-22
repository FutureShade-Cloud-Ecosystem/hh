package adapters

// quoteassist_reads_test.go, the two READ seams DIB-66/67 need, asserted on what they HANDED THE
// DOMAIN rather than on what came back.
//
// The house rule from this package's own fakes_test.go: a test that checked the return value would
// pass just as happily against an adapter that asked the domain for everything and filtered nothing.
// So both fakes below record their inputs, and every scoping test names them.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// -- doubles ------------------------------------------------------------------------------------

type fakeCounter struct {
	hits []product.CounterHit
	err  error

	gotQ      string
	gotBranch string
	gotLimit  int
	calls     int

	// factors stands in for product_uoms: (productID → unit code → qty_in_base). Absent means the
	// catalogue keeps no factor, which is what product.ErrNoConversion says in production.
	factors        map[string]map[string]float64
	convertErr     error
	gotConversions []string
}

func (f *fakeCounter) CounterSearch(_ context.Context, q product.CounterSearchQuery) ([]product.CounterHit, error) {
	f.calls++
	f.gotQ, f.gotBranch, f.gotLimit = q.Q, q.BranchID, q.Limit
	return f.hits, f.err
}

func (f *fakeCounter) ConvertUOM(_ context.Context, productID, fromUOM string) (float64, error) {
	f.gotConversions = append(f.gotConversions, productID+":"+fromUOM)
	if f.convertErr != nil {
		return 0, f.convertErr
	}
	factor, ok := f.factors[productID][fromUOM]
	if !ok {
		return 0, product.ErrNoConversion
	}
	return factor, nil
}

type fakeQuoteDetail struct {
	view  quote.StaffQuoteView
	err   error
	gotID string
	calls int
}

func (f *fakeQuoteDetail) StaffQuoteRead(_ context.Context, id string) (quote.StaffQuoteView, error) {
	f.calls++
	f.gotID = id
	return f.view, f.err
}

type fakeProductAttrs struct {
	byID   map[string]product.Product
	err    error
	gotIDs []string
}

func (f *fakeProductAttrs) Get(_ context.Context, id string) (product.Product, error) {
	f.gotIDs = append(f.gotIDs, id)
	if f.err != nil {
		return product.Product{}, f.err
	}
	p, ok := f.byID[id]
	if !ok {
		return product.Product{}, product.ErrNotFound
	}
	return p, nil
}

// -- the catalogue seam -------------------------------------------------------------------------

// TestQuoteCatalogue_PassesTheTermAndBranchStraightThrough, the counter's ordering is the ordering,
// and this adapter adds nothing. The moment it starts having views, the argument for reusing the
// counter search instead of writing a second matcher collapses.
func TestQuoteCatalogue_PassesTheTermAndBranchStraightThrough(t *testing.T) {
	counter := &fakeCounter{hits: []product.CounterHit{
		{ProductID: "p1", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", BaseUOM: "PC", SoldBy: "SF", MatchReason: "NAME_PREFIX"},
		{ProductID: "p2", SKU: "DBS-TB-BOREALIS", Name: "Techo-Bloc Borealis Step", BaseUOM: "PC", MatchReason: "FUZZY"},
	}}
	seam := NewQuoteCatalogueSearch(counter)

	got, err := seam.SearchCatalogue(context.Background(), quoteassist.CatalogueQuery{
		Text: "Techo-Bloc Blu 60", BranchID: "branch-trenton", Limit: 3,
	})
	if err != nil {
		t.Fatalf("SearchCatalogue: %v", err)
	}
	if counter.gotQ != "Techo-Bloc Blu 60" {
		t.Errorf("term reaching the counter search = %q", counter.gotQ)
	}
	if counter.gotBranch != "branch-trenton" {
		t.Errorf("branch reaching the counter search = %q, the on-hand figures are branch-scoped", counter.gotBranch)
	}
	if counter.gotLimit != 3 {
		t.Errorf("limit = %d, want 3", counter.gotLimit)
	}
	if len(got) != 2 {
		t.Fatalf("hits = %d, want both, in the counter's own order", len(got))
	}
	if got[0].ProductID != "p1" || got[1].ProductID != "p2" {
		t.Errorf("the adapter re-ordered the counter's results: %+v", got)
	}
	if got[0].MatchReason != "NAME_PREFIX" {
		t.Errorf("match_reason = %q, a searcher who cannot see WHY a row is in the list distrusts the list", got[0].MatchReason)
	}
	// sold_by is carried through as SellUOM, and an ABSENT sold_by stays absent. Defaulting it to the
	// base unit here would print "PC" on a proposal for the 30 of 32 real products whose sold_by the
	// catalogue leaves blank, a unit nobody chose, shown as though somebody had.
	if got[0].SellUOM != "SF" {
		t.Errorf("sell uom = %q, want the catalogue's declared SF", got[0].SellUOM)
	}
	if got[1].SellUOM != "" {
		t.Errorf("sell uom = %q, want empty for a product with no sold_by", got[1].SellUOM)
	}
	if got[1].BaseUOM != "PC" {
		t.Errorf("base uom = %q", got[1].BaseUOM)
	}
}

// TestQuoteCatalogue_QuotableUnitAsksTheCatalogueAndThrowsTheFactorAway.
//
// The yard keeps one product_uoms row per (product, unit) with the factor to base. This seam asks only
// whether the row EXISTS: the number is read by this adapter and never returned, because the
// multiplication belongs to pricing and a second copy inside a text extractor would be a second
// answer to "how many pieces in 480 square feet".
func TestQuoteCatalogue_QuotableUnitAsksTheCatalogueAndThrowsTheFactorAway(t *testing.T) {
	counter := &fakeCounter{factors: map[string]map[string]float64{
		// The REAL numbers for these two products, from the seeded product_uoms rows.
		"p-antico": {"SF": 5.10, "PC": 1, "PLT": 50},
		"p-clear":  {"T": 1},
	}}
	seam := NewQuoteCatalogueSearch(counter)

	for _, tc := range []struct {
		product, unit string
		want          bool
	}{
		{"p-antico", "SF", true},
		{"p-antico", "PLT", true}, // pallets ARE quotable, the yard keeps a skid factor
		{"p-antico", "T", false},  // a paver is not sold by the tonne
		{"p-clear", "T", true},
		{"p-clear", "PLT", false}, // and a bulk aggregate is not sold by the pallet
	} {
		got, err := seam.QuotableUnit(context.Background(), tc.product, tc.unit)
		if err != nil {
			t.Fatalf("QuotableUnit(%s, %s): %v, no factor is an ANSWER, not a fault", tc.product, tc.unit, err)
		}
		if got != tc.want {
			t.Errorf("QuotableUnit(%s, %s) = %v, want %v", tc.product, tc.unit, got, tc.want)
		}
	}
	if len(counter.gotConversions) != 5 {
		t.Errorf("conversions asked = %v, want one per question", counter.gotConversions)
	}
}

// TestQuoteCatalogue_QuotableUnitDistinguishesNoFactorFromNoAnswer, "the catalogue said no" and "the
// catalogue could not be asked" must not look the same to a rep: the first tells them the yard does not
// sell it that way, the second leaves the unit for them to confirm.
func TestQuoteCatalogue_QuotableUnitDistinguishesNoFactorFromNoAnswer(t *testing.T) {
	noFactor := NewQuoteCatalogueSearch(&fakeCounter{factors: map[string]map[string]float64{}})
	got, err := noFactor.QuotableUnit(context.Background(), "p-1", "SF")
	if err != nil || got {
		t.Errorf("no factor: got=%v err=%v, want false with no error", got, err)
	}

	broken := NewQuoteCatalogueSearch(&fakeCounter{convertErr: errors.New("connection reset")})
	if _, err := broken.QuotableUnit(context.Background(), "p-1", "SF"); err == nil {
		t.Error("a broken catalogue read was reported as 'we do not sell it that way'")
	}
}

// TestQuoteCatalogue_QuotableUnitDoesNotQueryForABlankArgument, both are client bugs rather than
// questions, and neither is worth a round-trip to establish.
func TestQuoteCatalogue_QuotableUnitDoesNotQueryForABlankArgument(t *testing.T) {
	counter := &fakeCounter{}
	seam := NewQuoteCatalogueSearch(counter)
	for _, tc := range [][2]string{{"", "SF"}, {"p-1", ""}, {"", ""}} {
		if got, err := seam.QuotableUnit(context.Background(), tc[0], tc[1]); got || err != nil {
			t.Errorf("QuotableUnit(%q, %q) = %v/%v", tc[0], tc[1], got, err)
		}
	}
	if len(counter.gotConversions) != 0 {
		t.Errorf("blank arguments cost %d queries", len(counter.gotConversions))
	}
}

// TestQuoteCatalogue_NoHitsIsAnEmptySliceNotNil, "nothing matched" is an answer the extractor renders
// as a "couldn't match" row, and it must not arrive looking like an absence.
func TestQuoteCatalogue_NoHitsIsAnEmptySliceNotNil(t *testing.T) {
	seam := NewQuoteCatalogueSearch(&fakeCounter{})
	got, err := seam.SearchCatalogue(context.Background(), quoteassist.CatalogueQuery{Text: "bobcat"})
	if err != nil {
		t.Fatalf("SearchCatalogue: %v", err)
	}
	if got == nil {
		t.Fatal("nil slice for no hits")
	}
	if len(got) != 0 {
		t.Fatalf("hits = %d", len(got))
	}
}

// TestQuoteCatalogue_ErrorsAreCarriedNotSwallowed, a failed read is not an empty catalogue. Empty
// means "we don't stock that"; failed means nothing was looked at, and telling a rep the first when
// the second happened is how a quote goes out short a line.
func TestQuoteCatalogue_ErrorsAreCarriedNotSwallowed(t *testing.T) {
	seam := NewQuoteCatalogueSearch(&fakeCounter{err: errors.New("boom")})
	if _, err := seam.SearchCatalogue(context.Background(), quoteassist.CatalogueQuery{Text: "x"}); err == nil {
		t.Fatal("a failed catalogue read was reported as an empty result")
	}
}

// TestQuoteCatalogue_NilServiceIsANilSeam, quoteassist reads a nil catalogue as "not wired in this
// deployment" and answers an honest 503 while scope-gap detection keeps working.
func TestQuoteCatalogue_NilServiceIsANilSeam(t *testing.T) {
	if got := NewQuoteCatalogueSearch(nil); got != nil {
		t.Fatalf("NewQuoteCatalogueSearch(nil) = %+v, want nil", got)
	}
}

// -- the quote-lines seam -----------------------------------------------------------------------

// TestQuoteScopeLines_JoinsTheQuoteToTheCatalogueClassification.
func TestQuoteScopeLines_JoinsTheQuoteToTheCatalogueClassification(t *testing.T) {
	quotes := &fakeQuoteDetail{view: quote.StaffQuoteView{
		ID: "q-1",
		Lines: []quote.QuoteViewLine{
			{LineNo: 1, ProductID: "p-blu60", Description: "Techo-Bloc Blu 60 Slab"},
			{LineNo: 2, ProductID: "p-screen", Description: "Limestone Screenings"},
			{LineNo: 3, ProductID: "p-blu60", Description: "Techo-Bloc Blu 60 Slab (walkway)"},
		},
	}}
	prods := &fakeProductAttrs{byID: map[string]product.Product{
		"p-blu60":  {ID: "p-blu60", Application: "Patio"},
		"p-screen": {ID: "p-screen", Application: "Base"},
	}}
	seam := NewQuoteScopeLines(quotes, prods, nil)

	lines, found, err := seam.QuoteScopeLines(context.Background(), "q-1")
	if err != nil || !found {
		t.Fatalf("QuoteScopeLines: err=%v found=%v", err, found)
	}
	if quotes.gotID != "q-1" {
		t.Errorf("quote id reaching the quote module = %q", quotes.gotID)
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %+v, want 3", lines)
	}
	if lines[0].Application != "Patio" || lines[1].Application != "Base" || lines[2].Application != "Patio" {
		t.Errorf("applications = %+v", lines)
	}
	// The line's OWN description, not the catalogue's current name for the product: a finding that
	// names "line 3" has to name it the way the quote does or the rep goes looking for a line that
	// is not there.
	if lines[2].Name != "Techo-Bloc Blu 60 Slab (walkway)" {
		t.Errorf("name = %q, want the quote line's own description", lines[2].Name)
	}
	// DEDUPLICATED: the same product on two lines is one catalogue read.
	if len(prods.gotIDs) != 2 {
		t.Errorf("catalogue reads = %v, want one per DISTINCT product", prods.gotIDs)
	}
}

// TestQuoteScopeLines_NotFoundAndForbiddenAreOneAnswer.
//
// The quote module went to some trouble to make those indistinguishable (ErrForbidden carries no
// detail, and a cross-branch probe gets a flat 403). An adapter that separated them again would
// rebuild the existence oracle that care was taken to remove.
func TestQuoteScopeLines_NotFoundAndForbiddenAreOneAnswer(t *testing.T) {
	for _, err := range []error{quote.ErrNotFound, quote.ErrForbidden} {
		seam := NewQuoteScopeLines(&fakeQuoteDetail{err: err}, &fakeProductAttrs{}, nil)
		lines, found, gotErr := seam.QuoteScopeLines(context.Background(), "q-elsewhere")
		if gotErr != nil {
			t.Errorf("%v became an error: %v", err, gotErr)
		}
		if found {
			t.Errorf("%v reported found=true", err)
		}
		if len(lines) != 0 {
			t.Errorf("%v returned lines", err)
		}
	}
}

// TestQuoteScopeLines_AnUnclassifiableProductDegradesQuietly.
//
// The direction of this degradation is the load-bearing decision: an unreadable product means one
// line cannot trigger OR satisfy a rule, so a catalogue hiccup produces FEWER suggestions rather
// than a wrong one. The opposite (failing the whole read) would take the panel down; treating a
// blank as a wildcard would silence it.
func TestQuoteScopeLines_AnUnclassifiableProductDegradesQuietly(t *testing.T) {
	quotes := &fakeQuoteDetail{view: quote.StaffQuoteView{Lines: []quote.QuoteViewLine{
		{LineNo: 1, ProductID: "p-gone", Description: "a deleted product"},
		{LineNo: 2, ProductID: "p-blu60", Description: "Techo-Bloc Blu 60 Slab"},
	}}}
	prods := &fakeProductAttrs{byID: map[string]product.Product{"p-blu60": {Application: "Patio"}}}
	seam := NewQuoteScopeLines(quotes, prods, nil)

	lines, found, err := seam.QuoteScopeLines(context.Background(), "q-1")
	if err != nil || !found {
		t.Fatalf("a missing product broke the whole read: err=%v found=%v", err, found)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %+v, want both, a line the catalogue cannot classify is still a line", lines)
	}
	if lines[0].Application != "" {
		t.Errorf("application = %q, want empty for an unclassifiable product", lines[0].Application)
	}
	if lines[1].Application != "Patio" {
		t.Errorf("the classifiable line lost its application: %+v", lines[1])
	}
	// And the blank neither triggers nor satisfies, which is scope.go's own rule, asserted here so
	// the two halves of the degradation are proved together.
	if got := quoteassist.Evaluate([]quoteassist.ScopeRule{{
		Code: "R", Title: "t", Advice: "a", WhenApps: []string{"PATIO"}, ExpectApps: []string{"JOINTING"}, Enabled: true,
	}}, lines); len(got) != 1 {
		t.Errorf("findings = %d, want the patio rule to still fire", len(got))
	}
}

// TestQuoteScopeLines_ABlankProductIDIsNotLookedUp, a line with no product cannot be classified and
// must not cost a catalogue round-trip to find that out.
func TestQuoteScopeLines_ABlankProductIDIsNotLookedUp(t *testing.T) {
	quotes := &fakeQuoteDetail{view: quote.StaffQuoteView{Lines: []quote.QuoteViewLine{
		{LineNo: 1, ProductID: "", Description: "freight"},
	}}}
	prods := &fakeProductAttrs{byID: map[string]product.Product{}}
	seam := NewQuoteScopeLines(quotes, prods, nil)
	lines, _, err := seam.QuoteScopeLines(context.Background(), "q-1")
	if err != nil {
		t.Fatalf("QuoteScopeLines: %v", err)
	}
	if len(prods.gotIDs) != 0 {
		t.Errorf("a blank product id was looked up: %v", prods.gotIDs)
	}
	if len(lines) != 1 || lines[0].Application != "" {
		t.Errorf("lines = %+v", lines)
	}
}

// TestQuoteScopeLines_LineCountIsBounded, the cost of this read has a stated ceiling rather than an
// assumed one, and the excess is logged rather than silently dropped.
func TestQuoteScopeLines_LineCountIsBounded(t *testing.T) {
	view := quote.StaffQuoteView{}
	for i := 1; i <= maxScopeLines+10; i++ {
		view.Lines = append(view.Lines, quote.QuoteViewLine{LineNo: i, ProductID: "p-blu60", Description: "slab"})
	}
	seam := NewQuoteScopeLines(&fakeQuoteDetail{view: view},
		&fakeProductAttrs{byID: map[string]product.Product{"p-blu60": {Application: "Patio"}}}, nil)
	lines, _, err := seam.QuoteScopeLines(context.Background(), "q-1")
	if err != nil {
		t.Fatalf("QuoteScopeLines: %v", err)
	}
	if len(lines) != maxScopeLines {
		t.Fatalf("lines = %d, want the stated ceiling of %d", len(lines), maxScopeLines)
	}
}

// TestQuoteScopeLines_EitherHalfMissingIsANilSeam.
//
// A reader that could answer "which lines" but not "what for" would report a paved surface with no
// jointing sand on every quote in the business, every line unclassified, every rule unsatisfied.
// That is worse than the feature being off, so a half-wired seam is no seam.
func TestQuoteScopeLines_EitherHalfMissingIsANilSeam(t *testing.T) {
	if got := NewQuoteScopeLines(nil, &fakeProductAttrs{}, nil); got != nil {
		t.Error("a seam with no quote reader is not nil")
	}
	if got := NewQuoteScopeLines(&fakeQuoteDetail{}, nil, nil); got != nil {
		t.Error("a seam with no catalogue classifier is not nil")
	}
}

// TestQuoteScopeLines_UnexpectedQuoteErrorsAreCarried, a database failure is not "no such quote".
func TestQuoteScopeLines_UnexpectedQuoteErrorsAreCarried(t *testing.T) {
	seam := NewQuoteScopeLines(&fakeQuoteDetail{err: errors.New("connection reset")}, &fakeProductAttrs{}, nil)
	if _, _, err := seam.QuoteScopeLines(context.Background(), "q-1"); err == nil {
		t.Fatal("a database failure was reported as a missing quote")
	}
}

// -- the parked main.go paste, compiled here so it cannot be wrong there -------------------------

// TestQuoteAssist_ParkedWiringCompiles constructs the EXACT chain PARK-6667-main-wiring.patch tells
// somebody to paste into main.go, out of the SHIPPED types.
//
// WHY THIS IS A TEST AND NOT A PARAGRAPH. main.go is outside this lane's fence, so the wiring block
// is written in a document and pasted by someone else, in another commit, probably in a hurry. If a
// constructor's signature did not fit, a missing argument, a seam that takes a *Service where the
// document says Service, an adapter whose nil-handling makes the chain unbuildable, the failure
// would surface in THEIR commit rather than in this one, at the least convenient moment.
//
// So the paste is compiled here. Nothing is called: the services are typed nils, which is legal
// because this test is about the SHAPE of the wiring and every one of these constructors is
// documented to accept a service it will not use until a request arrives. If this test compiles, the
// paste compiles.
func TestQuoteAssist_ParkedWiringCompiles(t *testing.T) {
	var (
		productSvc     *product.Service
		quoteStaffSvc  *quote.StaffService
		assistProvider assist.Provider = &spyProvider{}
		assistRepo     *assist.Repository
	)

	svc := quoteassist.NewService(
		NewQuoteCatalogueSearch(productSvc),
		NewQuoteScopeLines(quoteStaffSvc, productSvc, nil),
		quoteassist.NewRepository(nil),
		nil,
	)
	svc.WithAI(NewQuoteExtractRails(assistProvider, assistRepo, assistRepo, nil))

	if !svc.AIWired() {
		t.Fatal("the parked wiring does not attach the AI seam, WithAI's argument shape has drifted from the patch")
	}
	// The handler takes the service and the two guard decorators, in that order.
	h := quoteassist.NewHandler(svc)
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)

	// And the four routes the patch documents are the four that got registered.
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/quote-assist/extract"},
		{http.MethodGet, "/api/v1/quote-assist/scope-gaps"},
		{http.MethodGet, "/api/v1/quote-assist/scope-rules"},
		{http.MethodPatch, "/api/v1/quote-assist/scope-rules/SOME_RULE"},
	} {
		req := httptest.NewRequest(r.method, r.path, nil)
		handler, pattern := mux.Handler(req)
		if pattern == "" || handler == nil {
			t.Errorf("%s %s is not registered, PARK-6667-main-wiring.patch documents it", r.method, r.path)
		}
	}
}
