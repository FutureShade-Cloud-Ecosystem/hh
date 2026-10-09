package payments

// received_apply.go, ONE RECEIVED PAYMENT allocated by the server across invoices.
//
// The register's per invoice route (counter_receipt.go through one allocation) makes the CALLER
// split one cheque: money computed in the app, one cheque as several payment rows, nothing atomic.
// This file is the replacement's core: the caller names the account, the money (amount, method,
// reference) and a list of the account's open invoices; the SERVER splits it oldest first inside
// ONE transaction, one payment row, its payment_allocations, and one payment.received event whose
// GL consumer books one journal on the deterministic key payment.received:<payment id>, so a retry
// posts once.
//
// THE LOCK ORDER, and why it is what it is:
//
// The invoice rows are taken FOR NO KEY UPDATE, one statement per id, in ASCENDING ID ORDER, all of
// them before the first balance move draws the shared invoices sequence row. That is the order the
// till's returns take (PR 277, pos lockReturnInvoicesTx): every multi invoice writer that walks its
// rows in that one order can never meet another head on, whatever order the caller's list arrived
// in. The payments counter row is taken first of all (lockAndTotal's own first step, the one money
// lock order), which serialises this writer against every other payments writer (a settle, a
// reversal, a bounce) before either touches an invoice.
//
// The settle itself is the SHIPPED settle: the split is computed under the locks here, then handed
// to settleReceipt (the same function RecordReceipt and the portal card settle run), so there stays
// exactly ONE implementation of applying money to an invoice. settleReceipt re-locks and re-totals
// the same rows under the same transaction, a no-op read of rows this transaction already holds.
//
// Refusals leave nothing applied: every judgement (the account, the branch reach, the open status,
// the ceiling) runs inside the transaction BEFORE the payment row exists, so a refused request
// burns neither its idempotency key nor a cent.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/google/uuid"
)

// The outcome words one line of the split carries. CLOSES and PART mirror the settle's own status
// flip (an invoice closes when its balance reaches zero), NONE is an invoice the money never
// reached, kept in the answer so the desk sees the whole list it named.
const (
	ApplyOutcomeCloses = "CLOSES"
	ApplyOutcomePart   = "PART"
	ApplyOutcomeNone   = "NONE"
)

// MaxApplyInvoices bounds the list: a received payment naming more invoices than this is a bulk
// receipting workflow, not one cheque, and refusing it keeps the transaction's lock window honest.
const MaxApplyInvoices = 100

// NoSuchInvoiceError names one invoice the request may not apply against: missing, another
// account's, or outside a scoped caller's branch reach. The route answers all three the same way
// (its 404), the same no existence leak the till's pay balance door carries across the branch wall.
type NoSuchInvoiceError struct{ InvoiceID string }

func (e NoSuchInvoiceError) Error() string { return "payments: no such invoice: " + e.InvoiceID }

// ApplyRequest is one received payment the server is asked to allocate.
type ApplyRequest struct {
	CustomerID     string
	Method         string
	Reference      string // cheque number / EFT trace, recorded, never invented
	IdempotencyKey string
	ReceivedBy     string // the staff caller; also the allocator (0043 allocated_by)
	// BranchIDs is the caller's branch reach: nil reaches every branch (a global role), anything
	// else lists the branches whose invoices the caller may pay, and an invoice outside it answers
	// as not found. An empty non-nil list reaches nothing, fail closed.
	BranchIDs   []string
	InvoiceIDs  []string
	AmountCents int64
	// Preview answers the same split without writing anything: the locks are taken, the split is
	// computed against live balances, and the transaction rolls back.
	Preview bool
}

// ApplyLine is one invoice's share of the split.
type ApplyLine struct {
	InvoiceID         string `json:"invoice_id"`
	InvoiceNo         string `json:"invoice_no"`
	AppliedCents      int64  `json:"applied_cents"`
	Outcome           string `json:"outcome"`
	BalanceAfterCents int64  `json:"balance_after_cents"`
	StaysOpenCents    int64  `json:"stays_open_cents"`
}

