//go:build integration

// counter_till_integration_test.go, the RT-A ruling's integration legs over the kit's fixture
// world: the scratch database named by HH_CORE_TEST_DATABASE_URL, the frozen migration set
// applied (the compfixture discipline, the same gate-variable pattern). Branch A (FXNA) stands
// as Trenton, branch B (FXNB) as Kingston, and the fixture's contractor tenant stands as C01;
// the planted Holland paver stand-in carries a contractor pavers rule of 1800 bps off a 48700
// list, and its category policy carries a 2000 bps floor with enforcement DISABLED, so a gated
// resolve of the line would file a pricing_audit_log BYPASS row while the till's resolve must
// write none. The branch cost stands beside the ruled price as the margin reading; the gate
// reads only a caller supplied cost and the till sends none, so a floor block alone could never
// distinguish the two resolves (the HH-219 history), and the BYPASS row is what bites here.
//
// What is proven here, at Trenton, as Sales: C01's Holland paver line rings at its rule price
// with no block and the sale commits, the ring writes no BYPASS row (held at the database), the
// sale's lines equal the engine's own price, a pricing order for the line is created at the same
// price, and a Kingston login can neither park onto Trenton's list, nor list its tickets, nor
// resume nor discard one (by id the wall answers what a missing ticket answers), while anyone at
// Trenton picks a ticket up, no owner check, who resumed recorded. Run with the variable set:
//
//	HH_CORE_TEST_DATABASE_URL=... go test -tags integration -count=1 ./internal/pos/ -run TestCounterTill
//
// Row hygiene: the planted rows carry fixed ids in the kit's 3ef1 block and are deleted by this
// file alone; the parked tickets carry the notes marker below and are deleted at cleanup; the
// pricing orders delete by their created_by and branch. The ring itself is replay-covered by its
// fixed idempotency key, so a re-run against the same scratch database replays the committed
// sale instead of writing a second one, and the committed sale and its audit and event history
// stay, as history does.
package pos

import (
	"context"
	"os"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The till's own fixture ids, fixed and patterned like the kit's, deleted by this file alone.
const (
	fxTillCategoryID  = "3ef10000-0000-4000-8000-0000000001a1"
	fxTillProductID   = "3ef10000-0000-4000-8000-0000000001a2"
	fxTillRuleID      = "3ef10000-0000-4000-8000-0000000001a3"
	fxKingstonUserID  = "3ef10000-0000-4000-8000-0000000001b1"
	fxKingstonGrantID = "3ef10000-0000-4000-8000-0000000001b2"
	fxKingstonSub     = "fxtill-kingston-sales"
	fxTillSaleKey     = "3ef10000-0000-4000-8000-0000000001c1" // the ring's fixed replay key
	fxTillParkNotes   = "FX counter till fence ticket"
)

// The ruled numbers: 48700 list, less the 1800 bps contractor pavers rule, at 100 SF. The
// policy's floor sits at 2000 bps with enforcement disabled, and the branch cost puts the ruled
// price at 1236 bps, below that floor: the ruled line the till rings, on a category whose gated
// resolve would file a BYPASS row.
const (
	fxHollandListCents int64 = 48700
	fxHollandRuleCents int64 = 39934
	fxHollandLineCents int64 = 3993400
	fxFloorBps         int64 = 2000
	fxBranchCostCents  int64 = 35000
)

// counterTillFixture connects the scratch database, builds the kit's fixture world, plants the
// till's rows and returns the pool. Skips politely when the gate variable is unset.
func counterTillFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testkitDatabaseURL()
	if dsn == "" {
		t.Skip("HH_CORE_TEST_DATABASE_URL is not set: the counter till suites need the scratch database with the frozen migrations applied")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("fixture pool: %v", err)
	}
	t.Cleanup(pool.Close)
	db := testkit.PgxPool(pool)
	if err := testkit.VerifyBaseline(ctx, db); err != nil {
		t.Fatalf("fixture baseline: %v", err)
	}
	if err := testkit.Build(ctx, db); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	plantCounterTillRows(t, pool)
	return pool
}

func testkitDatabaseURL() string { return os.Getenv("HH_CORE_TEST_DATABASE_URL") }

