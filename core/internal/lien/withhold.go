package lien

// withhold.go, the ledger half of RT-2 (wave 3): an invoice that withholds holdback gets its
// RETENTION row, and a credit memo against it takes the credited part back out.
//
// The invoice writer calls BookRetention INSIDE the invoice's own transaction (the caller's
// transaction rides the context, customer.WithCallerTx), so the invoice, the GL event and the ledger
// row commit or fail together. Lien never reads the invoices table: billing hands over the facts.
//
// A withholding is ledgered only when a lien job is linked to the invoice's customer job (RT-1's
// link). With no link there is no lien job to hold the row (holdback_ledger.job_id is NOT NULL), so
// nothing is booked and the existing idempotent sync (SyncHoldbacks) books the invoice once a job is
// linked.

import (
	"context"
	"fmt"
	"strings"
)

// RetentionBooking is one withholding the invoice writer asks the ledger to hold.
type RetentionBooking struct {
	InvoiceID     string
	InvoiceNo     string
	CustomerJobID string
	BranchID      string
	AmountCents   int64
}

// BookRetention books one RETENTION row for an invoice's withholding on the lien job linked to the
// invoice's customer job. It answers false (and writes nothing) when no lien job is linked or the
// amount is not positive. A second call for the same invoice books nothing.
func (s *Service) BookRetention(ctx context.Context, b RetentionBooking) (bool, error) {
	if b.AmountCents <= 0 || strings.TrimSpace(b.CustomerJobID) == "" {
		return false, nil
	}
	// The pick and the write share one transaction (the invoice's own when it rides ctx), so the share lock
	// on the lien job's row (0.55) holds until the RETENTION row is in: a move of the job's link waits for
	// it and then sees the row. Lock order: the lien job row, then the invoice's advisory lock, then insert.
	var booked bool
	err := s.repo.InTx(ctx, func(ctx context.Context) error {
		jobID, ok, err := s.repo.LinkedLienJobLocked(ctx, b.CustomerJobID)
		if err != nil || !ok {
			return err
		}
		n, err := s.repo.InsertRetentionOnce(ctx, HoldbackEntry{
			JobID: jobID, InvoiceID: b.InvoiceID, BranchID: b.BranchID, EntryType: "RETENTION",
			AmountCents: b.AmountCents, Reason: "withheld on invoice " + b.InvoiceNo,
		})
		booked = n > 0
		return err
	})
	return booked, err
}

// HasLinkedLienJob answers whether a lien job (a RECEIVABLE one, not closed) is linked to the customer
// job. Only such a job withholds: holdback is a construction contract's retention, and the lien job is
// where the dealer says a job is one. It reads on the caller's transaction when one rides ctx.
func (s *Service) HasLinkedLienJob(ctx context.Context, customerJobID string) (bool, error) {
	if strings.TrimSpace(customerJobID) == "" {
		return false, nil
	}
	_, ok, err := s.repo.LinkedLienJob(ctx, customerJobID)
	return ok, err
}

// CreditReversal is the holdback part a credit memo took back from an invoice's retention.
type CreditReversal struct {
	InvoiceID   string // the invoice the memo reverses
	MemoNo      string
	BranchID    string
	AmountCents int64 // positive: what comes off the retention
}

// HoldbackMove is a signed change to an invoice's retention that is not a credit memo: a write off
// takes the holdback part out (negative) and its reversal puts it back (positive).
type HoldbackMove struct {
	InvoiceID   string
	BranchID    string
	Reason      string // the move's own key: one row per reason
	AmountCents int64  // signed, never zero
}

// BookHoldbackMove books one signed RETENTION row on the lien job whose ledger holds the invoice's
// retention, once per reason. It answers false when the invoice was never ledgered or the move was
// already booked.
func (s *Service) BookHoldbackMove(ctx context.Context, m HoldbackMove) (bool, error) {
	if m.AmountCents == 0 {
		return false, nil
	}
	if strings.TrimSpace(m.Reason) == "" {
		return false, fmt.Errorf("lien: a holdback move names its reason")
	}
	jobID, ok, err := s.repo.JobHoldingInvoice(ctx, m.InvoiceID)
	if err != nil || !ok {
		return false, err
	}
	n, err := s.repo.InsertReversalOnce(ctx, HoldbackEntry{
		JobID: jobID, InvoiceID: m.InvoiceID, BranchID: m.BranchID, EntryType: "RETENTION",
		AmountCents: m.AmountCents, Reason: m.Reason,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// BookCreditReversal takes a credit memo's holdback part off the retention of the invoice it
// reverses: a negative RETENTION row on the same lien job. The billing writer calls it on the memo's own
// transaction, so the memo, its holdback part and this row commit together. It answers false when the
// invoice was never ledgered (no lien job held it) and when the memo was already booked.
func (s *Service) BookCreditReversal(ctx context.Context, r CreditReversal) (bool, error) {
	if r.AmountCents <= 0 {
		return false, nil
	}
	if strings.TrimSpace(r.MemoNo) == "" {
		return false, fmt.Errorf("lien: a credit reversal of the holdback names its memo")
	}
	jobID, ok, err := s.repo.JobHoldingInvoice(ctx, r.InvoiceID)
	if err != nil || !ok {
		return false, err
	}
	n, err := s.repo.InsertReversalOnce(ctx, HoldbackEntry{
		JobID: jobID, InvoiceID: r.InvoiceID, BranchID: r.BranchID, EntryType: "RETENTION",
		AmountCents: -r.AmountCents, Reason: "credit memo " + r.MemoNo,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