// ApplyAnswer is the apply's answer: the payment (absent on a preview) and the split per invoice,
// oldest first, one line for every invoice the request named.
type ApplyAnswer struct {
	Preview        bool        `json:"preview"`
	PaymentID      string      `json:"payment_id,omitempty"`
	PaymentNo      string      `json:"payment_no,omitempty"`
	AmountCents    int64       `json:"amount_cents"`
	AppliedCents   int64       `json:"applied_cents"`
	UnappliedCents int64       `json:"unapplied_cents"`
	Lines          []ApplyLine `json:"lines"`
	Replayed       bool        `json:"replayed"`
}

// applyInvoiceRow is one locked invoice as the allocation walk reads it.
type applyInvoiceRow struct {
	InvoiceID     string
	InvoiceNo     string
	CustomerID    string
	BranchID      string
	Status        string
	IssueDate     time.Time
	DueDate       time.Time
	BalanceCents  int64
	HoldbackCents int64
}

// ApplyReceived records ONE received payment against a list of the account's open invoices and
// allocates it oldest first, atomically and idempotently.
func (r *Repository) ApplyReceived(ctx context.Context, in ApplyRequest) (ApplyAnswer, error) {
	if err := validateApply(in); err != nil {
		return ApplyAnswer{}, err
	}
	if r.pool == nil {
		return ApplyAnswer{}, errNoDB
	}
	// The replay fast path, before any lock and never on a preview (a preview writes nothing, so
	// there is nothing to replay; its key must stay unspent for the real apply that follows).
	if !in.Preview {
		if p, ok, err := r.receiptByKey(ctx, in.IdempotencyKey); err != nil {
			return ApplyAnswer{}, err
		} else if ok {
			return r.replayAnswer(ctx, p)
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ApplyAnswer{}, fmt.Errorf("apply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The money lock order's own first step: the payments counter row, before any invoice row, so
	// this writer serialises against every other payments writer at the counter. The exec itself
	// lives in repository.go beside every other counter lock this module takes.
	if err := r.LockPaymentsSequenceTx(ctx, tx); err != nil {
		return ApplyAnswer{}, err
	}

	rows, err := r.lockApplyInvoicesTx(ctx, tx, in)
	if err != nil {
		return ApplyAnswer{}, err
	}
	lines, capacity := allocateOldestFirst(rows, in.AmountCents)
	if in.AmountCents > capacity {
		return ApplyAnswer{}, httpx.BadRequestf(
			"the selected invoices can take at most %d cents right now; refresh and retry", capacity)
	}

	if in.Preview {
		// Nothing has been written; rolling back releases the locks with the ledger untouched.
		return ApplyAnswer{
			Preview: true, AmountCents: in.AmountCents, AppliedCents: sumApplied(lines),
			UnappliedCents: in.AmountCents - sumApplied(lines), Lines: lines,
		}, nil
	}

	// The allocations handed to the shipped settle are ordered by invoice id, ascending, so the
	// settle's own lock pass (which locks in allocation order) takes the rows in the same sorted
	// order this transaction already holds them in.
	allocs := make([]Allocation, 0, len(lines))
	for _, l := range lines {
		if l.AppliedCents > 0 {
			allocs = append(allocs, Allocation{InvoiceID: l.InvoiceID, AmountCents: l.AppliedCents})
		}
	}
	sort.Slice(allocs, func(i, j int) bool { return allocs[i].InvoiceID < allocs[j].InvoiceID })

	p, claimed, err := settleReceipt(ctx, tx, ReceiptRequest{
		CustomerID: in.CustomerID, Method: in.Method, Reference: in.Reference,
		IdempotencyKey: in.IdempotencyKey, ReceivedBy: in.ReceivedBy,
		Allocations: allocs,
	})
	if err != nil {
		return ApplyAnswer{}, err
	}
	if !claimed {
		// A twin committed this key while this statement waited on it: answer its payment.
		_ = tx.Rollback(ctx)
		if existing, ok, readErr := r.receiptByKey(ctx, in.IdempotencyKey); readErr != nil {
			return ApplyAnswer{}, readErr
		} else if ok {
			return r.replayAnswer(ctx, existing)
		}
		return ApplyAnswer{}, fmt.Errorf("payments: apply key %q was claimed and then vanished", in.IdempotencyKey)
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyAnswer{}, fmt.Errorf("apply: commit: %w", err)
	}
	return ApplyAnswer{
		PaymentID: p.ID, PaymentNo: p.PaymentNo, AmountCents: in.AmountCents,
		AppliedCents: sumApplied(lines), UnappliedCents: in.AmountCents - sumApplied(lines),
		Lines: lines,
	}, nil
}

// validateApply is the request check that runs before any database is consulted, the same posture
// validateReceipt holds: a malformed request is malformed with or without a pool behind it.
func validateApply(in ApplyRequest) error {
	if in.IdempotencyKey == "" {
		return httpx.BadRequestf("recording a payment needs an idempotency key")
	}
	if !validTender(in.Method) {
		return httpx.BadRequestf("tender must be one of %s", strings.Join(CounterTenders, ", "))
	}
	if in.AmountCents <= 0 {
		return httpx.BadRequestf("a payment amount must be greater than zero")
	}
	if len(in.InvoiceIDs) == 0 {
		return httpx.BadRequestf("a payment must be applied to at least one invoice")
	}
	if len(in.InvoiceIDs) > MaxApplyInvoices {
		return httpx.BadRequestf("a received payment may name at most %d invoices; record it as more than one payment", MaxApplyInvoices)
	}
	seen := map[string]bool{}
	for _, id := range in.InvoiceIDs {
		if _, err := uuid.Parse(id); err != nil {
			return httpx.BadRequestf("invoice ids must be uuids")
		}
		if seen[id] {
			return httpx.BadRequestf("the same invoice is named twice: %s", id)
		}
		seen[id] = true
	}
	return nil
}

// allocateOldestFirst is the pure walk: order the locked rows oldest first (issue date, due date,
// invoice number, id: deterministic whatever order the caller's list arrived in), then fill each
// invoice's payable (balance less holdback, floored at zero) until the money runs out. It answers
// one line for every row, applied or not, and the capacity the over-refusal reads.
//
// The outcome words mirror the settle's own status flip, which closes an invoice at balance zero:
// an invoice whose retained holdback keeps a balance stays PART however much of its payable it
// paid, because the answer must say what the ledger will say, not what a rounded screen would.
func allocateOldestFirst(rows []applyInvoiceRow, amountCents int64) (lines []ApplyLine, capacity int64) {
	ordered := append([]applyInvoiceRow(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if !a.IssueDate.Equal(b.IssueDate) {
			return a.IssueDate.Before(b.IssueDate)
		}
		if !a.DueDate.Equal(b.DueDate) {
			return a.DueDate.Before(b.DueDate)
		}
		if a.InvoiceNo != b.InvoiceNo {
			return a.InvoiceNo < b.InvoiceNo
		}
		return a.InvoiceID < b.InvoiceID
	})
	remaining := amountCents
	lines = make([]ApplyLine, 0, len(ordered))
	for _, row := range ordered {
		payable := row.BalanceCents - row.HoldbackCents
		if payable < 0 {
			payable = 0
		}
		capacity += payable
		take := payable
		if take > remaining {
			take = remaining
		}
		after := row.BalanceCents - take
		staysOpen := after - row.HoldbackCents
		if staysOpen < 0 {
			staysOpen = 0
		}
		outcome := ApplyOutcomeNone
		switch {
		case take > 0 && after <= 0:
			outcome = ApplyOutcomeCloses
		case take > 0:
			outcome = ApplyOutcomePart
		}
		lines = append(lines, ApplyLine{
			InvoiceID: row.InvoiceID, InvoiceNo: row.InvoiceNo, AppliedCents: take,
			Outcome: outcome, BalanceAfterCents: after, StaysOpenCents: staysOpen,
		})
		remaining -= take
	}
	return lines, capacity
}

func rowStatus(rows []applyInvoiceRow, invoiceID string) string {
	for _, row := range rows {
		if row.InvoiceID == invoiceID {
			return row.Status
		}
	}
	return ""
}

func sumApplied(lines []ApplyLine) int64 {
	var sum int64
	for _, l := range lines {
		sum += l.AppliedCents
	}
	return sum
}
