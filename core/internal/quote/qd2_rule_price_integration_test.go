//go:build integration

// qd2_rule_price_integration_test.go, the 409 at the rule price (plan 4.1,
// defect D1): a line whose rule price resolves below the margin floor is
// accepted at that price, as at the till, as Sales and as Sales manager.
// What such a line shows and logs is CP0-3 answer A: accepted with no
// manager, marked for the salesperson (the cost list read's
// rule_price_below_floor), logged for the pricing editors (one UPDATE row
// in the pricing log, read back through the pricingListAudit read filtered
// by entity type).
package quote

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5/pgxpool"
)

// qd2Pricer answers the resolve shape the real resolver returns for a rule
// price below the floor: a complete breakdown with Blocked set and the
// margin and floor figures riding it (the figures priceLine keeps).
type qd2Pricer struct {
	blocked     bool
	finalCents  int64
	priceUOM    string
	lineTotal   int64
	qtyBase     float64
	marginBps   int64
	floorBps    int64
	blockPolicy string
}

func (p qd2Pricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	final := p.finalCents
	if final == 0 {
		final = 1000
	}
	uom := p.priceUOM
	if uom == "" {
		uom = in.UOMEntered
	}
	qty := in.QtyEntered
	if p.qtyBase != 0 {
		qty = p.qtyBase
	}
	total := p.lineTotal
	if total == 0 {
		total = int64(qty) * final
	}
	policy := p.blockPolicy
	if policy == "" {
		policy = "MARGIN_BELOW_MINIMUM"
	}
	return pricing.Breakdown{
		FinalCents: final, PriceUOM: uom, QtyBase: qty, LineTotalCents: total,
		Blocked: p.blocked, BlockPolicy: policy,
		MarginBps: p.marginBps, FloorBps: p.floorBps,
	}, nil
}

// qd2Svc composes the service with a chosen pricer and arbiter: the qd1
// harness's composition with the resolve seam swapped, so a rule price
// assertion is about the quote path's decision, never the pricing fixtures'
// rates of the day.
func qd2Svc(t *testing.T, pool *pgxpool.Pool, now time.Time, pricer PriceResolver, arb *qd1Arbiter) *StaffService {
	t.Helper()
	repo := NewRepository(pool).withNow(func() time.Time { return now })
	aud := audit.NewService(audit.NewRepository(pool), nil)
	rail := identity.NewApprovals(identity.NewApprovalRepository(pool))
	arb.records = pricing.NewService(pricing.NewRepository(pool))
	return NewStaffService(repo).
		WithAudit(aud).
		WithAuditTrail(aud).
		WithPricing(pricer).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		WithActorNamer(repo).
		WithRoleConfig(qd1Cfg).
		WithFloorArbiter(arb).
		WithFloorRail(rail).
		withCaller(qCallerFrom).
		withNow(func() time.Time { return now })
}

// qd2AuditRows reads the pricing log through the pricingListAudit read
// (the same service the route serves), filtered by entity type, then
// narrowed to one quote's rows.
func qd2AuditRows(t *testing.T, pool *pgxpool.Pool, quoteID string) []pricing.AuditLogRow {
	t.Helper()
	rows, err := pricing.NewService(pricing.NewRepository(pool)).
		ListAudit(context.Background(), pricing.AuditFilter{EntityType: "margin_enforcement", Limit: 200})
	if err != nil {
		t.Fatalf("pricingListAudit read: %v", err)
	}
	out := []pricing.AuditLogRow{}
	for _, r := range rows {
		if r.RefEntityType == "quote" && r.RefEntityID == quoteID {
			out = append(out, r)
		}
	}
	return out
}

// qd2CleanAudit drops the pricing log rows a test wrote for its quote.
func qd2CleanAudit(t *testing.T, pool *pgxpool.Pool, quoteID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pricing_audit_log WHERE ref_entity_type = 'quote' AND ref_entity_id = $1`, quoteID)
	})
}

// A line whose rule price resolves below the floor is added and accepted,
// as Sales (the owner) and as Sales manager (Manage on any quote in its
// branch), the line landing at the resolver's own price.
func TestQD2_RulePriceBelowFloor_AddLineAcceptedAsSalesAndManager(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	mgr := qd1User(t, pool, "mgr")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)
	qd2CleanAudit(t, pool, quoteID)

	for _, tc := range []struct {
		name string
		role string
		user string
	}{
		{name: "sales", role: "INSIDE_SALES", user: alice},
		{name: "sales manager", role: "SALES_MANAGER", user: mgr},
	} {
		pricer := qd2Pricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}
		arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
		svc := qd2Svc(t, pool, now, pricer, &arb)
		ctx := qCtxAs(intStaff(intTrenton))
		ctx = qd1WithRole(ctx, tc.role, intTrenton, tc.user)

		res, err := svc.AddLine(ctx, quoteID, prod, "qd2 below floor rule price", "3", uom)
		if err != nil {
			t.Fatalf("%s: a line at its rule price must be accepted as at the till, got %T: %v", tc.name, err, err)
		}
		if res.Line.UnitPriceCents != 600 || res.Line.LineTotalCents != 1800 {
			t.Errorf("%s: the line must land at the resolver's price (600 x 3), got unit %d total %d",
				tc.name, res.Line.UnitPriceCents, res.Line.LineTotalCents)
		}
	}
}

// CP0-3 answer A's read and log halves: the accepted line carries the
// rule_price_below_floor mark for a cost list caller, and one UPDATE row in
// the pricing log, read back through the pricingListAudit read filtered by
// entity type.
func TestQD2_RulePriceBelowFloor_MarkedAndLogged(t *testing.T) {
	pool := intPool(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	alice := qd1User(t, pool, "alice")
	_, quoteID, prod, uom := qd1SeedOwnedQuote(t, pool, "DRAFT", alice, true)
	qd2CleanAudit(t, pool, quoteID)

	pricer := qd2Pricer{blocked: true, finalCents: 600, marginBps: 500, floorBps: 1200}
	arb := qd1Arbiter{marginBps: 500, floorBps: 1200}
	svc := qd2Svc(t, pool, now, pricer, &arb)
	ctx := qCtxAs(intStaff(intTrenton))
	ctx = qd1WithRole(ctx, "INSIDE_SALES", intTrenton, alice)

	res, err := svc.AddLine(ctx, quoteID, prod, "qd2 below floor rule price", "3", uom)
	if err != nil {
		t.Fatalf("the add must be accepted: %v", err)
	}

	// The mark: a cost list caller reads the rule price below floor flag.
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
		t.Fatalf("the line's rule price sits below the floor, the mark must say so (CP0-3), got %v", marked)
	}

	// The log: one UPDATE row for the quote, the pricing editors' read.
	rows := qd2AuditRows(t, pool, quoteID)
	if len(rows) != 1 {
		t.Fatalf("the accepted below floor rule price must write one pricing log row, got %d", len(rows))
	}
	row := rows[0]
	if row.Action != "UPDATE" {
		t.Errorf("the rule price row is an UPDATE row, got %q", row.Action)
	}
	if row.UserID != alice {
		t.Errorf("the row must name who wrote the line, got %q want %s", row.UserID, alice)
	}
	if row.FieldName != "line_2" {
		t.Errorf("the row names its line (the seeded quote's second), got %q", row.FieldName)
	}
}
