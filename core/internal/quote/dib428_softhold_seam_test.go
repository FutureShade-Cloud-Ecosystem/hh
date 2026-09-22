package quote

// dib428_softhold_seam_test.go, the quote-side consumer seam for W6-C's soft-hold-expiry read.
//
// What is worth testing here is NOT that a map copies fields. It is the one decision this side owns:
// an ended hold whose line is held AGAIN is not news, and must not put a banner on a quote that is
// fully covered. Everything downstream, the sentence, the count, the banner appearing at all -
// keys off AnyExpired, so getting that boolean wrong is the whole feature wrong.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// - the fakes -

// dib428Reader is the projection port with a scripted answer, the shape *order.Service satisfies
// through the main.go adapter (NOTES-FIX-428.md PARK 1).
type dib428Reader struct {
	st   SoftHoldStatus
	err  error
	asks int
}

func (r *dib428Reader) ExpiredSoftHolds(_ context.Context, quoteID string) (SoftHoldStatus, error) {
	r.asks++
	if r.err != nil {
		return SoftHoldStatus{}, r.err
	}
	st := r.st
	st.QuoteID = quoteID
	return st, nil
}

// dib428Store is the fake staffStore PLUS the holdStore half, so the service can read the two facts
// the consumer rules are decided against: this quote's currently-ACTIVE holds, and its lines.
type dib428Store struct {
	*fakeStaffStore
	held  []QuoteHold
	err   error
	lines []QuoteViewLine
}

func (s *dib428Store) QuoteHolds(_ context.Context, _ string) ([]QuoteHold, error) {
	return s.held, s.err
}

func (s *dib428Store) StaffDetail(ctx context.Context, quoteID string) (StaffQuoteView, error) {
	v, err := s.fakeStaffStore.StaffDetail(ctx, quoteID)
	if err != nil {
		return v, err
	}
	v.Lines = s.lines
	return v, nil
}

func (s *dib428Store) ReservableLinesTx(context.Context, pgx.Tx, string) ([]reservableLine, error) {
	return nil, nil
}

func (s *dib428Store) QuoteHoldContextTx(context.Context, pgx.Tx, string) (string, *time.Time, error) {
	return qBranch, nil, nil
}

func (s *dib428Store) LockQuoteTx(context.Context, string, func(context.Context, pgx.Tx) error) error {
	return nil
}

var _ holdStore = (*dib428Store)(nil)

// dib428Fixture wires the staff service over both halves, with the caller pinned. The quote carries
// one line for qProd by default, the state a quote is in when a hold on it can matter at all.
func dib428Fixture(t *testing.T, held []QuoteHold, reader *dib428Reader) (*StaffService, *dib428Store) {
	t.Helper()
	store := &dib428Store{
		fakeStaffStore: newFakeStaffStore("SENT"),
		held:           held,
		lines:          []QuoteViewLine{{LineNo: 1, ProductID: qProd, QtyBase: "12.0000"}},
	}
	svc := NewStaffService(store).
		WithPricing(qPricer{}).
		WithCostBasis(qInv{}).
		WithCustomerType(qCustType{}).
		withCaller(qCallerFrom).
		withNow(qNow)
	if reader != nil {
		svc.WithSoftHoldExpiry(reader)
	}
	return svc, store
}

func dib428Ended(product, name string, qty float64, status string) ExpiredHold {
	return ExpiredHold{
		ReservationID: "res-" + name, ProductID: product, ProductName: name, SKU: "SKU-" + name,
		BranchID: qBranch, QtyBase: qty, Status: status, EndedAt: "2026-08-10T09:00:00Z",
	}
}

// - the claims -

// An UNBOUND seam must be silent AND say so. "We did not ask" and "nothing expired" are different
// answers, and only the first one is a deployment note.
func TestDIB428_UnwiredSeamIsSilentAndSaysSoRatherThanErroring(t *testing.T) {
	svc, _ := dib428Fixture(t, nil, nil)
	st, wired, err := svc.SoftHoldExpiry(qCtxAs(qStaff()), qOrg)
	if err != nil {
		t.Fatalf("an unwired decoration must not fail a quote read: %v", err)
	}
	if wired {
		t.Error("seam_wired must be false when nothing is bound")
	}
	if st.AnyExpired || len(st.Expired) != 0 {
		t.Errorf("an unwired seam must report nothing, got %+v", st)
	}
	if st.Expired == nil {
		t.Error("Expired must marshal as [] rather than null, the surface iterates it")
	}
}

