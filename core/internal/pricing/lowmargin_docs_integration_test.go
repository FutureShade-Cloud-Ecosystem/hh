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
		Lines: []lmQuoteLine{{GrossUnit: 1000, DiscPct: 15}}}) // net 850 per SF: 1552 bps, below the 2000 floor

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
	if r.DocType != DocTypeQuote || r.UnitPriceCents != 850 || r.MarginBps != 1552 || r.Level != "critical" || r.CostInPriceUOMCents != 718 {
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
	if len(got) != 1 || got[0].UnitPriceCents != 850 || got[0].MarginBps != 1552 || got[0].Level != "critical" {
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
	stamp := fmt.Sprintf(`{"floor_clear":{"cleared_by":%q,"authority":"APPROVAL"}}`, lmManagerUser)
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
