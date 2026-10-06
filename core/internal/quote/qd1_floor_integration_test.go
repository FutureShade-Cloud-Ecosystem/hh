//go:build integration

// qd1_floor_integration_test.go, the floor decision of plan 3.3 at the two
// write arms (defects D2, D5, D6): the discount arm and the quantity reprice
// of a discounted line. The cases:
//
//	at the rule price           accepted (the decision is not asked)
//	below rule, above floor     accepted, no manager
//	below floor                 refused, typed 409, unless the caller clears
//	                             it itself (floor_clearers_own_branch, in its
//	                             own branch, under the dealer setting) or an
//	                             approved and unused request for exactly that
//	                             quote, that line, no deeper a discount, is
//	                             consumed by that write
//
// Also the wrong role ledger's floor rows (plan 3.4): rows 5, 6 and 10.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// - ledger row 5: below the floor, as Inside Sales or Outside Sales -

func TestQD1_Row5_BelowFloorRefusedTypedForSales(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")

	for _, role := range []string{"INSIDE_SALES", "OUTSIDE_SALES"} {
		_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
		arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
		svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
		ctx := qCtxAs(intStaff(intTrenton))
		ctx = qd1WithRole(ctx, role, intTrenton, seller)

		_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
		var ref *FloorRefusalError
		if !errors.As(err, &ref) {
			t.Fatalf("row 5 (%s): want *FloorRefusalError, got %T: %v", role, err, err)
		}
		if ref.Policy != "MARGIN_BELOW_MINIMUM" {
			t.Errorf("row 5: the refusal must name the policy, got %q", ref.Policy)
		}
		if len(ref.Approvers) != 3 {
			t.Fatalf("row 5: the refusal must name the General Manager, the Financial Controller and the branch's Sales manager, got %v", ref.Approvers)
		}
		for _, want := range []string{"GENERAL_MANAGER", "FINANCIAL_CONTROLLER", "SALES_MANAGER"} {
			found := false
			for _, got := range ref.Approvers {
				if got == want {
					found = true
				}
			}
			if !found {
				t.Errorf("row 5: the approvers must name %s, got %v", want, ref.Approvers)
			}
		}
		if !ref.ManagerMaySet {
			t.Error("row 5: the refusal must say a manager of the branch may set it")
		}
		if !ref.ShowFigures || ref.MarginBps != 500 || ref.FloorBps != 1200 {
			t.Errorf("row 5: sales is on the cost list, the figures must ride the refusal, got %+v", ref)
		}
		// The line is untouched.
		if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 2000 {
			t.Errorf("row 5: a refused discount changed the line, total now %d", got.LineTotalCents)
		}
		// No clearing was stamped and no BYPASS row written.
		if c := qd1LineClearing(t, pool, quoteID, 1); c != nil {
			t.Errorf("row 5: a refused write stamped a clearing: %v", c)
		}
		if rows := qd1BypassRows(t, pool, quoteID); len(rows) != 0 {
			t.Errorf("row 5: a refused write logged %v", rows)
		}
	}
}

// - ledger row 6: the same, as the Yard manager: no own branch clearing -

func TestQD1_Row6_YardManagerRefusedNoOwnBranchClearing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	ym := qd1User(t, pool, "ym")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", ym, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "YARD_MANAGER", intTrenton, ym)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
	var ref *FloorRefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("row 6: want *FloorRefusalError, got %T: %v", err, err)
	}
	if got := liveLine(t, pool, quoteID, 1); got.LineTotalCents != 2000 {
		t.Errorf("row 6: a refused discount changed the line, total now %d", got.LineTotalCents)
	}
}

// Below the floor, cleared by the Branch manager on its own quote, the
// clearing stamped on the line and a BYPASS row written.
func TestQD1_BelowFloorBranchManagerClearsOwnQuote(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "BRANCH_MANAGER", intTrenton, bm)

	res, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
	if err != nil {
		t.Fatalf("a Branch manager must clear the floor on its own quote in its branch: %v", err)
	}
	if res.Line.LineTotalCents != 1500 {
		t.Errorf("the discounted total must be 1500 (2000 less 25 percent), got %d", res.Line.LineTotalCents)
	}
	c := qd1LineClearing(t, pool, quoteID, 1)
	if c == nil {
		t.Fatal("the clearing must be stamped on the line")
	}
	if c["cleared_by"] != bm {
		t.Errorf("the stamp must name who cleared it, got %v want %s", c["cleared_by"], bm)
	}
	if c["authority"] != "MANAGER_SCOPE" {
		t.Errorf("the stamp must name the authority, got %v", c["authority"])
	}
	if c["disc_pct"] != float64(25) {
		t.Errorf("the stamp must carry the discount, got %v", c["disc_pct"])
	}
	rows := qd1BypassRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS" {
		t.Fatalf("the clearing must write one BYPASS row, got %v", rows)
	}
}