// The projection reports ended holds; the surface reports them back, with the product and quantity
// a rep can act on.
func TestDIB428_ExpiredHoldsReachTheSurfaceWithProductAndQuantity(t *testing.T) {
	reader := &dib428Reader{st: SoftHoldStatus{
		ActiveCount: 3,
		Expired: []ExpiredHold{
			dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired),
		},
		AnyExpired: true,
	}}
	svc, _ := dib428Fixture(t, nil, reader)

	st, wired, err := svc.SoftHoldExpiry(qCtxAs(qStaff()), qOrg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !wired {
		t.Error("seam_wired must be true once bound")
	}
	if !st.AnyExpired || len(st.Expired) != 1 {
		t.Fatalf("want one expired hold reported, got %+v", st)
	}
	if st.ActiveCount != 3 {
		t.Errorf("active = %d, want 3, the banner says '1 of 4', not 'everything is gone'", st.ActiveCount)
	}
	if got := st.Expired[0]; got.ProductName != "Bergerac Pavers" || got.QtyBase != 12 {
		t.Errorf("an expired hold must name its product and quantity, got %+v", got)
	}
	if st.QuoteID != qOrg {
		t.Errorf("quote id = %q, want the RESOLVED id %q (the surface may have passed #Q-1)", st.QuoteID, qOrg)
	}
}

// THE CASE THIS SEAM EXISTS FOR. The rep re-holds (or just edits the line's quantity, which re-syncs
// the holds). The EXPIRED row stays in the table forever; the line is held again. Read literally the
// banner would never clear, the stale banner soft_hold_expiry.go's own header warns about, and a
// re-hold button that visibly does nothing.
func TestDIB428_ARehedLineIsNoLongerNews_AndTheBannerGoesQuiet(t *testing.T) {
	reader := &dib428Reader{st: SoftHoldStatus{
		ActiveCount: 1,
		Expired:     []ExpiredHold{dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired)},
		AnyExpired:  true, // the projection is right: that hold DID end
	}}
	held := []QuoteHold{{
		ReservationID: "res-new", ProductID: qProd, ProductName: "Bergerac Pavers",
		BranchID: qBranch, HoldMode: "SOFT", QtyBase: 12,
	}}
	svc, _ := dib428Fixture(t, held, reader)

	st, _, err := svc.SoftHoldExpiry(qCtxAs(qStaff()), qOrg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(st.Expired) != 0 {
		t.Errorf("a line that is held again must drop off the banner, got %+v", st.Expired)
	}
	if st.AnyExpired {
		t.Error("AnyExpired must be derived from what the surface will RENDER, not copied from the " +
			"projection, otherwise the banner survives the re-hold that was supposed to clear it")
	}
}

// Supersession is keyed on (product, branch), not on the reservation id, because the hold sync is
// TOTAL: release-all then re-reserve, so a re-held line never carries its old row's id. Keyed by id
// the rule would never fire, which is the same as not having written it.
func TestDIB428_SupersessionKeysOnProductAndBranch_NotOnReservationID(t *testing.T) {
	roman := "77777777-7777-4777-8777-777777777777"
	ended := []ExpiredHold{
		dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired),
		dib428Ended(roman, "Roman Slab", 4, ResReleased),
	}
	onQuote := map[string]bool{qProd: true, roman: true}
	// A DIFFERENT reservation id for the same product, which is what a re-sync always produces.
	held := []QuoteHold{{ReservationID: "res-brand-new", ProductID: qProd, BranchID: qBranch, QtyBase: 12}}

	got := expiredHoldNews(ended, held, onQuote)
	if len(got) != 1 || got[0].ProductName != "Roman Slab" {
		t.Fatalf("want only the still-unheld product to survive, got %+v", got)
	}

	// A hold re-raised at a DIFFERENT branch does not cover this line: the stock the quote was
	// relying on is still not held where the quote is being fulfilled from.
	elsewhere := []QuoteHold{{ProductID: qProd, BranchID: qOther, QtyBase: 12}}
	if got := expiredHoldNews(ended, elsewhere, onQuote); len(got) != 2 {
		t.Errorf("a hold at another branch must not silence this quote's line, got %+v", got)
	}
}

