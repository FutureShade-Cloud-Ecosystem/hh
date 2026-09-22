//go:build integration

// dib428_softhold_expiry_integration_test.go, the whole road, on real rows:
//
//	a real quote → real SOFT holds raised by a real line write
//	  → the REAL inventory sweep (ExpireDueReservations) times one of them out
//	    → order's projection sees it → quote's consumer seam renders it
//
// The expiry is driven through the PRODUCTION sweep rather than by INSERTing a row that is already
// EXPIRED. Both are legitimate (W6-C's own unit-level fixtures seed the terminal states directly, to
// get all four outcomes into one quote cheaply), but the claim DIB-428 has to make is that the thing
// which ACTUALLY ends a hold in production ends up on a rep's screen. A seeded EXPIRED row proves the
// query; the sweep proves the feature.
//
// The adapter below is the production binding named in NOTES-FIX-428.md PARK 1, compiled and
// exercised here. A composition-root adapter is code no test in any module can reach (the W6-C M5
// lesson), so the one place it can be proven is a test that declares the same shape.
package quote

import (
	"context"
	"fmt"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dib428OrderExpiry is main.go's adapter, field for field: order's projection mapped onto the types
// this module owns. Same shape as w5aQuoteAvailability does for inventory.BranchStock.
type dib428OrderExpiry struct{ svc *order.Service }

func (a dib428OrderExpiry) ExpiredSoftHolds(ctx context.Context, quoteID string) (SoftHoldStatus, error) {
	st, err := a.svc.ExpiredSoftHolds(ctx, quoteID)
	if err != nil {
		return SoftHoldStatus{}, err
	}
	out := SoftHoldStatus{
		QuoteID: st.QuoteID, ActiveCount: st.ActiveCount, AnyExpired: st.AnyExpired,
		Expired: make([]ExpiredHold, 0, len(st.Expired)),
	}
	for _, h := range st.Expired {
		out.Expired = append(out.Expired, ExpiredHold{
			ReservationID: h.ReservationID, ProductID: h.ProductID, ProductName: h.ProductName,
			SKU: h.SKU, BranchID: h.BranchID, QtyBase: h.QtyBase, Status: h.Status,
			ExpiresAt: h.ExpiresAt, EndedAt: h.EndedAt,
		})
	}
	return out, nil
}

// dib428SecondProduct picks a seeded product that is NOT the one w5a's fixtures use, so a quote can
// carry one lapsed line and one that still stands, which is the only shape that can prove the
// "N of M" arithmetic on the banner.
func dib428SecondProduct(t *testing.T, pool *pgxpool.Pool) (id, uom string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text, base_uom::text FROM products
		 WHERE id <> $1::uuid AND is_active ORDER BY sku LIMIT 1`, w5aProduct).Scan(&id, &uom); err != nil {
		t.Fatalf("pick a second seeded product: %v", err)
	}
	return id, uom
}

// dib428Svc is w5aStaffSvc plus the DIB-428 seam, bound through the production adapter.
func dib428Svc(t *testing.T, pool *pgxpool.Pool) *StaffService {
	t.Helper()
	svc, _ := w5aStaffSvc(t, pool)
	svc.WithSoftHoldExpiry(dib428OrderExpiry{svc: order.NewService(order.NewRepository(pool))})
	if !svc.SoftHoldExpirySeamWired() {
		t.Fatal("the DIB-428 seam did not bind, the banner would never appear")
	}
	return svc
}

// dib428Age drags one product's ACTIVE hold on this quote into the past so the next sweep is due to
// take it. This is the ageing W6-C's own fixtures do (`expires_at = now() - interval '2 days'`),
// applied to a hold that a real line write raised.
func dib428Age(t *testing.T, pool *pgxpool.Pool, quoteID, productID string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `
		UPDATE stock_reservations SET expires_at = now() - interval '2 days'
		 WHERE source_type = 'quote' AND source_ref_id = $1::uuid AND product_id = $2::uuid
		   AND status = 'ACTIVE'`, quoteID, productID)
	if err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatal("no ACTIVE hold to age, the line write did not raise one, so this test would be vacuous")
	}
}

// TestIntegrationDIB428_TheSweepsSilenceReachesTheQuoteSurface is the ticket, end to end.
func TestIntegrationDIB428_TheSweepsSilenceReachesTheQuoteSurface(t *testing.T) {
	pool := intPool(t)
	ctx := w5aStaffCtx()
	_, qid := w5aSeedQuote(t, pool)
	svc := dib428Svc(t, pool)
	invSvc := inventory.NewService(inventory.NewRepository(pool))

	uom := w5aBaseUOM(t, pool)
	other, otherUOM := dib428SecondProduct(t, pool)
	if _, err := svc.AddLine(ctx, qid, w5aProduct, "the line that lapses", "12", uom); err != nil {
		t.Fatalf("add line 1: %v", err)
	}
	if _, err := svc.AddLine(ctx, qid, other, "the line that stands", "4", otherUOM); err != nil {
		t.Fatalf("add line 2: %v", err)
	}

	// Before the sweep: two live holds and NOTHING to say. A banner here would be noise on a quote
	// that is entirely covered, which is the state most quotes are in.
	st, wired, err := svc.SoftHoldExpiry(ctx, qid)
	if err != nil {
		t.Fatalf("read (clean quote): %v", err)
	}
	if !wired {
		t.Fatal("seam_wired must be true, the projection is bound")
	}
	if st.AnyExpired || len(st.Expired) != 0 {
		t.Fatalf("a quote whose holds all stand must say nothing, got %+v", st)
	}
	if st.ActiveCount != 2 {
		t.Fatalf("active = %d, want 2 (one hold per line)", st.ActiveCount)
	}

	// The rep goes away for three weeks. The sweep, the REAL one, the same job that runs nightly -
	// times the first line's hold out. Nothing writes to the quote; nothing tells anybody.
	dib428Age(t, pool, qid, w5aProduct)
	if _, err := invSvc.ExpireDueReservations(context.Background()); err != nil {
		t.Fatalf("the expiry sweep: %v", err)
	}

	st, _, err = svc.SoftHoldExpiry(ctx, qid)
	if err != nil {
		t.Fatalf("read (after the sweep): %v", err)
	}
	if !st.AnyExpired {
		t.Fatal("the sweep ended a hold and the quote surface still says nothing, DIB-428 exactly")
	}
	if len(st.Expired) != 1 || st.ActiveCount != 1 {
		t.Fatalf("want 1 of 2 lines no longer held, got %d expired / %d active (%+v)",
			len(st.Expired), st.ActiveCount, st.Expired)
	}
	got := st.Expired[0]
	if got.ProductID != w5aProduct {
		t.Errorf("the wrong line is named: %+v", got)
	}
	if got.Status != ResExpired {
		t.Errorf("status = %q, want EXPIRED, this hold was ended by the sweep, not released", got.Status)
	}
	if got.QtyBase != 12 || got.ProductName == "" {
		t.Errorf("the banner needs a product and a quantity to be actionable, got %+v", got)
	}
	if got.EndedAt == "" {
		t.Error("an ended hold must say when it ended")
	}

	// - and the half the projection alone cannot get right -
	// The rep re-holds from the banner: the SAME endpoint the affordance posts to. The EXPIRED row
	// stays in the table forever, so a surface that trusted the projection literally would keep
	// shouting about a line that is now held again, a button that visibly does nothing.
	applied, err := svc.ResyncQuoteHolds(ctx, qid)
	if err != nil {
		t.Fatalf("re-hold: %v", err)
	}
	if applied != 2 {
		t.Fatalf("re-hold applied %d hold(s), want 2, the affordance must actually re-raise them", applied)
	}
	st, _, err = svc.SoftHoldExpiry(ctx, qid)
	if err != nil {
		t.Fatalf("read (after the re-hold): %v", err)
	}
	if st.AnyExpired || len(st.Expired) != 0 {
		t.Fatalf("the banner survived the re-hold that was supposed to clear it: %+v", st.Expired)
	}
	if st.ActiveCount != 2 {
		t.Errorf("active = %d after the re-hold, want 2", st.ActiveCount)
	}

	// The EXPIRED row is still there, supersession is a READ-side judgement, not a delete. Proving
	// it explicitly, because "the banner cleared" would also be true if the re-hold had destroyed
	// history, and destroying history is not what happened.
	var ended int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM stock_reservations
		 WHERE source_type='quote' AND source_ref_id=$1::uuid AND status='EXPIRED'`, qid).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Errorf("expired rows = %d, want the 1 the sweep wrote, the read filters, it does not erase", ended)
	}
}

