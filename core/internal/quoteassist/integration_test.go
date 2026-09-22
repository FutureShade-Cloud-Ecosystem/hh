//go:build integration

// integration_test.go, DIB-66/67 against the REAL migrated Postgres and the REAL seeded catalogue.
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/quoteassist/
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE RATHER THAN AGAINST A FAKE
// ---------------------------------------------------------------------------------------
//  1. THE EXTRACTOR RESOLVES REAL PRODUCTS. The unit tests run against a crude substring fake, so
//     they can show that the extractor asks the catalogue and trusts nothing else, they cannot show
//     that "3/4 clear stone" actually finds DBS-AGG-34CLEAR through pg_trgm and the synonym table.
//     That is a property of SQL, and only a row can test it.
//  2. EXTRACTION ADDS NO LINES. quote_lines is counted before and after a real extraction over a real
//     quote. This is the ticket's central claim and the one a fake cannot say anything about, because
//     a fake has no table to write to.
//  3. THE SEEDED RULES ARE THE RULES. Migration 0159's rows are read through the real repository and
//     compared against the codes the unit tests name, so a seed that drifts from the tests fails
//     here rather than in a demo.
//  4. `enabled` IS A SWITCH IN THE DATABASE. The row is flipped through the real UPDATE and the
//     finding disappears; flipped back and it returns.
//
// FIXTURES CLEAN UP AFTER THEMSELVES. Everything is created under a per-run prefix and removed in
// reverse dependency order in t.Cleanup, so this file runs against a database that also holds the demo
// seed (and does) without either polluting the other.

package quoteassist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping quoteassist integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// -- the catalogue seam, over the real ranked search --------------------------------------------
//
// This file cannot import internal/product (that would put a domain import in this package's test
// binary and make the "imports nothing from another domain" claim untestable by grep). So the seam is
// satisfied here by the SQL the counter search is built on, the same relevance arms, narrowed to the
// three this extractor's terms actually exercise, and the ADAPTER over the real product.Service is
// integration-tested in internal/assist/adapters/quoteassist_integration_test.go.
//
// That split is deliberate: THIS file proves the extractor against the real catalogue DATA, and that
// file proves the adapter against the real catalogue CODE.
type sqlCatalogue struct{ pool *pgxpool.Pool }

