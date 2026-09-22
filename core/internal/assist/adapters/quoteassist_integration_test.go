//go:build integration

// quoteassist_integration_test.go, the two DIB-66/67 read adapters over the SHIPPED domain services
// on a REAL migrated Postgres.
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/assist/adapters/
//
// WHY THIS FILE EXISTS ALONGSIDE internal/quoteassist/integration_test.go, WHICH ALSO USES A DATABASE
// -------------------------------------------------------------------------------------------------
// The two prove different halves and neither can prove the other's:
//
//	quoteassist/integration_test.go   the ENGINE against the real catalogue DATA. It satisfies the
//	                                 seams with SQL of its own, because that package's test binary
//	                                 must not import a domain (that is the claim being kept).
//	THIS FILE                        the ADAPTERS against the real catalogue CODE, product.Service's
//	                                 own CounterSearch, with its own relevance arms, its own synonym
//	                                 table and its own limit clamping. If the seam did not fit the
//	                                 shipped service, or if the mapping dropped a field, only this
//	                                 file would notice.
//
// The load-bearing test is TestIntegration_QuoteExtract_EndToEndOverTheShippedCatalogue: a real
// paragraph, through the real ranked search, producing real SKUs. It is the closest thing to the
// walkthrough that exists in Go.

package adapters

import (
	"context"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quoteassist"
	"github.com/jackc/pgx/v5/pgxpool"
)

// shippedCatalogue builds the SAME product.Service main.go constructs, not a test variant. That is
// what makes this a test of the wiring and not only of the SQL.
func shippedCatalogue(t *testing.T, pool *pgxpool.Pool) *product.Service {
	t.Helper()
	// The same two lines main.go runs (cmd/server/main.go:171 and :354), minus the pricing anchor
	// this seam does not read: the constructor plus the branch-stock enrichment the counter search
	// attaches on-hand through. Wired because an UNwired one would quietly change the shape of what
	// comes back, and this test is about the real shape.
	svc := product.NewService(product.NewRepository(pool))
	svc.WithBranchStock(inventory.NewRepository(pool))
	return svc
}

// TestIntegration_QuoteExtract_EndToEndOverTheShippedCatalogue.
func TestIntegration_QuoteExtract_EndToEndOverTheShippedCatalogue(t *testing.T) {
	pool := qaPool(t)
	seam := NewQuoteCatalogueSearch(shippedCatalogue(t, pool))
	if seam == nil {
		t.Fatal("the catalogue seam is nil over a real service")
	}
	svc := quoteassist.NewService(seam, nil, nil, nil)

	const description = `Need 480 sq ft of Techo-Bloc Blu 60 for the back patio, 12 tonnes of 3/4 clear stone for the base,
6 bags of polymeric sand and 2 pallets of Mini-Creta wall block. Also a bobcat for the day.`

	got, err := svc.Extract(context.Background(),
		qaCaller(), quoteassist.ExtractRequest{Text: description})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, p := range got.Proposals {
		t.Logf("  %s  %-20s %-44s %6s %-4s %-6s (%s)", p.Ref, p.SKU, p.Name, p.QtyEntered, p.UOMEntered, p.Confidence, p.MatchReason)
	}
	for _, u := range got.Unmatched {
		t.Logf("  couldn't match: %q, %s", u.SourceText, u.Reason)
	}

	bySKU := map[string]quoteassist.Proposal{}
	for _, p := range got.Proposals {
		bySKU[p.SKU] = p
	}
	for _, want := range []string{"DBS-TB-BLU60", "DBS-AGG-34CLEAR", "DBS-GATOR-SAND", "DBS-TB-MINICRETA"} {
		if _, ok := bySKU[want]; !ok {
			t.Errorf("%s did not resolve through the SHIPPED counter search", want)
		}
	}
	// Units read from the text, against the units the real catalogue declares.
	if p := bySKU["DBS-TB-BLU60"]; p.UOMEntered != "SF" || p.QtyEntered != "480" {
		t.Errorf("Blu 60 = %s %s, want 480 SF", p.QtyEntered, p.UOMEntered)
	}
	if p := bySKU["DBS-AGG-34CLEAR"]; p.UOMEntered != "T" || p.QtyEntered != "12" {
		t.Errorf("clear stone = %s %s, want 12 T", p.QtyEntered, p.UOMEntered)
	}
	// And the pallets line lands as PLT, because the SHIPPED product.Service reports a real
	// product_uoms row for it. Asserted through the real code path because this is exactly the
	// question an invented fixture would have got wrong: an earlier version of this extractor decided
	// for itself that pallets were unquotable, and the real catalogue disagreed.
	//
	// The quantity is still 2. Nothing here multiplied by the skid factor, pricing owns that.
	if p := bySKU["DBS-TB-MINICRETA"]; p.UOMEntered != "PLT" || p.QtyEntered != "2" {
		t.Errorf("Mini-Creta = %s %s, want 2 PLT from the real product_uoms row", p.QtyEntered, p.UOMEntered)
	}
	if len(got.Unmatched) == 0 {
		t.Error("the bobcat vanished, an unmatched phrase must survive the real search too")
	}
}

