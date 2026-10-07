package pos

// quickpicks.go, the counter's QUICK-PICK GRID: the handful of products this yard sells all day,
// as tap-tiles that add at standard price in one gesture.
//
// THE SET IS CONFIGURED, NOT CODED (D-105, the ratified hybrid). Which products appear is
// PRESENTATION CURATION, so it lives in the shared settings kit's generic substrate rather than in
// a POS-shaped table of its own: dealer defaults per role profile in surface_role_items, one
// person's own arrangement in surface_user_items (whole or absent, a stored set IS the grid, and
// reset is a DELETE). The kit's routes are already mounted by the quote pilot, so a dealer curates
// this grid through the same PUT every other surface uses and POS adds no settings endpoint.
//
// AN ITEM ID IS A PRODUCT SKU, lowercased. The kit validates SHAPE only and never keeps a list
// (the 0149/0164 ruling), so the "registry" behind these ids is the catalogue itself. A curated SKU
// that no longer exists renders nothing, silently, the same answer the kit already gives for an
// unknown id, and the reason a retired product cannot break a counter screen.
//
// AND IT IS USEFUL BEFORE ANYONE CURATES ANYTHING. With no curation on file the set is DERIVED
// from what this store actually sells, top sellers off committed POS sales, falling back to the
// catalogue's own most-stocked lines on a yard that has not rung anything yet. That is the
// TopSellingApplications idiom (rev 8 item 4) applied one level down, and it is why the grid is
// worth looking at on day one instead of being an empty frame waiting for an admin.
//
// PRICES ON THE TILES ARE THE CUSTOMER'S, and they come from counterprice.go, the same engine
// read the search results and the ticket use. No cost, no margin: a quick-pick tile is the
// customer's side of the counter.

import (
	"context"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// QuickPickSurface is the settings-kit surface id the grid is curated under. POS does not MOUNT
// the kit (the quote pilot already does, and two mounts would collide on the mux), it only reads
// the curation for its own surface.
const QuickPickSurface = "pos.counter"

// quickPickKind is the kit item kind a tile is filed under: a chip is the kit's word for "a small
// curated affordance in a row", which is exactly what a quick-pick tile is.
const quickPickKind = settings.KindChip

// maxQuickPicks bounds the SET. The flat grid this replaced capped at a dozen for a real reason -
// a tap-grid stops being faster than searching somewhere around twelve tiles, because past that the
// counter person is reading, not tapping. The DRILLABLE hot list did not repeal that reason, it
// moved it: the cap that matters is now tiles PER LEVEL (maxHotLevel), and a tree of three levels
// can carry more leaves than a single screen ever could while still showing a dozen at a time.
const maxQuickPicks = 60

// maxHotLevel is how many tiles one level of the drill may show, the old flat cap, now applied
// where it belongs. The frontend holds the same number for the same reason; it is stated here
// because the SET is trimmed to fit the tree the client asked for, not to fit one screen.
const maxHotLevel = 12

// QuickPick is one tile. It carries the product identity the add path needs and the price picture
// the tile shows, nothing else, because a tile that has to be read is not a tile.
type QuickPick struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	SizeLine  string `json:"size_line,omitempty"`
	// UOM is the sell unit a tap would add in (sold_by || base_uom), the same unit addProduct
	// picks, so the ticket's one-product-one-line merge key matches on the first tap.
	UOM      string `json:"uom"`
	HasImage bool   `json:"has_image"`
	// Category / Subcategory are the DRILL: the client's bulk -> mulch -> black, carried as the
	// catalogue's own application and collection facets. Sent flat, one pair per tile, because
	// which grouping reads well at a counter is a presentation decision, the frontend builds the
	// tree, and changing its shape must never mean migrating a table.
	//
	// An item with neither facet is not an error and not dropped: it lands in the tree's ungrouped
	// level, where it is still one tap away. A curated tile that vanished because a product had a
	// blank column would be the worst kind of silent configuration failure.
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	// Price is the CUSTOMER's price picture for one unit, retail, their price, the saving. Absent
	// when the waterfall could not price the product: a tile with no price is honest, a tile
	// showing $0.00 is a sale nobody wants to ring.
	Price *pricing.CounterQuote `json:"price,omitempty"`
}

