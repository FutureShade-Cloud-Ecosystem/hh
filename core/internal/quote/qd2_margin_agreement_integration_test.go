//go:build integration

// qd2_margin_agreement_integration_test.go, the margin agreement pin (plan
// 3.5, the 0.5 item): the margin the quote screen reads (the QuoteViewLine
// marks and the margin glance) and the margin the floor decision uses are
// the same figure for the same line. The service is composed over the REAL
// pricing service (gate and UOM converter wired, the production shape) and
// the REAL inventory cost basis, so the agreement is between two live
// reads: the glance's WAC join over product_branch_costs and the decision's
// inventory CurrentCostCents, both against the same stored line.
//
// The figures are pinned as literals, not recomputed from the seed, so a
// drift on either side fails the test rather than echoing the other side:
// with the weighted average cost at 500 cents, a 550 cent rule price is
// ((550-500)*10000)/550 = 909 bps (below the 1200 bps default floor), and
// a 1000 cent rule price discounted 10 percent nets 900 cents at
// ((900-500)*10000)/900 = 4444 bps.
package quote

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd2SeedMarginQuote seeds alice's Trenton quote with the two lines the
// agreement is pinned on: line 1 at a 550 cent rule price (below the floor
// at its rule price, CP0-3's case) and line 2 at a 1000 cent rule price
// carrying a 10 percent discount. The product's weighted average cost at
// the branch is set to 500 cents and restored afterwards.
func qd2SeedMarginQuote(t *testing.T, pool *pgxpool.Pool, alice string) (quoteID, prod, uom string) {
	t.Helper()
	ctx := context.Background()
	prod, uom = anchoredProduct(t, pool)
	tag := time.Now().UnixNano()

	// The one cost row both reads strike over, restored to what it was.
	var hadCost *int64
	if err := pool.QueryRow(ctx,
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`,
		prod, intTrenton).Scan(&hadCost); err != nil {
		t.Fatalf("read the standing cost row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, updated_at)
		VALUES ($1::uuid, $2::uuid, 500, now())
		ON CONFLICT (product_id, branch_id) DO UPDATE SET avg_unit_cost_cents = 500`, prod, intTrenton); err != nil {
		t.Fatalf("seed the cost row: %v", err)
	}
	t.Cleanup(func() {
		if hadCost == nil {
			_, _ = pool.Exec(ctx,
				`DELETE FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`, prod, intTrenton)
			return
		}
		_, _ = pool.Exec(ctx, `
			UPDATE product_branch_costs SET avg_unit_cost_cents = $3
			WHERE product_id = $1 AND branch_id = $2`, prod, intTrenton, *hadCost)
	})

	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type) VALUES ($1::citext,$1::text,'CONTRACTOR') RETURNING id::text`,
		fmt.Sprintf("QD2-%d", tag)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, salesperson_id,
		                    subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', $4::uuid, 2900, 0, 2900) RETURNING id::text`,
		fmt.Sprintf("#Q-QD2-%d", tag), cust, intTrenton, alice).Scan(&quoteID); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	for _, ln := range []struct {
		no, disc int
		unit     int64
	}{
		{no: 1, unit: 550, disc: 0},
		{no: 2, unit: 1000, disc: 10},
	} {
		gross := ln.unit * 2
		net := (gross*int64(100-ln.disc) + 50) / 100
		if _, err := pool.Exec(ctx, `
			INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered,
			                         qty_base, unit_price_cents, price_uom, line_total_cents, disc_pct,
			                         pre_discount_total_cents)
			VALUES ($1::uuid, $2, $3::uuid, 'qd2 margin line', 2, $4, 2, $5, $4, $6, $7, $8)`,
			quoteID, ln.no, prod, uom, ln.unit, net, ln.disc, gross); err != nil {
			t.Fatalf("seed line %d: %v", ln.no, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_lines WHERE quote_id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, quoteID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return quoteID, prod, uom
}

func TestQD2_MarginAgreement_ScreenAndFloorDecision(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	quoteID, prod, uom := qd2SeedMarginQuote(t, pool, alice)

	// The production shape: the real pricing service (category resolver,
	// UOM converter, identity margin gate) as the floor arbiter, the real
	// inventory service as the cost basis.
	productSvc := product.NewService(product.NewRepository(pool))
	realPricing := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(productSvc).
		WithUOMConverter(productSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	invSvc := inventory.NewService(inventory.NewRepository(pool))
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	svc := NewStaffService(repo).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil)).
		WithAuditTrail(audit.NewService(audit.NewRepository(pool), nil)).
		WithPricing(qd2Pricer{}).
		WithCostBasis(invSvc).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		WithFloorArbiter(realPricing).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

	// The screen's margin glance.
	glance, err := svc.MarginGlance(ctx, quoteID)
	if err != nil {
		t.Fatalf("glance: %v", err)
	}
	if len(glance.Lines) != 2 {
		t.Fatalf("two lines seeded, got %+v", glance.Lines)
	}
	if glance.Lines[0].MarginBps == nil || glance.Lines[1].MarginBps == nil {
		t.Fatalf("both lines carry a cost row, neither margin may be absent, got %+v", glance.Lines)
	}

	// Line 1, at its rule price (the CP0-3 case): the glance's margin and
	// the floor question's margin at the same price agree, and the read's
	// rule_price_below_floor mark says what the floor question says. The
	// cost is read through the inventory seam, the same read the decision
	// makes, so the pin is about the two paths agreeing, not about a
	// hardcoded figure.
	cost, found, err := invSvc.CurrentCostCents(ctx, prod, intTrenton)
	if err != nil || !found {
		t.Fatalf("the cost basis the decision reads: found=%v err=%v", found, err)
	}
	atRule, err := realPricing.EvaluateLineFloor(ctx, pricing.FloorQuestion{
		CustomerID: quoteCustomer(t, pool, quoteID), CustomerType: "CONTRACTOR",
		ProductID: prod, BranchID: intTrenton,
		QtyEntered: 2, UOMEntered: uom, CostCents: cost,
		RuleUnitCents: 550, CandidateUnitCents: 550, PriceUOM: uom,
	})
	if err != nil {
		t.Fatalf("floor question at the rule price: %v", err)
	}
	if atRule.MarginBps != 909 {
		t.Errorf("line 1: the floor question's margin must be the pinned 909 bps, got %d", atRule.MarginBps)
	}
	if *glance.Lines[0].MarginBps != atRule.MarginBps {
		t.Errorf("line 1: the glance margin %d and the floor question margin %d disagree",
			*glance.Lines[0].MarginBps, atRule.MarginBps)
	}
	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if view.Lines[0].RulePriceBelowFloor == nil || !*view.Lines[0].RulePriceBelowFloor {
		t.Fatalf("line 1: the rule price is below the floor (the question says BelowFloor=%v), the mark must agree",
			atRule.BelowFloor)
	}
	if !atRule.BelowFloor {
		t.Fatal("line 1: fixture drifted, 550 over a 500 cost must sit below the 1200 bps default floor")
	}

	// Line 2, discounted: the margin the floor DECISION uses (decideLineFloor,
	// the same call the discount write makes) and the glance's margin over
	// the line's net total agree.
	_, branchID, err := svc.store.QuotePricingContext(ctx, quoteID)
	if err != nil {
		t.Fatalf("pricing context: %v", err)
	}
	out, err := svc.decideLineFloor(ctx, identity.StaffContext{
		UserID: alice, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{intTrenton: true},
	}, quoteID, branchID, prod, 2, 10, "", uom, 1000, 2000, uom, nil)
	if err != nil {
		t.Fatalf("floor decision: %v", err)
	}
	if !out.asked {
		t.Fatal("line 2: a 10 percent discount is a below rule write, the decision must be asked")
	}
	if out.answer.MarginBps != 4444 {
		t.Errorf("line 2: the decision's margin must be the pinned 4444 bps, got %d", out.answer.MarginBps)
	}
	if *glance.Lines[1].MarginBps != out.answer.MarginBps {
		t.Errorf("line 2: the glance margin %d and the floor decision margin %d disagree",
			*glance.Lines[1].MarginBps, out.answer.MarginBps)
	}
}

// quoteCustomer reads one quote's customer id for the floor question.
func quoteCustomer(t *testing.T, pool *pgxpool.Pool, quoteID string) string {
	t.Helper()
	var cust string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text FROM quotes WHERE id = $1`, quoteID).Scan(&cust); err != nil {
		t.Fatalf("read quote customer: %v", err)
	}
	return cust
}
