package lien

// holdback_standard_rate_test.go, the HST on holdback gross up reads the dealer's standard rate through
// the seam (item 3b): a California dealer grosses a holdback up by 7.75 percent, an Ontario dealer (or none
// wired) by 13 percent exactly as before, and a failed rate read refuses the sync rather than booking a
// holdback at a rate the dealer does not charge.

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

func syncOne(t *testing.T, svc func(*fakeStore) *Service, hst bool) (int64, error) {
	t.Helper()
	st := newFakeStore()
	job, _ := st.InsertJob(context.Background(), JobInput{JobNo: "J-HST", BranchID: "br1", CustomerID: "cust1", Direction: dirReceivable, HSTOnHoldback: hst}, "u")
	hb := fakeHoldback{invoices: []HoldbackInvoice{{InvoiceID: "inv1", InvoiceNo: "INV-1", CustomerID: "cust1", BranchID: "br1", HoldbackCents: 10000}}}
	res, err := svc(st).WithHoldbackFacts(hb).SyncHoldbacks(ctxWith(gm("u")), job.ID)
	return res.BookedCents, err
}

func TestHoldbackGrossUp_CaliforniaUsesTheDealersStandardRate(t *testing.T) {
	got, err := syncOne(t, func(st *fakeStore) *Service { return newSvc(st).WithStandardRate(fakeStandardRate{bps: 775}) }, true)
	if err != nil || got != 10775 {
		t.Fatalf("california holdback: %d %v, want 10775", got, err)
	}
}

func TestHoldbackGrossUp_OntarioUnchanged(t *testing.T) {
	for name, build := range map[string]func(*fakeStore) *Service{
		"unwired": func(st *fakeStore) *Service { return newSvc(st) },
		"ontario": func(st *fakeStore) *Service { return newSvc(st).WithStandardRate(fakeStandardRate{bps: 1300}) },
	} {
		if got, err := syncOne(t, build, true); err != nil || got != 11300 {
			t.Fatalf("%s: %d %v, want 11300", name, got, err)
		}
	}
}

// A job that does not opt into HST on holdback never asks for a rate.
func TestHoldbackGrossUp_NoFlagNoRateRead(t *testing.T) {
	got, err := syncOne(t, func(st *fakeStore) *Service {
		return newSvc(st).WithStandardRate(fakeStandardRate{err: errors.New("must not be read")})
	}, false)
	if err != nil || got != 10000 {
		t.Fatalf("no flag: %d %v", got, err)
	}
}

func TestHoldbackGrossUp_AFailedRateReadRefusesTheSync(t *testing.T) {
	_, err := syncOne(t, func(st *fakeStore) *Service {
		return newSvc(st).WithStandardRate(fakeStandardRate{err: errors.New("no standard rate")})
	}, true)
	if err == nil {
		t.Fatal("a holdback was grossed up at a guessed rate")
	}
}
