//go:build integration

package pricing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The report's quote side over a real Postgres: a quote line is read at the quote's own prices, net of
// its discount percent, graded against the branch's current cost like an order line.

const (
	lmTrenton = "5d1b3f6e-0000-4000-9000-000000000001"
	lmBrussSF = "SF"
)

type lmQuoteLine struct {
	GrossUnit int64  // the engine's gross unit price per SF, as stored
	DiscPct   int    // whole percent
	Metadata  string // the line's metadata JSON, "" for none
}

type lmQuote struct {
	ID          string // uuid
	No          string
	Status      string
	Salesperson string // users.id or "" for none
	CreatedBy   string // users.id or "" for none
	CreatedAt   time.Time
	Lines       []lmQuoteLine
}

// seedLMQuote plants one quote of Brussels lines at Trenton for the WALK-IN customer, forty SF a line
// (164 PC base), and removes it again at cleanup.
func seedLMQuote(t *testing.T, pool *pgxpool.Pool, q lmQuote) {
	t.Helper()
	ctx := context.Background()
	if q.Status == "" {
		q.Status = "SENT"
	}
	if q.CreatedAt.IsZero() {
		q.CreatedAt = time.Now()
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, salesperson_id, created_by, created_at)
		VALUES ($1, $2, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $3, $4,
		        NULLIF($5,'')::uuid, NULLIF($6,'')::uuid, $7)`,
		q.ID, q.No, lmTrenton, q.Status, q.Salesperson, q.CreatedBy, q.CreatedAt); err != nil {
		t.Fatalf("seed quote %s: %v", q.No, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, q.ID)
	})
	for i, ln := range q.Lines {
		gross := ln.GrossUnit * 40
		net := (gross*int64(100-ln.DiscPct) + 50) / 100
		meta := ln.Metadata
		if meta == "" {
			meta = "{}"
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
			                         unit_price_cents, price_uom, line_total_cents, disc_pct, pre_discount_total_cents, metadata)
			VALUES ($1, $2, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'), 'Brussels Block', 40, 'SF', 164,
			        $3, 'SF', $4, $5, $6, $7::jsonb)`,
			q.ID, i+1, ln.GrossUnit, net, ln.DiscPct, gross, meta); err != nil {
			t.Fatalf("seed quote %s line %d: %v", q.No, i+1, err)
		}
	}
}

func lmWindow() LowMarginFilter {
	return LowMarginFilter{From: time.Now().AddDate(0, 0, -30), To: time.Now().Add(time.Hour)}
}

func lmRowsByDoc(rows []LowMarginScanRow, docNo string) []LowMarginScanRow {
	var out []LowMarginScanRow
	for _, r := range rows {
		if r.DocNo == docNo {
			out = append(out, r)
		}
	}
	return out
}