// TestIntegration_QuoteExtract_MatchReasonComesFromTheRealRanker, the chip a rep reads ("matched:
// alias") is the counter search's own verdict, carried through untouched. A mapping that dropped it
// would leave every row unexplained, which is the DIB-450 finding.
func TestIntegration_QuoteExtract_MatchReasonComesFromTheRealRanker(t *testing.T) {
	pool := qaPool(t)
	seam := NewQuoteCatalogueSearch(shippedCatalogue(t, pool))

	hits, err := seam.SearchCatalogue(context.Background(), quoteassist.CatalogueQuery{Text: "DBS-TB-BLU60", Limit: 3})
	if err != nil {
		t.Fatalf("SearchCatalogue: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("a real SKU found nothing through the shipped search")
	}
	if hits[0].MatchReason != "EXACT" {
		t.Errorf("match_reason = %q, want the ranker's own EXACT for a SKU typed in full", hits[0].MatchReason)
	}
	if hits[0].SKU != "DBS-TB-BLU60" {
		t.Errorf("sku = %q", hits[0].SKU)
	}
	if hits[0].BaseUOM == "" {
		t.Error("base uom did not survive the mapping")
	}
	// sold_by is blank for DBS-TB-BLU60 in the real seed, and it comes through blank, the mapping
	// does not invent a unit. The unit a proposal ends up in comes from product_uoms, asked for
	// per unit, which is what the next assertion checks.
	quotable, err := seam.QuotableUnit(context.Background(), hits[0].ProductID, "SF")
	if err != nil {
		t.Fatalf("QuotableUnit: %v", err)
	}
	if !quotable {
		t.Error("the real product_uoms row for Blu 60 in SF was not found, the yard does keep one")
	}
	tonnes, err := seam.QuotableUnit(context.Background(), hits[0].ProductID, "T")
	if err != nil {
		t.Fatalf("QuotableUnit: %v", err)
	}
	if tonnes {
		t.Error("a slab reported as quotable by the tonne")
	}
}

// TestIntegration_QuoteExtract_AShortTermSearchesNothing, the counter search's own two-character
// floor, reached through the seam. An extractor that could trigger a full catalogue scan per stray
// character would be the most expensive query in the app.
func TestIntegration_QuoteExtract_AShortTermSearchesNothing(t *testing.T) {
	pool := qaPool(t)
	seam := NewQuoteCatalogueSearch(shippedCatalogue(t, pool))
	got, err := seam.SearchCatalogue(context.Background(), quoteassist.CatalogueQuery{Text: "a"})
	if err != nil {
		t.Fatalf("SearchCatalogue: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a one-character term returned %d hits", len(got))
	}
}

// -- the harness --------------------------------------------------------------------------------

func qaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping quoteassist adapter integration tests")
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

// qaCaller is an ordinary frontline rep. The extract path only reads the caller for the AI rails,
// which are deliberately NOT wired in these tests, the deterministic engine is the default and the
// default is what this file measures.
func qaCaller() assist.Caller {
	return assist.Caller{UserID: "user-rep", RoleCode: "SALES_REP", Tier: 3}
}
