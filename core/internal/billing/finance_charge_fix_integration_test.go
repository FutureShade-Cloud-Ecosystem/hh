//go:build integration

// finance_charge_fix_integration_test.go, wave 3 FC-1 fix round (the Opus read of PR 748): a credit
// memo against a charge reverses finance charge income, a charge carries no early payment
// discount, supplier deposits are not charged, and the charged balance moves exactly as the aging
// does when a payment, a credit or a write off lands on a past due invoice, before and after the
// month's last instant.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1Fix
package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// fcMemoPoster is the composition root's credit memo adapter in miniature: role to account code
// through the posting rules, then the journal through the GL module's seam. The sides come from
// billing's own ReversingLegsFor, which is the thing under test.
type fcMemoPoster struct {
	svc   *gl.Service
	rules *gl.Repository
}

func (p fcMemoPoster) PostCreditMemoJournal(ctx context.Context, in CreditMemoJournal) (string, error) {
	rules, err := p.rules.PostingAccounts(ctx, "invoice.finalized", in.BranchID)
	if err != nil {
		return "", err
	}
	var lines []gl.ExternalLine
	for _, leg := range in.ReversingLegsFor("HST") {
		code := rules[leg.Role]
		if code == "" {
			return "", fmt.Errorf("no %q posting rule", leg.Role)
		}
		lines = append(lines, gl.ExternalLine{AccountCode: code, DebitCents: leg.DebitCents, CreditCents: leg.CreditCents, Memo: leg.Memo})
	}
	return p.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: gl.SourceInvoice, SourceRefID: in.CreditMemoID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

// drainInvoiceJournal drains the outbox through the real GL consumer until the invoice's own
// journal has posted.
func (r *fcRig) drainInvoiceJournal(invoiceID string) {
	r.t.Helper()
	repo := NewRepository(r.pool)
	glSvc := gl.NewService(gl.NewRepository(r.pool)).WithInvoiceReader(repo).WithPaymentReader(till.NewRepository(r.pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(r.pool), nil)
	glSvc.RegisterPostingHandlers(d)
	key := "ar.invoice.finalized:" + invoiceID
	for i := 0; i < 200; i++ {
		d.DrainOnce(r.ctx)
		var posted int
		if err := r.pool.QueryRow(r.ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1 AND status = 'POSTED'`, key).Scan(&posted); err != nil {
			r.t.Fatalf("watch the journal: %v", err)
		}
		if posted > 0 {
			return
		}
	}
	r.t.Fatalf("the journal on key %s never posted", key)
}

// memoWriter is the run's writer with the memo poster bound.
func (r *fcRig) memoWriter(clock func() time.Time) *InvoiceWriter {
	glSvc := gl.NewService(gl.NewRepository(r.pool))
	return r.writer(clock).WithCreditMemoPoster(fcMemoPoster{svc: glSvc, rules: gl.NewRepository(r.pool)})
}

// chargeFor runs the month and returns the customer's charge document id.
func (r *fcRig) chargeFor(w *InvoiceWriter, cust string) string {
	r.t.Helper()
	res, err := w.RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		r.t.Fatalf("the run: %v", err)
	}
	for _, c := range res.Charges {
		if c.CustomerID == cust {
			return c.InvoiceID
		}
	}
	r.t.Fatalf("the run did not charge customer %s: %+v", cust, res)
	return ""
}

// P2-1: a credit memo against a finance charge document reverses 4220, never sales revenue 4000.
func TestIntegration_W3FC1Fix_AMemoAgainstAChargeReversesFinanceChargeIncome(t *testing.T) {
	for _, tc := range []struct {
		name      string
		amount    int64 // 0 is the whole charge
		reversed  int64
		remaining int64
	}{
		{"full memo", 0, 15_000, 0},
		{"partial memo", 5_000, 5_000, 10_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fcRigNew(t)
			cust := r.customer(r.branch, "MEMO")
			r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "MEMO")
			w := r.memoWriter(nil)

			agingBefore, gl1100Before := r.agingTotal(), r.glNet("1100")
			gl4220Before, gl4000Before := r.glNet("4220"), r.glNet("4000")

			chargeID := r.chargeFor(w, cust)
			r.drainInvoiceJournal(chargeID)

			memo, err := w.IssueCreditMemo(r.ctx, CreditMemoInput{
				CustomerID: cust, BranchID: r.branch, ReversesInvoiceID: chargeID,
				AmountCents: tc.amount, Memo: "forgiven", IdempotencyKey: "fc1fix-" + t.Name() + r.tag,
			})
			if err != nil {
				t.Fatalf("the memo against the charge was refused: %v", err)
			}
			if memo.GLJournalError != "" || memo.GLJournalID == "" {
				t.Fatalf("the memo's journal did not post: id=%q err=%q", memo.GLJournalID, memo.GLJournalError)
			}

			// 4220 holds what is left of the charge as income (credit balance, so the net debit is negative);
			// sales revenue 4000 is untouched by a finance charge and its reversal.
			if got := r.glNet("4220") - gl4220Before; got != -tc.remaining {
				t.Fatalf("4220 net debit moved %d, want %d (the charge less what the memo reversed)", got, -tc.remaining)
			}
			if got := r.glNet("4000") - gl4000Before; got != 0 {
				t.Fatalf("sales revenue 4000 moved %d, want 0: a memo against a charge must not reverse sales", got)
			}
			// The aging is tied to 1100.
			agingDelta, glDelta := r.agingTotal()-agingBefore, r.glNet("1100")-gl1100Before
			if agingDelta != tc.remaining || glDelta != tc.remaining {
				t.Fatalf("aging moved %d and GL 1100 moved %d, want both %d", agingDelta, glDelta, tc.remaining)
			}
		})
	}
}

// An ordinary invoice's memo still debits sales revenue 4000.
func TestIntegration_W3FC1Fix_AMemoAgainstAnOrdinaryInvoiceStillDebitsRevenue(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "ORDMEMO")
	inv := r.invoice(cust, r.branch, fcInv{
		no: "ORDMEMO", status: "FINALIZED", sourceKind: "ORDER",
		issue: "2026-08-01", due: "2026-08-31", total: 20_000, finalized: fc1At(10, 9, 0),
	})
	w := r.memoWriter(nil)
	before4000, before4220 := r.glNet("4000"), r.glNet("4220")
	if _, err := w.IssueCreditMemo(r.ctx, CreditMemoInput{
		CustomerID: cust, BranchID: r.branch, ReversesInvoiceID: inv,
		AmountCents: 0, Memo: "returned", IdempotencyKey: "fc1fix-ord" + r.tag,
	}); err != nil {
		t.Fatalf("the ordinary memo: %v", err)
	}
	if got := r.glNet("4000") - before4000; got != 20_000 {
		t.Fatalf("4000 net debit moved %d, want 20000 (an ordinary memo debits sales revenue)", got)
	}
	if got := r.glNet("4220") - before4220; got != 0 {
		t.Fatalf("4220 moved %d on an ordinary memo, want 0", got)
	}
}