// Below the floor, cleared by the Sales manager (Manage) on any quote in its
// branch, not only its own.
func TestQD1_BelowFloorSalesManagerClearsAnyQuoteInBranch(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "SALES_MANAGER", intTrenton, mgr)

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("a Sales manager must clear the floor on any quote in its branch: %v", err)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c == nil || c["authority"] != "MANAGER_SCOPE" {
		t.Fatalf("the clearing must be stamped with the manager scope authority, got %v", c)
	}
}

// Below the rule price and above the floor: accepted, logged, no manager.
// The UPDATE log row is QD-2's half; this pins the acceptance and that no
// BYPASS row and no clearing stamp appear.
func TestQD1_BelowRuleAboveFloorAcceptedNoManager(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	res, err := svc.SetLineDiscount(ctx, quoteID, 1, 10)
	if err != nil {
		t.Fatalf("a discount above the floor must be accepted with no manager: %v", err)
	}
	if res.Line.LineTotalCents != 1800 {
		t.Errorf("the discounted total must be 1800, got %d", res.Line.LineTotalCents)
	}
	if c := qd1LineClearing(t, pool, quoteID, 1); c != nil {
		t.Errorf("a write above the floor stamps no clearing: %v", c)
	}
	if rows := qd1BypassRows(t, pool, quoteID); len(rows) != 0 {
		t.Errorf("a write above the floor writes no BYPASS row, got %v", rows)
	}
}

// An approved and unused request is consumed by the write that uses it, and
// ledger row 10: the same approval offered again, or for another line or
// quote, is refused.
func TestQD1_Row10_ApprovedRequestUsedUpByFirstWrite(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	_, otherQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	// File and approve a request for line 1 of the first quote at 25 percent.
	lineID, prodID := qd1LineBinding(t, pool, quoteID, 1)
	filed, err := rail.FileFloorApproval(context.Background(), identity.FloorApprovalFiling{
		RequestedBy: seller, BranchID: intTrenton, SubjectRef: quoteID,
		Detail: "qd1: below floor request", LineNo: 1, DiscPct: 25, LineID: lineID, ProductID: prodID,
	})
	if err != nil {
		t.Fatalf("file floor approval: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM permission_approval_requests WHERE id = $1`, filed.ID)
	})
	decider := identity.StaffContext{UserID: qd1Approver(t, pool), Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, RoleCode: "GENERAL_MANAGER"}
	if err := rail.DecideApproval(context.Background(), filed.ID, decider, true, "qd1"); err != nil {
		t.Fatalf("decide floor approval: %v", err)
	}

	// The first write consumes it.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("an approved request must carry the write: %v", err)
	}
	c := qd1LineClearing(t, pool, quoteID, 1)
	if c == nil || c["authority"] != "APPROVAL" {
		t.Fatalf("the consumed approval must stamp the clearing with the APPROVAL authority, got %v", c)
	}
	rows := qd1BypassRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0] != "BYPASS" {
		t.Fatalf("the approval clearing must write one BYPASS row, got %v", rows)
	}

	// The same approval offered again, deeper: refused. The approval was
	// used up by the first write, and the line's standing clearing covers no
	// deeper a discount (plan 3.3), so a 30 percent ask has nothing to ride.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 30); err == nil {
		t.Fatal("row 10: a used approval carried a deeper write")
	} else if _, ok := err.(*FloorRefusalError); !ok {
		t.Fatalf("row 10: the second use must be the typed floor refusal, got %T: %v", err, err)
	}

	// Re-asserting the same 25 percent rides the standing clearing: the
	// approval is not reused (it is used up), the clearing needs no new one.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("a write no deeper and no lower needs no new clearing: %v", err)
	}

	// On another quote: refused.
	if _, err := svc.SetLineDiscount(ctx, otherQuote, 1, 25); err == nil {
		t.Fatal("row 10: an approval for one quote carried another quote's write")
	}
}

// D5: the quantity reprice of a discounted line runs the same floor decision.
func TestQD1_QtyRepriceOfDiscountedLineRunsFloorDecision(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 20)

	// The reprice at the new quantity lands below the floor (the arbiter's
	// figures), as the Work owner: refused.
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err == nil {
		t.Fatal("D5: a quantity reprice that repriced a discounted line below the floor went through")
	} else if _, ok := err.(*FloorRefusalError); !ok {
		t.Fatalf("D5: want the typed floor refusal, got %T: %v", err, err)
	}
}

