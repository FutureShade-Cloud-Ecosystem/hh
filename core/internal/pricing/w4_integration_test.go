//go:build integration

package pricing

// W4-A integration gates. These prove the things a DB-free fake CANNOT: that the real CHECKs accept
// what the service writes and reject what it must not, that the 0112 unique index actually
// deduplicates a retried accrual (the fake only emulates it), and that the E3 document ref survives
// the whole round trip, service write → pricing_audit_log row → ListAudit read → JSON projection.
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/pricing/
//
// NO DIB-396 LEASE IS TAKEN HERE, deliberately. The lease exists for fixtures that transiently
// break a WHOLE-DB invariant (the stock-drift key 3960001) or for assertions that read one. Nothing
// below touches stock_moves, stock_balances or payment_config: every fixture inserts into
// pricing-owned tables, asserts on rows it created by id, and deletes exactly those rows in a
// t.Cleanup. There is no window in which another package could observe a broken global invariant,
// so taking a lease would add contention without protecting anything.
//
// FIXTURE DISCIPLINE: every row created here is deleted by id on cleanup. Nothing truncates.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// w4Fixture grabs a real seeded product, its category, a branch and a vendor.
type w4Fixture struct {
	productID  string
	categoryID string
	baseUOM    string
	branchID   string
	vendorID   string
	customerID string
}

func newW4Fixture(t *testing.T, pool *pgxpool.Pool) w4Fixture {
	t.Helper()
	ctx := context.Background()
	var f w4Fixture
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code='PAVER' LIMIT 1`).Scan(&f.productID, &f.categoryID, &f.baseUOM); err != nil {
		t.Skipf("no PAVER product seeded: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code='KGN' LIMIT 1`).Scan(&f.branchID); err != nil {
		t.Skipf("no KGN branch seeded: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors LIMIT 1`).Scan(&f.vendorID); err != nil {
		t.Skipf("no vendor seeded: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&f.customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	return f
}

// - DIB-70: the promo window is enforced by the SQL, not only by the Go guard -

func TestIntegration_W4_ActivePromotionsRespectsTheWindowInSQL(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	repo := NewRepository(pool)

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_promotions (code, name, category_id, starts_at, ends_at, calc_type, discount_bps)
		VALUES ('IT-W4-JUNE','IT June Sale',$1,$2,$3,'PCT_OFF',2000) RETURNING id::text`,
		f.categoryID, start, end).Scan(&id); err != nil {
		t.Fatalf("seed promo: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_promotions WHERE id=$1`, id) })

	// The Go-side boundary tests pin withinWindow. This pins the WHERE clause, which is a separate
	// piece of code that could disagree with it, and if it did, a promo would be fetched-then-
	// discarded (harmless) or discarded-then-never-considered (a silently missing sale price).
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before the start", start.Add(-time.Second), false},
		{"at the start", start, true},
		{"mid-window", start.AddDate(0, 0, 14), true},
		{"one second before the end", end.Add(-time.Second), true},
		{"at the end", end, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := repo.ActivePromotions(ctx, f.productID, f.categoryID, f.branchID, c.at)
			if err != nil {
				t.Fatalf("ActivePromotions: %v", err)
			}
			found := false
			for _, p := range got {
				if p.ID == id {
					found = true
				}
			}
			if found != c.want {
				t.Fatalf("promo in window = %v, want %v", found, c.want)
			}
		})
	}
}