// plantCounterTillRows deletes this file's soft rows and plants its fixed rows again, so a re-run
// against a database the file has filled before is a clean re-plant. The product and its category
// are only ever deleted when nothing references them (a committed sale's order lines keep the
// product alive by design, the ring is replay-covered), so their deletes are best-effort and
// every insert carries ON CONFLICT DO NOTHING.
func plantCounterTillRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	deletes := []string{
		`DELETE FROM user_branch_grants WHERE user_id = '` + fxKingstonUserID + `'`,
		`DELETE FROM staff_role_grant WHERE id = '` + fxKingstonGrantID + `'`,
		`DELETE FROM users WHERE id = '` + fxKingstonUserID + `'`,
		`DELETE FROM pricing_rules WHERE id = '` + fxTillRuleID + `'`,
		`DELETE FROM product_branch_costs WHERE product_id = '` + fxTillProductID + `'`,
		`DELETE FROM pricing_category_policy WHERE category_id = '` + fxTillCategoryID + `'`,
		`DELETE FROM pricing_list_prices WHERE product_id = '` + fxTillProductID + `'`,
		`DELETE FROM product_uoms WHERE product_id = '` + fxTillProductID + `'`,
		`DELETE FROM pos_parked_tickets WHERE notes = '` + fxTillParkNotes + `'`,
	}
	for _, q := range deletes {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture re-plant delete: %v (%s)", err, q)
		}
	}
	// The catalog rows go only when the world lets them go; the inserts below re-plant them
	// otherwise, unchanged, by their fixed ids.
	_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = '`+fxTillProductID+`'`)
	_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE id = '`+fxTillCategoryID+`'`)
	plants := []string{
		// The pavers category, the Holland paver stand-in on it, SF throughout.
		`INSERT INTO product_categories (id, code, name) VALUES ('` + fxTillCategoryID + `', 'FX-PAVERS', 'Fixture Pavers')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO products (id, sku, name, category_id, base_uom, price_uom, org_id) VALUES
			('` + fxTillProductID + `', 'FX-HOLLAND', 'Fixture Holland Paver', '` + fxTillCategoryID + `', 'SF', 'SF', '` + testkit.OrgNorthID + `')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base) VALUES
			('` + fxTillProductID + `', 'SF', 1, TRUE)
			ON CONFLICT DO NOTHING`,
		// The retail anchor and the branch cost (the margin reading beside the ruled price).
		`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom) VALUES
			('` + fxTillProductID + `', 48700, 'SF')
			ON CONFLICT DO NOTHING`,
		// The category floor with enforcement DISABLED: the bite of the whole bench. A gated
		// resolve of this line (margin 1236 bps, below the 2000 bps floor) would file a BYPASS
		// audit row; the till's counter resolve must write none, and the ring asserts that at
		// the database below.
		`INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled) VALUES
			('` + fxTillCategoryID + `', 3000, ` + itoa(int(fxFloorBps)) + `, TRUE)
			ON CONFLICT DO NOTHING`,
		`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents) VALUES
			('` + fxTillProductID + `', '` + testkit.BranchNorthAID + `', ` + itoa(int(fxBranchCostCents)) + `)
			ON CONFLICT DO NOTHING`,
		// C01's contractor pavers rule: 18 percent off, all branches.
		`INSERT INTO pricing_rules (id, scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text, is_active) VALUES
			('` + fxTillRuleID + `', 'TIER_CATEGORY', 'CONTRACTOR', '` + fxTillCategoryID + `', 'PCT_OFF_LIST', 1800, 'SF', 40, 'Fixture contractor pavers rule', TRUE)
			ON CONFLICT DO NOTHING`,
		// The Kingston login: inside_sales at branch B only, the wall's far side.
		`INSERT INTO users (id, email, display_name, status, platform_user_id, org_id) VALUES
			('` + fxKingstonUserID + `', 'fxtill.kingston.sales@fixture.test', 'Fixture Kingston Sales', 'ACTIVE', '` + fxKingstonSub + `', '` + testkit.OrgNorthID + `')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id) VALUES
			('` + fxKingstonGrantID + `', '` + fxKingstonSub + `', 'inside_sales', '` + testkit.OrgNorthID + `', '` + testkit.BranchNorthBID + `')
			ON CONFLICT DO NOTHING`,
		`INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES
			('` + fxKingstonUserID + `', '` + testkit.BranchNorthBID + `', '` + testkit.OrgNorthID + `')
			ON CONFLICT DO NOTHING`,
	}
	for _, q := range plants {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture plant: %v (%s)", err, q)
		}
	}
	// A product that survived a cleanup keeps whatever category a past delete left it pointing
	// at (the category's delete SET NULLs the column), and a rule keyed by the category is only
	// as good as that pointer: re-point it on every plant.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET category_id = '`+fxTillCategoryID+`' WHERE id = '`+fxTillProductID+`'`); err != nil {
		t.Fatalf("fixture plant re-point: %v", err)
	}
	t.Cleanup(func() {
		cleanups := []string{
			`DELETE FROM pos_parked_tickets WHERE notes = '` + fxTillParkNotes + `'`,
			`DELETE FROM pricing_order_lines WHERE pricing_order_id IN
				(SELECT id FROM pricing_orders WHERE branch_id = '` + testkit.BranchNorthAID + `' AND created_by = '` + testkit.UserInsideSalesID + `')`,
			`DELETE FROM pricing_orders WHERE branch_id = '` + testkit.BranchNorthAID + `' AND created_by = '` + testkit.UserInsideSalesID + `'`,
			`DELETE FROM user_branch_grants WHERE user_id = '` + fxKingstonUserID + `'`,
			`DELETE FROM staff_role_grant WHERE id = '` + fxKingstonGrantID + `'`,
			`DELETE FROM users WHERE id = '` + fxKingstonUserID + `'`,
			`DELETE FROM pricing_rules WHERE id = '` + fxTillRuleID + `'`,
			`DELETE FROM product_branch_costs WHERE product_id = '` + fxTillProductID + `'`,
			`DELETE FROM pricing_category_policy WHERE category_id = '` + fxTillCategoryID + `'`,
			`DELETE FROM pricing_list_prices WHERE product_id = '` + fxTillProductID + `'`,
			`DELETE FROM product_uoms WHERE product_id = '` + fxTillProductID + `'`,
			`DELETE FROM products WHERE id = '` + fxTillProductID + `'`,
			`DELETE FROM product_categories WHERE id = '` + fxTillCategoryID + `'`,
		}
		for _, q := range cleanups {
			_, _ = pool.Exec(context.Background(), q)
		}
	})
}

