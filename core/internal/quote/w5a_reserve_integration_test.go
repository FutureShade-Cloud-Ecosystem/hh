//go:build integration

// w5a_reserve_integration_test.go, the quote half of W4 seam 3, against the live database.
//
// Three claims, and the middle one is the acceptance criterion "quote soft→hard on accept":
//
//	a quote line write raises a SOFT hold that does NOT reduce available
//	Convert turns those soft holds into the ORDER's HARD holds, in ONE transaction
//	a lost/withdrawn quote releases its holds
//
// SOFT-DOES-NOT-GATE is asserted explicitly rather than assumed, because it is the single most
// consequential property of the quote side: a soft hold that quietly reduced available would stop a
// counter clerk selling stock because a contractor is thinking about it, which is worse than not
// holding at all.
//
// DIB-396: these fixtures write stock_reservations on a SEEDED product, so they take the stock lease
// in SHARE mode and restore what they touch.
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	w5aTrenton = "5d1b3f6e-0000-4000-9000-000000000001" // seeded TRT branch (0003)
	w5aProduct = "a7000000-0000-4000-9000-000000000019" // DBS-AGG-HPB, real stock at Trenton
	w5aUser    = "40000061-0000-4000-9000-000000000001" // a REAL users row (created_by has an FK)
)

// - the production reserver, through the production adapter shape -

type w5aInvReserver struct{ svc *inventory.Service }

func (a w5aInvReserver) ReserveTx(ctx context.Context, tx pgx.Tx, in StockReserveInput) (StockReserveResult, error) {
	res, err := a.svc.ReserveTx(ctx, tx, inventory.ReserveInput{
		ProductID: in.ProductID, BranchID: in.BranchID, LocationID: in.LocationID,
		ResType: in.ResType, QtyBase: in.QtyBase, SourceType: in.SourceType,
		SourceRefID: in.SourceRefID, Reason: in.Reason, CreatedBy: in.CreatedBy,
		EnforceAvailable: in.EnforceAvailable, ExpiresAt: in.ExpiresAt,
	})
	if err != nil {
		return StockReserveResult{}, err
	}
	return StockReserveResult{
		ReservationID: res.Reservation.ID, HoldMode: res.Reservation.HoldMode, Applied: res.Applied,
	}, nil
}

func (a w5aInvReserver) ReleaseBySourceTx(ctx context.Context, tx pgx.Tx, sourceType, sourceRefID, status string) (int, error) {
	return a.svc.ReleaseBySourceTx(ctx, tx, sourceType, sourceRefID, status)
}

// w5aAvail bridges inventory's published QuoteAvailabilityReader onto this module's own copy -
// the same two-line translation main.go's w5aQuoteAvailability performs.
type w5aAvail struct{ svc *inventory.Service }

func (a w5aAvail) AvailabilityAtBranch(ctx context.Context, productID, branchID string) (BranchStock, error) {
	b, err := a.svc.AvailabilityAtBranch(ctx, productID, branchID)
	if err != nil {
		return BranchStock{}, err
	}
	return BranchStock{
		ProductID: b.ProductID, BranchID: b.BranchID, BranchCode: b.BranchCode, BranchName: b.BranchName,
		OnHandBase: b.OnHandBase, ReservedBase: b.ReservedBase, InTransitBase: b.InTransitBase,
		AvailableBase: b.AvailableBase, SoftHeldBase: b.SoftHeldBase, AvgCostCents: b.AvgCostCents,
	}, nil
}

func (a w5aAvail) AvailabilityPeek(ctx context.Context, orgID, productID string) ([]BranchPeek, error) {
	rows, err := a.svc.AvailabilityPeek(ctx, orgID, productID)
	if err != nil {
		return nil, err
	}
	out := make([]BranchPeek, len(rows))
	for i, b := range rows {
		out[i] = BranchPeek(b)
	}
	return out, nil
}

// w5aFixedPricer prices at a fixed rate so the fixture's arithmetic is deterministic. The quote path
// still resolves EVERY price through the seam, what is faked is the answer, not the routing.
type w5aFixedPricer struct{}

func (w5aFixedPricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	return pricing.Breakdown{
		FinalCents: 1000, PriceUOM: in.UOMEntered, QtyBase: in.QtyEntered,
		LineTotalCents: int64(in.QtyEntered) * 1000,
	}, nil
}

// w5aCostBasis satisfies the cost-basis half of the quote edit seams.
type w5aCostBasis struct{}

func (w5aCostBasis) CurrentCostCents(context.Context, string, string) (int64, bool, error) {
	return 0, false, nil
}