func (s sqlCatalogue) SearchCatalogue(ctx context.Context, q CatalogueQuery) ([]CatalogueHit, error) {
	term := strings.TrimSpace(q.Text)
	if len(term) < 2 {
		return []CatalogueHit{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.sku, p.name, p.base_uom, p.sold_by,
		       CASE
		         WHEN lower(p.sku::text) = lower($1)                     THEN 'EXACT'
		         WHEN lower(p.sku::text) LIKE lower($1) || '%'           THEN 'SKU_PREFIX'
		         WHEN lower(p.name) LIKE lower($1) || '%'                THEN 'NAME_PREFIX'
		         WHEN lower(p.name) LIKE '%' || lower($1) || '%'         THEN 'NAME'
		         WHEN lower(p.description) LIKE '%' || lower($1) || '%'  THEN 'DESCRIPTION'
		         ELSE 'FUZZY'
		       END AS match_reason,
		       similarity(p.sku || ' ' || p.name || ' ' || p.manufacturer || ' ' || p.collection, $1) AS sim
		  FROM products p
		 WHERE p.is_active
		   AND (
		         lower(p.sku::text) = lower($1)
		      OR lower(p.sku::text) LIKE lower($1) || '%'
		      OR lower(p.name) LIKE '%' || lower($1) || '%'
		      OR lower(p.description) LIKE '%' || lower($1) || '%'
		      OR similarity(p.sku || ' ' || p.name || ' ' || p.manufacturer || ' ' || p.collection, $1) > 0.22
		       )
		 ORDER BY CASE
		            WHEN lower(p.sku::text) = lower($1)           THEN 0
		            WHEN lower(p.sku::text) LIKE lower($1) || '%' THEN 1
		            WHEN lower(p.name) LIKE lower($1) || '%'      THEN 2
		            WHEN lower(p.name) LIKE '%' || lower($1) || '%' THEN 3
		            WHEN lower(p.description) LIKE '%' || lower($1) || '%' THEN 4
		            ELSE 6
		          END,
		          sim DESC, p.name
		 LIMIT $2`, term, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CatalogueHit, 0, limit)
	for rows.Next() {
		var h CatalogueHit
		var sim float64
		if err := rows.Scan(&h.ProductID, &h.SKU, &h.Name, &h.BaseUOM, &h.SellUOM, &h.MatchReason, &sim); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// QuotableUnit over the REAL product_uoms rows, the yard's own answer to "can this product be quoted
// in this unit". One row per (product, unit) with the factor to base; this asks only whether the row
// exists, because the factor belongs to pricing (units.go header).
func (s sqlCatalogue) QuotableUnit(ctx context.Context, productID, uomCode string) (bool, error) {
	if strings.TrimSpace(productID) == "" || strings.TrimSpace(uomCode) == "" {
		return false, nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM product_uoms WHERE product_id = $1 AND uom_code = $2)`,
		productID, uomCode).Scan(&ok)
	return ok, err
}

// -- 1 · the extractor against the real catalogue -----------------------------------------------

// TestIntegration_ExtractResolvesTheRealSeededCatalogue.
//
// The paragraph is the walkthrough's own, and every expectation names a SKU that exists in
// backend/cmd/seed/dibbits_seed.sql. Nothing here is invented: if a future catalogue import renames
// "3/4\" Clear Stone" this test tells us, which is precisely the coupling a text extractor should
// have to the data it extracts against.
func TestIntegration_ExtractResolvesTheRealSeededCatalogue(t *testing.T) {
	pool := testPool(t)
	svc := NewService(sqlCatalogue{pool}, nil, nil, nil)

	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: realJobDescription})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	t.Logf("proposals:")
	for _, p := range got.Proposals {
		t.Logf("  %s  %-20s %-44s %6s %-4s %-6s  %q", p.Ref, p.SKU, p.Name, p.QtyEntered, p.UOMEntered, p.Confidence, p.SourceText)
	}
	t.Logf("unmatched:")
	for _, u := range got.Unmatched {
		t.Logf("  %q, %s", u.SourceText, u.Reason)
	}

	bySKU := map[string]Proposal{}
	for _, p := range got.Proposals {
		bySKU[p.SKU] = p
	}
	// The items a real yard would expect to land, with the quantity and unit read from the text and
	// checked against the yard's OWN product_uoms rows.
	want := []struct {
		sku, qty, uom string
	}{
		{"DBS-TB-BLU60", "480", "SF"},
		{"DBS-AGG-34CLEAR", "12", "T"},
		{"DBS-GATOR-SAND", "6", "BAG"},
	}
	for _, w := range want {
		p, ok := bySKU[w.sku]
		if !ok {
			t.Errorf("%s did not resolve from the real catalogue", w.sku)
			continue
		}
		if p.QtyEntered != w.qty {
			t.Errorf("%s qty = %q, want %q", w.sku, p.QtyEntered, w.qty)
		}
		if p.UOMEntered != w.uom {
			t.Errorf("%s uom = %q, want %q", w.sku, p.UOMEntered, w.uom)
		}
	}
	// -- A REAL DATA GAP, SURFACED RATHER THAN PAPERED OVER --
	//
	// "40 linear feet of Banas coping" resolves the product and CANNOT resolve the unit, because the
	// seeded catalogue has no LF row in product_uoms for DBS-BANAS-COPING, only PC and PLT, even
	// though products.price_uom says LF. Three seeded products have that mismatch
	// (DBS-BANAS-COPING, DBS-BANAS-STEP, DBS-TB-BOREALIS: see NOTES-FIX-6667.md, park 6).
	//
	// The honest answer is the one asserted here: the quantity is read, the unit is left for the rep,
	// and the note names both units so somebody can go and fix the catalogue. The dishonest answer
	// would have been to trust products.price_uom, quote 40 LF, and let pricing fail, or worse,
	// silently price it per piece.
	if p, ok := bySKU["DBS-BANAS-COPING"]; ok {
		if p.QtyEntered != "40" {
			t.Errorf("coping qty = %q, want 40, the quantity is read whatever the unit does", p.QtyEntered)
		}
		if p.UOMEntered != "" || !p.NeedsUOM {
			t.Errorf("coping uom = %q needs_uom = %v, the catalogue has no LF factor for it", p.UOMEntered, p.NeedsUOM)
		}
		if !strings.Contains(p.UnitNote, "LF") {
			t.Errorf("coping unit_note = %q, want it to name the unit that could not be used", p.UnitNote)
		}
	} else {
		t.Error("DBS-BANAS-COPING did not resolve")
	}

	// The pallet line lands as PLT because the REAL product_uoms table carries a skid factor for
	// Mini-Creta. That is the correction TestExtract_PalletsAreQuotableBecauseTheCatalogueKeepsASkid-
	// Factor records: the yard sells pallets, and the extractor's job is to say what the rep said, not
	// to have an opinion about which units exist. Nothing here multiplied by the factor.
	if p, ok := bySKU["DBS-TB-MINICRETA"]; ok {
		if p.UOMEntered != "PLT" || p.QtyEntered != "2" {
			t.Errorf("the pallets line = %s %s, want 2 PLT from the real product_uoms row", p.QtyEntered, p.UOMEntered)
		}
	} else {
		t.Error("DBS-TB-MINICRETA did not resolve")
	}
	// And the bobcat is honestly reported rather than matched to something plausible.
	bobcat := false
	for _, u := range got.Unmatched {
		if strings.Contains(strings.ToLower(u.SourceText), "bobcat") {
			bobcat = true
		}
	}
	if bobcat == false {
		for _, p := range got.Proposals {
			if strings.Contains(strings.ToLower(p.SourceText), "bobcat") {
				t.Fatalf("the bobcat was matched to %s (%s), the yard does not rent equipment", p.SKU, p.Name)
			}
		}
		t.Error("the bobcat line vanished, it must appear as a couldn't-match row")
	}
}

