package pos

// quickpicks_test.go, the quick-pick grid: whose set, in what order, and what it may claim.
// DB-free, on the service_test.go harness; exactly additive.
//
// The two shipped fakeStore methods below are STUBS ON PURPOSE. Adding the quick-pick reads to
// posStore means *fakeStore has to answer them for every existing test in this package to keep
// compiling; answering "nothing" is the honest default for a ring test that has never heard of a
// quick-pick grid. The tests that care about catalogue rows use quickPickStore, which embeds the
// shipped fake and overrides exactly those two answers, so service_test.go stays byte-identical.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func (f *fakeStore) ReadProductsBySKU(context.Context, []string) ([]quickPickRow, error) {
	return nil, nil
}
func (f *fakeStore) TopSellingProducts(context.Context, int) ([]quickPickRow, error) {
	return nil, nil
}
func (f *fakeStore) InStockApplicationRepresentatives(context.Context) ([]quickPickRow, error) {
	return nil, nil
}

// quickPickStore is the shipped fake with a catalogue behind it.
type quickPickStore struct {
	*fakeStore
	catalogue []quickPickRow // resolvable by SKU (case-insensitively)
	top       []quickPickRow // the derived set
	skuAsks   [][]string     // what ReadProductsBySKU was asked, in order
	topAsks   int
	skuErr    error
	perApp    []quickPickRow // one in-stock product per application (the category representatives)
}

func (q *quickPickStore) InStockApplicationRepresentatives(context.Context) ([]quickPickRow, error) {
	return q.perApp, nil
}

func (q *quickPickStore) ReadProductsBySKU(_ context.Context, skus []string) ([]quickPickRow, error) {
	q.skuAsks = append(q.skuAsks, skus)
	if q.skuErr != nil {
		return nil, q.skuErr
	}
	var out []quickPickRow
	for _, want := range skus {
		for _, row := range q.catalogue {
			if strings.EqualFold(row.SKU, want) {
				out = append(out, row)
			}
		}
	}
	return out, nil
}

func (q *quickPickStore) TopSellingProducts(_ context.Context, limit int) ([]quickPickRow, error) {
	q.topAsks++
	if limit < len(q.top) {
		return q.top[:limit], nil
	}
	return q.top, nil
}

// fakeCuration answers one canned settings-kit config (or refuses).
type fakeCuration struct {
	cfg     settings.SurfaceConfig
	err     error
	surface string
}

// The variadic tail matches the widened kit read. This fake asks for no options and asserts none:
// POS reads its own caller's curation, which is what it always did, and the widening must not have
// changed that.
func (f *fakeCuration) Config(_ context.Context, surfaceID, _ string, _ ...settings.ConfigOpt) (settings.SurfaceConfig, error) {
	f.surface = surfaceID
	return f.cfg, f.err
}

func chip(id string, pos int, visible bool) settings.Item {
	return settings.Item{Kind: settings.KindChip, ID: id, Position: pos, Visible: visible}
}

func row(sku, name string) quickPickRow {
	return quickPickRow{ProductID: "prod-" + sku, SKU: sku, Name: name, BaseUOM: "EA", SoldBy: "SF"}
}

// quickHarness wires a POS service over a catalogue, a curation and a price authority.
func quickHarness(t *testing.T, cat, top []quickPickRow, cfg *settings.SurfaceConfig) (*harness, *quickPickStore, *fakeQuoter) {
	t.Helper()
	h := newHarness()
	st := &quickPickStore{fakeStore: h.store, catalogue: cat, top: top}
	h.svc.store = st
	q := &fakeQuoter{}
	h.svc.WithCounterQuoter(q)
	if cfg != nil {
		h.svc.WithSurfaceCuration(&fakeCuration{cfg: *cfg})
	}
	return h, st, q
}

func skus(picks []QuickPick) []string {
	out := make([]string, 0, len(picks))
	for _, p := range picks {
		out = append(out, p.SKU)
	}
	return out
}

// -- with nothing curated, the grid is what the yard actually sells ------------------------------