func TestIntegration_W4_PromoCHECKsRejectMalformedRows(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Each of these is a shape the service's validation also rejects. Pinning them at the DB too is
	// what makes the validation a CONVENIENCE (a friendly 422) rather than the only thing standing
	// between a hand-run SQL script and an unpriceable promo.
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"ends before starts",
			`INSERT INTO pricing_promotions (code,name,starts_at,ends_at,calc_type,discount_bps)
			 VALUES ('IT-W4-BAD1','bad',$1,$1 - interval '1 day','PCT_OFF',1000)`,
			[]any{start},
		},
		{
			"product AND category",
			`INSERT INTO pricing_promotions (code,name,product_id,category_id,starts_at,ends_at,calc_type,discount_bps)
			 VALUES ('IT-W4-BAD2','bad',$1,$2,$3,$3 + interval '1 day','PCT_OFF',1000)`,
			[]any{f.productID, f.categoryID, start},
		},
		{
			"PCT_OFF carrying a fixed price too",
			`INSERT INTO pricing_promotions (code,name,starts_at,ends_at,calc_type,discount_bps,price_cents,price_uom)
			 VALUES ('IT-W4-BAD3','bad',$1,$1 + interval '1 day','PCT_OFF',1000,500,$2)`,
			[]any{start, f.baseUOM},
		},
		{
			"FIXED_PRICE with no price_uom",
			`INSERT INTO pricing_promotions (code,name,starts_at,ends_at,calc_type,price_cents)
			 VALUES ('IT-W4-BAD4','bad',$1,$1 + interval '1 day','FIXED_PRICE',500)`,
			[]any{start},
		},
		{
			"a discount over 100%",
			`INSERT INTO pricing_promotions (code,name,starts_at,ends_at,calc_type,discount_bps)
			 VALUES ('IT-W4-BAD5','bad',$1,$1 + interval '1 day','PCT_OFF',10001)`,
			[]any{start},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, c.sql, c.args...); err == nil {
				_, _ = pool.Exec(ctx, `DELETE FROM pricing_promotions WHERE code LIKE 'IT-W4-BAD%'`)
				t.Fatalf("the database must refuse this row")
			}
		})
	}
}

// - DIB-154: clearance round-trip through the real resolver -