// TestIntegration_ExtractFindsAProductByItsRealSKU, the EXACT arm, and the one shape that earns HIGH.
func TestIntegration_ExtractFindsAProductByItsRealSKU(t *testing.T) {
	pool := testPool(t)
	svc := NewService(sqlCatalogue{pool}, nil, nil, nil)
	got, err := svc.Extract(context.Background(), staffCaller(), ExtractRequest{Text: "480 sq ft DBS-TB-BLU60"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) != 1 {
		t.Fatalf("proposals = %d: %+v / %+v", len(got.Proposals), got.Proposals, got.Unmatched)
	}
	p := got.Proposals[0]
	if p.SKU != "DBS-TB-BLU60" {
		t.Fatalf("sku = %q", p.SKU)
	}
	if p.MatchReason != "EXACT" {
		t.Errorf("match_reason = %q, want EXACT for a SKU typed in full", p.MatchReason)
	}
	if p.Confidence != ConfidenceHigh {
		t.Errorf("confidence = %q, want HIGH (why: %v)", p.Confidence, p.Why)
	}
	if p.UOMEntered != "SF" {
		t.Errorf("uom = %q, want SF, the catalogue keeps an SF factor for this product", p.UOMEntered)
	}
}

// -- 2 · extraction writes nothing --------------------------------------------------------------

// TestIntegration_Extract_AddsNoLines is the strongest form of the proposal-only invariant: a real
// quote, a real extraction over it, and quote_lines counted on both sides.
//
// Under mutation M1, the handler adds confirmed proposals itself, this test fails by name with a
// row count. Nothing else in the suite can produce that failure, because nothing else has a table.
func TestIntegration_Extract_AddsNoLines(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	w := newQuoteFixture(t, pool)

	before := countLines(t, pool, w.quoteID)
	if before != 0 {
		t.Fatalf("the fixture quote starts with %d lines, this test needs a clean one", before)
	}

	svc := NewService(sqlCatalogue{pool}, nil, nil, nil)
	got, err := svc.Extract(ctx, staffCaller(), ExtractRequest{Text: realJobDescription, BranchID: w.branchID})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got.Proposals) < 4 {
		t.Fatalf("proposals = %d, this test needs a real extraction to mean anything", len(got.Proposals))
	}

	after := countLines(t, pool, w.quoteID)
	if after != 0 {
		t.Fatalf("extraction put %d lines on the quote, a proposal is NOT a line", after)
	}
	// And the quote's money is untouched, which is the other half of "nothing was written".
	var subtotal, total int64
	if err := pool.QueryRow(ctx, `SELECT subtotal_cents, total_cents FROM quotes WHERE id = $1`, w.quoteID).
		Scan(&subtotal, &total); err != nil {
		t.Fatalf("read quote totals: %v", err)
	}
	if subtotal != 0 || total != 0 {
		t.Fatalf("extraction moved money: subtotal=%d total=%d", subtotal, total)
	}
}