func TestQuickPicks_UncuratedFallsBackToTopSellers(t *testing.T) {
	h, st, _ := quickHarness(t, nil, []quickPickRow{row("BLU-60", "Blu 60"), row("HOL-CS", "Holland")}, nil)
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{BranchID: "branch-trt"})
	if err != nil {
		t.Fatalf("an un-curated grid is not an error: %v", err)
	}
	if out.Source != "derived" {
		t.Fatalf("nobody curated anything, so the set is derived: %q", out.Source)
	}
	if got := skus(out.Items); len(got) != 2 || got[0] != "BLU-60" {
		t.Fatalf("the store's own top sellers, busiest first: %v", got)
	}
	if st.topAsks != 1 {
		t.Fatalf("the derived read runs exactly once: %d", st.topAsks)
	}
}

func TestQuickPicks_NoSalesAndNoCatalogueIsAnEmptyGridNotAnError(t *testing.T) {
	h, _, _ := quickHarness(t, nil, nil, nil)
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("a brand new yard draws an empty grid, calmly: %v", err)
	}
	if out.Items == nil {
		t.Fatalf("an empty LIST, never a null the client has to guard: %+v", out)
	}
}

// -- the dealer's default, and one person's own set ----------------------------------------------

func TestQuickPicks_DealerCurationWinsOverTheDerivedSet(t *testing.T) {
	cat := []quickPickRow{row("BLU-60", "Blu 60"), row("HOL-CS", "Holland"), row("SAND-HP", "HP sand")}
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		chip("sand-hp", 0, true), chip("blu-60", 1, true),
	}}
	h, st, _ := quickHarness(t, cat, []quickPickRow{row("HOL-CS", "Holland")}, &cfg)
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("quick picks: %v", err)
	}
	if out.Source != "dealer" {
		t.Fatalf("the role default answered: %q", out.Source)
	}
	if got := skus(out.Items); len(got) != 2 || got[0] != "SAND-HP" || got[1] != "BLU-60" {
		t.Fatalf("curated ORDER is the dealer's, not the database's: %v", got)
	}
	if st.topAsks != 0 {
		t.Fatalf("a curated grid does not also ask for top sellers: %d", st.topAsks)
	}
}

func TestQuickPicks_UserSetWinsWholeOverTheDealerDefault(t *testing.T) {
	cat := []quickPickRow{row("BLU-60", "Blu 60"), row("HOL-CS", "Holland"), row("SAND-HP", "HP sand")}
	cfg := settings.SurfaceConfig{
		RoleItems: []settings.Item{chip("sand-hp", 0, true), chip("blu-60", 1, true)},
		UserItems: []settings.Item{chip("hol-cs", 0, true)},
	}
	h, _, _ := quickHarness(t, cat, nil, &cfg)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Source != "user" {
		t.Fatalf("this person curated a grid: %q", out.Source)
	}
	if got := skus(out.Items); len(got) != 1 || got[0] != "HOL-CS" {
		t.Fatalf("the user's set wins WHOLE, the dealer default is not merged in: %v", got)
	}
}

func TestQuickPicks_HiddenAndUnknownItemsSimplyDoNotRender(t *testing.T) {
	cat := []quickPickRow{row("BLU-60", "Blu 60")}
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{
		chip("blu-60", 0, true),
		chip("hol-cs", 1, true),   // curated, then retired from the catalogue
		chip("sand-hp", 2, false), // curated, then hidden by the dealer
		{Kind: settings.KindColumn, ID: "blu-60", Position: 3, Visible: true}, // another kind entirely
	}}
	h, _, _ := quickHarness(t, cat, []quickPickRow{row("PAV-X", "Paver X")}, &cfg)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if got := skus(out.Items); len(got) != 1 || got[0] != "BLU-60" {
		t.Fatalf("an unknown id, a hidden row and a foreign kind all render nothing: %v", got)
	}
}

func TestQuickPicks_ACurationThatResolvesToNothingFallsBackRatherThanGoingBlank(t *testing.T) {
	// Every curated SKU has been retired. A grid that went empty because a product was deleted is
	// a broken screen, not a configuration statement.
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{chip("gone-1", 0, true), chip("gone-2", 1, true)}}
	h, st, _ := quickHarness(t, nil, []quickPickRow{row("BLU-60", "Blu 60")}, &cfg)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Source != "derived" || len(out.Items) != 1 {
		t.Fatalf("fall back to the derived set and say so: %+v", out)
	}
	if st.topAsks != 1 {
		t.Fatalf("the fallback read actually ran: %d", st.topAsks)
	}
}