func TestIntegration_W4_ClearanceBadgeAndPriceReachTheBreakdown(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	svc := realSvc(pool)
	withAnchor(t, pool, f.productID, 10000, f.baseUOM)

	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_clearance_items (product_id, reason, clearance_price_cents, price_uom)
		VALUES ($1,'IT discontinued colour',6500,$2)
		ON CONFLICT (product_id) DO UPDATE SET reason=EXCLUDED.reason,
		  clearance_price_cents=EXCLUDED.clearance_price_cents, price_uom=EXCLUDED.price_uom, is_active=TRUE`,
		f.productID, f.baseUOM); err != nil {
		t.Fatalf("seed clearance: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_clearance_items WHERE product_id=$1`, f.productID) })

	bd, err := svc.ResolvePricePreview(ctx, ResolveInput{
		ProductID: f.productID, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: f.baseUOM, BranchID: f.branchID,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !bd.Clearance || bd.ClearanceReason != "IT discontinued colour" {
		t.Fatalf("the clearance badge + reason must reach the breakdown, got %+v", bd)
	}
	if bd.FinalCents != 6500 {
		t.Fatalf("the clearance price must be the base: want 6500, got %d (source %s)", bd.FinalCents, bd.Source)
	}
}

// - DIB-69: the AT-MOST-ONCE guard is a DATABASE constraint -

func TestIntegration_W4_RebateAccrualIsDeduplicatedByTheUniqueIndex(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	svc := realSvc(pool)

	var progID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO rebate_programs (code,name,vendor_id,category_id,basis,rate_bps,starts_at)
		VALUES ('IT-W4-REBATE','IT rebate',$1,$2,'PCT_OF_SALE',250, now() - interval '1 day')
		RETURNING id::text`, f.vendorID, f.categoryID).Scan(&progID); err != nil {
		t.Fatalf("seed program: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rebate_programs WHERE id=$1`, progID) })

	// A stable, unique source id so the test is re-runnable without tripping over its own history.
	var sourceID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&sourceID); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rebate_accruals WHERE source_id=$1`, sourceID) })

	ev := RebateSaleEvent{
		SourceType: RebateSourcePOS, SourceID: sourceID, OccurredAt: time.Now(),
		Lines: []RebateSaleLine{{
			ProductID: f.productID, CategoryID: f.categoryID, VendorID: f.vendorID,
			QtyBase: 10, SaleCents: 100000, ExtCostCents: 60000, BranchID: f.branchID,
		}},
	}

	first, err := svc.AccrueSale(ctx, ev)
	if err != nil {
		t.Fatalf("first accrual: %v", err)
	}
	if len(first.Accrued) != 1 || first.TotalCents != 2500 {
		t.Fatalf("2.50%% of $1000 = $25.00 on one row, got %+v", first)
	}

	// THE POINT OF THIS TEST. The fake store emulates the unique index; only the real database
	// PROVES it. If ux_rebate_accrual_source were dropped or mis-specified, the Go code would
	// happily insert a second claim and the fake would never notice.
	second, err := svc.AccrueSale(ctx, ev)
	if err != nil {
		t.Fatalf("a retried delivery must not error: %v", err)
	}
	if second.SkippedCount != 1 || len(second.Accrued) != 0 {
		t.Fatalf("the retry must be absorbed, got %+v", second)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM rebate_accruals WHERE source_id=$1`, sourceID).Scan(&n); err != nil {
		t.Fatalf("count accruals: %v", err)
	}
	if n != 1 {
		t.Fatalf("exactly one claim may exist for one sale line; found %d", n)
	}

	// And the per-vendor ledger reads it back with its totals.
	led, err := svc.VendorRebateLedger(ctx, f.vendorID, nil, nil)
	if err != nil {
		t.Fatalf("VendorRebateLedger: %v", err)
	}
	found := false
	for _, r := range led.Rows {
		if r.SourceID == sourceID {
			found = true
			if r.AccrualCents != 2500 || r.ProgramCode != "IT-W4-REBATE" {
				t.Fatalf("the ledger row must carry the claim and its program code, got %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("the accrual must appear in its vendor's ledger")
	}
}

func TestIntegration_W4_ANegativeAccrualIsOnlyLegalOnAnAdjustment(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)

	var progID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO rebate_programs (code,name,vendor_id,basis,rate_bps,starts_at)
		VALUES ('IT-W4-NEG','IT neg',$1,'PCT_OF_SALE',250, now() - interval '1 day')
		RETURNING id::text`, f.vendorID).Scan(&progID); err != nil {
		t.Fatalf("seed program: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rebate_programs WHERE id=$1`, progID) })

	// A negative claim on a SALE is refused by the CHECK, that is what forces a clawback through
	// the ADJUSTMENT path, where a human signs for it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO rebate_accruals (program_id,vendor_id,product_id,source_type,source_id,qty_base,basis_cents,accrual_cents)
		VALUES ($1,$2,$3,'POS_SALE',gen_random_uuid(),1,1000,-100)`, progID, f.vendorID, f.productID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM rebate_accruals WHERE program_id=$1`, progID)
		t.Fatal("a negative accrual on a POS_SALE must violate the CHECK")
	}
	// The same negative claim as an ADJUSTMENT is legal.
	if _, err := pool.Exec(ctx, `
		INSERT INTO rebate_accruals (program_id,vendor_id,product_id,source_type,qty_base,basis_cents,accrual_cents)
		VALUES ($1,$2,$3,'ADJUSTMENT',1,1000,-100)`, progID, f.vendorID, f.productID); err != nil {
		t.Fatalf("a negative ADJUSTMENT must be accepted: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM rebate_accruals WHERE program_id=$1`, progID)
}

// - M2-uom: the seeded 0113 graph actually plans a real SKU -

func TestIntegration_W4_UOMPlanUsesTheSeededConversionGraph(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	// Pick a product the migration actually seeded a SF→PLT edge for, rather than assuming one.
	var productID string
	if err := pool.QueryRow(ctx, `
		SELECT product_id::text FROM pricing_uom_conversions
		 WHERE from_uom='SF' AND to_uom='PLT' AND is_active AND round_up_at_pick
		 ORDER BY product_id LIMIT 1`).Scan(&productID); err != nil {
		t.Skipf("migration 0113 seeded no SF→PLT edges: %v", err)
	}
	var factor float64
	if err := pool.QueryRow(ctx, `
		SELECT factor::float8 FROM pricing_uom_conversions
		 WHERE product_id=$1 AND from_uom='SF' AND to_uom='PLT' AND is_active`, productID).Scan(&factor); err != nil {
		t.Fatalf("read factor: %v", err)
	}

	p, err := svc.PlanUOM(ctx, UOMPlanInput{ProductID: productID, QtyEntered: 77, UOMEntered: "SF", WeightUOM: "T"})
	if err != nil {
		t.Fatalf("PlanUOM: %v", err)
	}
	if p.PickUOM != "PLT" {
		t.Fatalf("the pick unit must be discovered from the seeded round_up edge, got %q", p.PickUOM)
	}
	exact := 77 * factor
	wantPick := float64(int64(exact))
	if wantPick < exact {
		wantPick++
	}
	if p.PickQty != wantPick {
		t.Fatalf("77 SF × %v = %v pallets → %v picks, got %v", factor, exact, wantPick, p.PickQty)
	}
	if p.PickQty < p.PickQtyExact {
		t.Fatalf("a pick quantity may NEVER be less than the exact requirement: %v < %v", p.PickQty, p.PickQtyExact)
	}
	// 0113 writes every row as SEEDED_DEFAULT, so a plan built on it must declare itself
	// unconfirmed. If someone later confirms the factors, THIS assertion is the one to update -
	// deliberately, with the source column.
	if !p.Unconfirmed {
		t.Fatalf("a plan resting on seeded factors must be flagged unconfirmed")
	}
	if p.WeightUOM != "T" || p.WeightQty <= 0 {
		t.Fatalf("the SF→PLT→T chain must produce a weight, got %+v", p)
	}
}

// - E3 unpark: the document ref round-trips -

func TestIntegration_W4_E3_BypassRowCarriesTheQuoteDrillThrough(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	svc := realSvc(pool)
	withAnchor(t, pool, f.productID, 10000, f.baseUOM)

	// A real quote id, so the drill-through points at something a reader can actually open.
	var quoteID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM quotes ORDER BY created_at DESC LIMIT 1`).Scan(&quoteID); err != nil {
		t.Skipf("no quote seeded: %v", err)
	}

	// Force a BYPASS: disable margin enforcement on the product for the duration of the test.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_product_policy (product_id, margin_enforcement_disabled)
		VALUES ($1, TRUE)
		ON CONFLICT (product_id) DO UPDATE SET margin_enforcement_disabled=TRUE`, f.productID); err != nil {
		t.Skipf("cannot disable product enforcement: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_product_policy WHERE product_id=$1`, f.productID) })
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_type='margin_enforcement' AND ref_entity_id=$1`, quoteID)
	})

	bd, err := svc.ResolvePrice(ctx, ResolveInput{
		ProductID: f.productID, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: f.baseUOM,
		BranchID: f.branchID, CostCents: 9900,
		DocRefType: RefQuote, DocRefID: quoteID,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if bd.BypassReason != BypassProductPolicy {
		t.Fatalf("the breakdown must name the bypass reason, got %q", bd.BypassReason)
	}

	// The row landed, with BOTH the document drill-through and the reason. This is the assertion
	// that proves migration 0114 (widened CHECK + bypass_reason column) and the Go write agree -
	// if the CHECK rejected the row, AppendAudit's discarded error would have hidden it and the
	// count below would be 0.
	var refType, refID, reason string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(ref_entity_type,''), COALESCE(ref_entity_id::text,''), COALESCE(bypass_reason,'')
		  FROM pricing_audit_log
		 WHERE entity_type='margin_enforcement' AND action='BYPASS' AND ref_entity_id=$1
		 ORDER BY created_at DESC LIMIT 1`, quoteID).Scan(&refType, &refID, &reason); err != nil {
		t.Fatalf("the BYPASS row must exist with a quote ref: %v", err)
	}
	if refType != RefQuote || refID != quoteID {
		t.Fatalf("want a quote drill-through, got (%q,%q)", refType, refID)
	}
	if reason != BypassProductPolicy {
		t.Fatalf("want bypass_reason=%s, got %q", BypassProductPolicy, reason)
	}

	// And the viewer's read projects both fields back out, the half a UI actually consumes.
	rows, err := svc.ListAudit(ctx, AuditFilter{EntityType: "margin_enforcement", Action: "BYPASS", Limit: 50})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	seen := false
	for _, r := range rows {
		if r.RefEntityID == quoteID {
			seen = true
			if r.RefEntityType != RefQuote || r.BypassReason != BypassProductPolicy {
				t.Fatalf("the read projection must carry both fields, got %+v", r)
			}
		}
	}
	if !seen {
		t.Fatalf("the quote-referenced BYPASS row must come back out of ListAudit")
	}
}

func TestIntegration_W4_E3_BypassReasonIsRefusedOnANonBypassRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)

	// pricing_audit_bypass_reason_scope. Without it a CREATE row could acquire a reason and the
	// "how many lines bypassed the floor, and why" GROUP BY would double-count.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_audit_log (entity_type, entity_id, action, bypass_reason)
		VALUES ('pricing_rule',$1,'CREATE','PRODUCT_POLICY_DISABLED')`, f.productID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1 AND bypass_reason IS NOT NULL`, f.productID)
		t.Fatal("a bypass_reason on a non-BYPASS row must violate pricing_audit_bypass_reason_scope")
	}
	// An unknown reason is refused too.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_audit_log (entity_type, entity_id, action, bypass_reason)
		VALUES ('margin_enforcement',$1,'BYPASS','BECAUSE_I_SAID_SO')`, f.productID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1 AND bypass_reason IS NOT NULL`, f.productID)
		t.Fatal("an unknown bypass_reason must violate pricing_audit_bypass_reason_known")
	}
}