// -- 3 · the seeded rules ARE the rules ---------------------------------------------------------

// TestIntegration_SeededRulesMatchTheCodesTheTestsName keeps migration 0159 and the unit tests in
// step. A seed that drifts from the tests fails here rather than in a demo.
func TestIntegration_SeededRulesMatchTheCodesTheTestsName(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	rules, err := repo.Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	got := map[string]ScopeRule{}
	for _, r := range rules {
		got[r.Code] = r
	}
	for _, want := range seededRules() {
		have, ok := got[want.Code]
		if !ok {
			t.Errorf("%s is named by the unit tests and is not in the database", want.Code)
			continue
		}
		if strings.Join(have.WhenApps, ",") != strings.Join(want.WhenApps, ",") {
			t.Errorf("%s when_apps = %v, tests assume %v", want.Code, have.WhenApps, want.WhenApps)
		}
		if strings.Join(have.ExpectApps, ",") != strings.Join(want.ExpectApps, ",") {
			t.Errorf("%s expect_apps = %v, tests assume %v", want.Code, have.ExpectApps, want.ExpectApps)
		}
		if have.SortOrder != want.SortOrder {
			t.Errorf("%s sort_order = %d, tests assume %d", want.Code, have.SortOrder, want.SortOrder)
		}
		if have.SuggestQuery != want.SuggestQuery {
			t.Errorf("%s suggest_query = %q, tests assume %q", want.Code, have.SuggestQuery, want.SuggestQuery)
		}
		if strings.TrimSpace(have.Advice) == "" {
			t.Errorf("%s ships with no advice, the panel would render an empty suggestion", want.Code)
		}
		if !have.Enabled {
			t.Errorf("%s ships disabled, the seeded defaults are meant to work out of the box", want.Code)
		}
	}
	if len(rules) != len(seededRules()) {
		t.Errorf("the database holds %d rules and the tests name %d", len(rules), len(seededRules()))
	}
}

// TestIntegration_ScopeGapsOverARealPaverQuote wires the evaluator to real quote lines and real
// products, through the real rule table.
func TestIntegration_ScopeGapsOverARealPaverQuote(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	w := newQuoteFixture(t, pool)
	// Two paved lines and nothing else, the shape the ticket describes.
	w.addLine(t, 1, "DBS-TB-BLU60", "Techo-Bloc Blu 60 Slab", 480)
	w.addLine(t, 2, "DBS-UL-BRUSSELS", "Unilock Brussels Block", 120)

	svc := NewService(nil, sqlScopeLines{pool}, NewRepository(pool), nil)
	got, found, err := svc.ScopeGaps(ctx, w.quoteID)
	if err != nil || !found {
		t.Fatalf("ScopeGaps: err=%v found=%v", err, found)
	}
	codes := findingCodes(got.Findings)
	t.Logf("findings: %v (lines=%d rules=%d)", codes, got.LinesChecked, got.RulesEvaluated)
	for _, want := range []string{"SURFACE_NEEDS_BASE", "SURFACE_NEEDS_BEDDING", "SURFACE_NEEDS_JOINTING", "SURFACE_NEEDS_EDGING"} {
		if !contains(codes, want) {
			t.Errorf("%s did not fire on a bare paver quote: %v", want, codes)
		}
	}
	if contains(codes, "WALL_NEEDS_DRAINAGE") {
		t.Errorf("a wall rule fired on a paver quote: %v", codes)
	}

	// Add the real polymeric sand and the jointing suggestion goes away, through real rows.
	w.addLine(t, 3, "DBS-GATOR-SAND", "Alliance Gator Polymeric Sand", 6)
	got2, _, err := svc.ScopeGaps(ctx, w.quoteID)
	if err != nil {
		t.Fatalf("ScopeGaps: %v", err)
	}
	if contains(findingCodes(got2.Findings), "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("the jointing suggestion survived the jointing sand: %v", findingCodes(got2.Findings))
	}
}