// QuickPicks is the GET /api/v1/pos/quick-picks answer.
//
// Source names WHERE the set came from, "user" (this person curated it), "dealer" (their role
// profile's default), or "derived" (nobody has curated it yet, so these are the store's own top
// sellers). The counter shows the word nowhere; it exists so a support call can answer "why is
// Dave's grid different from mine" without a database session.
type QuickPicks struct {
	Source       string      `json:"source"`
	CustomerType string      `json:"customer_type"`
	Items        []QuickPick `json:"items"`
}

// SurfaceCuration is the settings kit's read, as POS needs it. Satisfied by *settings.Service.
// Declared here (rather than imported as a concrete type) so a unit test can answer a curation
// without a database, and so an unwired kit is a nil seam rather than a panic.
type SurfaceCuration interface {
	// The variadic tail is the settings kit's ConfigOpt list (session A widened the read so an
	// admin editor can ask for the profile it is EDITING rather than its own). POS asks for none of
	// them: the counter reads its own caller's curation, which is what it always did.
	Config(ctx context.Context, surfaceID, branchID string, opts ...settings.ConfigOpt) (settings.SurfaceConfig, error)
}

// WithSurfaceCuration overrides the curation seam (tests, and any future composition root that
// wants the read pointed elsewhere).
func (s *Service) WithSurfaceCuration(c SurfaceCuration) *Service { s.curation = c; return s }

// surfaceCuration resolves the curation reader. Like the counter price authority, it is BUILT FROM
// THE SERVICE'S OWN STORE rather than injected at cmd/*: the kit is a thin reader over the same
// pool, and the quote pilot established the pattern (builder.go settingsKitService). A scaffold
// store (no pool) simply has no curation, and no curation is the derived set, never an error.
func (s *Service) surfaceCuration() SurfaceCuration {
	if s.curation != nil {
		return s.curation
	}
	r, ok := s.store.(*Repository)
	if !ok || r.pool == nil {
		return nil
	}
	return settings.NewService(settings.NewStore(r.pool))
}

// QuickPickInput is the read's parameters. Everything is optional: a walk-in at the default branch
// asks with an empty struct and gets a perfectly good grid.
type QuickPickInput struct {
	BranchID   string
	CustomerID string
	Limit      int
}

