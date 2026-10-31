//go:build integration

// portal_payoff_integration_test.go, TM-2b: the portal charges the discounted payoff.
//
// The portal's payable (PayableInvoices) answers, for each invoice inside its 2/10 window, the payoff:
// the open amount less the discount the invoice's own terms allow on the house day, with the discount
// and its last day beside it. The intent is claimed for that payoff, the gateway charges it, and the
// settle takes the discount with no change on account. Outside the window nothing changes.
//
// What the portal allows on a partly paid invoice: the portal always pays the whole open amount of the
// invoices it is given (it names no amount), so the payoff of a partly paid invoice is its open amount
// less the terms' percent of that open amount, held under the running cap the desk uses (the terms'
// percent of the whole base, less the discount earlier receipts already took).
//
// The race: the amount charged is fixed at the intent. A payment started inside the window and
// settled after the house midnight keeps the discount it was quoted, because the customer was told
// and charged the payoff on the strength of it; refusing the discount at the settle would leave the
// invoice open by the discount they were never asked to pay.
//
// Run: go test -tags integration -count=1 ./internal/payments/ -run PortalPayoff
package payments

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// chargeLog is a gateway that records the amounts it was asked to charge and runs a hook inside the
// charge, so a leg can move the clock between the charge and the settle.
type chargeLog struct {
	*StubGateway
	mu       sync.Mutex
	amounts  []int64
	onCharge func()
}

func (g *chargeLog) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	g.mu.Lock()
	g.amounts = append(g.amounts, req.AmountCents)
	hook := g.onCharge
	g.mu.Unlock()
	if hook != nil {
		hook()
	}
	return g.StubGateway.Charge(ctx, req)
}

func (g *chargeLog) charged() []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int64(nil), g.amounts...)
}

// payoffWorld is the apply world with a service over the same repository and a movable clock.
type payoffWorld struct {
	*applyWorld
	svc *Service
	gw  *chargeLog
	now time.Time
	mu  sync.Mutex
}

func newPayoffWorld(t *testing.T) *payoffWorld {
	t.Helper()
	aw := newApplyWorld(t).bindTerms()
	pw := &payoffWorld{applyWorld: aw, gw: &chargeLog{StubGateway: NewStubGateway()}, now: time.Now()}
	aw.repo = aw.repo.WithDiscountClock(func() time.Time {
		pw.mu.Lock()
		defer pw.mu.Unlock()
		return pw.now
	})
	pw.svc = NewService(aw.repo, pw.gw)
	// Registered after the world's own cleanup, so it runs before it: intents hold the customer.
	t.Cleanup(func() {
		if _, err := aw.pool.Exec(context.Background(),
			`DELETE FROM payment_intents WHERE customer_id = $1::uuid`, aw.customerID); err != nil {
			t.Errorf("cleanup payment_intents: %v", err)
		}
	})
	return pw
}

func (pw *payoffWorld) setNow(at time.Time) {
	pw.mu.Lock()
	pw.now = at
	pw.mu.Unlock()
}

// payPortal runs the portal's pay flow for the named invoices on a one time token.
func (pw *payoffWorld) payPortal(t *testing.T, ids ...string) Payment {
	t.Helper()
	p, err := pw.svc.Pay(context.Background(), pw.customerID, PayRequest{InvoiceIDs: ids, Nonce: "nonce-" + pw.tag})
	if err != nil {
		t.Fatalf("portal pay: %v", err)
	}
	return p
}

func (pw *payoffWorld) lastCharge(t *testing.T) int64 {
	t.Helper()
	got := pw.gw.charged()
	if len(got) == 0 {
		t.Fatalf("the gateway was never charged")
	}
	return got[len(got)-1]
}

// TestPortalPayoff_InsideTheWindowChargesTheDiscountedPayoff: 2/10 on a 100000 invoice, paid on the
// discount day. The quote is 98000 with a 2000 discount and its last day; the gateway is charged
// 98000; the invoice closes with discount 2000 and no change on account.
func TestPortalPayoff_InsideTheWindowChargesTheDiscountedPayoff(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "IN", 100000, day)

	quote, err := pw.repo.PayableInvoices(ctx, pw.customerID, []string{inv})
	if err != nil {
		t.Fatalf("payable: %v", err)
	}
	if len(quote) != 1 || quote[0].AmountCents != 98000 || quote[0].DiscountCents != 2000 || quote[0].DiscountDay != day {
		t.Fatalf("the quote is the payoff, the discount and its last day (98000, 2000, %s); got %+v", day, quote)
	}

	p := pw.payPortal(t, inv)
	if got := pw.lastCharge(t); got != 98000 {
		t.Errorf("the gateway is charged the payoff 98000; got %d", got)
	}
	bal, st, disc, amt, change := pw.doorOutcome(t, inv, p.ID)
	if bal != 0 || st != "PAID" || disc != 2000 || amt != 98000 || change != 0 {
		t.Errorf("want balance 0 PAID, discount 2000, payment 98000, change 0; got balance %d %s, discount %d, payment %d, change %d",
			bal, st, disc, amt, change)
	}
	var intent int64
	if err := pw.pool.QueryRow(ctx, `SELECT amount_cents FROM payment_intents WHERE customer_id = $1::uuid`, pw.customerID).Scan(&intent); err != nil {
		t.Fatalf("read the intent: %v", err)
	}
	if intent != 98000 {
		t.Errorf("the intent is claimed for the payoff 98000; got %d", intent)
	}
}

