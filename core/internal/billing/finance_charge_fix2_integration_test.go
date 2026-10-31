//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// fcFailPoster is a memo poster that is down: the memo is issued and its journal is left for the
// repost sweep.
type fcFailPoster struct{}

func (fcFailPoster) PostCreditMemoJournal(context.Context, CreditMemoJournal) (string, error) {
	return "", errors.New("down")
}

// P3-A, the parked path: the journal ParkCreditMemoTx hands back for a memo against a finance
// charge resolves to DR 4220 / CR 1100, and gl accepts it parked. Without the reversed invoice's
// kind on the parked journal it resolves to DR 4000, and on approval sales revenue is understated.
func TestIntegration_W3FC1Fix2_AParkedMemoAgainstAChargeIsDebitedToFinanceChargeIncome(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "PARK")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "PARK")
	w := r.memoWriter(nil)
	chargeID := r.chargeFor(w, cust)

	tx, err := r.pool.Begin(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The park is rolled back: the leg proves the lines, and leaves nothing behind.
	defer func() { _ = tx.Rollback(r.ctx) }()
	_, j, err := w.ParkCreditMemoTx(r.ctx, tx, CreditMemoInput{CustomerID: cust, BranchID: r.branch,
		ReversesInvoiceID: chargeID, Memo: "forgive", IdempotencyKey: "w3fc1fix2-park" + r.tag})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if j.ReversesSourceKind != SourceFinanceCharge {
		t.Fatalf("the parked journal carries source kind %q, want %q", j.ReversesSourceKind, SourceFinanceCharge)
	}
	accounts, err := gl.NewRepository(r.pool).PostingAccounts(r.ctx, "invoice.finalized", r.branch)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := CreditMemoJournalLines(j, accounts)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	if len(lines) != 2 || lines[0].AccountCode != "4220" || lines[0].DebitCents != 15_000 ||
		lines[1].AccountCode != "1100" || lines[1].CreditCents != 15_000 {
		t.Fatalf("parked lines %+v, want DR 4220 15000 / CR 1100 15000", lines)
	}
	// gl accepts exactly these lines as a parked journal.
	var ext []gl.ExternalLine
	for _, l := range lines {
		ext = append(ext, gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo})
	}
	if _, err := gl.NewService(gl.NewRepository(r.pool)).ParkExternalJournalTx(r.ctx, tx, gl.ExternalJournal{
		Source: gl.SourceInvoice, SourceRefID: j.CreditMemoID, IdempotencyKey: j.IdempotencyKey,
		Memo: j.Memo, BranchID: j.BranchID, Lines: ext,
	}); err != nil {
		t.Fatalf("gl park: %v", err)
	}
}

// P3-A, the repost sweep: a memo against a finance charge whose journal failed is re-posted by the
// sweep against 4220, never 4000.
func TestIntegration_W3FC1Fix2_TheRepostSweepDebitsFinanceChargeIncomeForAMemoAgainstACharge(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "REPOST")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "REPOST")
	w := r.memoWriter(nil)
	chargeID := r.chargeFor(w, cust)
	r.drainInvoiceJournal(chargeID)
	b4220, b4000, b1100 := r.glNet("4220"), r.glNet("4000"), r.glNet("1100")

	memo, err := r.writer(nil).WithCreditMemoPoster(fcFailPoster{}).IssueCreditMemo(r.ctx, CreditMemoInput{
		CustomerID: cust, BranchID: r.branch, ReversesInvoiceID: chargeID, AmountCents: 6_000,
		Memo: "forgive", IdempotencyKey: "w3fc1fix2-repost" + r.tag})
	if err != nil || memo.GLJournalError == "" {
		t.Fatalf("the memo should issue with a failed journal: err=%v journal error=%q", err, memo.GLJournalError)
	}
	res, err := w.RepostFailedCreditMemoJournals(r.ctx, 100000)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Reposted < 1 {
		t.Fatalf("the sweep reposted %d, want the memo reposted: %+v", res.Reposted, res)
	}
	if d := r.glNet("4220") - b4220; d != 6_000 {
		t.Fatalf("4220 moved %d, want +6000 (debit)", d)
	}
	if d := r.glNet("4000") - b4000; d != 0 {
		t.Fatalf("4000 moved %d, want 0", d)
	}
	if d := r.glNet("1100") - b1100; d != -6_000 {
		t.Fatalf("1100 moved %d, want -6000", d)
	}
}