func TestIntegration_LowMarginDocs_QuotesGradedAtTheirOwnNetPrice(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	no := fmt.Sprintf("Q-LMD-%d", time.Now().UnixNano())
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000101", No: no,
		Lines: []lmQuoteLine{{GrossUnit: 1000, DiscPct: 15}}}) // net 850 per SF: 1558 bps, below the 2000 floor

	// Quotes only: the quote's line is there, graded at 850 not at the stored gross 1000.
	f := lmWindow()
	f.DocType = ScanDocsQuotes
	rows, err := svc.LowMarginScan(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	got := lmRowsByDoc(rows, no)
	if len(got) != 1 {
		t.Fatalf("want the one quote line in the quotes report, got %+v", rows)
	}
	r := got[0]
	if r.DocType != DocTypeQuote || r.UnitPriceCents != 850 || r.MarginBps != 1558 || r.Level != "critical" || r.CostInPriceUOMCents != 718 {
		t.Errorf("quote line graded wrong: %+v", r)
	}
	for _, o := range rows {
		if o.DocType != DocTypeQuote {
			t.Errorf("a quotes only report carried a %q row: %+v", o.DocType, o)
		}
	}

	// Orders only: the seeded order rows ride, the quote does not.
	f.DocType = ScanDocsOrders
	rows, err = svc.LowMarginScan(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(lmRowsByDoc(rows, no)) != 0 {
		t.Errorf("an orders only report carried the quote: %+v", rows)
	}
	if len(lmRowsByDoc(rows, "#SO-DPA3-CRIT")) != 1 {
		t.Errorf("an orders only report lost the seeded critical order: %+v", rows)
	}

	// Both (and the unset default): the two kinds together.
	for _, dt := range []string{ScanDocsBoth, ""} {
		f.DocType = dt
		rows, err = svc.LowMarginScan(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if len(lmRowsByDoc(rows, no)) != 1 || len(lmRowsByDoc(rows, "#SO-DPA3-CRIT")) != 1 {
			t.Errorf("doc type %q must carry both kinds: %+v", dt, rows)
		}
	}
}

// An order converted from a discounted quote is graded at the net price too.
func TestIntegration_LowMarginDocs_ADiscountedOrderLineIsGradedNet(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	no := fmt.Sprintf("SO-LMD-%d", time.Now().UnixNano())
	// An order converted from a discounted quote keeps the engine's gross in unit_price_cents and the
	// discount percent in the line's metadata: the report must grade the net, not the gross.
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment)
		VALUES ('0d4a0000-0000-4000-9000-000000000102', $1, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $2, 'INVOICED', 'PICKUP')`,
		no, lmTrenton); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = '0d4a0000-0000-4000-9000-000000000102'`)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents, metadata)
		VALUES ('0d4a0000-0000-4000-9000-000000000102', 1, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'),
		        'Brussels Block (15% off)', 40, 'SF', 164, 1000, 'SF', 34000,
		        '{"disc_pct":15,"pre_discount_total_cents":40000}'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	f := lmWindow()
	f.DocType = ScanDocsOrders
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	got := lmRowsByDoc(rows, no)
	if len(got) != 1 || got[0].UnitPriceCents != 850 || got[0].MarginBps != 1558 || got[0].Level != "critical" {
		t.Errorf("a discounted order line must be graded at its net price 850: %+v", got)
	}
}

// lmUser plants a staff user with a display label and returns its id; removed at cleanup.
func lmUser(t *testing.T, pool *pgxpool.Pool, id, label string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, display_name, status) VALUES ($1, $2, $3, 'ACTIVE')`,
		id, id+"@lowmargin.test", label); err != nil {
		t.Fatalf("seed user %s: %v", label, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
}

const (
	lmSalesUser   = "0d4a0000-0000-4000-9000-0000000000a1"
	lmManagerUser = "0d4a0000-0000-4000-9000-0000000000a2"
)

// Step 2 over a real Postgres: the salesperson's display label, the clearing stamp the quote line
// already carries (who, on what authority), and the document's own overall margin, a below floor line
// inside a healthy quote. The same clearing is found for the order the quote became, through the
// quote line it was copied from.
func TestIntegration_LowMarginDocs_RowsCarrySalespersonClearingAndDocumentMargin(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	lmUser(t, pool, lmSalesUser, "Outside Sales, Trenton")
	lmUser(t, pool, lmManagerUser, "Sales Manager, Trenton")
	no := fmt.Sprintf("Q-LMR-%d", time.Now().UnixNano())
	stamp := fmt.Sprintf(`{"floor_clear":{"cleared_by":%q,"authority":"MANAGER_SCOPE","disc_pct":15,"margin_bps":1552}}`, lmManagerUser)
	quoteID := "0d4a0000-0000-4000-9000-000000000201"
	seedLMQuote(t, pool, lmQuote{ID: quoteID, No: no, Salesperson: lmSalesUser,
		Lines: []lmQuoteLine{
			{GrossUnit: 1000, DiscPct: 15, Metadata: stamp}, // net 850: critical, cleared by the manager
			{GrossUnit: 2400, DiscPct: 0},                   // healthy, not a row, but it lifts the quote's margin
		}})

	f := lmWindow()
	f.DocType = ScanDocsQuotes
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	got := lmRowsByDoc(rows, no)
	if len(got) != 1 {
		t.Fatalf("want the one below floor line of the quote, got %+v", got)
	}
	r := got[0]
	if r.Salesperson != "Outside Sales, Trenton" || r.SalespersonID != lmSalesUser {
		t.Errorf("salesperson: %q / %q", r.Salesperson, r.SalespersonID)
	}
	if !r.ClearedBelowFloor || r.ClearedBy != "Sales Manager, Trenton" || r.ClearedByID != lmManagerUser || r.ClearAuthority != "MANAGER_SCOPE" {
		t.Errorf("clearing: %+v", r)
	}
	// Revenue 34000 + 96000, cost 28700 on each line: (130000-57400)*1e4/130000 = 5584.
	if r.DocMarginBps == nil || *r.DocMarginBps != 5584 {
		t.Errorf("document margin = %v, want 5584", r.DocMarginBps)
	}

	// The order the quote became: the order line is found cleared through its quote line.
	ono := fmt.Sprintf("SO-LMR-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, status, fulfillment, created_by)
		VALUES ('0d4a0000-0000-4000-9000-000000000202', $1, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $2, $3, 'INVOICED', 'PICKUP', $4)`,
		ono, lmTrenton, quoteID, lmManagerUser); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = '0d4a0000-0000-4000-9000-000000000202'`)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents, metadata)
		SELECT '0d4a0000-0000-4000-9000-000000000202', line_no, product_id, description, qty_entered, uom_entered, qty_base,
		       unit_price_cents, price_uom, line_total_cents,
		       CASE WHEN disc_pct > 0 THEN jsonb_build_object('disc_pct', disc_pct) ELSE '{}'::jsonb END
		  FROM quote_lines WHERE quote_id = $1`, quoteID); err != nil {
		t.Fatal(err)
	}
	f.DocType = ScanDocsOrders
	rows, err = svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	og := lmRowsByDoc(rows, ono)
	if len(og) != 1 {
		t.Fatalf("want the order's below floor line, got %+v", og)
	}
	o := og[0]
	if !o.ClearedBelowFloor || o.ClearedBy != "Sales Manager, Trenton" || o.ClearAuthority != "MANAGER_SCOPE" {
		t.Errorf("the order line must carry its quote line's clearing: %+v", o)
	}
	// The order's salesperson is the quote's salesperson, not whoever converted it.
	if o.Salesperson != "Outside Sales, Trenton" {
		t.Errorf("order salesperson = %q, want the quote's", o.Salesperson)
	}
	if o.DocMarginBps == nil || *o.DocMarginBps != 5584 {
		t.Errorf("order document margin = %v, want 5584", o.DocMarginBps)
	}
}

