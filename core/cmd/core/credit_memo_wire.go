package main

// credit_memo_wire.go: THE ADAPTERS THE CREDIT MEMO ROUTE NEEDS.
//
// The route itself lives in the billing module (core/internal/billing/credit_memo_route.go) because the
// credit memo's rules are billing's: the reversal, the tax, the cap against what an invoice has left,
// the subledger row. What billing does not have is the other side of the three tables a parked credit
// touches:
//
//   - the POSTING RULES (gl's posting_rules): which accounts a credit books against at a branch,
//   - the THRESHOLD (gl_policy.je_approval_threshold_cents): when a credit needs a second pair of eyes,
//   - the PARKED JOURNAL (gl_journals): the reversing entry that waits for the approval.
//
// Only the composition root may write three modules' rows in one transaction, so the park is bound here
// and nowhere else. The adapters below do no arithmetic: the roles, the amounts and the accounts all
// come from billing and from gl's own reads.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// creditMemoRulesAdapter reads the accounts a credit memo may book against at a branch.
//
// The event type is the invoice's own (invoice.finalized), because a credit memo's reversing entry is
// the invoice's entry with the sides swapped: the same accounts, the same branch override.
type creditMemoRulesAdapter struct{ rules *gl.Repository }

func (a creditMemoRulesAdapter) CreditMemoAccounts(ctx context.Context, branchID string) (map[string]string, error) {
	return a.rules.PostingAccounts(ctx, "invoice.finalized", branchID)
}

// creditMemoThresholdAdapter reads the branch's maker-checker threshold.
//
// gl's own read is the branch's row over the org default (gl's Policy), so the threshold this route
// parks against is the same number the ledger's own parked journals are read against.
type creditMemoThresholdAdapter struct{ rules *gl.Repository }

func (a creditMemoThresholdAdapter) CreditMemoThresholdCents(ctx context.Context, branchID string) (int64, error) {
	p, err := a.rules.Policy(ctx, branchID)
	if err != nil {
		return 0, fmt.Errorf("read the approval threshold: %w", err)
	}
	return p.JEApprovalThresholdCents, nil
}

// creditMemoParkerAdapter holds an over-threshold credit: the memo document, the parked reversing
// journal and the approval request, in ONE transaction.
//
// The order inside the transaction is the order of dependency: the memo document first, because the
// journal names the memo in its source, and the journal second, because the approval request names the
// journal. A failure at any point rolls the whole thing back, so a half-parked credit cannot exist.
type creditMemoParkerAdapter struct {
	pool      *pgxpool.Pool
	w         *billing.InvoiceWriter
	gl        *gl.Service
	rules     *gl.Repository
	approvals *identity.Approvals
}

func (a creditMemoParkerAdapter) ParkCreditMemo(ctx context.Context, in billing.CreditMemoParkRequest) (billing.CreditMemoParked, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return billing.CreditMemoParked{}, fmt.Errorf("billing: begin the parked credit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	doc, journal, err := a.w.ParkCreditMemoTx(ctx, tx, in.Memo)
	if err != nil {
		return billing.CreditMemoParked{}, err
	}
	accounts, err := a.rules.PostingAccounts(ctx, "invoice.finalized", in.Memo.BranchID)
	if err != nil {
		return billing.CreditMemoParked{}, fmt.Errorf("read the credit memo posting rules: %w", err)
	}
	lines, err := billing.CreditMemoJournalLines(journal, accounts)
	if err != nil {
		return billing.CreditMemoParked{}, err
	}
	glLines := make([]gl.ExternalLine, 0, len(lines))
	for _, l := range lines {
		glLines = append(glLines, gl.ExternalLine{
			AccountCode: l.AccountCode, DebitCents: l.DebitCents,
			CreditCents: l.CreditCents, Memo: l.Memo,
		})
	}
	journalID, err := a.gl.ParkExternalJournalTx(ctx, tx, gl.ExternalJournal{
		Source:         gl.SourceInvoice,
		SourceRefID:    doc.InvoiceID,
		IdempotencyKey: journal.IdempotencyKey,
		Memo:           journal.Memo,
		BranchID:       journal.BranchID,
		Lines:          glLines,
	})
	if err != nil {
		return billing.CreditMemoParked{}, fmt.Errorf("park the credit memo journal: %w", err)
	}
	approvalID, fan, err := a.approvals.FileCreditMemoApprovalTx(ctx, tx, journalID, journal.BranchID,
		in.AmountCents, in.RequestedBy, creditMemoApprovalWords(doc, in))
	if err != nil {
		return billing.CreditMemoParked{}, fmt.Errorf("file the credit memo approval: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return billing.CreditMemoParked{}, fmt.Errorf("commit the parked credit: %w", err)
	}
	// The cards name a request that has now committed, so they run after the commit, never before.
	if fan != nil {
		fan(ctx)
	}
	return billing.CreditMemoParked{
		CreditMemoID: doc.InvoiceID,
		CreditMemoNo: doc.InvoiceNo,
		JournalID:    journalID,
		ApprovalID:   approvalID,
	}, nil
}

// creditMemoApprovalWords is what the Controller reads on the card before they sign. It names the
// credit, the customer and the reason, because a card that says only "a journal entry is waiting" is
// a card nobody can decide.
func creditMemoApprovalWords(doc billing.InvoiceDoc, in billing.CreditMemoParkRequest) string {
	return fmt.Sprintf("Credit memo %s for %s: %s", doc.InvoiceNo, billing.Money(in.AmountCents), in.Reason)
}

// newCreditMemoHandler builds the credit memo route's handler over the invoice writer, gl's posting
// rules and the approvals rail.
func newCreditMemoHandler(pool *pgxpool.Pool, w *billing.InvoiceWriter, glSvc *gl.Service, approvals *identity.Approvals, log *slog.Logger) *billing.CreditMemoRouteHandler {
	rules := gl.NewRepository(pool)
	svc := billing.NewCreditMemoService(w, log).
		WithPostingRules(creditMemoRulesAdapter{rules: rules}).
		WithThreshold(creditMemoThresholdAdapter{rules: rules}).
		WithParker(creditMemoParkerAdapter{
			pool: pool, w: w, gl: glSvc, rules: rules, approvals: approvals,
		}).
		// The fence's org reads are the owning modules' own: the branches module answers whether a
		// branch is the caller org's, and the customer module answers the account's home branch.
		WithBranchOrg(branches.NewRepository(pool)).
		WithCustomerHome(customer.NewRepository(pool)).
		// Wave 3 JA-2: the money desk's direct document takes a job, fenced by the customer
		// module's own read.
		WithJobFence(newCustomerJobFence(pool))
	return billing.NewCreditMemoRouteHandler(svc)
}