// tillStaffCtx stamps a guard-shaped caller for a named fixture user, the way the RequireStaff
// guard stamps any request.
func tillStaffCtx(userID, roleCode string, tier identity.Tier, branches ...string) context.Context {
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: userID, RoleCode: roleCode, Tier: tier, Scope: identity.ScopeBranch,
		Status: "ACTIVE", Branches: granted,
	})
}

// tillSalesCtx is the Sales login at Trenton (branch A); tillKingstonCtx the Sales login at
// Kingston (branch B); tillManagerCtx the Sales manager at Trenton.
func tillSalesCtx() context.Context {
	return tillStaffCtx(testkit.UserInsideSalesID, "INSIDE_SALES", identity.TierFrontline, testkit.BranchNorthAID)
}

func tillKingstonCtx() context.Context {
	return tillStaffCtx(fxKingstonUserID, "INSIDE_SALES", identity.TierFrontline, testkit.BranchNorthBID)
}

func tillManagerCtx() context.Context {
	return tillStaffCtx(testkit.UserSalesMgrID, "SALES_MANAGER", identity.TierManager, testkit.BranchNorthAID)
}

// counterTillPricing is the same pricing authority realPosSvc wires, for holding the committed
// line against the engine's own answer.
func counterTillPricing(pool *pgxpool.Pool) *pricing.Service {
	cat := product.NewService(product.NewRepository(pool))
	return pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(cat).
		WithUOMConverter(cat).
		WithMarginGate(pricing.NewIdentityMarginGate())
}

