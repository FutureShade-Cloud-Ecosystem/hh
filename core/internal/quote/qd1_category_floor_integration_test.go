//go:build integration

// qd1_category_floor_integration_test.go pins the floor SOURCE (P1-3, the
// PR 117 review's finding): the decision the quote module makes must use the
// line's category policy row, the same row the resolver reads, never the
// 1200 default a missing lookup falls back to. The arbiter here is the REAL
// pricing service composed as production wires it (wire_money.go), against
// the seeded database, where all six categories carry a 2000 bps floor; the
// cost seam stays the deterministic double (500 cents), so every margin in
// the file is a fixed figure:
//
//	40 percent off a 1000 cent unit  -> candidate 600, margin 1666 bps
//	1666 is above the 1200 default and below the seeded 2000 floor, so the
//	whole band between the two is the finding's own probe.
//
// The same source must answer everywhere the floor is spoken: the decision,
// the refusal's figures, the request door's detail, the BYPASS log row and
// the read's rule_price_below_floor mark.
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd1RealFloorSvc composes the service with the REAL pricing arbiter, the
// composition wire_money.go builds: the repository, the product catalogue's
// category resolver and uom converter, the identity margin gate. Only the
// cost basis stays the double, so the margins under test are fixed.
func qd1RealFloorSvc(t *testing.T, pool *pgxpool.Pool, now time.Time) *StaffService {
	t.Helper()
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	prodSvc := product.NewService(product.NewRepository(pool))
	pricingSvc := pricing.NewService(pricing.NewRepository(pool)).
		WithCategoryResolver(prodSvc).
		WithUOMConverter(prodSvc).
		WithMarginGate(pricing.NewIdentityMarginGate())
	return NewStaffService(repo).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		WithFloorArbiter(pricingSvc).
		WithFloorRail(identity.NewApprovals(identity.NewApprovalRepository(pool))).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
}

// qd1ProductCategory reads the product's own category id, the row the
// resolver's catalog seam answers.
func qd1ProductCategory(t *testing.T, pool *pgxpool.Pool, productID string) string {
	t.Helper()
	var cat string
	if err := pool.QueryRow(context.Background(),
		`SELECT category_id::text FROM products WHERE id = $1`, productID).Scan(&cat); err != nil {
		t.Fatalf("read the product's category: %v", err)
	}
	return cat
}

