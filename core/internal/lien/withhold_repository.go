package lien

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LinkedLienJob answers the lien job linked to a customer job (RT-1's link), on a caller's
// transaction when one rides ctx. The oldest linked RECEIVABLE job that is not closed wins, by
// creation then id, so the answer is the same on every read.
func (r *Repository) LinkedLienJob(ctx context.Context, customerJobID string) (string, bool, error) {
	q, err := r.q(ctx)
	if err != nil {
		return "", false, err
	}
	var id string
	err = q.QueryRow(ctx, `
		SELECT id::text FROM lien_jobs
		 WHERE customer_job_id = $1::uuid
		   AND status <> 'CLOSED'
		   AND COALESCE(metadata->>'holdback_direction', 'RECEIVABLE') = 'RECEIVABLE'
		 ORDER BY created_at, id LIMIT 1`, customerJobID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lien: the lien job linked to a customer job: %w", err)
	}
	return id, true, nil
}

// JobHoldingInvoice answers the lien job whose ledger holds an invoice's RETENTION.
func (r *Repository) JobHoldingInvoice(ctx context.Context, invoiceID string) (string, bool, error) {
	q, err := r.q(ctx)
	if err != nil {
		return "", false, err
	}
	var id string
	err = q.QueryRow(ctx, `
		SELECT job_id::text FROM holdback_ledger
		 WHERE invoice_id = $1::uuid AND entry_type = 'RETENTION' AND amount_cents > 0
		 ORDER BY created_at, id LIMIT 1`, invoiceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("lien: the job holding an invoice's retention: %w", err)
	}
	return id, true, nil
}

// InsertRetentionOnce books an invoice's RETENTION unless the invoice already has a positive one.
// It answers the rows written.
func (r *Repository) InsertRetentionOnce(ctx context.Context, e HoldbackEntry) (int64, error) {
	q, err := r.q(ctx)
	if err != nil {
		return 0, err
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO holdback_ledger (job_id, invoice_id, branch_id, entry_type, amount_cents, reason)
		SELECT $1::uuid, $2::uuid, $3::uuid, 'RETENTION', $4, $5
		 WHERE NOT EXISTS (SELECT 1 FROM holdback_ledger
		                    WHERE invoice_id = $2::uuid AND entry_type = 'RETENTION' AND amount_cents > 0)`,
		e.JobID, e.InvoiceID, e.BranchID, e.AmountCents, e.Reason)
	if err != nil {
		return 0, fmt.Errorf("lien: book the retention of invoice %s: %w", e.InvoiceID, err)
	}
	return tag.RowsAffected(), nil
}

// InsertReversalOnce books one signed RETENTION move unless the invoice already has a move with that
// reason (a credit memo's number or a write off's id is in the reason, one move per reason).
func (r *Repository) InsertReversalOnce(ctx context.Context, e HoldbackEntry) (int64, error) {
	q, err := r.q(ctx)
	if err != nil {
		return 0, err
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO holdback_ledger (job_id, invoice_id, branch_id, entry_type, amount_cents, reason)
		SELECT $1::uuid, $2::uuid, $3::uuid, 'RETENTION', $4, $5
		 WHERE NOT EXISTS (SELECT 1 FROM holdback_ledger
		                    WHERE invoice_id = $2::uuid AND entry_type = 'RETENTION'
		                      AND reason = $5)`,
		e.JobID, e.InvoiceID, e.BranchID, e.AmountCents, e.Reason)
	if err != nil {
		return 0, fmt.Errorf("lien: book the credit reversal on invoice %s: %w", e.InvoiceID, err)
	}
	return tag.RowsAffected(), nil
}