// THE FALSE ALARM THAT WAS MEASURED, NOT IMAGINED. The hold sync is total, every line write
// releases ALL of a quote's holds and re-raises them, so an ordinary second line write leaves a
// RELEASED row behind on a quote where nothing has gone wrong. The integration test caught this
// reporting "2 expired / 1 active" on a two-line quote whose only real event was one sweep.
//
// One line is ONE piece of news, and the news is its most recent ending.
func TestDIB428_ChurnFromTheTotalSyncIsNotNews_OneLineIsOneItem(t *testing.T) {
	released := dib428Ended(qProd, "Bergerac Pavers", 12, ResReleased)
	released.EndedAt = "2026-08-01T09:00:00Z" // the ordinary re-sync when line 2 was added
	expired := dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired)
	expired.EndedAt = "2026-08-10T02:00:00Z" // the sweep, later, the real event
	// Deliberately OLDEST FIRST: the rule must not depend on the projection's ORDER BY, which is a
	// detail of a module this one does not own.
	got := expiredHoldNews([]ExpiredHold{released, expired}, nil, map[string]bool{qProd: true})

	if len(got) != 1 {
		t.Fatalf("one line must produce one banner item, got %d: %+v", len(got), got)
	}
	if got[0].Status != ResExpired {
		t.Errorf("the item must carry the line's MOST RECENT ending (%q), got %q, a rep told "+
			"'released' about a hold the sweep expired is being told the wrong story",
			ResExpired, got[0].Status)
	}
}

// A line the rep REMOVED released its hold, by their own hand. Telling them its stock is no longer
// held is telling them what they just did, and it is on a quote that no longer wants that stock.
func TestDIB428_ALineTheRepRemovedIsNotAnExpiry(t *testing.T) {
	gone := "88888888-8888-4888-8888-888888888888"
	ended := []ExpiredHold{
		dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired),
		dib428Ended(gone, "Roman Slab", 4, ResReleased),
	}
	got := expiredHoldNews(ended, nil, map[string]bool{qProd: true}) // only qProd is still quoted
	if len(got) != 1 || got[0].ProductID != qProd {
		t.Fatalf("only lines the quote still carries are news, got %+v", got)
	}

	// nil lines means "could not read them", which must switch the rule OFF rather than silence
	// everything, an unavailable read must not hide a real expiry.
	if got := expiredHoldNews(ended, nil, nil); len(got) != 2 {
		t.Errorf("unknown lines must leave the projection's answer alone, got %+v", got)
	}
}

// A projection that fails is NOT reported as "nothing expired". The endpoint is its own fetch behind
// its own component, so an error here costs the decoration and not the rep's quote, and a swallowed
// error would be indistinguishable from silence, which is the exact defect DIB-428 closes.
func TestDIB428_AFailingProjectionIsReported_NotReadAsNothingExpired(t *testing.T) {
	boom := errors.New("stock_reservations: connection reset")
	svc, _ := dib428Fixture(t, nil, &dib428Reader{err: boom})

	_, wired, err := svc.SoftHoldExpiry(qCtxAs(qStaff()), qOrg)
	if err == nil {
		t.Fatal("a projection failure must surface as an error, not as a confident empty banner")
	}
	if !wired {
		t.Error("the seam IS bound, the failure is the read's, and the envelope must not blame wiring")
	}
}

// Branch reach is proved the same way every other read on this service proves it: a caller who
// cannot reach the quote's branch gets ErrNotFound, and the projection is never even asked.
func TestDIB428_AQuoteOutOfTheCallersReachIsNotFound_AndIsNeverRead(t *testing.T) {
	reader := &dib428Reader{st: SoftHoldStatus{
		Expired: []ExpiredHold{dib428Ended(qProd, "Bergerac Pavers", 12, ResExpired)}, AnyExpired: true,
	}}
	svc, store := dib428Fixture(t, nil, reader)
	store.branchID = qOther // the quote lives at a branch this caller cannot see

	if _, _, err := svc.SoftHoldExpiry(qCtxAs(qStaff()), qOrg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound (never an existence leak), got %v", err)
	}
	if reader.asks != 0 {
		t.Errorf("the projection was read %d time(s) for a quote the caller cannot reach", reader.asks)
	}
}