func TestQuickPicks_AnUnreadableCurationDrawsTheDerivedGrid(t *testing.T) {
	h := newHarness()
	st := &quickPickStore{fakeStore: h.store, top: []quickPickRow{row("BLU-60", "Blu 60")}}
	h.svc.store = st
	h.svc.WithCounterQuoter(&fakeQuoter{})
	h.svc.WithSurfaceCuration(&fakeCuration{err: errors.New("settings: unreachable")})
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("a settings read that fails must not fault a counter screen: %v", err)
	}
	if out.Source != "derived" || len(out.Items) != 1 {
		t.Fatalf("the grid still opens on something real: %+v", out)
	}
}

func TestQuickPicks_AsksTheKitForThePosSurface(t *testing.T) {
	h := newHarness()
	h.svc.store = &quickPickStore{fakeStore: h.store}
	h.svc.WithCounterQuoter(&fakeQuoter{})
	fc := &fakeCuration{}
	h.svc.WithSurfaceCuration(fc)
	_, _ = h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if fc.surface != QuickPickSurface || QuickPickSurface != "pos.counter" {
		t.Fatalf("the counter's curation lives under its own surface id: %q", fc.surface)
	}
}

// -- the grid is bounded -------------------------------------------------------------------------

func TestQuickPicks_IsCappedAtAGridsWorth(t *testing.T) {
	var top []quickPickRow
	var items []settings.Item
	var cat []quickPickRow
	for i := 0; i < maxQuickPicks+8; i++ {
		sku := string(rune('a'+i%26)) + "-sku"
		top = append(top, row(sku, "P"))
		cat = append(cat, row(sku, "P"))
		items = append(items, chip(strings.ToLower(sku), i, true))
	}
	cfg := settings.SurfaceConfig{RoleItems: items}
	h, _, _ := quickHarness(t, cat, top, &cfg)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{Limit: 999})
	if len(out.Items) > maxQuickPicks {
		t.Fatalf("the curated set is bounded, whatever a caller asks for: %d", len(out.Items))
	}
}

// -- the DRILL: category -> subcategory -> item (the hot list) -----------------------------------

func TestQuickPicks_TilesCarryTheTwoFacetsTheHotListDrillsBy(t *testing.T) {
	// Ryan Rood's example is bulk -> mulch -> black, which is exactly the catalogue's own
	// application -> collection facets. They ride out flat, one pair per tile; the frontend builds
	// the tree, because how a counter reads a grouping is presentation and must not be a schema.
	mulch := quickPickRow{
		ProductID: "prod-MUL-BLK", SKU: "MUL-BLK", Name: "Black", BaseUOM: "YD", SoldBy: "YD",
		Application: "Bulk", Collection: "Mulch",
	}
	h, _, _ := quickHarness(t, nil, []quickPickRow{mulch}, nil)
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("want one tile, got %d", len(out.Items))
	}
	if out.Items[0].Category != "Bulk" || out.Items[0].Subcategory != "Mulch" {
		t.Fatalf("the drill facets must reach the tile: %+v", out.Items[0])
	}
}

func TestQuickPicks_ABlankFacetIsNotADroppedTile(t *testing.T) {
	// The worst kind of silent configuration failure: a dealer curates a product and it is simply
	// not on the screen, because a catalogue column happened to be empty. The tile ships with empty
	// facets and the frontend files it under its ungrouped level, still one tap from the ticket.
	bare := quickPickRow{ProductID: "prod-GIFT", SKU: "GIFT", Name: "Gift Card", BaseUOM: "EA"}
	h, _, _ := quickHarness(t, nil, []quickPickRow{bare}, nil)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if len(out.Items) != 1 {
		t.Fatalf("a product with no facets must still be a tile, got %d", len(out.Items))
	}
	if out.Items[0].Category != "" || out.Items[0].Subcategory != "" {
		t.Fatalf("a blank facet must stay blank, not be invented: %+v", out.Items[0])
	}
}