// A later write that keeps the discount no deeper and the margin no lower
// needs no new clearing (plan 3.3, the stamped clearing survives).
func TestQD1_ClearingSurvivesNoDeeperNoLower(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)

	// The branch manager clears line 1 at 25 percent, margin 500. A Branch
	// manager holds Work, so its clearing is on its OWN quote (plan 3.3).
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	bmCtx := qCtxAs(intStaff(intTrenton))
	bmCtx = qd1WithRole(bmCtx, "BRANCH_MANAGER", intTrenton, bm)
	if _, err := svc.SetLineDiscount(bmCtx, quoteID, 1, 25); err != nil {
		t.Fatalf("the branch manager's clearing: %v", err)
	}

	// The owner (the branch manager itself) reprices the quantity; the
	// margin is no lower (the arbiter answers the same figures): no new
	// clearing is asked.
	bmCtx2 := qCtxAs(intStaff(intTrenton))
	bmCtx2 = qd1WithRole(bmCtx2, "BRANCH_MANAGER", intTrenton, bm)
	if _, err := svc.EditLineQty(bmCtx2, quoteID, 1, "5"); err != nil {
		t.Fatalf("a reprice no deeper and no lower must not ask again: %v", err)
	}
}

// The dealer setting switch: with manager_own_branch_floor_clearing off,
// the manager's own below floor price goes to leadership like anyone's.
func TestQD1_SettingOffManagersPriceGoesToLeadership(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)

	off := qd1CfgNoManagerClearing(t)
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, off)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "BRANCH_MANAGER", intTrenton, bm)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
	var ref *FloorRefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("with the setting off the manager's price must go to leadership, got %T: %v", err, err)
	}
}

// qd1Approver seeds a General Manager user to decide requests in tests.
func qd1Approver(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return qd1User(t, pool, "gmapprover")
}

// The read side's floor marks: a cost list caller sees who cleared a below
// floor price on the line; a caller off the list learns neither mark.
func TestQD1_ReadMarks_ClearedByForCostListOnly(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	hr := qd1User(t, pool, "hr")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, qd1Cfg)
	bmCtx := qCtxAs(intStaff(intTrenton))
	bmCtx = qd1WithRole(bmCtx, "BRANCH_MANAGER", intTrenton, bm)
	if _, err := svc.SetLineDiscount(bmCtx, quoteID, 1, 25); err != nil {
		t.Fatalf("the branch manager's clearing: %v", err)
	}

	view, err := svc.StaffQuoteRead(bmCtx, quoteID)
	if err != nil {
		t.Fatalf("read as the clearer: %v", err)
	}
	if len(view.Lines) != 1 || view.Lines[0].FloorClearedBy != bm {
		t.Fatalf("a cost list caller must see who cleared the below floor price, got %+v", view.Lines)
	}
	// CP0-3's mark rides for the same caller: this line's rule price sits
	// below the floor (the arbiter's figures), and the cost list caller is
	// told so.
	if view.Lines[0].RulePriceBelowFloor == nil || !*view.Lines[0].RulePriceBelowFloor {
		t.Fatalf("a cost list caller must see that the rule price sits below the floor, got %+v", view.Lines[0].RulePriceBelowFloor)
	}

	hrCtx := qCtxAs(intStaff(intTrenton))
	hrCtx = qd1WithRole(hrCtx, "HR", intTrenton, hr)
	hview, err := svc.StaffQuoteRead(hrCtx, quoteID)
	if err != nil {
		t.Fatalf("read as hr: %v", err)
	}
	if len(hview.Lines) != 1 {
		t.Fatalf("hr still reads the quote's lines, got %+v", hview.Lines)
	}
	if hview.Lines[0].FloorClearedBy != "" || hview.Lines[0].RulePriceBelowFloor != nil {
		t.Fatalf("a caller off the cost list must learn neither mark, got %+v", hview.Lines[0])
	}
}

// qd1StaleDiscStore is the real repository with one lie: its floor state
// read always answers disc 0, the discount the decision saw a moment
// before another write set one. It simulates the race the lock time
// re-check exists for, deterministically.
type qd1StaleDiscStore struct {
	*Repository
}

func (r qd1StaleDiscStore) LineFloorState(ctx context.Context, quoteID string, lineNo int) (string, int, int64, int64, string, *floorClearingStamp, error) {
	lineID, _, unit, gross, uom, clearing, err := r.Repository.LineFloorState(ctx, quoteID, lineNo)
	return lineID, 0, unit, gross, uom, clearing, err
}