// TestPortalPayoff_OutsideTheWindowAndWithoutTermsNothingChanges: a discount day that has passed, and
// an invoice with no discount terms, are charged the open amount.
func TestPortalPayoff_OutsideTheWindowAndWithoutTermsNothingChanges(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	late := pw.discountInvoice(t, pw.customerID, "LATE", 100000, dayShift(-1))
	plain := pw.invoice(t, pw.customerID, "PLAIN", 5, -25, 30000)

	quote, err := pw.repo.PayableInvoices(ctx, pw.customerID, []string{late, plain})
	if err != nil {
		t.Fatalf("payable: %v", err)
	}
	for _, q := range quote {
		if q.DiscountCents != 0 || q.DiscountDay != "" {
			t.Errorf("no discount and no day outside the window or without terms; got %+v", q)
		}
	}
	p := pw.payPortal(t, late, plain)
	if got := pw.lastCharge(t); got != 130000 {
		t.Errorf("charged the open amounts, 130000; got %d", got)
	}
	bal, st, disc, amt, change := pw.doorOutcome(t, late, p.ID)
	if bal != 0 || st != "PAID" || disc != 0 || amt != 130000 || change != 0 {
		t.Errorf("the late invoice closes at face: balance %d %s, discount %d, payment %d, change %d", bal, st, disc, amt, change)
	}
}

// TestPortalPayoff_TheDiscountDayAtHalfPastElevenAndAtTheHouseMidnight is the evening guard leg: the
// discount day itself is inside the window at 23:30 house time, and the house midnight that ends it
// is outside, whatever the UTC day says.
func TestPortalPayoff_TheDiscountDayAtHalfPastElevenAndAtTheHouseMidnight(t *testing.T) {
	pw := newPayoffWorld(t)
	day := houseToday()
	inside := pw.discountInvoice(t, pw.customerID, "E23", 100000, day)
	outside := pw.discountInvoice(t, pw.customerID, "E00", 100000, day)

	pw.setNow(houseInstant(t, day, 23, 30))
	pw.payPortal(t, inside)
	if got := pw.lastCharge(t); got != 98000 {
		t.Errorf("at 23:30 on the discount day the payoff is charged, 98000; got %d", got)
	}

	next := dayOf(day, 1)
	pw.setNow(houseInstant(t, next, 0, 0))
	pw.payPortal(t, outside)
	if got := pw.lastCharge(t); got != 100000 {
		t.Errorf("at the house midnight the window is closed, 100000; got %d", got)
	}
}

// TestPortalPayoff_APartlyPaidInvoiceDiscountsWhatIsStillOpen: 40000 was applied at the desk inside
// the window (discount 800, open 59200). The portal's payoff is the open amount less the terms'
// percent of it (1184), under the running cap (2000 less the 800 taken leaves 1200).
func TestPortalPayoff_APartlyPaidInvoiceDiscountsWhatIsStillOpen(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "PART", 100000, day)
	if _, err := pw.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: pw.customerID, Method: "CHEQUE", Reference: "CHQ P1",
		IdempotencyKey: pw.tag + "-part", InvoiceIDs: []string{inv}, AmountCents: 40000,
	}); err != nil {
		t.Fatalf("the desk's part payment: %v", err)
	}
	if bal, st := pw.invoiceState(t, inv); bal != 59200 || st != "PARTIALLY_PAID" {
		t.Fatalf("setup: want 59200 PARTIALLY_PAID; got %d %s", bal, st)
	}

	p := pw.payPortal(t, inv)
	if got := pw.lastCharge(t); got != 58016 {
		t.Errorf("the payoff of the open 59200 is 58016; got %d", got)
	}
	bal, st, _, amt, change := pw.doorOutcome(t, inv, p.ID)
	if bal != 0 || st != "PAID" || amt != 58016 || change != 0 {
		t.Errorf("want balance 0 PAID, payment 58016, change 0; got %d %s, %d, %d", bal, st, amt, change)
	}
}