func TestQuickPicks_FacetsAreTrimmedSoWhitespaceIsNotACategory(t *testing.T) {
	// A catalogue with '  Bulk ' in a column would otherwise produce two groups that look identical
	// on screen and cannot both be tapped into.
	padded := quickPickRow{
		ProductID: "prod-P", SKU: "P", Name: "P", BaseUOM: "EA",
		Application: "  Bulk ", Collection: " Mulch  ",
	}
	h, _, _ := quickHarness(t, nil, []quickPickRow{padded}, nil)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Items[0].Category != "Bulk" || out.Items[0].Subcategory != "Mulch" {
		t.Fatalf("facets must be trimmed: %q / %q", out.Items[0].Category, out.Items[0].Subcategory)
	}
}

func TestQuickPicks_TheSetGrewForTheTreeButTheSCREENCapDidNot(t *testing.T) {
	// The flat grid capped at a dozen because a tap-grid stops being faster than searching past
	// twelve tiles. The drill did not repeal that, it moved it: a tree shows a dozen PER LEVEL, so
	// the SET may be larger while the screen never is. Both numbers are stated so a future reader
	// cannot mistake the larger cap for the old rule being abandoned.
	if maxHotLevel != 12 {
		t.Fatalf("the per-level cap is the rule that protects the counter: %d", maxHotLevel)
	}
	if maxQuickPicks <= maxHotLevel {
		t.Fatalf("a drillable set that cannot exceed one level is not a tree: %d", maxQuickPicks)
	}
	if maxQuickPicks > maxCounterPriceItems {
		t.Fatalf("every tile is priced in ONE counter-price read; the set must fit it: %d > %d",
			maxQuickPicks, maxCounterPriceItems)
	}
}

// -- the tiles carry the CUSTOMER's price, and never invent one ----------------------------------

func TestQuickPicks_TilesCarryTheCustomerPricePicture(t *testing.T) {
	h, _, q := quickHarness(t, nil, []quickPickRow{row("BLU-60", "Blu 60")}, nil)
	q.out = []pricing.CounterQuote{{
		ProductID: "prod-BLU-60", PriceUOM: "SF", RetailCents: 2200, CustomerCents: 1804,
		SavingCents: 396, SavingBps: 1800, Basis: pricing.BasisAccount,
	}}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{CustomerID: "cust-abc"})
	if len(out.Items) != 1 || out.Items[0].Price == nil {
		t.Fatalf("a priced tile shows the price: %+v", out.Items)
	}
	p := out.Items[0].Price
	if p.RetailCents != 2200 || p.CustomerCents != 1804 || p.SavingCents != 396 {
		t.Fatalf("retail, their price and the saving come from the engine verbatim: %+v", p)
	}
	if q.got[0].UOMEntered != "SF" {
		t.Fatalf("the tile is priced in the unit a tap would ADD in (sold_by): %+v", q.got[0])
	}
	if q.got[0].QtyEntered != 1 {
		t.Fatalf("a tile prices one unit: %+v", q.got[0])
	}
}

func TestQuickPicks_AnUnpriceableTileShowsNoPriceRatherThanZero(t *testing.T) {
	h, _, q := quickHarness(t, nil, []quickPickRow{row("BLU-60", "Blu 60")}, nil)
	q.out = []pricing.CounterQuote{{ProductID: "prod-BLU-60", Unpriceable: true}}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if len(out.Items) != 1 {
		t.Fatalf("the tile still renders, it is tappable and the add re-prices: %+v", out.Items)
	}
	if out.Items[0].Price != nil {
		t.Fatalf("no price is no claim, never $0.00: %+v", out.Items[0].Price)
	}
}

func TestQuickPicks_APriceAuthorityThatCannotAnswerLeavesTilesTappable(t *testing.T) {
	h, _, q := quickHarness(t, nil, []quickPickRow{row("BLU-60", "Blu 60")}, nil)
	q.err = errors.New("pricing: unreachable")
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("a display price is worth a glance, never a sale: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Price != nil {
		t.Fatalf("priceless but present: %+v", out.Items)
	}
}

// -- the tile is the customer's side of the counter ----------------------------------------------

