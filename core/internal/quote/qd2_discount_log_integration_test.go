//go:build integration

// qd2_discount_log_integration_test.go, the log on both trails (plan 4.2):
// every write that leaves a line below its rule price records, on the
// write's own transaction, the quote's change trail row (the discount
// before and after and the line total) and one pricing log row through the
// pricing seam: a line above the floor is an UPDATE row, a below floor
// cleared line a BYPASS row naming the authority. The pricing rows are
// read back through the pricingListAudit read filtered by entity type.
package quote

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd2Clock is the suite's fixed decision clock: the injected `now` every qd2
// test pins its repository and service to (the qd1 pattern). One literal, in
// one helper, so a new fixture adds no calendar date of its own.
func qd2Clock() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }

// qd2TrailField reads one QUOTE_CHANGED row's before and after for a field
// on one line of one quote. ok=false when no such row stands.
func qd2TrailField(t *testing.T, pool *pgxpool.Pool, quoteID, field string, lineNo int) (before, after string, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT changes->>'before', changes->>'after' FROM audit_log
		 WHERE entity_type = 'quote' AND entity_id = $1 AND action = $2
		   AND changes->>'field' = $3 AND (changes->>'line_no')::int = $4`,
		quoteID, audit.ActQuoteChanged, field, lineNo).Scan(&before, &after)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false
		}
		t.Fatalf("read trail field %s: %v", field, err)
	}
	return before, after, true
}

// A discount that lands below the rule price and above the floor, written
// by Sales on its own quote: accepted with no approval, one UPDATE row in
// the pricing log, and the change trail row (the discount before and after
// and the line total) on the write's own transaction.
func TestQD2_DiscountAboveFloor_UpdateRowAndTrail(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	res, err := svc.SetLineDiscount(ctx, quoteID, 1, 10)
	if err != nil {
		t.Fatalf("a discount above the floor must be accepted with no approval: %v", err)
	}
	if res.Line.LineTotalCents != 1800 {
		t.Errorf("the discounted total must be 1800, got %d", res.Line.LineTotalCents)
	}

	// The pricing log: one UPDATE row, read through the pricingListAudit
	// read filtered by entity type.
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 {
		t.Fatalf("the discount must write one pricing log row, got %d", len(rows))
	}
	row := rows[0]
	if row.Action != "UPDATE" {
		t.Errorf("a line above the floor is an UPDATE row, got %q", row.Action)
	}
	if row.BypassReason != "" {
		t.Errorf("an UPDATE row names no bypass authority, got %q", row.BypassReason)
	}
	if row.UserID != seller {
		t.Errorf("the row must name who discounted the line, got %q want %s", row.UserID, seller)
	}
	if row.FieldName != "line_1" {
		t.Errorf("the row names its line, got %q", row.FieldName)
	}
	if row.OldValue != "rule 1000 at 10%" {
		t.Errorf("the row carries the rule price and the discount, got %q", row.OldValue)
	}
	if row.NewValue != "net 900 margin 2000 floor 1200" {
		t.Errorf("the row carries the net price, the margin and the floor, got %q", row.NewValue)
	}

	// The quote's own change trail: the discount before and after, and the
	// line total.
	if b, a, ok := qd2TrailField(t, pool, quoteID, "disc_pct", 1); !ok {
		t.Error("the trail must carry the discount change (disc_pct)")
	} else if b != "0" || a != "10" {
		t.Errorf("the trail's disc_pct must read 0 to 10, got %s to %s", b, a)
	}
	if b, a, ok := qd2TrailField(t, pool, quoteID, "line_total_cents", 1); !ok {
		t.Error("the trail must carry the line total change")
	} else if b != "2000" || a != "1800" {
		t.Errorf("the trail's line_total_cents must read 2000 to 1800, got %s to %s", b, a)
	}
}

// The quantity reprice of a discounted line rides the same record: the
// reprice that leaves the line above the floor writes the UPDATE row.
func TestQD2_QtyRepriceAboveFloor_UpdateRow(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 20)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("a reprice above the floor must be accepted: %v", err)
	}
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0].Action != "UPDATE" {
		t.Fatalf("the reprice must write one UPDATE row, got %+v", rows)
	}
	if rows[0].OldValue != "rule 1000 at 20%" {
		t.Errorf("the row carries the stored discount the reprice reapplied, got %q", rows[0].OldValue)
	}
}

// The BYPASS row stays correct (a below floor price cleared by the Branch
// manager on its own quote), and a later write that rides the standing
// clearing adds no UPDATE row beside it.
func TestQD2_BypassRowStillCorrect_NoRowForStandingClearing(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	bm := qd1User(t, pool, "bm")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", bm, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "BRANCH_MANAGER", intTrenton, bm)

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("the branch manager's clearing: %v", err)
	}
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0].Action != "BYPASS" {
		t.Fatalf("the clearing must write one BYPASS row, got %+v", rows)
	}
	if rows[0].BypassReason != "MANAGER_SCOPE_OVERRIDE" {
		t.Errorf("the BYPASS row names its authority, got %q", rows[0].BypassReason)
	}

	// A re-assertion of the same discount rides the standing clearing: no
	// new row, UPDATE or BYPASS, beside the clearing's own.
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 25); err != nil {
		t.Fatalf("a write no deeper and no lower needs no new clearing: %v", err)
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("a standing clearing is not re-logged, got %+v", rows)
	}
}

// qd2CancelOnCommitStore cancels the request context the instant the guarded
// write returns: the line is committed, and everything the write still owed
// at that point runs on a context that is now dead. A log row that rides the
// write's own transaction is already committed with it; a best-effort append
// after the fact is not.
type qd2CancelOnCommitStore struct {
	*Repository
	cancel context.CancelFunc
}

func (w *qd2CancelOnCommitStore) UpdateLineDiscountGuarded(ctx context.Context, quoteID string, lineNo, discPct int, allowedFrom []string, actorID string, plan floorWritePlan, hooks quoteDiscountHooks) (QuoteEditResult, error) {
	res, err := w.Repository.UpdateLineDiscountGuarded(ctx, quoteID, lineNo, discPct, allowedFrom, actorID, plan, hooks)
	w.cancel()
	return res, err
}

// The pricing log row must survive a request context that dies the moment
// the write commits (plan 4.2: the row rides the write's own transaction, so
// a client disconnect between commit and log cannot lose it).
func TestQD2_PricingLogRow_SurvivesContextCancelledAfterCommit(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	wrapped := &qd2CancelOnCommitStore{Repository: repo}
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(wrapped).
		WithAudit(aud).WithAuditTrail(aud).
		WithPricing(qd2Pricer{}).WithCostBasis(qInv{}).WithCustomerType(qCustType{}).
		WithActorNamer(repo).WithRoleConfig(qd1Cfg).
		WithFloorArbiter(&arb).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })

	// The cancel wraps the FINAL caller context: qCtxAs re-roots on
	// Background, so a cancel layered before the role stamp would be
	// discarded with it.
	ctx, cancel := context.WithCancel(qd1WithRole(qCtxAs(intStaff(intTrenton)), "INSIDE_SALES", intTrenton, seller))
	defer cancel()
	wrapped.cancel = cancel

	res, err := svc.SetLineDiscount(ctx, quoteID, 1, 10)
	if err != nil {
		t.Fatalf("the discount write itself is committed, the refusal cannot come from it: %v", err)
	}
	if res.Line.LineTotalCents != 1800 {
		t.Errorf("the discounted total must be 1800, got %d", res.Line.LineTotalCents)
	}
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 || rows[0].Action != "UPDATE" {
		t.Fatalf("the pricing log row rode the write's commit, a context cancelled after it cannot lose the row, got %+v", rows)
	}
}

// qd2FailAppendArbiter writes the row through the real pricing service on
// the caller's transaction and then reports the append failed: the INSERT
// has ridden the write, and its error must take the whole write with it.
type qd2FailAppendArbiter struct{ qd1Arbiter }

func (a *qd2FailAppendArbiter) RecordFloorDecisionTx(ctx context.Context, tx pgx.Tx, rec pricing.FloorRecord) error {
	if a.records != nil {
		if err := a.records.RecordFloorDecisionTx(ctx, tx, rec); err != nil {
			return err
		}
	}
	return errors.New("qd2: the pricing log append failed on purpose")
}

// A failed pricing log append must refuse the write it rides and leave no
// row behind: the write and its row are one unit, a committed line with no
// log row (or a log row for a rolled back line) cannot happen.
func TestQD2_PricingLogRow_FailingAppendRollsTheWriteBack(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd2FailAppendArbiter{qd1Arbiter{marginBps: 2000, floorBps: 1200}}
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(repo).
		WithAudit(aud).WithAuditTrail(aud).
		WithPricing(qd2Pricer{}).WithCostBasis(qInv{}).WithCustomerType(qCustType{}).
		WithActorNamer(repo).WithRoleConfig(qd1Cfg).
		WithFloorArbiter(&arb).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 10); err == nil {
		t.Fatal("a failed pricing log append must refuse the write it was riding")
	}
	var disc int
	if err := pool.QueryRow(context.Background(),
		`SELECT disc_pct FROM quote_lines WHERE quote_id = $1 AND line_no = 1`, quoteID).Scan(&disc); err != nil {
		t.Fatalf("read the line back: %v", err)
	}
	if disc != 0 {
		t.Errorf("the rolled back write must leave the line undiscounted, disc_pct %d", disc)
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 0 {
		t.Fatalf("a rolled back write leaves no pricing log row, got %+v", rows)
	}
}

// An idempotent rewrite adds no pricing log row: three re-assertions of the
// same discount write one UPDATE row (the first), a changed discount writes
// one more. A retried request must not log twice.
func TestQD2_IdenticalDiscountRewrite_AddsNoRow(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	for i := 0; i < 3; i++ {
		if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 10); err != nil {
			t.Fatalf("the same discount re-asserted must be accepted: %v", err)
		}
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("three identical discount writes must leave one pricing log row, got %+v", rows)
	}
	if _, err := svc.SetLineDiscount(ctx, quoteID, 1, 15); err != nil {
		t.Fatalf("a deeper discount above the floor must be accepted: %v", err)
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 2 {
		t.Fatalf("a changed discount writes one more row, got %+v", rows)
	}
}

// The same discipline on the reprice arm: a same-quantity reprice that
// strikes the same rule price adds no second rule price row, a quantity
// change that re-strikes the below floor rule price does.
func TestQD2_IdenticalQtyReprice_AddsNoRow(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd2CleanAudit(t, pool, quoteID)

	// The rule price the resolver strikes sits below the floor, so the
	// first reprice at the same quantity logs it (CP0-3 answer A).
	pricer := qd2Pricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}
	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, pricer, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "2"); err != nil {
		t.Fatalf("the first reprice at the standing quantity must be accepted: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.EditLineQty(ctx, quoteID, 1, "2"); err != nil {
			t.Fatalf("an identical reprice must be accepted: %v", err)
		}
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 1 {
		t.Fatalf("the first reprice logged the below floor rule price, the identical repeats must add nothing, got %+v", rows)
	}
	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("a changed quantity must be accepted: %v", err)
	}
	if rows := qd2AuditRows(t, pool, quoteID); len(rows) != 2 {
		t.Fatalf("a changed quantity that re-strikes the below floor rule price writes one more row, got %+v", rows)
	}
}

// The reprice's rule price row (CP0-3 answer A) on a line that carries a
// discount: the row must record the line's real discount and the net that
// discount leaves, not disc 0 with the net equal to the rule. Removing the
// row from the reprice path fails this test, and so does misstating it.
func TestQD2_QtyRepriceRulePriceRow_CarriesTheLinesDiscount(t *testing.T) {
	pool := intPool(t)
	now := qd2Clock()
	seller := qd1User(t, pool, "seller")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", seller, true)
	qd1SetDisc(t, pool, quoteID, 1, 20)
	qd2CleanAudit(t, pool, quoteID)

	// The reprice strikes a 600 cent rule price that sits below the floor
	// (the resolver says blocked) on a line carrying a 20 percent stored
	// discount, and the floor question at the discounted candidate answers
	// above the floor: the write owes its UPDATE row AND the rule price
	// row, and both carry the line's real figures (600 at 20 percent nets
	// 480).
	pricer := qd2Pricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}
	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, pricer, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, seller)

	if _, err := svc.EditLineQty(ctx, quoteID, 1, "5"); err != nil {
		t.Fatalf("the reprice above the floor must be accepted: %v", err)
	}
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 2 {
		t.Fatalf("the reprice owes its floor UPDATE row and the rule price row beside it, got %+v", rows)
	}
	for _, r := range rows {
		if r.OldValue != "rule 600 at 20%" {
			t.Errorf("both rows carry the line's real discount (20 percent of a 600 cent rule), got %q", r.OldValue)
		}
		if !strings.HasPrefix(r.NewValue, "net 480 margin ") {
			t.Errorf("both rows carry the net the line's discount leaves (480), got %q", r.NewValue)
		}
	}
}