type w5aRetail struct{}

func (w5aRetail) CustomerType(context.Context, string) (string, error) { return "RETAIL", nil }

// - fixtures -

// w5aStaffCtx is an active Trenton manager. UserID must be a REAL users row: stock_reservations
// carries an FK on created_by (0115), which in production is always satisfied by the guard-stamped
// caller.
func w5aStaffCtx() context.Context {
	// A Branch manager at Work (plan 3.3); the W5A fixtures' quotes are
	// created by this caller, so it owns them.
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: w5aUser, RoleCode: "BRANCH_MANAGER", Status: identity.StatusActive, Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Branches: map[string]bool{w5aTrenton: true},
	})
}

// w5aStaffSvc builds the staff quote service with the real inventory service behind both W5 ports.
func w5aStaffSvc(t *testing.T, pool *pgxpool.Pool) (*StaffService, *Repository) {
	t.Helper()
	repo := NewRepository(pool)
	invSvc := inventory.NewService(inventory.NewRepository(pool))
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewStaffService(repo).
		WithAudit(auditSvc).
		WithPricing(w5aFixedPricer{}).
		WithCostBasis(w5aCostBasis{}).
		WithCustomerType(w5aRetail{}).
		WithRoleConfig(qTestRoleConfig()).
		WithFloorArbiter(&unitFloorArbiter{}).
		WithStockReserver(w5aInvReserver{svc: invSvc}).
		WithAvailability(w5aAvail{svc: invSvc})
	repo.WithStockReserver(w5aInvReserver{svc: invSvc})
	w5aStampOrg(t, pool)
	if !svc.QuoteEditSeamsWired() {
		t.Fatal("the quote edit seams did not bind")
	}
	if !svc.StockSeamWired() || !svc.AvailabilitySeamWired() {
		t.Fatal("the W5 quote seams did not bind, the whole quote-hold lane would be inert")
	}
	return svc, repo
}

// w5aSeedQuote creates a customer + a DRAFT quote at Trenton with a real expiry, and registers the
// full cleanup INCLUDING the reservations and the qty_reserved projection.
func w5aSeedQuote(t *testing.T, pool *pgxpool.Pool) (cust, qid string) {
	t.Helper()
	ctx := context.Background()
	dib396LeaseShared(t, pool, dib396StockDriftLease)

	stamp := time.Now().UnixNano()
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'W5A Quote Tenant') RETURNING id::text`,
		fmt.Sprintf("W5AQ-%d", stamp)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until, created_by, salesperson_id)
		VALUES ($1, $2::uuid, $3::uuid, 'DRAFT', CURRENT_DATE + 14, $4::uuid, $4::uuid) RETURNING id::text`,
		fmt.Sprintf("#W5AQ-%d", stamp), cust, w5aTrenton, w5aUser).Scan(&qid); err != nil {
		t.Fatalf("seed quote: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		var ords []string
		rows, err := pool.Query(c, `SELECT id::text FROM orders WHERE quote_id = $1`, qid)
		if err == nil {
			for rows.Next() {
				var o string
				_ = rows.Scan(&o)
				ords = append(ords, o)
			}
			rows.Close()
		}
		refs := append([]string{qid}, ords...)
		for _, ref := range refs {
			_, _ = pool.Exec(c, `
				UPDATE stock_balances sb
				   SET qty_reserved = GREATEST(sb.qty_reserved - h.held, 0)
				  FROM (SELECT product_id, location_id, SUM(qty_base) AS held
				          FROM stock_reservations
				         WHERE source_ref_id = $1 AND status = 'ACTIVE' AND hold_mode = 'HARD'
				         GROUP BY product_id, location_id) h
				 WHERE sb.product_id = h.product_id AND sb.location_id = h.location_id`, ref)
			_, _ = pool.Exec(c, `DELETE FROM stock_reservations WHERE source_ref_id = $1`, ref)
			_, _ = pool.Exec(c, `DELETE FROM audit_log WHERE entity_id = $1`, ref)
		}
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE quote_id = $1`, qid)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, qid)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})
	return cust, qid
}

// w5aHoldFacts reads the holds a source document owns, straight from Postgres.
func w5aHoldFacts(t *testing.T, pool *pgxpool.Pool, sourceType, ref string) (n int, qty float64, mode string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*), COALESCE(SUM(qty_base),0), COALESCE(MAX(hold_mode),'')
		  FROM stock_reservations
		 WHERE source_type = $1 AND source_ref_id = $2 AND status = 'ACTIVE'`, sourceType, ref).
		Scan(&n, &qty, &mode); err != nil {
		t.Fatalf("read holds: %v", err)
	}
	return n, qty, mode
}

