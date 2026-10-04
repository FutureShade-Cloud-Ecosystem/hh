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
	"testing"
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
