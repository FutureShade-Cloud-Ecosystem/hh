//go:build integration

// qd2_floor_source_integration_test.go pins two seams of QD-2's logging on
// the seeded database: the rule price paths fail closed when no arbiter is
// wired (as the discount path does), and the floor the decision and the log
// rows carry is the line's CATEGORY policy floor, so a per-category floor
// that differs from both the 1200 default and the seeded 2000 changes the
// answer.
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// qd2NoArbiterSvc composes the service with a pricer and NO floor arbiter.
func qd2NoArbiterSvc(t *testing.T, repo *Repository, pricer PriceResolver) *StaffService {
	t.Helper()
	return NewStaffService(repo).
		WithPricing(pricer).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		withCaller(qCallerFrom).
		withNow(qd2Clock)
}

// A below floor rule price cannot be logged with no arbiter wired, so the add
// and the no-discount reprice refuse with the not configured error and write
// nothing, where they used to accept the line and log nothing.
func TestQD2_RulePriceBelowFloor_FailsClosedWithoutArbiter(t *testing.T) {
	pool := intPool(t)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	repo := NewRepository(pool)
	pricer := qd2Pricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}
	svc := qd2NoArbiterSvc(t, repo, pricer)
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.AddLine(ctx, quoteID, prod, "qd2 no arbiter", "3", uom); !errors.Is(err, ErrEditNotConfigured) {
		t.Fatalf("an add at a below floor rule price with no arbiter must fail closed, got %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM quote_lines WHERE quote_id = $1`, quoteID).Scan(&n); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if n != 1 {
		t.Errorf("the refused add must leave the quote at its one line, got %d", n)
	}

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "3"); !errors.Is(err, ErrEditNotConfigured) {
		t.Fatalf("a reprice at a below floor rule price with no arbiter must fail closed, got %v", err)
	}
	if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 2000 {
		t.Errorf("the refused reprice changed the line, total now %d", got.LineTotalCents)
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 0 {
		t.Errorf("a refused write logged %+v", rows)
	}
}

// The floor is the line's CATEGORY policy floor, on the REAL resolver and the
// REAL arbiter against the seeded database. The category's floor is moved to
// 9500 bps, a figure that is neither the 1200 default nor the seeded 2000, and
// the rule price the resolver strikes (6000 against the 500 cent cost, margin
// 9166) is then below it. Everything QD-2 adds answers that one floor: the add
// is accepted and its rule price row and read mark carry 9500, and a discount
// on the line is refused for Inside Sales on 9500. A floor read that fell back
// to the 1200 default would accept both, write no row and mark nothing.
func TestQD2_CategoryFloor_DrivesTheDecisionAndTheRulePriceRow(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)
	cat := qd1ProductCategory(t, pool, prod)

	var target, floor int64
	if err := pool.QueryRow(context.Background(),
		`SELECT margin_target_bps, margin_floor_bps FROM pricing_category_policy WHERE category_id = $1`, cat).
		Scan(&target, &floor); err != nil {
		t.Fatalf("read the category policy: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE pricing_category_policy SET margin_target_bps = 10000, margin_floor_bps = 9500 WHERE category_id = $1`, cat); err != nil {
		t.Fatalf("move the category floor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE pricing_category_policy SET margin_target_bps = $2, margin_floor_bps = $3 WHERE category_id = $1`,
			cat, target, floor)
	})

	prodSvc := product.NewService(product.NewRepository(pool))
	resolver := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(prodSvc).
		WithUOMConverter(prodSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	svc := qd1RealFloorSvc(t, pool, now).WithPricing(resolver)
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

	res, err := svc.AddLine(ctx, quoteID, prod, "qd2 category floor", "3", uom)
	if err != nil {
		t.Fatalf("a line at a below floor rule price is accepted: %v", err)
	}
	if res.Line.UnitPriceCents != 6000 {
		t.Fatalf("the resolver strikes the seeded 6000 cent rule price, got %d", res.Line.UnitPriceCents)
	}
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0].Action != "UPDATE" ||
		!strings.HasSuffix(rows[0].NewValue, "margin 9166 floor 9500") {
		t.Fatalf("the rule price row must carry the category's floor (margin 9166 floor 9500), got %+v", rows)
	}
	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var marked *bool
	for _, ln := range view.Lines {
		if ln.LineNo == res.Line.LineNo {
			marked = ln.RulePriceBelowFloor
		}
	}
	if marked == nil || !*marked {
		t.Fatalf("the rule price sits below the category's floor and must be marked, got %v", marked)
	}

	// The decision reads the same floor: 10 percent off the 6000 rule price
	// leaves margin 9074, below 9500, so Inside Sales is refused with the
	// category's figures.
	_, err = svc.SetLineDiscount(ctx, quoteID, res.Line.LineNo, 10)
	var ref *FloorRefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("a discount below the category's floor must be refused, got %T: %v", err, err)
	}
	if ref.FloorBps != 9500 {
		t.Errorf("the refusal must carry the category's 9500 floor, got %+v", ref)
	}
}
