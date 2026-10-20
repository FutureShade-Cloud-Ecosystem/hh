package export

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// catalogue.go, DIB-441's consumer: the product catalogue as a native .xlsx.
//
// IT READS THE SAME SERVICES THE SCREEN READS. The catalogue page makes exactly two calls for the
// data in its table: GET /api/v1/products (product.Service.List, which already carries the DIB-404
// per-branch on-hand and the DIB-437 list price WITH the anchor's own price UOM) and
// GET /api/v1/inventory/browse (inventory.Service.Browse, the DIB-439 batched four-bucket stock
// read that supplies "available"). This exporter calls those two services, through the narrow
// interfaces below, with the same filters. It does NOT re-derive any of it from SQL: a second copy of
// "what is available" is a second definition of available, and the file would eventually disagree
// with the screen it claims to be a copy of.
//
// WHY CONSUMER-DECLARED INTERFACES rather than the concrete services. The export module must be
// mountable in a test with two fakes, and it must not be the thing that decides how a product is
// read. *product.Service and *inventory.Service satisfy these as written, so the wiring is one line
// and no adapter type exists to drift.

// ProductLister is the catalogue list read, *product.Service.
//
// The enrichment matters as much as the rows: List attaches OnHandByBranch (DIB-404) and
// ListPriceCents/ListPriceUOM (DIB-437) when the service has its stock and price seams bound. Unwired,
// those fields are absent and the export renders blanks, the same degradation the screen shows, not
// an error.
type ProductLister interface {
	List(ctx context.Context, f product.Filter) ([]product.Product, error)
}

// StockBrowser is the DIB-439 batched stock read, *inventory.Service.
type StockBrowser interface {
	Browse(ctx context.Context, f inventory.BrowseFilter) (inventory.BrowseResult, error)
}

// stockReadLimit mirrors the catalogue component's STOCK_READ_LIMIT: the browse read clamps its own
// limit to 200, so asking for exactly that is asking for everything it will give. A row the browse
// read did not return has no position, and its Available cell is blank.
const stockReadLimit = 200

// CatalogueQuery is the export's filter set, the same facets the screen's filter bar carries, plus
// the column it is sorted by, so the file and the screen agree row for row.
type CatalogueQuery struct {
	Filter product.Filter
	// Sort is a column key ("" = the API's own name order) and Desc its direction. The catalogue
	// sorts client-side (DIB-436), so the component passes what the user clicked.
	Sort SortKey
	Desc bool
	// FilterSummary is the human rendering of the applied filters ("Manufacturer: Techo-Bloc ·
	// Colour: Charcoal"), stamped onto the sheet. A filtered extract with no provenance becomes "the
	// catalogue" to whoever finds it in a shared drive.
	FilterSummary string
}

// SortKey names a sortable catalogue column. The set mirrors the screen's clickable headers exactly
// (_catalogue.ts SortKey), a key the screen cannot produce is not a key this exporter honours.
type SortKey string

const (
	SortName         SortKey = "name"
	SortManufacturer SortKey = "manufacturer"
	SortColour       SortKey = "color"
	SortApplication  SortKey = "application"
	SortOnHand       SortKey = "on_hand"
	SortAvailable    SortKey = "available"
	SortListPrice    SortKey = "list_price"
)

// ParseSortKey accepts a sort key from the wire. An unknown key is not an error, it is "no sort",
// because a bad query parameter should not deny somebody their export.
func ParseSortKey(raw string) SortKey {
	switch SortKey(strings.TrimSpace(raw)) {
	case SortName:
		return SortName
	case SortManufacturer:
		return SortManufacturer
	case SortColour:
		return SortColour
	case SortApplication:
		return SortApplication
	case SortOnHand:
		return SortOnHand
	case SortAvailable:
		return SortAvailable
	case SortListPrice:
		return SortListPrice
	}
	return ""
}

// CatalogueService builds the catalogue workbook.
type CatalogueService struct {
	products ProductLister
	stock    StockBrowser
	log      *slog.Logger
	now      func() time.Time
}

// NewCatalogueService binds the two reads. Either may be nil: a nil product read is a hard error
// (there is nothing to export), a nil stock read simply leaves Available blank, exactly what the
// screen does when the staff-guarded browse call fails.
func NewCatalogueService(products ProductLister, stock StockBrowser, log *slog.Logger) *CatalogueService {
	if log == nil {
		log = slog.Default()
	}
	return &CatalogueService{products: products, stock: stock, log: log, now: time.Now}
}

// WithClock pins the clock (tests, and any future "as at" export).
func (s *CatalogueService) WithClock(now func() time.Time) *CatalogueService {
	if now != nil {
		s.now = now
	}
	return s
}

