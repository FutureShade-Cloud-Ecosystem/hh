package inventory

// browse.go, DIB-61: the inventory browse surface, a stock-aware product list filtered by the
// A-18 promoted UDF columns, with COUNT BADGES on every facet value.
//
// WHY THE COUNTS ARE THE HARD PART. A facet list without counts is a set of guesses: a user picks
// "Charcoal", gets nothing, backs out, picks "Slate", gets nothing, and learns not to use the
// filters. The counts turn the filter panel into a map of the data. The cost is that each facet
// aggregate re-applies the current predicate, so the same WHERE clause runs once per facet plus once
// for the rows, which is why migration 0118 adds the trigram index over the text layer and the
// partial indexes over the boolean facets.
//
// THE FACET-COUNT SEMANTIC THAT MATTERS. Counts for facet F are computed with every filter APPLIED
// EXCEPT F's own. If they were computed with F applied too, every unselected value in F would read
// zero the moment you selected one, the panel would collapse to a single row and you could never
// see what else was available without clearing the filter. This is the standard e-commerce
// behaviour, and getting it wrong is the single most common facet bug.
//
// STOCK-AWARE, because this is the INVENTORY browse and not the catalogue: every row carries the
// four buckets at the selected branch (DIB-73), so someone looking for "grey slabs we can actually
// sell today" gets that in one screen instead of a filter here and a stock lookup there.

import (
	"context"
	"strings"
)

// BrowseFilter is the applied filter set. Empty fields are "no constraint".
type BrowseFilter struct {
	// Text runs against the trigram layer (sku + name + manufacturer + collection, 0118).
	Text string `json:"text"`
	// BranchID scopes the stock columns. "" = engagement-wide totals.
	BranchID string `json:"branch_id"`

	// The A-18 promoted UDF columns (0018).
	ColorFamily []string `json:"color_family"`
	Texture     []string `json:"texture"`
	ProductSize []string `json:"product_size"`
	SoldBy      []string `json:"sold_by"`
	// The classic catalogue attributes (0002). See facetFields for why both sets are here.
	Manufacturer []string `json:"manufacturer"`
	Collection   []string `json:"collection"`
	Color        []string `json:"color"`
	Finish       []string `json:"finish"`
	Application  []string `json:"application"`

	Featured     bool `json:"is_featured"`
	SpecialOrder bool `json:"is_special_order"`
	// InStockOnly grades AVAILABLE (DIB-60), not on-hand.
	InStockOnly bool `json:"in_stock_only"`

	Limit int `json:"limit"`
	// Offset is how many matching rows to skip before this page, DIB-452. See Browse for why the
	// cap stayed and paging was added instead of raising it.
	Offset int `json:"offset"`
}

