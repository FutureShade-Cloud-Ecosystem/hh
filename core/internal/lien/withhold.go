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
	jobID, ok, err := s.repo.LinkedLienJob(ctx, b.CustomerJobID)
	if err != nil || !ok {
		return false, err
	}
	n, err := s.repo.InsertRetentionOnce(ctx, HoldbackEntry{
		JobID: jobID, InvoiceID: b.InvoiceID, BranchID: b.BranchID, EntryType: "RETENTION",
		AmountCents: b.AmountCents, Reason: "withheld on invoice " + b.InvoiceNo,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CreditReversal is the holdback part a credit memo took back from an invoice's retention.
type CreditReversal struct {
	InvoiceID   string // the invoice the memo reverses
	MemoNo      string
	BranchID    string
	AmountCents int64 // positive: what comes off the retention
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
