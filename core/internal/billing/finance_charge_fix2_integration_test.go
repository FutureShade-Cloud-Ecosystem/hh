//go:build integration

package billing

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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

// fcAuditSpy records the sensitive access rows the route writes.
type fcAuditSpy struct{ rows []audit.SensitiveAccess }

func (a *fcAuditSpy) Record(_ context.Context, row audit.SensitiveAccess) error {
	a.rows = append(a.rows, row)
	return nil
}

// fcRoute is the credit memo route over the rig: the posting rules a branch resolves (finance
// charge income at 4220 beside revenue at 4000), the rig's org and branch, and an audit spy.
func (r *fcRig) fcRoute(thresholdCents int64, parker CreditMemoParker) (*CreditMemoService, *fcAuditSpy, string, CreditMemoCaller) {
	r.t.Helper()
	spy := &fcAuditSpy{}
	svc := NewCreditMemoService(r.memoWriter(nil), slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: map[string]string{
			RoleAR: "1100", RoleRevenue: "4000", RoleTax: "2200", RoleFinanceCharge: "4220",
		}}).
		WithThreshold(creditMemoFixedThreshold{cents: thresholdCents}).
		WithBranchOrg(cmFixedBranchOrgs{r.branch: fc1OrgID}).
		WithAudit(spy)
	if parker != nil {
		svc = svc.WithParker(parker)
	}
	caller := CreditMemoCaller{OrgID: fc1OrgID, Scope: identity.ScopeGlobal, Branches: map[string]bool{}}
	return svc, spy, fc1OrgID, caller
}

// stampOrg gives a rig document the rig's org, which the route's fence reads.
func (r *fcRig) stampOrg(invoiceID string) {
	r.t.Helper()
	if _, err := r.pool.Exec(r.ctx, `UPDATE invoices SET org_id = $1::uuid WHERE id = $2::uuid`, fc1OrgID, invoiceID); err != nil {
		r.t.Fatalf("stamp the org: %v", err)
	}
}

// P3-B: the route names the account the journal really debits. For a memo against a finance charge
// that is 4220: the answer and the audit row say 4220, a request naming 4220 is accepted, and a
// request naming 4000 is refused in words.
func TestIntegration_W3FC1Fix2_TheRouteNamesTheAccountTheJournalDebits(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "ROUTE")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "ROUTE")
	chargeID := r.chargeFor(r.memoWriter(nil), cust)
	r.stampOrg(chargeID)
	ordinary := r.invoice(cust, r.branch, fcInv{
		no: "ORD", status: "FINALIZED", sourceKind: "ORDER",
		issue: "2026-08-01", due: fc1Due31, total: 40_000, finalized: fc1At(11, 9, 0),
	})
	r.stampOrg(ordinary)

	req := func(invoiceID, account, why string, cents int64) CreditMemoRouteRequest {
		return CreditMemoRouteRequest{CustomerID: cust, BranchID: r.branch, InvoiceID: invoiceID,
			AmountCents: cents, GLAccount: account, Reason: why}
	}
	route := func(threshold int64, parker CreditMemoParker) (*CreditMemoService, *fcAuditSpy, CreditMemoCaller) {
		svc, spy, _, caller := r.fcRoute(threshold, parker)
		svc = svc.WithCustomerHome(cmFixedHomes{cust: r.branch})
		return svc, spy, caller
	}

	t.Run("a request naming 4000 for a charge is refused in words", func(t *testing.T) {
		svc, spy, caller := route(0, nil)
		_, err := svc.Issue(r.ctx, req(chargeID, "4000", "forgive-4000"+r.tag, 5_000), caller)
		if err == nil || !strings.Contains(err.Error(), "credit account 4220, not 4000") {
			t.Fatalf("naming 4000 for a charge: err=%v, want it refused as crediting 4220, not 4000", err)
		}
		if len(spy.rows) != 0 {
			t.Fatalf("a refused request wrote %d audit rows", len(spy.rows))
		}
	})

	t.Run("a request naming 4220 for a charge is accepted, and the answer and the audit row say 4220", func(t *testing.T) {
		svc, spy, caller := route(0, nil)
		res, err := svc.Issue(r.ctx, req(chargeID, "4220", "forgive-4220"+r.tag, 5_000), caller)
		if err != nil {
			t.Fatalf("naming 4220 for a charge: %v", err)
		}
		if res.Status != CreditMemoIssued || res.GLAccount != "4220" {
			t.Fatalf("answer %q on account %q, want ISSUED on 4220", res.Status, res.GLAccount)
		}
		if len(spy.rows) != 1 || spy.rows[0].Extra["gl_account"] != "4220" {
			t.Fatalf("audit rows %+v, want one naming gl_account 4220", spy.rows)
		}
		// A retry of the same request answers with the same account.
		again, err := svc.Issue(r.ctx, req(chargeID, "4220", "forgive-4220"+r.tag, 5_000), caller)
		if err != nil || !again.Reused || again.GLAccount != "4220" {
			t.Fatalf("retry: err=%v reused=%v account=%q, want a reuse on 4220", err, again.Reused, again.GLAccount)
		}
	})

	t.Run("a parked memo against a charge answers and audits 4220", func(t *testing.T) {
		svc, spy, caller := route(1, &creditMemoParkSpy{})
		res, err := svc.Issue(r.ctx, req(chargeID, "", "forgive-parked"+r.tag, 5_000), caller)
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if res.Status != CreditMemoAwaitingApproval || res.GLAccount != "4220" {
			t.Fatalf("answer %q on account %q, want AWAITING on 4220", res.Status, res.GLAccount)
		}
		if len(spy.rows) != 1 || spy.rows[0].Extra["gl_account"] != "4220" {
			t.Fatalf("audit rows %+v, want one naming gl_account 4220", spy.rows)
		}
	})

	t.Run("an ordinary memo is unchanged: 4000 is the account, 4220 is refused", func(t *testing.T) {
		svc, spy, caller := route(0, nil)
		_, err := svc.Issue(r.ctx, req(ordinary, "4220", "ordinary-4220"+r.tag, 4_000), caller)
		if err == nil || !strings.Contains(err.Error(), "credit account 4000, not 4220") {
			t.Fatalf("naming 4220 for an ordinary invoice: err=%v, want it refused as crediting 4000, not 4220", err)
		}
		res, err := svc.Issue(r.ctx, req(ordinary, "4000", "ordinary-4000"+r.tag, 4_000), caller)
		if err != nil {
			t.Fatalf("naming 4000 for an ordinary invoice: %v", err)
		}
		if res.GLAccount != "4000" || len(spy.rows) != 1 || spy.rows[0].Extra["gl_account"] != "4000" {
			t.Fatalf("answer on %q, audit %+v, want 4000 on both", res.GLAccount, spy.rows)
		}
	})
}
