//go:build integration

// reapplied_reads_integration_test.go: the register shows the money a reversal held and a manager put on an invoice
// (SF-2r). Re-application writes payment_reapplications and a subledger row, not payment_allocations, so an invoice's
// Applied figure and its timeline omitted it: the balance fell with no applied entry to explain why. The read goes
// through the payments module's seam (the census refuses the register reading its table).
package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// reappliedWorld takes a payment of cents on invoice `first`, reverses it, and re-applies the named slices.
type reappliedWorld struct {
	f       *d510Fixture
	repo    *payments.Repository
	payment string
}

func newReappliedWorld(t *testing.T, cents int64) (*reappliedWorld, string) {
	t.Helper()
	f := newD510Fixture(t)
	first := f.invoice(t, "RAFIRST", "FINALIZED", 10, 1_000_000, 1_000_000, "INVOICE")
	repo := payments.NewRepository(f.pool)
	p, err := repo.Pay(context.Background(), f.customerID, "CARD", "gw-ra-"+f.tag, []payments.Allocation{{InvoiceID: first, AmountCents: cents}})
	if err != nil {
		t.Fatalf("take the payment: %v", err)
	}
	svc := payments.NewReverseService(repo)
	if _, err := svc.Reverse(context.Background(), payments.ReverseInput{PaymentID: p.ID, Reason: "wrong account", IsManager: true}); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	return &reappliedWorld{f: f, repo: repo, payment: p.ID}, first
}

func (w *reappliedWorld) reapply(t *testing.T, key string, allocs ...payments.Allocation) {
	t.Helper()
	if _, err := payments.NewReverseService(w.repo).Reapply(context.Background(), payments.ReapplyInput{
		PaymentID: w.payment, Reason: "rung on the wrong account", IdempotencyKey: key + w.f.tag, Allocations: allocs, IsManager: true,
	}); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

func (w *reappliedWorld) detail(t *testing.T, invoice string) InvoiceDetail {
	t.Helper()
	d, err := w.f.service(d510Manager()).WithReapplied(NewPaymentsReappliedReader(w.repo)).Detail(context.Background(), invoice, true)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	return d
}

func reappliedEntries(d InvoiceDetail) []ActivityEntry {
	var out []ActivityEntry
	for _, e := range d.Activity {
		if e.Kind == "PAYMENT" && strings.Contains(e.Detail, "re applied from a reversed payment") {
			out = append(out, e)
		}
	}
	return out
}

// (1) A reversed payment re-applied to an invoice shows in that invoice's Applied figure and its timeline, and the
// figure equals what the balance moved (total less balance, with nothing else having touched the invoice).
func TestRegister_AReappliedSliceShowsInAppliedAndTheTimeline(t *testing.T) {
	w, first := newReappliedWorld(t, 10_000)
	w.reapply(t, "ra-one-", payments.Allocation{InvoiceID: first, AmountCents: 10_000})
	d := w.detail(t, first)
	t.Logf("applied %d balance %d total %d, timeline %+v", d.Totals.AppliedCents, d.Totals.BalanceCents, d.Totals.TotalCents, d.Activity)
	if moved := d.Totals.TotalCents - d.Totals.BalanceCents; d.Totals.AppliedCents != moved || moved != 10_000 {
		t.Errorf("Applied reads %d, the balance moved %d, want both 10000", d.Totals.AppliedCents, moved)
	}
	entries := reappliedEntries(d)
	if len(entries) != 1 || entries[0].AmountCents != 10_000 || !strings.Contains(entries[0].Detail, "PAY-") && !strings.Contains(entries[0].Detail, "CARD") {
		t.Errorf("the timeline carries %+v, want one re applied entry of 10000 naming the payment", entries)
	}
	if entries != nil && entries[0].At.Before(time.Now().Add(-time.Minute)) {
		t.Errorf("the entry is dated %v, want now", entries[0].At)
	}
	// Without the trail the entry is absent, as every payment entry is.
	dn, err := w.f.service(d510Manager()).WithReapplied(NewPaymentsReappliedReader(w.repo)).Detail(context.Background(), first, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := reappliedEntries(dn); len(got) != 0 {
		t.Errorf("a caller without the payment trail sees %+v", got)
	}
}

// (2) A split across two invoices shows on each, and each figure equals what its balance moved.
func TestRegister_ASplitShowsOnEachInvoice(t *testing.T) {
	w, first := newReappliedWorld(t, 10_000)
	second := w.f.invoice(t, "RASECOND", "FINALIZED", 10, 500_000, 500_000, "INVOICE")
	w.reapply(t, "ra-split-", payments.Allocation{InvoiceID: first, AmountCents: 6_000}, payments.Allocation{InvoiceID: second, AmountCents: 4_000})
	for _, c := range []struct {
		id    string
		cents int64
	}{{first, 6_000}, {second, 4_000}} {
		d := w.detail(t, c.id)
		if moved := d.Totals.TotalCents - d.Totals.BalanceCents; d.Totals.AppliedCents != moved || moved != c.cents {
			t.Errorf("invoice %s: Applied %d, balance moved %d, want both %d", d.InvoiceNo, d.Totals.AppliedCents, moved, c.cents)
		}
		if got := reappliedEntries(d); len(got) != 1 || got[0].AmountCents != c.cents {
			t.Errorf("invoice %s: timeline %+v, want one re applied entry of %d", d.InvoiceNo, got, c.cents)
		}
	}
}

// A reversal alone nets the Applied figure to zero: the original's allocation and the reversal's contra allocation
// cancel, and neither is money that is applied now.
func TestRegister_AReversalNetsTheAppliedFigureToZero(t *testing.T) {
	w, first := newReappliedWorld(t, 10_000)
	d := w.detail(t, first)
	if d.Totals.AppliedCents != 0 {
		t.Errorf("after a reversal Applied reads %d, want 0 (balance %d of total %d)", d.Totals.AppliedCents, d.Totals.BalanceCents, d.Totals.TotalCents)
	}
}