// TestIntegration_DisablingARuleInTheDatabaseSilencesIt is the config claim end to end: the real
// UPDATE, the real read, the real evaluator.
//
// The rule is restored in t.Cleanup because this database also carries the demo seed and a test that
// left a rule switched off would quietly change what the next demo shows.
func TestIntegration_DisablingARuleInTheDatabaseSilencesIt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	w := newQuoteFixture(t, pool)
	w.addLine(t, 1, "DBS-TB-BLU60", "Techo-Bloc Blu 60 Slab", 480)

	repo := NewRepository(pool)
	svc := NewService(nil, sqlScopeLines{pool}, repo, nil)

	before, _, err := svc.ScopeGaps(ctx, w.quoteID)
	if err != nil {
		t.Fatalf("ScopeGaps: %v", err)
	}
	if !contains(findingCodes(before.Findings), "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("the jointing rule did not fire to begin with: %v", findingCodes(before.Findings))
	}

	t.Cleanup(func() {
		if _, err := repo.SetRuleEnabled(context.Background(), "SURFACE_NEEDS_JOINTING", true, ""); err != nil {
			t.Errorf("could not restore SURFACE_NEEDS_JOINTING: %v", err)
		}
	})
	rule, err := repo.SetRuleEnabled(ctx, "SURFACE_NEEDS_JOINTING", false, "")
	if err != nil {
		t.Fatalf("SetRuleEnabled: %v", err)
	}
	if rule.Enabled {
		t.Fatal("the RETURNING row still reads enabled")
	}

	after, _, err := svc.ScopeGaps(ctx, w.quoteID)
	if err != nil {
		t.Fatalf("ScopeGaps: %v", err)
	}
	if contains(findingCodes(after.Findings), "SURFACE_NEEDS_JOINTING") {
		t.Fatalf("a rule disabled IN THE DATABASE still produced a suggestion: %v", findingCodes(after.Findings))
	}
	if len(after.Findings) != len(before.Findings)-1 {
		t.Errorf("findings went %d → %d; disabling one rule changed more than one", len(before.Findings), len(after.Findings))
	}
	if after.RulesEvaluated != before.RulesEvaluated-1 {
		t.Errorf("rules_evaluated went %d → %d", before.RulesEvaluated, after.RulesEvaluated)
	}

	// And the listing still shows it, because an admin cannot switch back on what they cannot see.
	listing, err := svc.Rules(ctx)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	seen := false
	for _, r := range listing {
		if r.Code == "SURFACE_NEEDS_JOINTING" {
			seen = true
			if r.Enabled {
				t.Error("the listing reports the disabled rule as enabled")
			}
		}
	}
	if !seen {
		t.Fatal("a disabled rule disappeared from the admin listing")
	}
}

// TestIntegration_SetRuleEnabledOnAnUnknownCodeIsNotFound.
func TestIntegration_SetRuleEnabledOnAnUnknownCodeIsNotFound(t *testing.T) {
	repo := NewRepository(testPool(t))
	if _, err := repo.SetRuleEnabled(context.Background(), "NO_SUCH_RULE", true, ""); err != ErrRuleNotFound {
		t.Fatalf("err = %v, want ErrRuleNotFound", err)
	}
}