// qd1FloorLogRows reads the pricing log rows the decision wrote for one
// quote, action and new_value, the columns that carry the floor figure.
func qd1FloorLogRows(t *testing.T, pool *pgxpool.Pool, quoteID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT action || ' ' || new_value FROM pricing_audit_log
		 WHERE ref_entity_type = 'quote' AND ref_entity_id = $1 ORDER BY created_at`, quoteID)
	if err != nil {
		t.Fatalf("read pricing log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan pricing log: %v", err)
		}
		out = append(out, v)
	}
	return out
}

// The decision itself: a 40 percent discount on a 1000 cent line with a 500
// cent cost (1666 bps) is below the seeded category floor (2000), so it is
// refused for Inside Sales with the refusal naming the CATEGORY's figures,
// and the branch Sales manager clears it with the log row carrying the same
// floor.
func TestQD1_CategoryFloor_DecisionUsesTheCategorysFloor(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1RealFloorSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 40)
	ref, ok := err.(*FloorRefusalError)
	if !ok {
		t.Fatalf("a 1666 bps margin is below the category's 2000 floor, Inside Sales must be refused, got %T: %v", err, err)
	}
	if !ref.ShowFigures || ref.MarginBps != 1666 || ref.FloorBps != 2000 {
		t.Errorf("the refusal's figures must be the category's own (margin 1666 floor 2000), got %+v", ref)
	}
	// The line is untouched and nothing was logged.
	if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 2000 {
		t.Errorf("a refused discount changed the line, total now %d", got.LineTotalCents)
	}
	if rows := qd1FloorLogRows(t, pool, quoteID); len(rows) != 0 {
		t.Errorf("a refused discount logged %v", rows)
	}

	// The branch Sales manager clears the same ask (Manage, its own branch),
	// and the BYPASS row's floor figure is the category's.
	mgrCtx := qCtxAs(intStaff(intTrenton))
	mgrCtx = qd1WithRole(mgrCtx, "SALES_MANAGER", intTrenton, mgr)
	if _, err := svc.SetLineDiscount(mgrCtx, quoteID, 1, 40); err != nil {
		t.Fatalf("the branch Sales manager must clear a below floor price in its branch: %v", err)
	}
	rows := qd1FloorLogRows(t, pool, quoteID)
	if len(rows) != 1 || !strings.Contains(rows[0], "floor 2000") || !strings.Contains(rows[0], "margin 1666") {
		t.Fatalf("the BYPASS row must carry the category's figures (net 600 margin 1666 floor 2000), got %v", rows)
	}
}

// The same refusal is cleared by an approved request: the request door files
// it with the category's floor in the approver's detail, and the write
// consumes it.
func TestQD1_CategoryFloor_ApprovalClearsWhatTheDefaultWouldWaveThrough(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	svc := qd1RealFloorSvc(t, pool, now)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// The door itself must see the category floor: the detail the approver
	// reads names the 2000 bps floor, not the 1200 default.
	ans, err := svc.RequestFloorApproval(ctx, quoteID, 1, 40, "qd1 category floor")
	if err != nil {
		t.Fatalf("the request door must file a below category floor ask: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, ans.Request.ID)
	})
	var detail string
	if err := pool.QueryRow(context.Background(),
		`SELECT detail FROM permission_approval_requests WHERE id = $1`, ans.Request.ID).Scan(&detail); err != nil {
		t.Fatalf("read the filed detail: %v", err)
	}
	if !strings.Contains(detail, "2000 bps floor") || !strings.Contains(detail, "1666 bps") {
		t.Fatalf("the approver's detail must carry the category's figures, got %q", detail)
	}

	decider := identity.StaffContext{UserID: qd1Approver(t, pool), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}
	if err := rail.DecideApproval(context.Background(), ans.Request.ID, decider, true, "qd1"); err != nil {
		t.Fatalf("decide the request: %v", err)
	}

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 40); err != nil {
		t.Fatalf("the approved request must carry the write the default floor would have waved through: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c == nil || c["authority"] != "APPROVAL" {
		t.Fatalf("the consumed approval must stamp the APPROVAL authority, got %v", c)
	}
	rows := qd1FloorLogRows(t, pool, quoteID)
	if len(rows) != 1 || !strings.Contains(rows[0], "floor 2000") {
		t.Fatalf("the BYPASS row must carry the category's floor, got %v", rows)
	}
}

// A category with enforcement disabled asks no floor question at all: the
// same 40 percent goes through for Inside Sales with no manager, no clearing
// and no log row, exactly as a live resolve would bypass the gate.
func TestQD1_CategoryFloor_EnforcementDisabledAsksNoFloorQuestion(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	// The CATEGORY's switch, not the product's: the product switch worked
	// before the fix, only the category row was unreachable (empty id). 45
	// percent leaves a 909 bps margin, UNDER the 1200 default, so a decision
	// that fell back to the default would ask the floor question and refuse.
	cat := qd1ProductCategory(t, pool, prod)
	if _, err := pool.Exec(context.Background(),
		`UPDATE pricing_category_policy SET margin_enforcement_disabled = true WHERE category_id = $1`, cat); err != nil {
		t.Fatalf("seed the category's enforcement switch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE pricing_category_policy SET margin_enforcement_disabled = false WHERE category_id = $1`, cat)
	})

	svc := qd1RealFloorSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	res, err := svc.SetLineDiscount(ctx, quoteID, 1, 45)
	if err != nil {
		t.Fatalf("with enforcement disabled the gate does not run: %v", err)
	}
	if res.Line.LineTotalCents != 1100 {
		t.Errorf("the discounted total must be 1100, got %d", res.Line.LineTotalCents)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c != nil {
		t.Errorf("no floor question means no clearing stamp, got %v", c)
	}
	if rows := qd1FloorLogRows(t, pool, quoteID); len(rows) != 0 {
		t.Errorf("no floor question means no log row, got %v", rows)
	}
}