// As Sales at Trenton: C01's Holland paver line rings at its rule price below the category
// floor, the sale commits, the ring files no BYPASS audit row though the category's enforcement
// is disabled (the gated resolve of this line would file one, so this is what distinguishes the
// counter resolve from the gated one at the database), and the committed line equals the
// engine's own price for the line.
func TestCounterTill_RingsRulePriceBelowFloor(t *testing.T) {
	pool := counterTillFixture(t)
	svc := realPosSvc(t, pool, stubCharger{})
	var bypassBefore int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_audit_log WHERE action = 'BYPASS' AND entity_type = 'margin_enforcement'
		 AND entity_id IN ($1, $2)`, fxTillCategoryID, fxTillProductID).Scan(&bypassBefore); err != nil {
		t.Fatalf("count the BYPASS rows before the ring: %v", err)
	}
	out, err := svc.Sale(tillSalesCtx(), SaleInput{
		IdempotencyKey: fxTillSaleKey, BranchID: testkit.BranchNorthAID,
		CustomerID: testkit.TenantAlphaID, Tender: TenderCash, SaleType: SaleCollectNow,
		TillSessionID: openTill(t, pool, testkit.BranchNorthAID),
		Lines:         []LineInput{{ProductID: fxTillProductID, QtyEntered: 100, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("the till rings the ruled line unblocked: %v", err)
	}
	if out.Status != "COMMITTED" {
		t.Fatalf("the ruled line commits: %+v", out)
	}
	if out.SubtotalCents != fxHollandLineCents {
		t.Fatalf("subtotal at the rule price %d, got %d", fxHollandLineCents, out.SubtotalCents)
	}
	var unit, total int64
	if err := pool.QueryRow(context.Background(),
		`SELECT unit_price_cents, line_total_cents FROM order_lines WHERE order_id=$1 AND line_no=1`, out.OrderID).
		Scan(&unit, &total); err != nil {
		t.Fatalf("read the committed line: %v", err)
	}
	if unit != fxHollandRuleCents || total != fxHollandLineCents {
		t.Fatalf("committed line %d/%d, want %d/%d", unit, total, fxHollandRuleCents, fxHollandLineCents)
	}
	// The sale's lines equal the engine's price: the same line through the same counter resolve.
	lp, err := counterTillPricing(pool).DeriveCounterLinePrice(context.Background(), pricing.ResolveInput{
		CustomerID: testkit.TenantAlphaID, CustomerType: "CONTRACTOR", ProductID: fxTillProductID,
		BranchID: testkit.BranchNorthAID, QtyEntered: 100, UOMEntered: "SF",
	})
	if err != nil {
		t.Fatalf("counter resolve: %v", err)
	}
	if lp.UnitPriceCents != unit || lp.LineTotalCents != total {
		t.Fatalf("the engine prices the line %d/%d, the sale rang %d/%d",
			lp.UnitPriceCents, lp.LineTotalCents, unit, total)
	}
	if lp.Breakdown.Blocked {
		t.Fatalf("the engine's counter resolve does not block: %+v", lp.Breakdown)
	}
	// The bite at the database: the ring through the real surface wrote no BYPASS row for this
	// category or product, though the policy's enforcement is disabled and a gated resolve of
	// the line would have filed one.
	var bypassAfter int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pricing_audit_log WHERE action = 'BYPASS' AND entity_type = 'margin_enforcement'
		 AND entity_id IN ($1, $2)`, fxTillCategoryID, fxTillProductID).Scan(&bypassAfter); err != nil {
		t.Fatalf("count the BYPASS rows after the ring: %v", err)
	}
	if bypassAfter != bypassBefore {
		t.Fatalf("the till's ring must write no BYPASS row: before %d, after %d", bypassBefore, bypassAfter)
	}
}