// - promo + contract, end to end through the real repository -

func TestIntegration_W4_ContractPriceStillBeatsALivePromo(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newW4Fixture(t, pool)
	svc := realSvc(pool)
	withAnchor(t, pool, f.productID, 10000, f.baseUOM)

	var promoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_promotions (code,name,category_id,starts_at,ends_at,calc_type,discount_bps)
		VALUES ('IT-W4-LIVE','IT live sale',$1, now() - interval '1 day', now() + interval '30 days','PCT_OFF',5000)
		RETURNING id::text`, f.categoryID).Scan(&promoID); err != nil {
		t.Fatalf("seed promo: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_promotions WHERE id=$1`, promoID) })

	var ruleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_id, product_id, calc_type, price_cents, price_uom, priority, detail_text)
		VALUES ('CUSTOMER_PRODUCT',$1,$2,'ABSOLUTE',7000,$3,91,'IT W4 contract')
		RETURNING id::text`, f.customerID, f.productID, f.baseUOM).Scan(&ruleID); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, ruleID) })
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1`, ruleID) })

	// Retail, no contract: the 50%-off sale prices the line.
	retail, err := svc.ResolvePricePreview(ctx, ResolveInput{
		ProductID: f.productID, CustomerType: "RETAIL", QtyEntered: 1, UOMEntered: f.baseUOM, BranchID: f.branchID,
	})
	if err != nil {
		t.Fatalf("retail resolve: %v", err)
	}
	if retail.Source != SourcePromotion || retail.FinalCents != 5000 {
		t.Fatalf("a walk-in gets the sale: want PROMOTION/5000, got %s/%d", retail.Source, retail.FinalCents)
	}

	// The contract customer keeps their negotiated price even though the sale is cheaper. That is
	// the ruling: a contract is a promise, in BOTH directions.
	contract, err := svc.ResolvePricePreview(ctx, ResolveInput{
		CustomerID: f.customerID, CustomerType: "CONTRACTOR", ProductID: f.productID,
		QtyEntered: 1, UOMEntered: f.baseUOM, BranchID: f.branchID,
	})
	if err != nil {
		t.Fatalf("contract resolve: %v", err)
	}
	if contract.Source != SourceCustomerProduct || contract.FinalCents != 7000 {
		t.Fatalf("the contract price is final: want CUSTOMER_PRODUCT_RULE/7000, got %s/%d", contract.Source, contract.FinalCents)
	}
}