// TestPortalPayoff_StartedInsideTheWindowAndSettledAfterMidnightKeepsTheQuote is the race: the
// charge goes out at 23:59 on the discount day and the settle runs after the house midnight. The
// amount charged is fixed at the intent (98000), so the settle takes the discount the customer was
// quoted: the invoice closes, discount 2000, change 0.
func TestPortalPayoff_StartedInsideTheWindowAndSettledAfterMidnightKeepsTheQuote(t *testing.T) {
	pw := newPayoffWorld(t)
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "RACE", 100000, day)

	pw.setNow(houseInstant(t, day, 23, 59))
	pw.gw.onCharge = func() { pw.setNow(houseInstant(t, dayOf(day, 1), 0, 1)) }

	p := pw.payPortal(t, inv)
	if got := pw.lastCharge(t); got != 98000 {
		t.Errorf("charged the quoted payoff, 98000; got %d", got)
	}
	bal, st, disc, amt, change := pw.doorOutcome(t, inv, p.ID)
	if bal != 0 || st != "PAID" || disc != 2000 || amt != 98000 || change != 0 {
		t.Errorf("settled after midnight on the quote: want balance 0 PAID, discount 2000, payment 98000, change 0; got %d %s, %d, %d, %d",
			bal, st, disc, amt, change)
	}
}

// TestPortalPayoff_AResumedSettleAfterMidnightKeepsTheQuote: the processor approved at 98000 and the
// settle was refused, leaving a CHARGING intent with its ref stamped. The retry comes after the
// house midnight: it re-quotes as of the intent, matches the amount charged and settles it.
func TestPortalPayoff_AResumedSettleAfterMidnightKeepsTheQuote(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "RESUME", 100000, day)
	req := PayRequest{InvoiceIDs: []string{inv}, Nonce: "nonce-" + pw.tag}

	intent, claimed, err := pw.repo.ClaimIntent(ctx, IntentClaim{
		Key: idempotencyKey(pw.customerID, req), CustomerID: pw.customerID, AmountCents: 98000,
		InvoiceIDs: inv, Gateway: pw.gw.Name(),
	})
	if err != nil || !claimed {
		t.Fatalf("claim the intent: claimed=%v err=%v", claimed, err)
	}
	if err := pw.repo.StampIntentRef(ctx, intent.ID, pw.gw.Name(), "gw-resume-"+pw.tag); err != nil {
		t.Fatalf("stamp the ref: %v", err)
	}

	pw.setNow(time.Now().Add(26 * time.Hour))
	p, err := pw.svc.Pay(ctx, pw.customerID, req)
	if err != nil {
		t.Fatalf("the resumed settle: %v", err)
	}
	if n := len(pw.gw.charged()); n != 0 {
		t.Errorf("a resume never charges again; the gateway saw %d charges", n)
	}
	bal, st, disc, amt, change := pw.doorOutcome(t, inv, p.ID)
	if bal != 0 || st != "PAID" || disc != 2000 || amt != 98000 || change != 0 {
		t.Errorf("want balance 0 PAID, discount 2000, payment 98000, change 0; got %d %s, %d, %d, %d", bal, st, disc, amt, change)
	}
}

// TestPortalPayoff_ASettleRefusesAQuotePastTheInvoiceTerms: the settle checks the quote it is
// handed. A discount past the terms' percent, and a discount on an invoice with no terms, are
// refused with nothing written.
func TestPortalPayoff_ASettleRefusesAQuotePastTheInvoiceTerms(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	terms := pw.discountInvoice(t, pw.customerID, "OVER", 100000, houseToday())
	plain := pw.invoice(t, pw.customerID, "NOTERMS", 5, -25, 30000)

	for name, a := range map[string]Allocation{
		"past the terms percent": {InvoiceID: terms, AmountCents: 95000, DiscountCents: 5000},
		"no discount terms":      {InvoiceID: plain, AmountCents: 27000, DiscountCents: 3000},
	} {
		if _, err := pw.repo.Pay(ctx, pw.customerID, "CARD", "gw-over-"+pw.tag, []Allocation{a}); err == nil {
			t.Errorf("%s: the settle must refuse the quote", name)
		}
	}
	if bal, _ := pw.invoiceState(t, terms); bal != 100000 {
		t.Errorf("nothing written on a refused quote; the invoice balance is %d", bal)
	}
}