// QuickPicks resolves the grid: whose set, which products, and what this customer pays for them.
func (s *Service) QuickPicks(ctx context.Context, in QuickPickInput) (QuickPicks, error) {
	limit := in.Limit
	if limit <= 0 || limit > maxQuickPicks {
		limit = maxQuickPicks
	}
	out := QuickPicks{Source: "derived", CustomerType: "RETAIL", Items: []QuickPick{}}

	skus, source := s.curatedQuickPickSKUs(ctx, in.BranchID, limit)
	out.Source = source
	var rows []quickPickRow
	var err error
	if len(skus) > 0 {
		rows, err = s.store.ReadProductsBySKU(ctx, skus)
		if err != nil {
			return out, err
		}
		rows = orderBySKU(rows, skus)
	}
	// No curation, or a curated set whose SKUs have all been retired: fall back to what the yard
	// actually sells. A grid that went empty because someone deleted a product is a broken screen,
	// not a configuration statement.
	if len(rows) == 0 {
		out.Source = "derived"
		rows, err = s.store.TopSellingProducts(ctx, limit)
		if err != nil {
			return out, err
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	rows = s.withEveryStockedCategory(ctx, rows, limit)
	for _, r := range rows {
		uom := r.SoldBy
		if uom == "" {
			uom = r.BaseUOM
		}
		out.Items = append(out.Items, QuickPick{
			ProductID: r.ProductID, SKU: r.SKU, Name: r.Name,
			SizeLine: r.SizeLine(), UOM: uom, HasImage: r.HasImage,
			Category: strings.TrimSpace(r.Application), Subcategory: strings.TrimSpace(r.Collection),
		})
	}
	s.priceQuickPicks(ctx, in, &out)
	return out, nil
}

// withEveryStockedCategory appends, after the rows already chosen (curated or derived, in their
// order), one in-stock representative for each category (application) the yard holds stock in that
// the rows do not already carry. A curated board shows what the dealer put on it, and a stocked
// category left off it (round 9: Pavers beside a board of mulch, aggregates and soils) was simply
// missing from the drill. The grid's cap still holds, and an unreadable read adds nothing: the
// chosen rows are never lost to it.
func (s *Service) withEveryStockedCategory(ctx context.Context, rows []quickPickRow, limit int) []quickPickRow {
	if len(rows) >= limit {
		return rows
	}
	reps, err := s.store.InStockApplicationRepresentatives(ctx)
	if err != nil || len(reps) == 0 {
		return rows
	}
	have := make(map[string]bool, len(rows))
	haveSKU := make(map[string]bool, len(rows))
	for _, r := range rows {
		have[strings.ToLower(strings.TrimSpace(r.Application))] = true
		haveSKU[strings.ToLower(r.SKU)] = true
	}
	for _, r := range reps {
		if len(rows) >= limit {
			break
		}
		key := strings.ToLower(strings.TrimSpace(r.Application))
		if key == "" || have[key] || haveSKU[strings.ToLower(r.SKU)] {
			continue
		}
		have[key] = true
		rows = append(rows, r)
	}
	return rows
}

// curatedQuickPickSKUs reads the kit for this surface and returns the curated SKUs in curated
// order, plus which level answered. The user's own set wins WHOLE (the 0140/0149 ruling): a
// person who curated a grid gets exactly that grid, and the dealer default is not merged into it.
func (s *Service) curatedQuickPickSKUs(ctx context.Context, branchID string, limit int) ([]string, string) {
	kit := s.surfaceCuration()
	if kit == nil {
		return nil, "derived"
	}
	cfg, err := kit.Config(ctx, QuickPickSurface, branchID)
	if err != nil {
		return nil, "derived" // an unreadable curation draws the derived grid, never a fault banner
	}
	if ids := visibleItemIDs(cfg.UserItems, limit); len(ids) > 0 {
		return ids, "user"
	}
	if ids := visibleItemIDs(cfg.RoleItems, limit); len(ids) > 0 {
		return ids, "dealer"
	}
	return nil, "derived"
}

// visibleItemIDs pulls the visible chip ids out of a kit item set, in position order. The kit
// already sorts by position; hidden rows are skipped because "hidden" is how a dealer removes a
// tile without forgetting which one it was.
func visibleItemIDs(items []settings.Item, limit int) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Kind != quickPickKind || !it.Visible {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(it.ID))
		if id == "" {
			continue
		}
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// orderBySKU puts the store's rows back into the CURATED order. A dealer who dragged a tile to
// the front meant it, and a database's idea of order is not that.
func orderBySKU(rows []quickPickRow, skus []string) []quickPickRow {
	bySKU := make(map[string]quickPickRow, len(rows))
	for _, r := range rows {
		bySKU[strings.ToLower(r.SKU)] = r
	}
	out := make([]quickPickRow, 0, len(rows))
	for _, s := range skus {
		if r, ok := bySKU[s]; ok {
			out = append(out, r)
		}
	}
	return out
}

// priceQuickPicks attaches the customer's price picture to every tile through the SAME read the
// search results use. Best-effort by design: a price authority that cannot answer leaves the tiles
// priceless and tappable, the add path re-prices authoritatively anyway, so a missing display
// price costs a glance, never a sale.
func (s *Service) priceQuickPicks(ctx context.Context, in QuickPickInput, out *QuickPicks) {
	if len(out.Items) == 0 {
		return
	}
	items := make([]CounterPriceItem, 0, len(out.Items))
	for _, it := range out.Items {
		items = append(items, CounterPriceItem{ProductID: it.ProductID, QtyEntered: 1, UOMEntered: it.UOM})
	}
	prices, err := s.CounterPrices(ctx, CounterPriceInput{
		BranchID: in.BranchID, CustomerID: in.CustomerID, Items: items,
	})
	if err != nil {
		return
	}
	out.CustomerType = prices.CustomerType
	byProduct := make(map[string]pricing.CounterQuote, len(prices.Prices))
	for _, p := range prices.Prices {
		byProduct[p.ProductID] = p
	}
	for i := range out.Items {
		p, ok := byProduct[out.Items[i].ProductID]
		if !ok || p.Unpriceable || p.CustomerCents <= 0 {
			continue // no price is no claim
		}
		q := p
		out.Items[i].Price = &q
	}
}
