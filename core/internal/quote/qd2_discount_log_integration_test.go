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
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	alice := qd1User(t, pool, "alice")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

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
	if row.UserID != alice {
		t.Errorf("the row must name who discounted the line, got %q want %s", row.UserID, alice)
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
	alice := qd1User(t, pool, "alice")
	_, quoteID, _, _ := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)
	qd1SetDisc(t, pool, quoteID, 1, 20)
	qd2CleanAudit(t, pool, quoteID)

	arb := qd1Arbiter{marginBps: 2000, floorBps: 1200}
	svc := qd2Svc(t, pool, now, qd2Pricer{}, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

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