// intentCount reads how many intents the customer holds: a refused tap must leave none.
func (pw *payoffWorld) intentCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := pw.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_intents WHERE customer_id = $1::uuid`, pw.customerID).Scan(&n); err != nil {
		t.Fatalf("count intents: %v", err)
	}
	return n
}

// TestPortalPayoff_ThePayBodyCarriesTheShownAmountAndALapsedQuoteIsRefused is the reader's case: the
// list loads at 23:50 on the discount day (the sheet shows 98000) and the tap comes at 00:02. The core
// re-quotes 100000, differs from the shown amount and refuses before claiming the intent: nothing is
// charged. The sheet re-quotes 100000 and a second tap with that amount settles.
func TestPortalPayoff_ThePayBodyCarriesTheShownAmountAndALapsedQuoteIsRefused(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	day := houseToday()
	inv := pw.discountInvoice(t, pw.customerID, "SHOWN", 100000, day)

	pw.setNow(houseInstant(t, day, 23, 50))
	shown, err := pw.repo.PayableInvoices(ctx, pw.customerID, []string{inv})
	if err != nil || len(shown) != 1 || shown[0].AmountCents != 98000 {
		t.Fatalf("the list at 23:50 shows 98000; got %+v err %v", shown, err)
	}

	pw.setNow(houseInstant(t, dayOf(day, 1), 0, 2))
	req := PayRequest{InvoiceIDs: []string{inv}, Nonce: "nonce-" + pw.tag, ExpectedCents: 98000}
	_, err = pw.svc.Pay(ctx, pw.customerID, req)
	if !errors.Is(err, ErrAmountChanged) {
		t.Fatalf("a tap at 00:02 on a sheet that showed 98000 must be refused as a changed amount; got %v", err)
	}
	if n := len(pw.gw.charged()); n != 0 {
		t.Errorf("nothing is charged on a refused tap; the gateway saw %d charges", n)
	}
	if n := pw.intentCount(t); n != 0 {
		t.Errorf("no intent is claimed on a refused tap; found %d", n)
	}
	if bal, st := pw.invoiceState(t, inv); bal != 100000 || st != "FINALIZED" {
		t.Errorf("the invoice is untouched: want 100000 FINALIZED; got %d %s", bal, st)
	}

	again, err := pw.repo.PayableInvoices(ctx, pw.customerID, []string{inv})
	if err != nil || len(again) != 1 || again[0].AmountCents != 100000 || again[0].DiscountCents != 0 {
		t.Fatalf("the sheet re-quotes 100000 with no discount; got %+v err %v", again, err)
	}

	req.ExpectedCents = 100000
	if _, err := pw.svc.Pay(ctx, pw.customerID, req); err != nil {
		t.Fatalf("the second tap, with the new amount confirmed: %v", err)
	}
	if got := pw.lastCharge(t); got != 100000 {
		t.Errorf("the second tap charges 100000; got %d", got)
	}
}

// TestPortalPayoff_ADeskPaymentBetweenTheListAndTheTapRefuses: the list showed 98000, a desk cheque of
// 40000 lands, and the tap re-quotes 58016. Different from what was shown, so nothing is charged.
func TestPortalPayoff_ADeskPaymentBetweenTheListAndTheTapRefuses(t *testing.T) {
	ctx := context.Background()
	pw := newPayoffWorld(t)
	inv := pw.discountInvoice(t, pw.customerID, "DESK", 100000, houseToday())

	if _, err := pw.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: pw.customerID, Method: "CHEQUE", Reference: "CHQ D1",
		IdempotencyKey: pw.tag + "-desk", InvoiceIDs: []string{inv}, AmountCents: 40000,
	}); err != nil {
		t.Fatalf("the desk payment: %v", err)
	}
	_, err := pw.svc.Pay(ctx, pw.customerID, PayRequest{InvoiceIDs: []string{inv}, Nonce: "nonce-" + pw.tag, ExpectedCents: 98000})
	if !errors.Is(err, ErrAmountChanged) {
		t.Fatalf("a desk payment since the list must refuse the tap; got %v", err)
	}
	if n := len(pw.gw.charged()); n != 0 {
		t.Errorf("nothing is charged; the gateway saw %d charges", n)
	}
	if n := pw.intentCount(t); n != 0 {
		t.Errorf("no intent is claimed; found %d", n)
	}
	if bal, st := pw.invoiceState(t, inv); bal != 59200 || st != "PARTIALLY_PAID" {
		t.Errorf("only the desk's payment is on the invoice: want 59200 PARTIALLY_PAID; got %d %s", bal, st)
	}
}

// TestPortalPayoff_ATapThatMatchesWhatWasShownCharges: the shown amount equals the quote, so the pay
// goes through as before.
func TestPortalPayoff_ATapThatMatchesWhatWasShownCharges(t *testing.T) {
	pw := newPayoffWorld(t)
	inv := pw.discountInvoice(t, pw.customerID, "MATCH", 100000, houseToday())
	if _, err := pw.svc.Pay(context.Background(), pw.customerID,
		PayRequest{InvoiceIDs: []string{inv}, Nonce: "nonce-" + pw.tag, ExpectedCents: 98000}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if got := pw.lastCharge(t); got != 98000 {
		t.Errorf("charged 98000; got %d", got)
	}
}