func TestQuickPicks_CarryNoCostOrMarginBytes(t *testing.T) {
	b, err := json.Marshal(QuickPicks{Source: "dealer", CustomerType: "CONTRACTOR", Items: []QuickPick{{
		ProductID: "prod-1", SKU: "BLU-60", Name: "Blu 60", UOM: "SF",
		Price: &pricing.CounterQuote{RetailCents: 2200, CustomerCents: 1804, SavingCents: 396},
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"cost", "margin", "floor", "profit", "wac"} {
		if strings.Contains(strings.ToLower(string(b)), banned) {
			t.Fatalf("a quick-pick tile must not carry %q: %s", banned, b)
		}
	}
}

// -- the sell unit a tap adds in -----------------------------------------------------------------

func TestQuickPicks_UOMIsTheSellUnitTheAddPathWillUse(t *testing.T) {
	rows := []quickPickRow{
		{ProductID: "p1", SKU: "A", Name: "Sold by", BaseUOM: "EA", SoldBy: "PLT"},
		{ProductID: "p2", SKU: "B", Name: "No sold_by", BaseUOM: "T"},
	}
	h, _, _ := quickHarness(t, nil, rows, nil)
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Items[0].UOM != "PLT" {
		t.Fatalf("sold_by is the sell unit: %+v", out.Items[0])
	}
	if out.Items[1].UOM != "T" {
		t.Fatalf("base_uom is the fallback, the same fallback addProduct makes: %+v", out.Items[1])
	}
}

// -- the size line -------------------------------------------------------------------------------

func TestQuickPickRow_SizeLinePrefersThePackWordingThenDimensions(t *testing.T) {
	if got := (quickPickRow{ProductSize: "Pallet of 120", DimensionsLWH: "60x60x60"}).SizeLine(); got != "Pallet of 120" {
		t.Fatalf("the catalogue's own wording wins: %q", got)
	}
	if got := (quickPickRow{DimensionsLWH: "60x60x60"}).SizeLine(); got != "60x60x60" {
		t.Fatalf("dimensions are the fallback: %q", got)
	}
	if got := (quickPickRow{}).SizeLine(); got != "" {
		t.Fatalf("nothing to say says nothing, a blank second row reads as missing data: %q", got)
	}
}

// -- every category with stock shows on the grid (round 9) ---------------------------------------
//
// A curated board shows exactly what a dealer put on it, and the demo dealer's board is mulch,
// aggregates and soils: Pavers, which the yard stocks, had no tile at all. A category with stock
// now gets one representative tile after the curated ones, so no stocked category is missing from
// the drill. Curated tiles keep their order and win their own category.

func appRow(sku, name, application string) quickPickRow {
	r := row(sku, name)
	r.Application = application
	return r
}

func TestQuickPicks_AStockedCategoryTheCurationLeftOutGetsATileAfterTheCuratedOnes(t *testing.T) {
	cat := []quickPickRow{appRow("MUL-BLK", "Black Mulch", "Mulch"), appRow("SOIL-TOP", "Topsoil", "Soil")}
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{chip("mul-blk", 0, true), chip("soil-top", 1, true)}}
	h, st, _ := quickHarness(t, cat, nil, &cfg)
	st.perApp = []quickPickRow{
		appRow("MUL-RED", "Red Mulch", "Mulch"), // Mulch is already on the board: no second tile
		appRow("PAV-HOL", "Holland Paver", "Paver"),
		appRow("WALL-8", "Wall Block", "Wall"),
	}
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("quick picks: %v", err)
	}
	got := skus(out.Items)
	want := []string{"MUL-BLK", "SOIL-TOP", "PAV-HOL", "WALL-8"}
	if len(got) != len(want) {
		t.Fatalf("curated tiles first, then one per missing stocked category: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order is curated then the representatives: %v", got)
		}
	}
	if out.Source != "dealer" {
		t.Fatalf("the source still names the curation: %q", out.Source)
	}
}

func TestQuickPicks_TheCategoryMatchIgnoresCaseAndSpaces(t *testing.T) {
	cat := []quickPickRow{appRow("MUL-BLK", "Black Mulch", " mulch ")}
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{chip("mul-blk", 0, true)}}
	h, st, _ := quickHarness(t, cat, nil, &cfg)
	st.perApp = []quickPickRow{appRow("MUL-RED", "Red Mulch", "MULCH")}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if got := skus(out.Items); len(got) != 1 {
		t.Fatalf("the same category in another case is the same category: %v", got)
	}
}