// -- the walkthrough's fixtures, produced from the REAL catalogue -------------------------------

// TestFixture_WriteRealCatalogueBodies writes the bytes the Chromium harness replays for the two
// routes main.go has not been wired for yet (PARK 1).
//
// IT RUNS AGAINST THE REAL SEEDED CATALOGUE, and that is the point. A screenshot taken against a
// hand-written fixture, or against the unit tests' substring fake, would show a rep six products
// that agree with the frontend because the same person wrote both. These rows came out of pg_trgm and
// product_uoms, which is why the walkthrough shows a coping line with no unit (the LF factor is
// genuinely missing from the seed) rather than a tidy list of six perfect matches.
//
// A TEST, not a script, so the fixtures cannot drift from a passing suite. When
// QUOTEASSIST_FIXTURE_DIR is unset, CI, and every ordinary run, nothing is written.
//
//	QUOTEASSIST_FIXTURE_DIR=/abs/path DATABASE_URL=... go test -tags integration ./internal/quoteassist/ -run Fixture
func TestFixture_WriteRealCatalogueBodies(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	extractSvc := NewService(sqlCatalogue{pool}, nil, nil, nil)
	extraction, err := extractSvc.Extract(ctx, staffCaller(),
		ExtractRequest{Text: realJobDescription, BranchID: ""})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(extraction.Proposals) < 4 {
		t.Fatalf("only %d proposals, the fixture would show an empty screen", len(extraction.Proposals))
	}
	if len(extraction.Unmatched) == 0 {
		t.Fatal("no unmatched rows, the walkthrough's couldn't-match shot needs one")
	}

	// THE SUGGESTIONS ARE COMPUTED FOR THE QUOTE THE WALKTHROUGH ACTUALLY OPENS.
	//
	// An earlier version of this fixture built a throw-away paver quote, which produced findings that
	// named lines the rep on screen could not see ("because of line 2, Unilock Brussels Block" on a
	// quote with no Brussels Block). A suggestion panel whose reasons do not match the quote in front
	// of it is exactly the thing that makes a rep stop trusting it, so the fixture now answers for the
	// FIRST seeded quote that has lines, which is the one the walkthrough clicks.
	var seededQuoteID string
	if err := pool.QueryRow(ctx, `
		SELECT q.id FROM quotes q JOIN quote_lines ql ON ql.quote_id = q.id
		 GROUP BY q.id, q.quote_no ORDER BY q.quote_no LIMIT 1`).Scan(&seededQuoteID); err != nil {
		t.Fatalf("no seeded quote with lines: %v", err)
	}
	repo := NewRepository(pool)
	gapsSvc := NewService(nil, sqlScopeLines{pool}, repo, nil)
	gaps, found, err := gapsSvc.ScopeGaps(ctx, seededQuoteID)
	if err != nil || !found {
		t.Fatalf("ScopeGaps: err=%v found=%v", err, found)
	}
	if len(gaps.Findings) == 0 {
		t.Fatal("no findings on a bare paver quote, the suggestion shot needs some")
	}
	rules, err := gapsSvc.Rules(ctx)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}

	dir := os.Getenv("QUOTEASSIST_FIXTURE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		t.Logf("wrote %s (%d bytes)", name, len(b))
	}
	write("extract.json", extraction)
	write("scope-gaps.json", gaps)
	// The rules surface wraps its array, exactly as the handler does.
	write("scope-rules.json", map[string]any{"rules": rules})
	if err := os.WriteFile(filepath.Join(dir, "job-description.txt"), []byte(realJobDescription), 0o644); err != nil {
		t.Fatalf("write description: %v", err)
	}
	// The quote these fixtures were produced against, so a reader can check that the findings on
	// screen are that quote's own answer.
	if err := os.WriteFile(filepath.Join(dir, "quote.txt"), []byte(seededQuoteID), 0o644); err != nil {
		t.Fatalf("write quote id: %v", err)
	}
}

