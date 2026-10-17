package gl

// nsf_standard_rate_test.go, the NSF fee backout reads the dealer's standard rate through the seam
// (item 3b): a tax-inclusive fee splits at the rate the dealer's jurisdiction binds, an Ontario dealer
// (or none wired) splits at 13 percent exactly as before, and a failing read refuses the posting.

import (
	"context"
	"errors"
	"testing"
)

type fakeStandardRate struct {
	bps int
	err error
}

func (f fakeStandardRate) StandardRateBps(context.Context, string) (int, error) { return f.bps, f.err }

func bounceTaxableFee() map[string]any {
	return map[string]any{
		"payment_id": "p-ca", "branch_id": "b1", "method": "CHEQUE", "amount_cents": 6000,
		"fee_cents": 4500, "fee_taxable": true,
	}
}

func TestNSFBackout_CaliforniaSplitsAtTheDealersStandardRate(t *testing.T) {
	f := newFakeStore()
	svc := newTestService(f).WithStandardRate(fakeStandardRate{bps: 775})
	if err := svc.consume(context.Background(), ev("payment.bounced", bounceTaxableFee())); err != nil {
		t.Fatalf("consume: %v", err)
	}
	d := f.lastInserted()
	// net = 4500 * 10000 / 10775 = 4176 (rounded down), embedded tax = 4500 - 4176 = 324
	wantLeg(t, d, "4200", 0, 4176)
	wantLeg(t, d, "2200", 0, 324)
	if !d.balanced() {
		t.Fatalf("unbalanced: dr=%d cr=%d", d.debitTotal(), d.creditTotal())
	}
}

func TestNSFBackout_OntarioUnchangedWithNoSourceAndWithAnOntarioSource(t *testing.T) {
	for name, svcFor := range map[string]func(*fakeStore) *Service{
		"unwired": func(f *fakeStore) *Service { return newTestService(f) },
		"ontario": func(f *fakeStore) *Service { return newTestService(f).WithStandardRate(fakeStandardRate{bps: 1300}) },
	} {
		f := newFakeStore()
		if err := svcFor(f).consume(context.Background(), ev("payment.bounced", bounceTaxableFee())); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		d := f.lastInserted()
		wantLeg(t, d, "4200", 0, 3982)
		wantLeg(t, d, "2200", 0, 518)
	}
}

func TestNSFBackout_AFailedRateReadRefusesThePosting(t *testing.T) {
	f := newFakeStore()
	svc := newTestService(f).WithStandardRate(fakeStandardRate{err: errors.New("no standard rate")})
	if err := svc.consume(context.Background(), ev("payment.bounced", bounceTaxableFee())); err == nil {
		t.Fatal("a taxable fee was split at a guessed rate")
	}
	// A fee that is not taxable never asks.
	f2 := newFakeStore()
	nontax := bounceTaxableFee()
	nontax["fee_taxable"] = false
	if err := newTestService(f2).WithStandardRate(fakeStandardRate{err: errors.New("x")}).consume(context.Background(), ev("payment.bounced", nontax)); err != nil {
		t.Fatalf("a non taxable fee asked for a rate: %v", err)
	}
}