func TestQuickPicks_TheRepresentativesNeverPushTheGridPastItsCap(t *testing.T) {
	var cat []quickPickRow
	var chips []settings.Item
	for i := 0; i < maxQuickPicks; i++ {
		sku := "SKU-" + strings.Repeat("x", i%5) + string(rune('A'+i%26)) + string(rune('a'+i/26))
		cat = append(cat, appRow(sku, "P", "Mulch"))
		chips = append(chips, chip(strings.ToLower(sku), i, true))
	}
	cfg := settings.SurfaceConfig{RoleItems: chips}
	h, st, _ := quickHarness(t, cat, nil, &cfg)
	st.perApp = []quickPickRow{appRow("PAV-HOL", "Holland Paver", "Paver")}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if len(out.Items) > maxQuickPicks {
		t.Fatalf("the cap holds: %d", len(out.Items))
	}
}

// -- the append respects whose grid it is (PR 253 review P2-1) -----------------------------------

func TestQuickPicks_ThePersonsOwnCuratedGridGetsNoRepresentatives(t *testing.T) {
	cat := []quickPickRow{appRow("MUL-BLK", "Black Mulch", "Mulch")}
	cfg := settings.SurfaceConfig{UserItems: []settings.Item{chip("mul-blk", 0, true)}}
	h, st, _ := quickHarness(t, cat, nil, &cfg)
	st.perApp = []quickPickRow{appRow("PAV-HOL", "Holland Paver", "Paver")}
	out, err := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if err != nil {
		t.Fatalf("quick picks: %v", err)
	}
	if out.Source != "user" {
		t.Fatalf("the person's set answered: %q", out.Source)
	}
	if got := skus(out.Items); len(got) != 1 || got[0] != "MUL-BLK" {
		t.Fatalf("a grid a person curated is exactly that grid: %v", got)
	}
}

func TestQuickPicks_ADerivedGridGetsTheMissingCategories(t *testing.T) {
	top := []quickPickRow{appRow("MUL-BLK", "Black Mulch", "Mulch")}
	h, st, _ := quickHarness(t, nil, top, nil)
	st.perApp = []quickPickRow{appRow("PAV-HOL", "Holland Paver", "Paver")}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Source != "derived" {
		t.Fatalf("nobody curated: %q", out.Source)
	}
	if got := skus(out.Items); len(got) != 2 || got[1] != "PAV-HOL" {
		t.Fatalf("the derived grid gains the stocked category it lacks: %v", got)
	}
}

func TestQuickPicks_ATileTheDealerHidSkipsTheAppendForThatProduct(t *testing.T) {
	cat := []quickPickRow{appRow("MUL-BLK", "Black Mulch", "Mulch")}
	// The dealer hid the Holland tile: "hidden" is how a dealer removes a tile, and it must not return.
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{chip("mul-blk", 0, true), chip("pav-hol", 1, false)}}
	h, st, _ := quickHarness(t, cat, nil, &cfg)
	st.perApp = []quickPickRow{
		appRow("PAV-HOL", "Holland Paver", "Paver"),
		appRow("WALL-8", "Wall Block", "Wall"),
	}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	got := skus(out.Items)
	for _, s := range got {
		if s == "PAV-HOL" {
			t.Fatalf("a tile the kit hides was added back: %v", got)
		}
	}
	if len(got) != 2 || got[1] != "WALL-8" {
		t.Fatalf("the other missing category still gets its tile: %v", got)
	}
}

func TestQuickPicks_AHiddenTileInTheUsersOwnSetOrTheDealersIsHiddenWhenDerived(t *testing.T) {
	// Every dealer row hidden: the grid is derived, and the hidden SKU still does not come back
	// through the representatives.
	cfg := settings.SurfaceConfig{RoleItems: []settings.Item{chip("pav-hol", 0, false)}}
	h, st, _ := quickHarness(t, nil, []quickPickRow{appRow("MUL-BLK", "Black Mulch", "Mulch")}, &cfg)
	st.perApp = []quickPickRow{appRow("PAV-HOL", "Holland Paver", "Paver")}
	out, _ := h.svc.QuickPicks(context.Background(), QuickPickInput{})
	if out.Source != "derived" {
		t.Fatalf("every dealer row hidden is a derived grid: %q", out.Source)
	}
	for _, s := range skus(out.Items) {
		if s == "PAV-HOL" {
			t.Fatalf("a hidden tile came back on a derived grid: %v", skus(out.Items))
		}
	}
}