func w5aQtyReserved(t *testing.T, pool *pgxpool.Pool) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(qty_reserved),0) FROM stock_balances WHERE product_id = $1 AND branch_id = $2`,
		w5aProduct, w5aTrenton).Scan(&q); err != nil {
		t.Fatalf("read qty_reserved: %v", err)
	}
	return q
}

// - the claims -

// TestIntegration_W5A_QuoteLineWriteSoftReservesAndDoesNotGate. The hold appears, it is SOFT, and -
// the part that matters, availability is UNCHANGED.
func TestIntegration_W5A_QuoteLineWriteSoftReservesAndDoesNotGate(t *testing.T) {
	pool := intPool(t)
	_, qid := w5aSeedQuote(t, pool)
	svc, _ := w5aStaffSvc(t, pool)
	ctx := w5aStaffCtx()

	availBefore := w5aAvailableAt(t, pool, svc, ctx)
	reservedBefore := w5aQtyReserved(t, pool)

	if _, err := svc.AddLine(ctx, qid, w5aProduct, "W5A quoted line", "8", w5aBaseUOM(t, pool)); err != nil {
		t.Fatalf("add quote line: %v", err)
	}

	n, qty, mode := w5aHoldFacts(t, pool, "quote", qid)
	if n != 1 || qty != 8 {
		t.Fatalf("holds after the line write = %d row(s) for %v, want 1 for 8", n, qty)
	}
	if mode != "SOFT" {
		t.Fatalf("hold mode = %q, want SOFT under the default branch policy, a HARD hold on a live "+
			"quote would block a cash sale for a customer who is merely thinking about it", mode)
	}
	if got := w5aQtyReserved(t, pool); got != reservedBefore {
		t.Fatalf("qty_reserved moved to %v (from %v) on a SOFT hold, SOFT must not be subtracted",
			got, reservedBefore)
	}
	if got := w5aAvailableAt(t, pool, svc, ctx); got != availBefore {
		t.Fatalf("available moved to %v (from %v) on a SOFT hold, the whole point of SOFT is that it "+
			"is visible and does NOT gate", got, availBefore)
	}
	// The hold expires WITH the quote (valid_until + 14 days), not on a 72h clock that would kill it
	// while the quote is still live.
	var expires *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT expires_at FROM stock_reservations WHERE source_ref_id = $1 AND status='ACTIVE'`, qid).
		Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires == nil {
		t.Fatal("a quote hold with no expiry is a leak, an abandoned quote would hold forever")
	}
	if d := time.Until(*expires); d < 13*24*time.Hour {
		t.Fatalf("hold expires in %v, want ~14 days, it must outlive the quote's own validity window "+
			"rather than a default TTL", d)
	}
}

