package purchasing

// ap_revaluation_runs.go, the revaluation's run record (migration 0476, W3 FX-2b): one row per
// branch and house month, whether the run posted a journal or not. The guards read it (an earlier
// month unreversed, a later month posted), a zero run is recorded on it, and the reversal stamps
// it. The journal ids are plain values: the GL owns its journals and the module reads them only
// through the GL seam.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// apRevalBranchLockPrefix names the one advisory lock a branch's revaluation work takes: both the
// run and the reversal hold it for their whole transaction, so two runs for adjacent months (or a
// run beside a reversal) of one branch are serialised and the second judges the first's committed
// record.
const apRevalBranchLockPrefix = apRevalKeyPrefix + "branch:"

// APRevaluationRunRecord is one run's record: what was measured, at what rate, the journal if any,
// who pressed it and when, and the reversal if it has posted.
type APRevaluationRunRecord struct {
	BranchID          string     `json:"branch_id"`
	Month             string     `json:"month"`
	FXRate            float64    `json:"fx_rate"`
	Bills             int        `json:"bills"`
	DocOpenCents      int64      `json:"doc_open_cents"`
	CarriedCadCents   int64      `json:"carried_cad_cents"`
	RevaluedCadCents  int64      `json:"revalued_cad_cents"`
	DifferenceCents   int64      `json:"difference_cents"`
	JournalID         string     `json:"journal_id,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	ReversalJournalID string     `json:"reversal_journal_id,omitempty"`
	ReversedBy        string     `json:"reversed_by,omitempty"`
	ReversedAt        *time.Time `json:"reversed_at,omitempty"`
}

const apRevalRunColumns = `branch_id::text, month, fx_rate::float8, bills, doc_open_cents, carried_cad_cents,
	revalued_cad_cents, difference_cents, COALESCE(journal_id::text, ''), created_by, created_at,
	COALESCE(reversal_journal_id::text, ''), reversed_by, reversed_at`

func scanAPRevalRun(row pgx.Row) (APRevaluationRunRecord, error) {
	var r APRevaluationRunRecord
	err := row.Scan(&r.BranchID, &r.Month, &r.FXRate, &r.Bills, &r.DocOpenCents, &r.CarriedCadCents,
		&r.RevaluedCadCents, &r.DifferenceCents, &r.JournalID, &r.CreatedBy, &r.CreatedAt,
		&r.ReversalJournalID, &r.ReversedBy, &r.ReversedAt)
	return r, err
}

// LockRevaluationBranchTx holds the branch's revaluation lock until the transaction ends.
func (r *Repository) LockRevaluationBranchTx(ctx context.Context, tx pgx.Tx, branchID string) error {
	return r.LockPaymentKeyTx(ctx, tx, apRevalBranchLockPrefix+branchID)
}

// RevaluationRunTx reads one branch month's record.
func (r *Repository) RevaluationRunTx(ctx context.Context, tx pgx.Tx, branchID, month string) (APRevaluationRunRecord, bool, error) {
	rec, err := scanAPRevalRun(r.q(tx).QueryRow(ctx,
		`SELECT `+apRevalRunColumns+` FROM ap_revaluation_runs WHERE branch_id = $1::uuid AND month = $2`, branchID, month))
	if err == pgx.ErrNoRows {
		return APRevaluationRunRecord{}, false, nil
	}
	if err != nil {
		return APRevaluationRunRecord{}, false, fmt.Errorf("purchasing: read revaluation run: %w", err)
	}
	return rec, true, nil
}

// InsertRevaluationRunTx files a run's record in the run's own transaction.
func (r *Repository) InsertRevaluationRunTx(ctx context.Context, tx pgx.Tx, rec APRevaluationRunRecord) error {
	var journal any
	if rec.JournalID != "" {
		journal = rec.JournalID
	}
	if _, err := r.q(tx).Exec(ctx, `
		INSERT INTO ap_revaluation_runs
		    (branch_id, month, fx_rate, bills, doc_open_cents, carried_cad_cents, revalued_cad_cents,
		     difference_cents, journal_id, created_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10)`,
		rec.BranchID, rec.Month, rec.FXRate, rec.Bills, rec.DocOpenCents, rec.CarriedCadCents,
		rec.RevaluedCadCents, rec.DifferenceCents, journal, rec.CreatedBy); err != nil {
		return fmt.Errorf("purchasing: file revaluation run: %w", err)
	}
	return nil
}

// UnreversedRevaluationBeforeTx answers the earliest month of the branch, before month, whose run
// posted a journal that has no reversal yet.
func (r *Repository) UnreversedRevaluationBeforeTx(ctx context.Context, tx pgx.Tx, branchID, month string) (string, bool, error) {
	var found string
	err := r.q(tx).QueryRow(ctx, `
		SELECT month FROM ap_revaluation_runs
		 WHERE branch_id = $1::uuid AND month < $2
		   AND journal_id IS NOT NULL AND reversal_journal_id IS NULL
		 ORDER BY month LIMIT 1`, branchID, month).Scan(&found)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("purchasing: read earlier revaluation runs: %w", err)
	}
	return found, true, nil
}

// PostedRevaluationAfterTx answers the earliest month of the branch, after month, whose run
// posted a journal (reversed or not).
func (r *Repository) PostedRevaluationAfterTx(ctx context.Context, tx pgx.Tx, branchID, month string) (string, bool, error) {
	var found string
	err := r.q(tx).QueryRow(ctx, `
		SELECT month FROM ap_revaluation_runs
		 WHERE branch_id = $1::uuid AND month > $2 AND journal_id IS NOT NULL
		 ORDER BY month LIMIT 1`, branchID, month).Scan(&found)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("purchasing: read later revaluation runs: %w", err)
	}
	return found, true, nil
}

// MarkRevaluationReversedTx stamps the run's record with its reversal, in the reversal's own
// transaction. A run filed before the record existed has no row to stamp, which is not an error.
func (r *Repository) MarkRevaluationReversedTx(ctx context.Context, tx pgx.Tx, branchID, month, reversalJournalID, by string) error {
	if _, err := r.q(tx).Exec(ctx, `
		UPDATE ap_revaluation_runs
		   SET reversal_journal_id = $3::uuid, reversed_by = $4, reversed_at = now()
		 WHERE branch_id = $1::uuid AND month = $2 AND journal_id IS NOT NULL AND reversal_journal_id IS NULL`,
		branchID, month, reversalJournalID, by); err != nil {
		return fmt.Errorf("purchasing: stamp revaluation reversal: %w", err)
	}
	return nil
}

// ListRevaluationRuns reads a branch's runs, newest month first.
func (r *Repository) ListRevaluationRuns(ctx context.Context, branchID string) ([]APRevaluationRunRecord, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.q(nil).Query(ctx,
		`SELECT `+apRevalRunColumns+` FROM ap_revaluation_runs WHERE branch_id = $1::uuid ORDER BY month DESC`, branchID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list revaluation runs: %w", err)
	}
	defer rows.Close()
	var out []APRevaluationRunRecord
	for rows.Next() {
		rec, err := scanAPRevalRun(rows)
		if err != nil {
			return nil, fmt.Errorf("purchasing: scan revaluation run: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DueRevaluationRun is one posted run with no reversal yet: the branch and the house month.
type DueRevaluationRun struct {
	BranchID string
	Month    string
}

// UnreversedRevaluationRuns lists the runs that posted a journal and have no reversal, oldest month
// first. A nil branch list is every branch (the worker's global reach); otherwise only those named.
func (r *Repository) UnreversedRevaluationRuns(ctx context.Context, branchIDs []string) ([]DueRevaluationRun, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT branch_id::text, month FROM ap_revaluation_runs
		 WHERE journal_id IS NOT NULL AND reversal_journal_id IS NULL
		   AND ($1::uuid[] IS NULL OR branch_id = ANY($1::uuid[]))
		 ORDER BY month, branch_id`, branchIDs)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list unreversed revaluation runs: %w", err)
	}
	defer rows.Close()
	var out []DueRevaluationRun
	for rows.Next() {
		var d DueRevaluationRun
		if err := rows.Scan(&d.BranchID, &d.Month); err != nil {
			return nil, fmt.Errorf("purchasing: scan unreversed revaluation run: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