// An order line whose price was edited after the quote is no longer the line that was cleared: it
// does not inherit the stamp.
func TestIntegration_LowMarginDocs_AnEditedOrderLineDoesNotInheritTheClearing(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	lmUser(t, pool, lmManagerUser, "Sales Manager, Trenton")
	stamp := fmt.Sprintf(`{"floor_clear":{"cleared_by":%q,"authority":"APPROVAL","disc_pct":15,"margin_bps":1558}}`, lmManagerUser)
	quoteID := "0d4a0000-0000-4000-9000-000000000203"
	seedLMQuote(t, pool, lmQuote{ID: quoteID, No: fmt.Sprintf("Q-LME-%d", time.Now().UnixNano()),
		Lines: []lmQuoteLine{{GrossUnit: 1000, DiscPct: 15, Metadata: stamp}}})
	ono := fmt.Sprintf("SO-LME-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, status, fulfillment)
		VALUES ('0d4a0000-0000-4000-9000-000000000204', $1, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $2, $3, 'INVOICED', 'PICKUP')`,
		ono, lmTrenton, quoteID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = '0d4a0000-0000-4000-9000-000000000204'`)
	})
	// The price on the order differs from the cleared quote line's: 800, not the quote's gross 1000.
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
		                         unit_price_cents, price_uom, line_total_cents)
		VALUES ('0d4a0000-0000-4000-9000-000000000204', 1, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'),
		        'Brussels Block', 40, 'SF', 164, 800, 'SF', 32000)`); err != nil {
		t.Fatal(err)
	}
	f := lmWindow()
	f.DocType = ScanDocsOrders
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	got := lmRowsByDoc(rows, ono)
	if len(got) != 1 || got[0].ClearedBelowFloor {
		t.Errorf("an edited order line must not carry the quote line's clearing: %+v", got)
	}
}

// Step 3 over a real Postgres: the salesperson, branch, document date range and cleared only filters,
// the margin sort, and the cursor page.
func TestIntegration_LowMarginDocs_FiltersSortAndCursorPage(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	lmUser(t, pool, lmSalesUser, "Outside Sales, Trenton")
	lmUser(t, pool, lmManagerUser, "Sales Manager, Trenton")
	stamp := fmt.Sprintf(`{"floor_clear":{"cleared_by":%q,"authority":"MANAGER_SCOPE","disc_pct":15,"margin_bps":1558}}`, lmManagerUser)
	tag := time.Now().UnixNano()
	old := time.Now().AddDate(0, 0, -20)
	// qa: the salesperson's, cleared, 850 net. qb: the manager's own, uncleared, 870 net.
	// qc: the salesperson's, uncleared, 900 net, but dated twenty days back.
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000301", No: fmt.Sprintf("Q-LMF-A-%d", tag),
		Salesperson: lmSalesUser, Lines: []lmQuoteLine{{GrossUnit: 850, Metadata: stamp}}})
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000302", No: fmt.Sprintf("Q-LMF-B-%d", tag),
		CreatedBy: lmManagerUser, Lines: []lmQuoteLine{{GrossUnit: 870}}})
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000303", No: fmt.Sprintf("Q-LMF-C-%d", tag),
		Salesperson: lmSalesUser, CreatedAt: old, Lines: []lmQuoteLine{{GrossUnit: 900}}})
	mine := func(rows []LowMarginScanRow) []string {
		var out []string
		for _, r := range rows {
			if len(r.DocNo) > 6 && r.DocNo[:6] == "Q-LMF-" {
				out = append(out, r.DocNo[6:7])
			}
		}
		return out
	}
	run := func(mut func(*LowMarginFilter)) []string {
		f := lmWindow()
		f.DocType = ScanDocsQuotes
		mut(&f)
		rows, err := svc.LowMarginScan(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return mine(rows)
	}
	want := func(name string, got []string, w ...string) {
		t.Helper()
		if !eq(got, w) {
			t.Errorf("%s: %v, want %v", name, got, w)
		}
	}
	want("ascending", run(func(f *LowMarginFilter) {}), "A", "B", "C")
	want("descending", run(func(f *LowMarginFilter) { f.Sort = SortMarginDesc }), "C", "B", "A")
	want("salesperson", run(func(f *LowMarginFilter) { f.SalespersonID = lmSalesUser }), "A", "C")
	want("a creator is the salesperson when none is named", run(func(f *LowMarginFilter) { f.SalespersonID = lmManagerUser }), "B")
	want("cleared only", run(func(f *LowMarginFilter) { f.ClearedOnly = true }), "A")
	want("date range excludes the old quote", run(func(f *LowMarginFilter) { f.From = time.Now().AddDate(0, 0, -5) }), "A", "B")
	want("date range reaches the old quote", run(func(f *LowMarginFilter) {
		f.From, f.To = time.Now().AddDate(0, 0, -25), time.Now().AddDate(0, 0, -10)
	}), "C")
	want("another branch holds none of them", run(func(f *LowMarginFilter) { f.BranchID = "5d1b3f6e-0000-4000-9000-0000000000ff" }))
	want("this branch holds all", run(func(f *LowMarginFilter) { f.BranchID = lmTrenton }), "A", "B", "C")

	// The cursor page: two at a time over the whole quotes report, every row once.
	f := lmWindow()
	f.DocType = ScanDocsQuotes
	all, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	var walked []string
	cursor := ""
	for i := 0; i < 50; i++ {
		page, err := svc.LowMarginScanPage(ctx, f, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, ids(page.Items)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !eq(walked, ids(all)) {
		t.Errorf("paged walk %v differs from the whole report %v", walked, ids(all))
	}
}

// The branch wall in the feed's own SQL: nil reads every branch, a list reads only those, an empty
// list reads nothing.
func TestIntegration_LowMarginDocs_BranchWallInTheQuery(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	count := func(ids []string) int {
		f := lmWindow()
		f.BranchIDs = ids
		rows, err := svc.LowMarginScan(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		return len(lmRowsByDoc(rows, "#SO-DPA3-CRIT"))
	}
	if count(nil) != 1 || count([]string{lmTrenton}) != 1 {
		t.Errorf("nil and the granted branch must read the seeded Trenton order")
	}
	if count([]string{"5d1b3f6e-0000-4000-9000-0000000000ff"}) != 0 || count([]string{}) != 0 {
		t.Errorf("another branch, and an empty grant, must read nothing")
	}
}

// Tombstones (deleted_at, a sync fact) are never report rows: a deleted quote, a deleted line of a
// live quote, a deleted order and a deleted order line all stay off the report, and a deleted line
// does not count toward its document's margin.
func TestIntegration_LowMarginDocs_TombstonesNeverAppear(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	tag := time.Now().UnixNano()
	live := fmt.Sprintf("Q-LMT-LIVE-%d", tag)
	gone := fmt.Sprintf("Q-LMT-GONE-%d", tag)
	lineGone := fmt.Sprintf("Q-LMT-LINE-%d", tag)
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000401", No: live, Lines: []lmQuoteLine{{GrossUnit: 850}}})
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000402", No: gone, Lines: []lmQuoteLine{{GrossUnit: 850}}})
	seedLMQuote(t, pool, lmQuote{ID: "0d4a0000-0000-4000-9000-000000000403", No: lineGone,
		Lines: []lmQuoteLine{{GrossUnit: 850}, {GrossUnit: 2400}}})
	for _, q := range []string{
		`UPDATE quotes SET deleted_at = now() WHERE id = '0d4a0000-0000-4000-9000-000000000402'`,
		// the second line (healthy, 2400) of the third quote is the one deleted
		`UPDATE quote_lines SET deleted_at = now() WHERE quote_id = '0d4a0000-0000-4000-9000-000000000403' AND line_no = 2`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Two orders, one deleted whole, one with a deleted line.
	ordGone, ordLine := fmt.Sprintf("SO-LMT-GONE-%d", tag), fmt.Sprintf("SO-LMT-LINE-%d", tag)
	for id, no := range map[string]string{"0d4a0000-0000-4000-9000-000000000404": ordGone, "0d4a0000-0000-4000-9000-000000000405": ordLine} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment)
			VALUES ($1, $2, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $3, 'INVOICED', 'PICKUP')`, id, no, lmTrenton); err != nil {
			t.Fatal(err)
		}
		id := id
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, id) })
		for line, unit := range []int64{850, 2400} {
			if _, err := pool.Exec(ctx, `
				INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
				VALUES ($1, $2, (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'), 'Brussels Block', 40, 'SF', 164, $3, 'SF', $4)`,
				id, line+1, unit, unit*40); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, q := range []string{
		`UPDATE orders SET deleted_at = now() WHERE id = '0d4a0000-0000-4000-9000-000000000404'`,
		`UPDATE order_lines SET deleted_at = now() WHERE order_id = '0d4a0000-0000-4000-9000-000000000405' AND line_no = 2`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := svc.LowMarginScan(ctx, lmWindow())
	if err != nil {
		t.Fatal(err)
	}
	if len(lmRowsByDoc(rows, live)) != 1 {
		t.Errorf("the live quote must be a row: %+v", rows)
	}
	if n := len(lmRowsByDoc(rows, gone)); n != 0 {
		t.Errorf("a deleted quote is on the report (%d rows)", n)
	}
	if n := len(lmRowsByDoc(rows, ordGone)); n != 0 {
		t.Errorf("a deleted order is on the report (%d rows)", n)
	}
	// The surviving critical line of a document whose healthy line was deleted: one row, and its
	// document margin is the one line's own (the deleted line adds no revenue).
	for _, no := range []string{lineGone, ordLine} {
		got := lmRowsByDoc(rows, no)
		if len(got) != 1 {
			t.Errorf("%s: want only the surviving line, got %+v", no, got)
			continue
		}
		if got[0].DocMarginBps == nil {
			t.Errorf("%s: no document margin, want 1558 off the surviving line alone", no)
		} else if *got[0].DocMarginBps != 1558 {
			t.Errorf("%s: document margin %d, want 1558 off the surviving line alone", no, *got[0].DocMarginBps)
		}
	}
}

// The stamp stands for the discount it cleared and anything shallower, and cleared_below_floor is true
// only for a line below its floor now that a stamp covers. A converted order line inherits the same
// rule: the stamp rides to the order, the order line's own discount is what it is compared with.
func TestIntegration_LowMarginDocs_ClearedMeansBelowTheFloorNowAndCovered(t *testing.T) {
	pool := intPool(t)
	svc := realSvc(pool)
	ctx := context.Background()
	lmUser(t, pool, lmManagerUser, "Sales Manager, Trenton")
	stamp := fmt.Sprintf(`{"floor_clear":{"cleared_by":%q,"authority":"MANAGER_SCOPE","disc_pct":25,"margin_bps":433}}`, lmManagerUser)
	tag := time.Now().UnixNano()
	// Gross 1000 a SF, forty SF, cost 28700: 25 percent off is a margin of 433 bps (below the floor),
	// 15 percent off 1558, 30 percent off negative, 5 percent off 2447 (back above the floor, warn).
	cases := []struct {
		name    string
		disc    int
		cleared bool
		level   string
	}{
		{"SAME", 25, true, "critical"}, {"SHALLOW", 15, true, "critical"}, {"DEEPER", 30, false, "critical"}, {"BACK", 5, false, "warn"},
	}
	quoteIDs := map[string]string{}
	for i, c := range cases {
		id := fmt.Sprintf("0d4a0000-0000-4000-9000-00000000050%d", i+1)
		quoteIDs[c.name] = id
		seedLMQuote(t, pool, lmQuote{ID: id, No: fmt.Sprintf("Q-LMS-%s-%d", c.name, tag),
			Lines: []lmQuoteLine{{GrossUnit: 1000, DiscPct: c.disc, Metadata: stamp}}})
	}
	f := lmWindow()
	f.DocType = ScanDocsQuotes
	rows, err := svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := lmRowsByDoc(rows, fmt.Sprintf("Q-LMS-%s-%d", c.name, tag))
		if len(got) != 1 {
			t.Fatalf("%s: %+v", c.name, got)
		}
		r := got[0]
		if r.Level != c.level || r.ClearedBelowFloor != c.cleared {
			t.Errorf("%s: level %q cleared_below_floor %v, want %q %v (margin %d)", c.name, r.Level, r.ClearedBelowFloor, c.level, c.cleared, r.MarginBps)
		}
		if r.ClearDiscPct == nil || *r.ClearDiscPct != 25 || r.ClearMarginBps == nil || *r.ClearMarginBps != 433 || r.ClearedBy != "Sales Manager, Trenton" {
			t.Errorf("%s: the stamp's own figures are missing: %+v", c.name, r)
		}
	}
	f.ClearedOnly = true
	rows, err = svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		n := len(lmRowsByDoc(rows, fmt.Sprintf("Q-LMS-%s-%d", c.name, tag)))
		if (n == 1) != c.cleared {
			t.Errorf("cleared_only on %s: %d rows, want cleared=%v", c.name, n, c.cleared)
		}
	}

	// The orders those quotes became: same rule through the inherited stamp.
	for i, c := range cases {
		oid := fmt.Sprintf("0d4a0000-0000-4000-9000-00000000051%d", i+1)
		ono := fmt.Sprintf("SO-LMS-%s-%d", c.name, tag)
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, status, fulfillment)
			VALUES ($1, $2, (SELECT id FROM customers WHERE account_no = 'WALK-IN'), $3, $4, 'INVOICED', 'PICKUP')`,
			oid, ono, lmTrenton, quoteIDs[c.name]); err != nil {
			t.Fatal(err)
		}
		oid2 := oid
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, oid2) })
		if _, err := pool.Exec(ctx, `
			INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
			                         unit_price_cents, price_uom, line_total_cents, metadata)
			SELECT $1, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents,
			       CASE WHEN disc_pct > 0 THEN jsonb_build_object('disc_pct', disc_pct) ELSE '{}'::jsonb END
			  FROM quote_lines WHERE quote_id = $2`, oid, quoteIDs[c.name]); err != nil {
			t.Fatal(err)
		}
	}
	f = lmWindow()
	f.DocType = ScanDocsOrders
	rows, err = svc.LowMarginScan(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := lmRowsByDoc(rows, fmt.Sprintf("SO-LMS-%s-%d", c.name, tag))
		if len(got) != 1 || got[0].ClearedBelowFloor != c.cleared || got[0].ClearDiscPct == nil || *got[0].ClearDiscPct != 25 {
			t.Errorf("order %s: %+v, want cleared=%v with the stamp's figures", c.name, got, c.cleared)
		}
	}
}