// The floor is honoured from the category row in BOTH directions: with the
// category's floor moved BELOW the default (500 bps), a 909 bps margin (45
// percent) the 1200 default would refuse is accepted with no manager, so the
// decision provably reads the category row and not the constant.
func TestQD1_CategoryFloor_LowerCategoryFloorHonoured(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	cat := qd1ProductCategory(t, pool, prod)

	if _, err := pool.Exec(context.Background(),
		`UPDATE pricing_category_policy SET margin_floor_bps = 500 WHERE category_id = $1`, cat); err != nil {
		t.Fatalf("lower the category floor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE pricing_category_policy SET margin_floor_bps = 2000 WHERE category_id = $1`, cat)
	})

	svc := qd1RealFloorSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 45); err != nil {
		t.Fatalf("a 909 bps margin sits above the category's lowered 500 floor and must be accepted with no manager: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c != nil {
		t.Errorf("a write above the floor stamps no clearing, got %v", c)
	}
}

// The read's rule_price_below_floor mark uses the same source: a line whose
// RULE price sits between the two floors (unit 600, cost 500, margin 1666)
// is marked below floor for a cost list caller under the category's 2000,
// where the 1200 default would leave it unmarked.
func TestQD1_CategoryFloor_ReadMarkUsesTheCategorysFloor(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	if _, err := pool.Exec(context.Background(), `
		UPDATE quote_lines SET unit_price_cents = 600, line_total_cents = 1200
		 WHERE quote_id = $1 AND line_no = 1`, quoteID); err != nil {
		t.Fatalf("seed a rule price between the two floors: %v", err)
	}
	_ = prod
	_ = uom

	svc := qd1RealFloorSvc(t, pool, now)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	view, err := svc.StaffQuoteRead(ctx, quoteID)
	if err != nil {
		t.Fatalf("read the quote: %v", err)
	}
	if len(view.Lines) != 1 || view.Lines[0].RulePriceBelowFloor == nil || !*view.Lines[0].RulePriceBelowFloor {
		t.Fatalf("a 1666 bps rule price is below the category's 2000 floor and must be marked for a cost list caller, got %+v", view.Lines)
	}

	// And the write side agrees with the mark: a discount on that line is
	// refused for Inside Sales on the same floor.
	_, err = svc.SetLineDiscount(ctx, quoteID, 1, 10)
	if !errors.As(err, new(*FloorRefusalError)) {
		t.Fatalf("the decision must agree with the mark and refuse, got %T: %v", err, err)
	}
}

// qd1FailingCategory is a catalog whose category read fails.
type qd1FailingCategory struct{}

func (qd1FailingCategory) GetCategory(context.Context, string) (string, error) {
	return "", errors.New("catalog down")
}
func (qd1FailingCategory) GetBaseUOM(context.Context, string) (string, error) {
	return "", errors.New("catalog down")
}

// P2-2 (round 3): a failed category read refuses the write. No line change,
// no log row, and an approved request for the line stays unused.
func TestQD1_CategoryReadErrorRefusesAndConsumesNothing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, prod, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	lineID, _ := qd1LineBinding(t, pool, quoteID, 1)
	filed := qd1FileAndApprove(t, pool, seller, intTrenton, quoteID, lineID, prod, 1, 40)

	real := pricing.NewService(pricing.NewRepository(pool)).WithCategoryResolver(qd1FailingCategory{})
	svc := qd1SvcArbiter(t, pool, now, real)
	ctx := qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 40)
	if err == nil {
		t.Fatal("a failed category read must refuse the write")
	}
	var refusal *FloorRefusalError
	if errors.As(err, &refusal) {
		t.Fatalf("a failed read is an error, not a floor refusal answered from a default: %v", err)
	}
	if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 2000 {
		t.Fatalf("the line must be untouched, got total %d", got.LineTotalCents)
	}
	if n := qd1ApprovalUnused(t, pool, filed.ID); n != 0 {
		t.Fatalf("no approval may be consumed, got %d used stamps", n)
	}
	if rows := qd1BypassRows(t, pool, quoteID); len(rows) != 0 {
		t.Fatalf("a refused write logs nothing, got %v", rows)
	}
}
