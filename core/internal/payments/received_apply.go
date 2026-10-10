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
	in, err := validateApply(in)
	if err != nil {
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
			return r.replayAnswer(ctx, p, in)
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

	// The counter has now serialised this writer against every payments writer, so a twin of this
	// payment (the same key) that committed while this request waited on the counter is already
	// visible here. Re-read the key on the transaction BEFORE the invoice judgements: a duplicate
	// of a payment that closed its invoices must answer the first result, not trip the not open or
	// ceiling refusal on the balances the twin already moved (the same in-transaction re-check the
	// till's return makes after its sale lock, and QD-5's rule at counter_receipt.go). A preview
	// makes no re-read, exactly as it takes no fast path before the transaction: it writes nothing,
	// so no twin of it can exist and no key of it is spent; its answer is the fresh split against
	// the live balances whatever key it rode in on (the delta reviews' P3).
	if !in.Preview {
		if _, _, _, ok, err := receiptByKeyOn(ctx, tx, in.IdempotencyKey); err != nil {
			return ApplyAnswer{}, err
		} else if ok {
			_ = tx.Rollback(ctx)
			existing, found, readErr := r.receiptByKey(ctx, in.IdempotencyKey)
			if readErr != nil {
				return ApplyAnswer{}, readErr
			}
			if !found {
				return ApplyAnswer{}, fmt.Errorf("payments: apply key %q was claimed and then vanished", in.IdempotencyKey)
			}
			return r.replayAnswer(ctx, existing, in)
		}
	}

	rows, err := r.lockApplyInvoicesTx(ctx, tx, in)
	if err != nil {
		return ApplyAnswer{}, err
	}
	// ONE branch per payment: the payment row carries a single branch, every subledger row is
	// stamped with it and the GL consumer books the whole journal to it, so a list spanning two
	// branches would move the other branch's AR with no journal behind it and put both branches'
	// tie outs out by the other's share. The honest shape is a payment per branch.
	for _, row := range rows[1:] {
		if row.BranchID != rows[0].BranchID {
			return ApplyAnswer{}, httpx.BadRequestf(
				"one payment applies to one branch's invoices; record a payment per branch")
		}
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

	// The named list rides the payment row (see ReceiptRequest.NamedInvoices) so a replay of this
	// key can be judged against the body that posted, including an invoice the walk never reached.
	named := append([]string(nil), in.InvoiceIDs...)
	sort.Strings(named)
	p, claimed, err := settleReceipt(ctx, tx, ReceiptRequest{
		CustomerID: in.CustomerID, Method: in.Method, Reference: in.Reference,
		IdempotencyKey: in.IdempotencyKey, ReceivedBy: in.ReceivedBy,
		Allocations: allocs, NamedInvoices: named,
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
			return r.replayAnswer(ctx, existing, in)
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
// validateReceipt holds: a malformed request is malformed with or without a pool behind it. It
// answers the request with its account id and its invoice ids NORMALISED, each trimmed and
// re-spelled in the canonical form, so the rest of the walk (the replay body, the duplicate
// refusal, the sort, the locks, the stored body) sees one spelling per id: uuid.Parse accepts
// upper case, braces, the urn prefix, the undashed form and Postgres folds them all onto the same
// row, so the raw strings cannot be the identity, and a malformed account id must be refused here
// in words, never left to the lock statement's uuid cast to surface as the route's 500.
func validateApply(in ApplyRequest) (ApplyRequest, error) {
	if in.IdempotencyKey == "" {
		return ApplyRequest{}, httpx.BadRequestf("recording a payment needs an idempotency key")
	}
	if !validTender(in.Method) {
		return ApplyRequest{}, httpx.BadRequestf("tender must be one of %s", strings.Join(CounterTenders, ", "))
	}
	if in.AmountCents <= 0 {
		return ApplyRequest{}, httpx.BadRequestf("a payment amount must be greater than zero")
	}
	cu, err := uuid.Parse(strings.TrimSpace(in.CustomerID))
	if err != nil {
		return ApplyRequest{}, httpx.BadRequestf("customer id must be a uuid")
	}
	in.CustomerID = cu.String()
	if len(in.InvoiceIDs) == 0 {
		return ApplyRequest{}, httpx.BadRequestf("a payment must be applied to at least one invoice")
	}
	if len(in.InvoiceIDs) > MaxApplyInvoices {
		return ApplyRequest{}, httpx.BadRequestf("a received payment may name at most %d invoices; record it as more than one payment", MaxApplyInvoices)
	}
	ids := make([]string, 0, len(in.InvoiceIDs))
	seen := map[string]bool{}
	for _, raw := range in.InvoiceIDs {
		u, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return ApplyRequest{}, httpx.BadRequestf("invoice ids must be uuids")
		}
		id := u.String()
		if seen[id] {
			return ApplyRequest{}, httpx.BadRequestf("the same invoice is named twice: %s", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	in.InvoiceIDs = ids
	return in, nil
}

// normalisedInvoiceIDs answers a list of invoice ids in the one spelling the walk locks in: each
// trimmed, parsed and re-spelled canonical, then the list sorted. Two lists that differ only in
// order, or in the uuid spellings Parse accepts (case, braces, the undashed form), are one list.
func normalisedInvoiceIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		u, err := uuid.Parse(strings.TrimSpace(id))
		if err != nil {
			return nil, err
		}
		out = append(out, u.String())
	}
	sort.Strings(out)
	return out, nil
}

// sameReplayBody answers whether the stored payment is THIS request's own replay. The body of a
// received payment is its account, its amount, its method, its reference and its named invoice
// list, the last compared after the same normalising the route applies (canonical spellings,
// order free, exactly the freedom ruling 73 gives the list). A key that recorded a different
// payment is not a bearer of that payment's answer.
func sameReplayBody(p Payment, storedReference string, storedNamed []string, in ApplyRequest) bool {
	if p.CustomerID != in.CustomerID || p.AmountCents != in.AmountCents || p.Method != in.Method ||
		storedReference != in.Reference {
		return false
	}
	stored, err := normalisedInvoiceIDs(storedNamed)
	if err != nil {
		return false
	}
	named, err := normalisedInvoiceIDs(in.InvoiceIDs)
	if err != nil {
		return false
	}
	if len(stored) != len(named) {
		return false
	}
	for i := range stored {
		if stored[i] != named[i] {
			return false
		}
	}
	return true
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