// TestIntegrationDIB428_AConvertedQuoteIsNotAlarmed. The CONSUMED case, at the surface. W6-C proves
// order's read excludes it; this proves nothing downstream puts it back, a quote that converted and
// shipped is the happiest outcome in the system and must never wear an expiry banner.
func TestIntegrationDIB428_AConvertedQuoteIsNotAlarmed(t *testing.T) {
	pool := intPool(t)
	ctx := w5aStaffCtx()
	_, qid := w5aSeedQuote(t, pool)
	svc := dib428Svc(t, pool)

	if _, err := svc.AddLine(ctx, qid, w5aProduct, "line", "6", w5aBaseUOM(t, pool)); err != nil {
		t.Fatalf("add line: %v", err)
	}
	// Convert closes the soft holds CONSUMED, the status the conversion path writes and nothing else
	// may. Driving it through ReleaseQuoteHolds keeps this test to the seam under examination rather
	// than re-testing W5-A's whole conversion.
	if _, err := svc.ReleaseQuoteHolds(ctx, qid, ResConsumed); err != nil {
		t.Fatalf("consume the holds: %v", err)
	}

	st, _, err := svc.SoftHoldExpiry(ctx, qid)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if st.AnyExpired || len(st.Expired) != 0 {
		t.Fatalf("a CONSUMED hold is the quote converting and the goods shipping; it must never read "+
			"as an expiry: %+v", st.Expired)
	}
	if st.ActiveCount != 0 {
		t.Errorf("active = %d, want 0, the holds were consumed", st.ActiveCount)
	}

	// Belt and braces: the rows really are CONSUMED, so this test is not passing because the fixture
	// quietly failed to write anything.
	var n int
	if err := pool.QueryRow(context.Background(), fmt.Sprintf(`
		SELECT COUNT(*) FROM stock_reservations
		 WHERE source_type='quote' AND source_ref_id=$1::uuid AND status='%s'`, ResConsumed),
		qid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("consumed rows = %d, want 1, the fixture proved nothing", n)
	}
}