// TestQD1_QtyRepriceRecordsTheDiscountItRead (P2-6): the quantity arm
// always records the stored discount it read, even 0, so the guarded
// write re-checks it under the lock. A discount set between the read and
// the commit used to ride the reprice unchecked, because a disc of 0
// recorded nothing and the lock time check never ran.
func TestQD1_QtyRepriceRecordsTheDiscountItRead(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)
	// The row carries a 25 percent discount the decision did not see.
	qd1SetDisc(t, pool, quoteID, 1, 25)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(qd1StaleDiscStore{repo}).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		WithFloorArbiter(&arb).
		WithFloorRail(identity.NewApprovals(identity.NewApprovalRepository(pool))).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "BRANCH_MANAGER", intTrenton, bm)

	_, err := svc.EditLineQty(ctx, quoteID, 1, "5")
	if err == nil {
		t.Fatal("a discount set between the floor read and the lock must refuse the reprice, the figures nobody decided on cannot ride it")
	}
}

// The availability reads gate avg_cost_cents by the cost list (X3, the
// review's P2-8): under the default every Quote Desk role is on the list,
// but a dealer who removes one would read cost through the availability
// chips. The stock view still renders for the off-list caller, the cost
// figure does not.
func TestQD1_AvailabilityCostGatedByCostList(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	hidden := qd1User(t, pool, "hiddensales")
	onList := qd1User(t, pool, "onlist")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", hidden, true)

	cfg := qd1CfgCostHiddenFrom(t, "inside_sales")
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, cfg)
	svc.WithAvailability(qd1CostAvail{cost: 4321})

	// A caller OFF the list: the grades render, the cost does not.
	hidCtx := qCtxAs(intStaff(intTrenton))
	hidCtx = qd1WithRole(hidCtx, "INSIDE_SALES", intTrenton, hidden)
	rows, _, err := svc.QuoteAvailability(hidCtx, quoteID)
	if err != nil {
		t.Fatalf("availability for an off-list caller: %v", err)
	}
	if len(rows) != 1 || rows[0].Grade == "" {
		t.Fatalf("the stock view must still render for an off-list caller, got %+v", rows)
	}
	if rows[0].Branch.AvgCostCents != 0 {
		t.Errorf("a caller off the cost list must not read avg_cost_cents through the availability chips, got %d",
			rows[0].Branch.AvgCostCents)
	}
	peek, err := svc.CrossBranchPeek(hidCtx, "00000000-0000-4000-8000-0000000000aa")
	if err != nil {
		t.Fatalf("cross branch peek for an off-list caller: %v", err)
	}
	for _, b := range peek {
		if b.AvgCostCents != 0 {
			t.Errorf("a caller off the cost list must not read avg_cost_cents through the branch peek, got %d", b.AvgCostCents)
		}
	}

	// A caller ON the list still reads it.
	onCtx := qCtxAs(intStaff(intTrenton))
	onCtx = qd1WithRole(onCtx, "BRANCH_MANAGER", intTrenton, onList)
	rows, _, err = svc.QuoteAvailability(onCtx, quoteID)
	if err != nil {
		t.Fatalf("availability for an on-list caller: %v", err)
	}
	if len(rows) != 1 || rows[0].Branch.AvgCostCents != 4321 {
		t.Fatalf("a caller on the cost list must read avg_cost_cents, got %+v", rows)
	}
}

// The refusal's margin figures ride for callers on the cost list alone
// (plan 1.2): the figures are cost information, and a dealer who narrows
// the list must not read them out of a 409 body.
func TestQD1_RefusalFiguresForCostListOnly(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)

	cfg := qd1CfgCostHiddenFrom(t, "inside_sales")
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd1SvcFloor(t, pool, now, arb, cfg)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	_, err := svc.SetLineDiscount(ctx, quoteID, 1, 25)
	ref, ok := err.(*FloorRefusalError)
	if !ok {
		t.Fatalf("want the typed refusal, got %T: %v", err, err)
	}
	if ref.ShowFigures || ref.MarginBps != 0 || ref.FloorBps != 0 {
		t.Fatalf("a caller off the cost list must not receive the refusal's margin figures, got %+v", ref)
	}

	// The same refusal as a caller on the list carries them. The yard
	// manager is on the list and never clears the floor itself, and the
	// second quote is its own, so the ownership rung does not answer first.
	ym := qd1User(t, pool, "ym")
	_, ymQuote, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", ym, true)
	ymCtx := qCtxAs(intStaff(intTrenton))
	ymCtx = qd1WithRole(ymCtx, "YARD_MANAGER", intTrenton, ym)
	_, yerr := svc.SetLineDiscount(ymCtx, ymQuote, 1, 25)
	yref, ok := yerr.(*FloorRefusalError)
	if !ok {
		t.Fatalf("want the typed refusal for the yard manager, got %T: %v", yerr, yerr)
	}
	if !yref.ShowFigures || yref.MarginBps != 500 || yref.FloorBps != 1200 {
		t.Fatalf("a caller on the cost list must receive the refusal's margin figures, got %+v", yref)
	}
}