// A pricing order for the same line is created, at the rule price against the 48700 list.
func TestCounterTill_PricingOrder_CreatedAtRulePrice(t *testing.T) {
	pool := counterTillFixture(t)
	svc := realPosSvc(t, pool, stubCharger{})
	po, err := svc.CreatePricingOrder(tillSalesCtx(), PricingOrderInput{
		BranchID: testkit.BranchNorthAID, CustomerID: testkit.TenantAlphaID, CustomerName: "Fixture C01",
		Lines: []PricingOrderLineInput{{ProductID: fxTillProductID, QtyEntered: 100, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("pricing order created: %v", err)
	}
	if po.DocNo == "" || len(po.Lines) != 1 {
		t.Fatalf("the pricing order carries its line: %+v", po)
	}
	ln := po.Lines[0]
	if ln.UnitPriceCents != fxHollandRuleCents || ln.ListPriceCents != fxHollandListCents || ln.LineTotalCents != fxHollandLineCents {
		t.Fatalf("list-vs-offered at the rule price: %+v", ln)
	}
	if po.SubtotalCents != fxHollandLineCents {
		t.Fatalf("subtotal %d, want %d", po.SubtotalCents, fxHollandLineCents)
	}
}

// The parked fence at Trenton: a Kingston login can neither park onto Trenton's list, nor list
// its tickets, nor resume nor discard one (by id the wall answers what a missing ticket answers);
// anyone at Trenton parks and picks up any ticket, no owner check, and who resumed stays recorded.
func TestCounterTill_ParkedFence_KingstonOutOfTrenton(t *testing.T) {
	pool := counterTillFixture(t)
	svc := realPosSvc(t, pool, stubCharger{})
	// Parking is a write across the wall too: Kingston cannot plant a ticket on Trenton's list.
	if _, err := svc.Park(tillKingstonCtx(), ParkInput{
		BranchID: testkit.BranchNorthAID, Notes: "must never land",
		Lines: []ParkedLineInput{{ProductID: fxTillProductID, QtyEntered: 1, UOMEntered: "SF"}},
	}); !isForbidden(err) {
		t.Fatalf("a Kingston login cannot park onto Trenton's list, got %v", err)
	}
	// A Trenton login parks at its own branch: the ticket the fence protects below.
	t1, err := svc.Park(tillSalesCtx(), ParkInput{
		BranchID: testkit.BranchNorthAID, CustomerID: testkit.TenantAlphaID, Notes: fxTillParkNotes,
		Lines: []ParkedLineInput{{ProductID: fxTillProductID, QtyEntered: 12, UOMEntered: "SF"}},
	})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	// Kingston, on the wall's far side: the list refused, the by-id verbs answered what a
	// missing ticket answers, all before any read a ticket could leak or a write could move.
	if _, err := svc.ListParked(tillKingstonCtx(), testkit.BranchNorthAID); !isForbidden(err) {
		t.Fatalf("a Kingston login cannot list Trenton's tickets, got %v", err)
	}
	if _, err := svc.ResumeParked(tillKingstonCtx(), t1.ID); !isNotFound(err) {
		t.Fatalf("a ticket behind the wall answers what a missing ticket answers, got %v", err)
	}
	if err := svc.DiscardParked(tillKingstonCtx(), t1.ID); !isNotFound(err) {
		t.Fatalf("a ticket behind the wall answers what a missing ticket answers, got %v", err)
	}
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM pos_parked_tickets WHERE id=$1`, t1.ID).Scan(&status); err != nil || status != "PARKED" {
		t.Fatalf("the fenced ticket is intact: %s %v", status, err)
	}
	var planted int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_parked_tickets WHERE notes = 'must never land'`).Scan(&planted); err != nil || planted != 0 {
		t.Fatalf("the foreign park must never land on Trenton's list: %d %v", planted, err)
	}
	// Anyone at Trenton picks it up: the sales manager, not who parked it, resumes it.
	list, err := svc.ListParked(tillManagerCtx(), testkit.BranchNorthAID)
	if err != nil {
		t.Fatalf("Trenton lists its own tickets: %v", err)
	}
	found := false
	for _, tk := range list {
		if tk.ID == t1.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the parked ticket is on Trenton's list: %+v", list)
	}
	res, err := svc.ResumeParked(tillManagerCtx(), t1.ID)
	if err != nil || res.Status != "RESUMED" {
		t.Fatalf("anyone at the branch picks up any parked sale: %v %+v", err, res)
	}
	var resumedBy string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(resumed_by::text,'') FROM pos_parked_tickets WHERE id=$1`, t1.ID).Scan(&resumedBy); err != nil {
		t.Fatalf("read resumed_by: %v", err)
	}
	if resumedBy != testkit.UserSalesMgrID {
		t.Fatalf("who resumed stays recorded: %q", resumedBy)
	}
}