// BrowseRow is one product on the browse surface, with its stock position.
type BrowseRow struct {
	ProductID    string `json:"product_id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Collection   string `json:"collection"`
	ColorFamily  string `json:"color_family"`
	Texture      string `json:"texture"`
	ProductSize  string `json:"product_size"`
	SoldBy       string `json:"sold_by"`
	Color        string `json:"color"`
	Finish       string `json:"finish"`
	Application  string `json:"application"`
	IsFeatured   bool   `json:"is_featured"`
	IsSpecial    bool   `json:"is_special_order"`

	OnHandBase    float64 `json:"on_hand_base"`
	ReservedBase  float64 `json:"reserved_base"`
	AvailableBase float64 `json:"available_base"`
	InTransitBase float64 `json:"in_transit_base"`
	AvgCostCents  int64   `json:"avg_cost_cents"`
}

// FacetValue is one selectable value and how many products carry it under the current filter.
type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facet is one filterable dimension with its values, most common first.
type Facet struct {
	Field  string       `json:"field"`
	Label  string       `json:"label"`
	Values []FacetValue `json:"values"`
}

// BrowseResult is the surface's single round trip: the rows, the facets, and the honest total.
type BrowseResult struct {
	Rows   []BrowseRow `json:"rows"`
	Facets []Facet     `json:"facets"`
	// Total is the number of products matching the filter BEFORE the limit. Rendering "showing 50"
	// with no denominator is how a user concludes the filter is broken when it is merely paged.
	Total int `json:"total"`

	// Limit and Offset echo the page this result IS, after the service's clamping, DIB-452.
	//
	// They are echoed rather than left for the caller to remember because the caller's request and
	// the server's answer are not always the same page: ask for limit=5000 and you get 60. A pager
	// that assumes it received what it asked for walks off the end of the list (or loops forever)
	// the first time a clamp disagrees with it, and the failure looks like missing data rather than
	// like a client bug. With these two fields the next page is arithmetic on facts, not on hope.
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// HasMore reports whether another page exists after this one. Total counts the whole match set
// before the limit, so this is the only comparison a pager needs, and having it here means the
// off-by-one lives in one place instead of once per consumer.
func (r BrowseResult) HasMore() bool { return r.Offset+len(r.Rows) < r.Total }

// facetFields is the facet set, in display order, mapping the wire field name to the products column
// and the human label. Data-driven so adding a dimension to the panel is one line here rather than a
// sixth copy of the same aggregate.
//
// WHY BOTH THE A-18 COLUMNS AND THE CLASSIC ONES. DIB-61 names the A-18 promoted UDF columns
// (color_family / texture / product_size / sold_by, migration 0018). On the demo seed, three of those
// four are EMPTY IN EVERY ROW, the M4 BisTrack ETL that fills them from dbo.Product.udf* has not run
// against this data, and it is not this bundle's to run. A panel built only on them renders three
// dead controls on every environment a reviewer actually opens.
//
// The fix is not to fake data into the promoted columns. It is to face the fact that this surface's
// job is "filter the catalogue by its attributes", and the catalogue has attributes in both places:
// collection / color / finish / application (0002) carry the demo data, the A-18 columns carry the
// real BisTrack data. Both are listed; Browse DROPS any facet with no values, so each environment
// shows exactly the dimensions it actually has, and the A-18 facets light up on their own the day the
// ETL fills them, with no code change here.
var facetFields = []struct{ Field, Column, Label string }{
	// A-18 promoted UDFs (DIB-61's named target).
	{"color_family", "color_family", "Colour family"},
	{"texture", "texture", "Texture"},
	{"product_size", "product_size", "Size"},
	{"sold_by", "sold_by", "Sold by"},
	// Classic catalogue attributes.
	{"manufacturer", "manufacturer", "Manufacturer"},
	{"collection", "collection", "Collection"},
	{"color", "color", "Colour"},
	{"finish", "finish", "Finish"},
	{"application", "application", "Application"},
}

// Browse runs the filtered row read and every facet count.
//
// DIB-452, WHY THE 200 CAP STAYED AND PAGING WAS ADDED INSTEAD.
//
// The catalogue list gets its availability column from this read, and the catalogue list is unpaged.
// Past 200 matching products the extra rows had no bucket to join to, so they rendered an em dash
// forever: not "we do not know yet", which is what an em dash means, but "we stopped asking".
//
// The tempting fix is to raise the cap. It is the wrong one. Every row here drags three correlated
// lateral aggregates over the stock tables behind it, and the facet counts re-run the same predicate
// once per facet; the cap is what keeps one browse call bounded, and a catalogue does not stop
// growing because a limit clause did. So the cap stays exactly where it was and the read learned to
// say WHICH page it is answering for, one bounded request per page, as many pages as the caller
// needs, and a Total that has always been honest about how many there are.
//
// The clamping is deliberately unchanged from what shipped: a limit outside (0,200] falls back to
// 60 rather than to 200, because that is what every existing caller has been getting and this ticket
// is not the place to quietly re-tune anyone's page size. Offset is floored at zero; a negative
// offset is refused at the handler, so this is a belt on the in-process path.
func (s *Service) Browse(ctx context.Context, f BrowseFilter) (BrowseResult, error) {
	f.Limit, f.Offset = browsePage(f.Limit, f.Offset)
	f.Text = strings.TrimSpace(f.Text)
	return s.repo.Browse(ctx, f)
}

// The page bounds, named rather than spelled inline so the frontend's STOCK_READ_LIMIT and the
// export's stockReadLimit have something to point at.
const (
	// browseMaxLimit is the most rows one browse call will ever answer for. See Browse for why this
	// is a cap on the PAGE and not a cap on what a caller can eventually read.
	browseMaxLimit = 200
	// browseDefaultLimit is what an unspecified, or nonsensical, limit falls back to. It is not
	// browseMaxLimit: this is the size a screenful has always been, and changing it here would
	// re-tune every existing caller's first request as a side effect of a paging ticket.
	browseDefaultLimit = 60
)

// browsePage clamps a requested page onto the page the read will actually answer for.
//
// Split out as a pure function so the clamp can be tested without a database, which matters more
// than it looks, because this is where a paging off-by-one would live and the only other way to see
// it is to notice a missing product in a list of several hundred.
func browsePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > browseMaxLimit {
		limit = browseDefaultLimit
	}
	if offset < 0 {
		// A negative offset is refused at the handler with a 400 (offsetParam). Reaching zero here
		// is the in-process belt: SQL would reject it outright and the caller would get a 500 for
		// what is plainly a bad request.
		offset = 0
	}
	return limit, offset
}

// - the SQL builder -

// browseWhere builds the shared predicate. skipField names a facet whose own constraint is DROPPED -
// that is the facet-count semantic described in the file header. args is appended to in place and the
// returned SQL uses positional parameters consistent with it.
func browseWhere(f BrowseFilter, skipField string, args *[]any) string {
	var w []string
	add := func(v any) string {
		*args = append(*args, v)
		return "$" + itoaPos(len(*args))
	}
	w = append(w, "p.is_active")

	if f.Text != "" {
		// The concatenation is byte-identical to the expression migration 0118 indexes; any drift and
		// the GIN index silently stops being used, which shows up as a slow page and not as an error.
		w = append(w, "(p.sku::text || ' ' || p.name || ' ' || p.manufacturer || ' ' || p.collection) ILIKE '%' || "+add(f.Text)+" || '%'")
	}
	inList := func(field, column string, vals []string) {
		if field == skipField || len(vals) == 0 {
			return
		}
		w = append(w, "p."+column+" = ANY("+add(vals)+"::text[])")
	}
	inList("color_family", "color_family", f.ColorFamily)
	inList("texture", "texture", f.Texture)
	inList("product_size", "product_size", f.ProductSize)
	inList("sold_by", "sold_by", f.SoldBy)
	inList("manufacturer", "manufacturer", f.Manufacturer)
	inList("collection", "collection", f.Collection)
	inList("color", "color", f.Color)
	inList("finish", "finish", f.Finish)
	inList("application", "application", f.Application)

	if f.Featured && skipField != "is_featured" {
		w = append(w, "p.is_featured")
	}
	if f.SpecialOrder && skipField != "is_special_order" {
		w = append(w, "p.is_special_order")
	}
	if f.InStockOnly {
		// DIB-60: in stock means AVAILABLE. Correlated EXISTS rather than a join so a product with
		// balance rows at several locations is counted once, not once per location.
		bp := add(f.BranchID)
		w = append(w, `EXISTS (SELECT 1 FROM stock_balances sb
		                        WHERE sb.product_id = p.id
		                          AND (`+bp+` = '' OR sb.branch_id = `+bp+`::uuid)
		                          AND sb.on_hand_base - sb.qty_reserved > 0)`)
	}
	return strings.Join(w, " AND ")
}

// itoaPos renders a positional parameter index.
func itoaPos(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Browse is the pgx read behind Service.Browse.
func (r *Repository) Browse(ctx context.Context, f BrowseFilter) (BrowseResult, error) {
	if r.pool == nil {
		return BrowseResult{}, errNoDB
	}
	var out BrowseResult
	// The page this result IS, echoed back after the service's clamping so a pager never has to
	// assume it got what it asked for (DIB-452).
	out.Limit, out.Offset = f.Limit, f.Offset

	// - rows -
	rowArgs := []any{}
	where := browseWhere(f, "", &rowArgs)
	branchParam := "$" + itoaPos(len(rowArgs)+1)
	rowArgs = append(rowArgs, f.BranchID)
	limitParam := "$" + itoaPos(len(rowArgs)+1)
	rowArgs = append(rowArgs, f.Limit)
	offsetParam := "$" + itoaPos(len(rowArgs)+1)
	rowArgs = append(rowArgs, f.Offset)

	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.sku::text, p.name, p.manufacturer, p.collection,
		       p.color_family, p.texture, p.product_size, p.sold_by,
		       p.color, p.finish, p.application,
		       p.is_featured, p.is_special_order,
		       COALESCE(bal.on_hand, 0), COALESCE(bal.reserved, 0),
		       COALESCE(tr.qty, 0), COALESCE(cost.avg, 0)
		  FROM products p
		  LEFT JOIN LATERAL (
		      SELECT SUM(sb.on_hand_base) AS on_hand, SUM(sb.qty_reserved) AS reserved
		        FROM stock_balances sb
		       WHERE sb.product_id = p.id
		         AND (`+branchParam+` = '' OR sb.branch_id = `+branchParam+`::uuid)
		  ) bal ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT SUM(tl.qty_shipped_base - tl.qty_received_base) AS qty
		        FROM stock_transfer_lines tl
		        JOIN stock_transfers t ON t.id = tl.transfer_id
		       WHERE tl.product_id = p.id AND t.status = 'IN_TRANSIT'
		         AND tl.qty_shipped_base > tl.qty_received_base
		         AND (`+branchParam+` = '' OR t.to_branch_id = `+branchParam+`::uuid)
		  ) tr ON TRUE
		  LEFT JOIN LATERAL (
		      SELECT ROUND(AVG(pbc.avg_unit_cost_cents))::bigint AS avg
		        FROM product_branch_costs pbc
		       WHERE pbc.product_id = p.id
		         AND (`+branchParam+` = '' OR pbc.branch_id = `+branchParam+`::uuid)
		  ) cost ON TRUE
		 WHERE `+where+`
		 ORDER BY p.sku
		 LIMIT `+limitParam+`
		 OFFSET `+offsetParam, rowArgs...)
	// ORDER BY p.sku is what makes OFFSET paging correct here rather than merely usual: products.sku
	// carries a UNIQUE index, so the ordering is TOTAL and page N+1 cannot repeat or skip a row that
	// page N already answered for. An ORDER BY on a non-unique column would leave ties in an order
	// the planner is free to change between the two queries, and the symptom of that is a product
	// that silently never appears on any page, which is the bug this ticket is closing, arriving by
	// a different road.
	if err != nil {
		return BrowseResult{}, err
	}
	func() {
		defer rows.Close()
		for rows.Next() {
			var b BrowseRow
			if err = rows.Scan(&b.ProductID, &b.SKU, &b.Name, &b.Manufacturer, &b.Collection,
				&b.ColorFamily, &b.Texture, &b.ProductSize, &b.SoldBy,
				&b.Color, &b.Finish, &b.Application, &b.IsFeatured, &b.IsSpecial,
				&b.OnHandBase, &b.ReservedBase, &b.InTransitBase, &b.AvgCostCents); err != nil {
				return
			}
			b.AvailableBase = AvailableBase(b.OnHandBase, b.ReservedBase)
			out.Rows = append(out.Rows, b)
		}
		err = rows.Err()
	}()
	if err != nil {
		return BrowseResult{}, err
	}

	// - total (pre-limit) -
	//
	// Only the WHERE args here: the row query needed extra parameters for its lateral stock joins,
	// and appending those to a statement that does not reference them is a "expected N arguments"
	// error from pgx, not a harmless extra.
	totalArgs := []any{}
	totalWhere := browseWhere(f, "", &totalArgs)
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*)::int FROM products p WHERE `+totalWhere, totalArgs...).Scan(&out.Total); err != nil {
		return BrowseResult{}, err
	}

	// - facets -
	for _, ff := range facetFields {
		facetArgs := []any{}
		fw := browseWhere(f, ff.Field, &facetArgs)
		vrows, err := r.pool.Query(ctx, `
			SELECT p.`+ff.Column+` AS value, count(*)::int
			  FROM products p
			 WHERE `+fw+` AND p.`+ff.Column+` <> ''
			 GROUP BY 1
			 ORDER BY 2 DESC, 1 ASC
			 LIMIT 40`, facetArgs...)
		if err != nil {
			return BrowseResult{}, err
		}
		facet := Facet{Field: ff.Field, Label: ff.Label}
		scanErr := func() error {
			defer vrows.Close()
			for vrows.Next() {
				var v FacetValue
				if err := vrows.Scan(&v.Value, &v.Count); err != nil {
					return err
				}
				facet.Values = append(facet.Values, v)
			}
			return vrows.Err()
		}()
		if scanErr != nil {
			return BrowseResult{}, scanErr
		}
		// A facet with nothing in it is not shown. An empty "Texture" panel is a dead control that
		// teaches the user the filters do not work.
		if len(facet.Values) > 0 {
			out.Facets = append(out.Facets, facet)
		}
	}

	// The two boolean facets are counted as single toggles rather than value lists.
	for _, bf := range []struct{ Field, Column, Label string }{
		{"is_featured", "is_featured", "Dibbits favourite"},
		{"is_special_order", "is_special_order", "Special order"},
	} {
		boolArgs := []any{}
		bw := browseWhere(f, bf.Field, &boolArgs)
		var n int
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*)::int FROM products p WHERE `+bw+` AND p.`+bf.Column, boolArgs...).Scan(&n); err != nil {
			return BrowseResult{}, err
		}
		if n > 0 {
			out.Facets = append(out.Facets, Facet{
				Field: bf.Field, Label: bf.Label, Values: []FacetValue{{Value: "true", Count: n}},
			})
		}
	}

	return out, nil
}