// TestIntegration_W5A_HoldsFollowTheLines, the sync is TOTAL, so a quantity change leaves exactly
// one hold and a removal leaves none. A per-line diff is where stale holds come from.
func TestIntegration_W5A_HoldsFollowTheLines(t *testing.T) {
	pool := intPool(t)
	_, qid := w5aSeedQuote(t, pool)
	svc, _ := w5aStaffSvc(t, pool)
	ctx := w5aStaffCtx()
	uom := w5aBaseUOM(t, pool)

	if _, err := svc.AddLine(ctx, qid, w5aProduct, "line", "8", uom); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.EditLineQty(ctx, qid, 1, "20"); err != nil {
		t.Fatalf("edit qty: %v", err)
	}
	n, qty, _ := w5aHoldFacts(t, pool, "quote", qid)
	if n != 1 || qty != 20 {
		t.Fatalf("after a qty edit: %d hold(s) for %v, want exactly 1 for 20, the 8-unit hold survived",
			n, qty)
	}
	if _, err := svc.RemoveLine(ctx, qid, 1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n, _, _ := w5aHoldFacts(t, pool, "quote", qid); n != 0 {
		t.Fatalf("after removing the only line: %d hold(s), want 0", n)
	}
}

// TestIntegration_W5A_ConvertTurnsSoftIntoHard is THE acceptance criterion for the quote side.
//
// It also pins the RULING: the soft holds close as CONSUMED (the interest became a commitment), not
// RELEASED, and the hard holds are keyed to the ORDER, which is why the conversion happens at
// Convert and not at Accept: the order id does not exist before that INSERT.
func TestIntegration_W5A_ConvertTurnsSoftIntoHard(t *testing.T) {
	pool := intPool(t)
	cust, qid := w5aSeedQuote(t, pool)
	svc, repo := w5aStaffSvc(t, pool)
	ctx := w5aStaffCtx()

	reservedBefore := w5aQtyReserved(t, pool)
	if _, err := svc.AddLine(ctx, qid, w5aProduct, "line", "12", w5aBaseUOM(t, pool)); err != nil {
		t.Fatalf("add line: %v", err)
	}
	if n, _, mode := w5aHoldFacts(t, pool, "quote", qid); n != 1 || mode != "SOFT" {
		t.Fatalf("precondition: %d SOFT hold(s) (%s), want 1 SOFT", n, mode)
	}

	// Walk the quote to ACCEPTED and give the account headroom, then Convert, the production path.
	// The DIB-387 credit gate runs inside Convert's transaction and refuses a COD account, which is
	// correct and is another module's rule; the fixture satisfies it rather than bypassing it.
	if _, err := pool.Exec(context.Background(),
		`UPDATE quotes SET status='ACCEPTED' WHERE id=$1`, qid); err != nil {
		t.Fatal(err)
	}
	setCreditLimit(t, pool, cust, 5_000_00)
	res, err := repo.Convert(context.Background(), cust, qid, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	// The SOFT holds are gone, closed as CONSUMED.
	if n, _, _ := w5aHoldFacts(t, pool, "quote", qid); n != 0 {
		t.Fatalf("%d SOFT hold(s) survived the conversion, want 0", n)
	}
	var closed string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM stock_reservations WHERE source_ref_id = $1 AND status <> 'ACTIVE'`, qid).
		Scan(&closed); err != nil {
		t.Fatalf("read the closed quote hold: %v", err)
	}
	if closed != ResConsumed {
		t.Fatalf("the quote's hold closed as %q, want CONSUMED, the interest did not evaporate, it "+
			"became a commitment, and RELEASED would say the opposite", closed)
	}

	// The ORDER now holds it, HARD, and availability finally moved.
	n, qty, mode := w5aHoldFacts(t, pool, "order", res.OrderID)
	if n != 1 || qty != 12 || mode != "HARD" {
		t.Fatalf("order holds after conversion: %d row(s) for %v (%s), want 1 HARD hold for 12, "+
			"soft→hard is the whole point of the conversion", n, qty, mode)
	}
	if got := w5aQtyReserved(t, pool) - reservedBefore; got != 12 {
		t.Fatalf("qty_reserved moved by %v, want 12, a HARD hold MUST reduce available (a SOFT one "+
			"did not, and that asymmetry is the feature)", got)
	}
}

// TestIntegration_W5A_ReleasingALostQuoteReturnsTheHolds. A quote that is lost is no longer interest
// in that stock; a hold left behind is what makes a visibility surface stop being believed.
func TestIntegration_W5A_ReleasingALostQuoteReturnsTheHolds(t *testing.T) {
	pool := intPool(t)
	_, qid := w5aSeedQuote(t, pool)
	svc, _ := w5aStaffSvc(t, pool)
	ctx := w5aStaffCtx()

	if _, err := svc.AddLine(ctx, qid, w5aProduct, "line", "5", w5aBaseUOM(t, pool)); err != nil {
		t.Fatalf("add line: %v", err)
	}
	n, err := svc.ReleaseQuoteHolds(ctx, qid, ResReleased)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if n != 1 {
		t.Fatalf("released %d hold(s), want 1", n)
	}
	if n, _, _ := w5aHoldFacts(t, pool, "quote", qid); n != 0 {
		t.Fatalf("%d ACTIVE hold(s) survived the release", n)
	}
}

// TestIntegration_W5A_AvailabilityChipsGradeAgainstAvailable, the at-quote surface. A line the
// branch can cover is GREEN; a line larger than everything on hand is THIN or NONE. The seam is the
// one W4-B published and compile-asserted; this proves the CONSUMER binds it and grades correctly.
func TestIntegration_W5A_AvailabilityChipsGradeAgainstAvailable(t *testing.T) {
	pool := intPool(t)
	_, qid := w5aSeedQuote(t, pool)
	svc, _ := w5aStaffSvc(t, pool)
	ctx := w5aStaffCtx()
	uom := w5aBaseUOM(t, pool)

	// SELF-CALIBRATING against whatever the branch actually has right now.
	//
	// An earlier version asserted "a 1-unit line grades GREEN", which passes on a pristine seed and
	// fails the moment a real confirmed order holds Trenton's stock, which is precisely the state
	// this whole wave creates. A test that only passes on an untouched database is a flake with a
	// delayed fuse, so the quantities below are derived from the LIVE availability instead.
	avail := w5aAvailableAt(t, pool, svc, ctx)

	if avail > 0 {
		if _, err := svc.AddLine(ctx, qid, w5aProduct, "a line the branch can cover",
			formatQty(avail), uom); err != nil {
			t.Fatalf("add line: %v", err)
		}
		lines, wired, err := svc.QuoteAvailability(ctx, qid)
		if err != nil {
			t.Fatalf("availability: %v", err)
		}
		if !wired {
			t.Fatal("the availability seam reported unwired")
		}
		if len(lines) != 1 {
			t.Fatalf("graded %d line(s), want 1", len(lines))
		}
		if lines[0].Grade != AvailGreen {
			t.Fatalf("a line for exactly the %v available graded %q, want GREEN, the boundary case "+
				"(qty == available) must not fall to THIN on a decimal round trip", avail, lines[0].Grade)
		}
		if lines[0].Branch.BranchCode == "" {
			t.Error("the chip must carry the branch it is grading, or a cross-branch peek has no anchor")
		}
	} else {
		// The branch has nothing spare. Any positive quantity must grade NONE, never GREEN off a
		// plentiful on-hand that is entirely spoken for, which is the oversell DIB-60 exists to stop.
		if _, err := svc.AddLine(ctx, qid, w5aProduct, "a line the branch cannot cover", "1", uom); err != nil {
			t.Fatalf("add line: %v", err)
		}
		lines, _, err := svc.QuoteAvailability(ctx, qid)
		if err != nil {
			t.Fatal(err)
		}
		if lines[0].Grade != AvailNone {
			t.Fatalf("a line against %v available (on hand %v, held %v) graded %q, want NONE",
				avail, lines[0].Branch.OnHandBase, lines[0].Branch.ReservedBase, lines[0].Grade)
		}
	}

	// A line far larger than anything on hand grades NONE or THIN, never GREEN.
	if _, err := svc.EditLineQty(ctx, qid, 1, "999999"); err != nil {
		t.Fatalf("edit qty: %v", err)
	}
	lines, _, err := svc.QuoteAvailability(ctx, qid)
	if err != nil {
		t.Fatal(err)
	}
	if lines[0].Grade == AvailGreen {
		t.Fatalf("a 999999-unit line against %v available graded GREEN, the chip is not grading "+
			"against availability at all", lines[0].Branch.AvailableBase)
	}

	// The cross-branch peek returns EVERY branch, including ones with none (inventory's stated rule:
	// a missing row reads as a bug, not as an answer).
	peek, err := svc.CrossBranchPeek(ctx, w5aOrgID, w5aProduct)
	if err != nil {
		t.Fatalf("cross-branch peek: %v", err)
	}
	if len(peek) < 2 {
		t.Fatalf("peek returned %d branch row(s), want every branch", len(peek))
	}
}

// w5aBaseUOM reads the fixture product's base UOM so a line is written in a unit the product has.
func w5aBaseUOM(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var uom string
	if err := pool.QueryRow(context.Background(),
		`SELECT base_uom::text FROM products WHERE id = $1`, w5aProduct).Scan(&uom); err != nil {
		t.Fatalf("read base uom: %v", err)
	}
	return uom
}

// w5aAvailableAt reads AVAILABLE at Trenton through the same seam the chips use.
func w5aAvailableAt(t *testing.T, pool *pgxpool.Pool, svc *StaffService, ctx context.Context) float64 {
	t.Helper()
	rows, err := svc.CrossBranchPeek(ctx, w5aOrgID, w5aProduct)
	if err != nil {
		t.Fatalf("read availability: %v", err)
	}
	for _, r := range rows {
		if r.BranchID == w5aTrenton {
			return r.AvailableBase
		}
	}
	t.Fatal("Trenton absent from the cross-branch read")
	return 0
}

// w5aOrgID is an invented org anchor the seeded branches (which carry no org) are stamped with for the
// leg, so the cross-branch peek, which holds to the caller's org, has branches to answer with.
const w5aOrgID = "88a00000-0000-4000-8000-0000000000a5"

func w5aStampOrg(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO org (id, slug, name) VALUES ($1, 'ux88-w5a-org', 'UX88 W5A Org') ON CONFLICT (id) DO NOTHING`, w5aOrgID); err != nil {
		t.Fatalf("plant the org anchor: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH' AND org_id IS NULL`, w5aOrgID); err != nil {
		t.Fatalf("stamp the seeded branches: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE locations SET org_id = NULL WHERE org_id = $1`, w5aOrgID)
	})
}