// catalogueColumns are the sheet's columns, in order, with their widths.
//
// THEY ARE THE SCREEN'S COLUMNS post-DIB-435..439, product, manufacturer, colour, application, on
// hand, available, list price, with two departures, both deliberate and both already house doctrine
// (_catalogue.ts csvHeaders):
//
//	SKU gets its own column. On screen it lives UNDER the product name in the same cell; in a
//	spreadsheet a two-line cell is not sortable, filterable or VLOOKUP-able, and the SKU is the join
//	key every downstream use of this file needs.
//
//	The UNITS are their own columns rather than glued to the numbers. "800 PC" is a string to a
//	spreadsheet and 800 is a number, and the entire reason to ship a real .xlsx instead of a CSV is
//	that the number sums. One Qty UOM column serves both quantity columns because both are counted in
//	the same stocking unit; the price carries its own unit because the anchor's UOM (DIB-437) is a
//	different fact from the stocking one and the two disagree in real data.
var catalogueColumns = []Column{
	{Header: "SKU", Width: 20},
	{Header: "Product", Width: 38},
	{Header: "Manufacturer", Width: 20},
	{Header: "Colour", Width: 18},
	{Header: "Application", Width: 18},
	{Header: "On hand", Width: 12},
	{Header: "Available", Width: 12},
	{Header: "Qty UOM", Width: 10},
	{Header: "List price", Width: 14},
	{Header: "Price UOM", Width: 11},
}

// Workbook runs the two reads and renders the sheet.
func (s *CatalogueService) Workbook(ctx context.Context, q CatalogueQuery) (*Workbook, error) {
	if s.products == nil {
		return nil, fmt.Errorf("export: catalogue product read is not wired")
	}
	rows, err := s.products.List(ctx, q.Filter)
	if err != nil {
		return nil, err
	}
	buckets := s.readStock(ctx, q.Filter)
	return s.build(rows, buckets, q), nil
}

// OPEN BY DESIGN (UX-89): the available quantity at a named branch is the stock glance's own figure, the
// same one the browse read answers to any staff caller with no branch wall. The export adds nothing to
// it, so it is listed open beside the glance rather than fenced alone; the day the glance takes a wall,
// this read takes the same one.
//
// readStock runs the batched four-bucket read and indexes it by product id.
//
// A FAILURE HERE IS NOT AN EXPORT FAILURE. The browse read is a separate, staff-guarded call that the
// catalogue screen is already allowed to lose without losing the table (empty state ≠ error state) -
// the rows and their prices are real either way, and Available renders blank. Denying the whole
// download because one of two reads was unhappy would be strictly worse for the person who clicked.
func (s *CatalogueService) readStock(ctx context.Context, f product.Filter) map[string]inventory.BrowseRow {
	if s.stock == nil {
		return nil
	}
	res, err := s.stock.Browse(ctx, browseFilterFor(f))
	if err != nil {
		s.log.Warn("export: catalogue stock read failed; Available exports blank", "err", err)
		return nil
	}
	out := make(map[string]inventory.BrowseRow, len(res.Rows))
	for _, r := range res.Rows {
		if r.ProductID == "" {
			continue
		}
		out[r.ProductID] = r
	}
	return out
}

// browseFilterFor maps the catalogue's filter onto the browse read's, field for field, the same
// mapping the component does when it builds the /inventory/browse query string. The browse filter
// takes lists where the catalogue takes single values, so each non-empty facet becomes a one-element
// list; an empty one is left nil (no constraint) rather than []string{""}, which would filter to
// products whose colour is literally the empty string.
func browseFilterFor(f product.Filter) inventory.BrowseFilter {
	one := func(v string) []string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return inventory.BrowseFilter{
		Text:         strings.TrimSpace(f.Q),
		BranchID:     f.BranchID,
		Manufacturer: one(f.Manufacturer),
		Collection:   one(f.Collection),
		Color:        one(f.Color),
		Finish:       one(f.Finish),
		Application:  one(f.Application),
		ColorFamily:  one(f.ColorFamily),
		Limit:        stockReadLimit,
	}
}

// build renders the rows into a sheet. Split from Workbook so every rule below is unit-testable
// without a database or a fake service.
func (s *CatalogueService) build(rows []product.Product, buckets map[string]inventory.BrowseRow, q CatalogueQuery) *Workbook {
	ordered := sortRows(rows, buckets, q.Sort, q.Desc)

	wb := New()
	sh := wb.AddSheet("Catalogue")
	sh.Title = "Product catalogue"
	sh.Subtitle = subtitle(s.now(), len(ordered), q.FilterSummary)
	sh.Columns = catalogueColumns
	sh.FreezeHeader = true

	for _, p := range ordered {
		b, hasStock := buckets[p.ID]
		var available Cell
		if hasStock {
			available = Num(b.AvailableBase)
		} else {
			// No browse row = no position. Blank, never 0: the screen draws an em dash here, and a
			// fabricated zero in a spreadsheet is worse than a gap because it sums.
			available = Blank()
		}
		sh.AddRow(
			Str(p.SKU),
			Str(p.Name),
			Str(p.Manufacturer),
			Str(colourOf(p)),
			Str(p.Application),
			NumOr(onHandTotal(p)),
			available,
			Str(p.BaseUOM),
			MoneyOr(p.ListPriceCents),
			Str(p.ListPriceUOM),
		)
	}
	return wb
}

