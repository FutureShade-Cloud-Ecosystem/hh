package quoteassist

// fakes_test.go, the doubles every unit test in this package runs against.
//
// THEY RECORD WHAT THEY WERE ASKED, and that is the point. The two claims this package makes about
// itself, "the product always comes from the catalogue" and "the AI is only ever asked to split
// text", are claims about what CROSSES A SEAM, and the only way to assert them from outside is to
// look at what the seam actually received. A test that checked a return value would pass just as
// happily against an extractor that ignored the catalogue and made the products up.

import (
	"context"
	"errors"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

var errBoomQA = errors.New("the catalogue fell over")

// -- catalogue ----------------------------------------------------------------------------------

// fakeCatalogue answers by SUBSTRING over a small stand-in catalogue whose rows are copied from the
// real seeded one (SKUs, names, units and applications all real). Copied rather than invented so a
// unit test's expectations and the integration test's expectations describe the same products.
type fakeCatalogue struct {
	rows []CatalogueHit
	err  error

	// gotQueries is every term the extractor searched for, in order. Named by the tests that assert
	// the two-attempt rule and the ones that assert a model's words never bypass the search.
	gotQueries []string
	gotBranch  string
	calls      int

	// quotable is the stand-in for product_uoms: (productID → set of unit codes the yard has a factor
	// for). Seeded from the REAL rows (SELECT sku, uom_code FROM product_uoms) so the unit tests and
	// the integration tests agree about which units a product can be quoted in.
	quotable map[string]map[string]bool
	// unitErr makes QuotableUnit fail, for the test that an unanswerable seam leaves the unit to the
	// rep rather than accepting or dropping it.
	unitErr error
	// gotUnitChecks records every (product, unit) pair asked about, the assertion that the CATALOGUE
	// decided, and not this package.
	gotUnitChecks []string
}

func (f *fakeCatalogue) QuotableUnit(_ context.Context, productID, uomCode string) (bool, error) {
	f.gotUnitChecks = append(f.gotUnitChecks, productID+":"+uomCode)
	if f.unitErr != nil {
		return false, f.unitErr
	}
	return f.quotable[productID][uomCode], nil
}

func (f *fakeCatalogue) SearchCatalogue(_ context.Context, q CatalogueQuery) ([]CatalogueHit, error) {
	f.calls++
	f.gotQueries = append(f.gotQueries, q.Text)
	f.gotBranch = q.BranchID
	if f.err != nil {
		return nil, f.err
	}
	term := strings.ToLower(strings.TrimSpace(q.Text))
	if term == "" {
		return []CatalogueHit{}, nil
	}
	out := []CatalogueHit{}
	for _, r := range f.rows {
		if matchesFake(r, term) {
			out = append(out, r)
		}
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// matchesFake is a deliberately CRUDE stand-in for the ranked search: every word of the term must
// appear in the row's sku+name. Crude on purpose, a fake that was as clever as pg_trgm would let a
// test pass because the fake was clever, and the ranked search itself is exercised for real in
// integration_test.go against the seeded catalogue.
func matchesFake(r CatalogueHit, term string) bool {
	hay := strings.ToLower(r.SKU + " " + r.Name)
	for _, w := range strings.Fields(term) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// seededCatalogue mirrors the real catalogue rows this lane's tests name, INCLUDING their real
// product_uoms rows.
//
// Note what the real data says and what an invented fixture would have got wrong: SellUOM is blank
// for every one of these products (sold_by is empty in 30 of 32 real rows), the pavers and slabs are
// quotable in SF, PC and PLT, and the bulk aggregates are quotable in T alone. So "480 sq ft of Blu
// 60" works because the yard keeps an SF factor, not because anything here assumed one.
func seededCatalogue() *fakeCatalogue {
	return &fakeCatalogue{
		rows: []CatalogueHit{
			{ProductID: "prod-blu60", SKU: "DBS-TB-BLU60", Name: "Techo-Bloc Blu 60 Slab", BaseUOM: "PC", MatchReason: "NAME"},
			{ProductID: "prod-brussels", SKU: "DBS-UL-BRUSSELS", Name: "Unilock Brussels Block", BaseUOM: "PC", SellUOM: "SF", MatchReason: "NAME"},
			{ProductID: "prod-clear", SKU: "DBS-AGG-34CLEAR", Name: "3/4\" Clear Stone", BaseUOM: "T", SellUOM: "T", MatchReason: "NAME"},
			{ProductID: "prod-gator", SKU: "DBS-GATOR-SAND", Name: "Alliance Gator Polymeric Sand", BaseUOM: "BAG", MatchReason: "NAME"},
			{ProductID: "prod-screen", SKU: "DBS-AGG-SCREEN", Name: "Limestone Screenings", BaseUOM: "T", MatchReason: "NAME"},
			{ProductID: "prod-minicreta", SKU: "DBS-TB-MINICRETA", Name: "Techo-Bloc Mini-Creta Wall", BaseUOM: "PC", MatchReason: "NAME"},
			{ProductID: "prod-step", SKU: "DBS-BANAS-STEP", Name: "Banas Natural Step", BaseUOM: "PC", MatchReason: "NAME"},
		},
		quotable: map[string]map[string]bool{
			"prod-blu60":     {"SF": true, "PC": true, "PLT": true},
			"prod-brussels":  {"SF": true, "PC": true, "PLT": true},
			"prod-clear":     {"T": true},
			"prod-gator":     {"BAG": true},
			"prod-screen":    {"T": true},
			"prod-minicreta": {"SF": true, "PC": true, "PLT": true},
			"prod-step":      {"PC": true, "PLT": true, "KG": true},
		},
	}
}

// -- the AI seam --------------------------------------------------------------------------------

// fakeAI is a provider-shaped double for the AIExtractor seam.
type fakeAI struct {
	seg  Segmentation
	err  error
	got  string
	call int
}

func (f *fakeAI) SegmentText(_ context.Context, _ assist.Caller, text string) (Segmentation, error) {
	f.call++
	f.got = text
	return f.seg, f.err
}

// -- quote lines + rules ------------------------------------------------------------------------

type fakeLines struct {
	lines   []ScopeLine
	found   bool
	err     error
	gotID   string
	callsIn int
}

func (f *fakeLines) QuoteScopeLines(_ context.Context, quoteID string) ([]ScopeLine, bool, error) {
	f.callsIn++
	f.gotID = quoteID
	return f.lines, f.found, f.err
}

type fakeRules struct {
	rules []ScopeRule
	err   error

	setCode    string
	setEnabled bool
	setActor   string
	setCalls   int
}

func (f *fakeRules) Rules(context.Context) ([]ScopeRule, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]ScopeRule(nil), f.rules...), nil
}

func (f *fakeRules) SetRuleEnabled(_ context.Context, code string, enabled bool, actorID string) (ScopeRule, error) {
	f.setCalls++
	f.setCode, f.setEnabled, f.setActor = code, enabled, actorID
	for i := range f.rules {
		if f.rules[i].Code == code {
			f.rules[i].Enabled = enabled
			return f.rules[i], nil
		}
	}
	return ScopeRule{}, ErrRuleNotFound
}

// seededRules are the migration's own defaults, in the shape the evaluator sees them. Kept in step
// with 0159 by TestIntegration_SeededRulesMatchTheCodesTheTestsName.
func seededRules() []ScopeRule {
	return []ScopeRule{
		{Code: "SURFACE_NEEDS_BASE", Title: "Granular base for the paved area", Advice: "This quote has a paved surface but no granular base.", WhenApps: []string{"PAVER", "PATIO"}, ExpectApps: []string{"BASE"}, SuggestQuery: "limestone screenings", Enabled: true, SortOrder: 10},
		{Code: "SURFACE_NEEDS_BEDDING", Title: "Bedding course under the units", Advice: "There is a paved surface here with no bedding layer.", WhenApps: []string{"PAVER", "PATIO"}, ExpectApps: []string{"BEDDING"}, SuggestQuery: "HPB bedding", Enabled: true, SortOrder: 20},
		{Code: "SURFACE_NEEDS_JOINTING", Title: "Jointing sand for the paved area", Advice: "A paved area is quoted with no jointing sand.", WhenApps: []string{"PAVER", "PATIO"}, ExpectApps: []string{"JOINTING"}, SuggestQuery: "polymeric sand", Enabled: true, SortOrder: 30},
		{Code: "SURFACE_NEEDS_EDGING", Title: "Edge restraint around the perimeter", Advice: "Nothing on this quote restrains the edge of the paved area.", WhenApps: []string{"PAVER", "PATIO"}, ExpectApps: []string{"EDGING", "RESTRAINT"}, SuggestQuery: "edge restraint", Enabled: true, SortOrder: 40},
		{Code: "WALL_NEEDS_BASE", Title: "Levelling pad under the wall", Advice: "A retaining wall is quoted with no base material.", WhenApps: []string{"WALL"}, ExpectApps: []string{"BASE"}, SuggestQuery: "limestone screenings", Enabled: true, SortOrder: 50},
		{Code: "WALL_NEEDS_DRAINAGE", Title: "Drainage stone behind the wall", Advice: "This wall has no drainage stone behind it.", WhenApps: []string{"WALL"}, ExpectApps: []string{"DRAINAGE"}, SuggestQuery: "3/4 clear stone", Enabled: true, SortOrder: 60},
		{Code: "CAPS_NEED_ADHESIVE", Title: "Adhesive for the caps and steps", Advice: "Caps or steps are quoted with no adhesive.", WhenApps: []string{"COPING", "STEP"}, ExpectApps: []string{"ADHESIVE"}, SuggestQuery: "concrete adhesive", Enabled: true, SortOrder: 70},
	}
}

// staffCaller is an ordinary frontline rep, the person this feature is for.
func staffCaller() assist.Caller {
	return assist.Caller{UserID: "user-rep", RoleCode: "SALES_REP", Tier: 3, BranchIDs: []string{"branch-trenton"}}
}

// findingCodes is the assertion shorthand every scope test uses: which rules fired, in order.
func findingCodes(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.RuleCode)
	}
	return out
}