// -- the fixture --------------------------------------------------------------------------------

// sqlScopeLines satisfies QuoteLineReader over the real tables, for the same reason sqlCatalogue
// exists: this package's test binary must not import internal/quote either. The real ADAPTER over
// quote.StaffQuoteRead is integration-tested next door.
type sqlScopeLines struct{ pool *pgxpool.Pool }

func (s sqlScopeLines) QuoteScopeLines(ctx context.Context, quoteID string) ([]ScopeLine, bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quotes WHERE id = $1)`, quoteID).Scan(&exists); err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ql.line_no, ql.description, COALESCE(p.application, '')
		  FROM quote_lines ql
		  LEFT JOIN products p ON p.id = ql.product_id
		 WHERE ql.quote_id = $1
		 ORDER BY ql.line_no`, quoteID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []ScopeLine{}
	for rows.Next() {
		var l ScopeLine
		if err := rows.Scan(&l.LineNo, &l.Name, &l.Application); err != nil {
			return nil, false, err
		}
		out = append(out, l)
	}
	return out, true, rows.Err()
}

type quoteFixture struct {
	pool                *pgxpool.Pool
	quoteID, branchID   string
	customerID, quoteNo string
}

// newQuoteFixture creates one DRAFT quote against real seeded branch/customer rows and removes it in
// reverse dependency order afterwards.
func newQuoteFixture(t *testing.T, pool *pgxpool.Pool) *quoteFixture {
	t.Helper()
	ctx := context.Background()
	w := &quoteFixture{pool: pool}
	tag := fmt.Sprintf("QA%d", time.Now().UnixNano()%1_000_000_000)

	// The branch a quote belongs to is a `locations` row of type BRANCH, the FK on quotes.branch_id
	// points at locations(id), and this fixture proves reach the way the app does rather than
	// inventing an id.
	if err := pool.QueryRow(ctx, `SELECT id FROM locations WHERE is_active AND loc_type = 'BRANCH' ORDER BY code LIMIT 1`).
		Scan(&w.branchID); err != nil {
		t.Fatalf("no seeded branch location: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM customers ORDER BY created_at LIMIT 1`).Scan(&w.customerID); err != nil {
		t.Fatalf("no seeded customer: %v", err)
	}
	w.quoteNo = "Q-" + tag
	err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, 'DRAFT', now() + interval '30 days', 0, 0, 0)
		RETURNING id`, w.quoteNo, w.customerID, w.branchID).Scan(&w.quoteID)
	if err != nil {
		t.Fatalf("insert quote: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		if _, err := pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, w.quoteID); err != nil {
			t.Logf("cleanup quote_lines: %v", err)
		}
		if _, err := pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, w.quoteID); err != nil {
			t.Logf("cleanup quote: %v", err)
		}
	})
	return w
}

// addLine writes one quote line directly. DIRECTLY, and only in this test file: the point of the
// fixture is to have lines to EVALUATE, and going through the audited write path would drag pricing,
// stock holds and an identity context into a test about scope rules. The shipped write path is
// exercised where it belongs, quote's own integration tests, and the Chromium walkthrough, which
// confirms proposals through the real POST.
func (w *quoteFixture) addLine(t *testing.T, lineNo int, sku, description string, qty float64) {
	t.Helper()
	ctx := context.Background()
	var productID, baseUOM string
	if err := w.pool.QueryRow(ctx, `SELECT id, base_uom FROM products WHERE sku = $1`, sku).Scan(&productID, &baseUOM); err != nil {
		t.Fatalf("seeded product %s: %v", sku, err)
	}
	_, err := w.pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, $2, $3, $4, $5, $6, $5, 0, $6, 0)`,
		w.quoteID, lineNo, productID, description, qty, baseUOM)
	if err != nil {
		t.Fatalf("insert quote line %d: %v", lineNo, err)
	}
}

func countLines(t *testing.T, pool *pgxpool.Pool, quoteID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM quote_lines WHERE quote_id = $1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count quote_lines: %v", err)
	}
	return n
}