// subtitle stamps the sheet with when it was produced, how many rows it holds, and the filter that
// produced it.
func subtitle(now time.Time, rows int, filterSummary string) string {
	noun := "products"
	if rows == 1 {
		noun = "product"
	}
	parts := []string{fmt.Sprintf("%d %s", rows, noun), "generated " + now.Format("2006-01-02 15:04")}
	if fs := strings.TrimSpace(filterSummary); fs != "" {
		parts = append(parts, "filter: "+fs)
	}
	return strings.Join(parts, " · ")
}

// colourOf is the screen's Colour cell (DIB-435): the product's own colour, falling back to the A-18
// promoted family for a product only catalogued at family level.
func colourOf(p product.Product) string {
	if p.Color != "" {
		return p.Color
	}
	return p.ColorFamily
}

// onHandTotal sums the row's per-branch on-hand, mirroring _catalogue.ts. Nil (blank), never 0 -
// when the ledger has no row for the product at all: the backend is careful to distinguish "no stock
// record" from "zero on hand", and this is the export that would throw that away.
func onHandTotal(p product.Product) *float64 {
	if len(p.OnHandByBranch) == 0 {
		return nil
	}
	total := 0.0
	for _, qty := range p.OnHandByBranch {
		total += qty
	}
	return &total
}

// sortRows orders the rows the way the screen does (_catalogue.ts sortRows), with the two rules that
// make a sorted export match a sorted table:
//
//  1. UNKNOWN SORTS LAST IN BOTH DIRECTIONS. A row with no ledger entry is not a small number. If it
//     sorted as 0 it would head the ascending list, and merely flipping it with the direction opens
//     the descending list, the "highest on hand first" one people actually want, with a wall of
//     blanks.
//  2. TIES BREAK ON SKU, always ascending, so two products at the same price never shuffle between
//     one export and the next.
func sortRows(rows []product.Product, buckets map[string]inventory.BrowseRow, key SortKey, desc bool) []product.Product {
	out := make([]product.Product, len(rows))
	copy(out, rows)
	if key == "" {
		return out
	}
	sign := 1
	if desc {
		sign = -1
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		as, an, aok := sortValue(a, buckets, key)
		bs, bn, bok := sortValue(b, buckets, key)
		switch {
		case !aok && !bok:
			return a.SKU < b.SKU
		case !aok:
			return false // unknown last, whichever way the arrow points
		case !bok:
			return true
		}
		cmp := 0
		if isNumericSort(key) {
			switch {
			case an < bn:
				cmp = -1
			case an > bn:
				cmp = 1
			}
		} else {
			cmp = strings.Compare(as, bs)
		}
		if cmp != 0 {
			return cmp*sign < 0
		}
		return a.SKU < b.SKU
	})
	return out
}

func isNumericSort(key SortKey) bool {
	switch key {
	case SortOnHand, SortAvailable, SortListPrice:
		return true
	}
	return false
}

// sortValue returns the comparable for one row and column: the string, the number, and whether the
// row HAS a value at all. The third return is the point, "no value" is a state, not a zero.
func sortValue(p product.Product, buckets map[string]inventory.BrowseRow, key SortKey) (string, float64, bool) {
	text := func(v string) (string, float64, bool) {
		if v == "" {
			return "", 0, false
		}
		return strings.ToLower(v), 0, true
	}
	switch key {
	case SortName:
		return text(p.Name)
	case SortManufacturer:
		return text(p.Manufacturer)
	case SortColour:
		return text(colourOf(p))
	case SortApplication:
		return text(p.Application)
	case SortOnHand:
		if t := onHandTotal(p); t != nil {
			return "", *t, true
		}
	case SortAvailable:
		if b, ok := buckets[p.ID]; ok {
			return "", b.AvailableBase, true
		}
	case SortListPrice:
		if p.ListPriceCents != nil {
			return "", float64(*p.ListPriceCents), true
		}
	}
	return "", 0, false
}

// CatalogueFilename is the download name: catalogue-YYYY-MM-DD.xlsx. Dated so a folder of them is
// orderable and nobody has to open three files to find today's.
func CatalogueFilename(now time.Time) string {
	return "catalogue-" + now.Format("2006-01-02") + ".xlsx"
}
