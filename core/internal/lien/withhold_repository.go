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

// LinkedLienJobLocked answers LinkedLienJob's job after taking its row FOR SHARE on the caller's transaction,
// so a move of its link (LockJobLink, FOR UPDATE) waits for the transaction and a withhold booking on the
// job is never one whose link moved away under it. Shares are compatible, so withholds do not queue behind
// one another. When the candidate's link moved before the lock was won, the pick is made again.
func (r *Repository) LinkedLienJobLocked(ctx context.Context, customerJobID string) (string, bool, error) {
	q, err := r.q(ctx)
	if err != nil {
		return "", false, err
	}
	for attempt := 0; attempt < 8; attempt++ {
		id, ok, err := r.LinkedLienJob(ctx, customerJobID)
		if err != nil || !ok {
			return "", false, err
		}
		var locked string
		err = q.QueryRow(ctx, `
			SELECT id::text FROM lien_jobs
			 WHERE id = $1::uuid AND customer_job_id = $2::uuid
			   AND status <> 'CLOSED'
			   AND COALESCE(metadata->>'holdback_direction', 'RECEIVABLE') = 'RECEIVABLE'
			   FOR SHARE`, id, customerJobID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the link moved while the lock was awaited
		}
		if err != nil {
			return "", false, fmt.Errorf("lien: lock the lien job linked to a customer job: %w", err)
		}
		return locked, true, nil
	}
	return "", false, fmt.Errorf("lien: the lien job linked to a customer job kept moving")
}

// LinkedLienJobs answers EVERY open RECEIVABLE lien job linked to a customer job, oldest first. More than one
// may be linked; RT-3 judges them all (LinkedLienJob names only the oldest).
func (r *Repository) LinkedLienJobs(ctx context.Context, customerJobID string) ([]string, error) {
	q, err := r.q(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT id::text FROM lien_jobs
		 WHERE customer_job_id = $1::uuid
		   AND status <> 'CLOSED'
		   AND COALESCE(metadata->>'holdback_direction', 'RECEIVABLE') = 'RECEIVABLE'
		 ORDER BY created_at, id`, customerJobID)
	if err != nil {
		return nil, fmt.Errorf("lien: the lien jobs linked to a customer job: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("lien: scan a linked lien job: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// JobsHoldingInvoices answers each distinct lien job whose ledger holds a positive RETENTION of any of the
// invoices, oldest first.
func (r *Repository) JobsHoldingInvoices(ctx context.Context, invoiceIDs []string) ([]string, error) {
	if len(invoiceIDs) == 0 {
		return nil, nil
	}
	q, err := r.q(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT job_id::text FROM holdback_ledger
		 WHERE invoice_id = ANY($1::uuid[]) AND entry_type = 'RETENTION' AND amount_cents > 0
		 GROUP BY job_id ORDER BY min(created_at), job_id`, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("lien: the jobs holding invoices' retention: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("lien: scan a holding job: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
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

// InsertRetentionOnceIfLinked is InsertRetentionOnce for a caller that read the lien job's link earlier (the
// holdback sync). On one transaction it takes the lien job's row FOR SHARE with the link rechecked in the
// same statement, and books only if the job is still linked to the invoice's customer job; otherwise it
// books nothing and answers 0. The order is the withhold's (the lien job row, then the invoice's advisory
// lock, then the insert), so a move of the link (FOR UPDATE on the same row first) waits for the booking and
// then refuses, or commits first and the booking is skipped (0.55).
func (r *Repository) InsertRetentionOnceIfLinked(ctx context.Context, e HoldbackEntry, customerJobID string) (int64, error) {
	var n int64
	err := r.InTx(ctx, func(ctx context.Context) error {
		q, err := r.q(ctx)
		if err != nil {
			return err
		}
		var locked string
		err = q.QueryRow(ctx, `
			SELECT id::text FROM lien_jobs
			 WHERE id = $1::uuid AND customer_job_id = $2::uuid
			   FOR SHARE`, e.JobID, customerJobID).Scan(&locked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the link moved after the sync read it: nothing is booked on this job
		}
		if err != nil {
			return fmt.Errorf("lien: lock the lien job the sync books on: %w", err)
		}
		n, err = r.InsertRetentionOnce(ctx, e)
		return err
	})
	return n, err
}

// InsertRetentionOnce books an invoice's RETENTION unless the invoice already has a positive one.
// It answers the rows written. Two callers booking the same invoice at once book it once: the guard
// is a read, so the invoice's id is locked (a transaction scoped advisory lock) before it, and the
// second caller reads the first one's row once the lock is its own.
func (r *Repository) InsertRetentionOnce(ctx context.Context, e HoldbackEntry) (int64, error) {
	var n int64
	err := r.InTx(ctx, func(ctx context.Context) error {
		q, err := r.q(ctx)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('holdback_ledger:' || $1::text, 0))`, e.InvoiceID); err != nil {
			return fmt.Errorf("lien: lock the invoice's retention: %w", err)
		}
		tag, err := q.Exec(ctx, `
			INSERT INTO holdback_ledger (job_id, invoice_id, branch_id, entry_type, amount_cents, reason)
			SELECT $1::uuid, $2::uuid, $3::uuid, 'RETENTION', $4, $5
			 WHERE NOT EXISTS (SELECT 1 FROM holdback_ledger
			                    WHERE invoice_id = $2::uuid AND entry_type = 'RETENTION' AND amount_cents > 0)`,
			e.JobID, e.InvoiceID, e.BranchID, e.AmountCents, e.Reason)
		if err != nil {
			return fmt.Errorf("lien: book the retention of invoice %s: %w", e.InvoiceID, err)
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err
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
