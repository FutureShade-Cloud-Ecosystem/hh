package printing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements both the queue store and the receipt reads over pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold/offline mode).
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var errNoPool = errors.New("printing: no database pool configured")

const jobCols = `id::text, doc_type, ref_id::text, branch_id::text, render_target, status,
	COALESCE(requested_by::text,''), failure_reason, requested_at, printed_at`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.DocType, &j.RefID, &j.BranchID, &j.RenderTarget, &j.Status,
		&j.RequestedBy, &j.FailureReason, &j.RequestedAt, &j.PrintedAt)
	return j, err
}

// InsertJob writes one QUEUED row.
func (r *Repository) InsertJob(ctx context.Context, j Job) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoPool
	}
	return scanJob(r.pool.QueryRow(ctx, `
		INSERT INTO print_queue (doc_type, ref_id, branch_id, render_target, status, requested_by, requested_at)
		VALUES ($1, $2::uuid, $3::uuid, $4, 'QUEUED', NULLIF($5,'')::uuid, COALESCE($6, now()))
		RETURNING `+jobCols,
		j.DocType, j.RefID, j.BranchID, j.RenderTarget, j.RequestedBy, nullTime(j.RequestedAt)))
}

// MarkJobPrinted flips QUEUED → PRINTED in ONE guarded statement. The status predicate is IN the
// UPDATE, not a read-then-write: two concurrent marks cannot both report success, so a PRINTED row
// corresponds to exactly one print and the reprint count stays honest.
//
// found=false → no such job. found=true, updated=false → the job exists but was not QUEUED.
func (r *Repository) MarkJobPrinted(ctx context.Context, id string, at time.Time) (bool, bool, error) {
	if r.pool == nil {
		return false, false, errNoPool
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE print_queue SET status = 'PRINTED', printed_at = $2, updated_at = now()
		 WHERE id = $1::uuid AND status = 'QUEUED'`, id, at)
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() == 1 {
		return true, true, nil
	}
	// Nothing moved: distinguish "no such job" from "not QUEUED" so the caller can answer honestly.
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM print_queue WHERE id = $1::uuid)`, id).Scan(&exists); err != nil {
		return false, false, err
	}
	return exists, false, nil
}

// ListJobsForRef returns one document's print history, newest first.
func (r *Repository) ListJobsForRef(ctx context.Context, docType, refID string) ([]Job, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+jobCols+` FROM print_queue WHERE doc_type = $1 AND ref_id = $2::uuid
		  ORDER BY requested_at DESC, id DESC`, docType, refID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CountPrintedForRef counts the COMPLETED prints of one document, the receipt's reprint counter.
// QUEUED and FAILED rows are deliberately excluded: paper that never came out of a printer is not a
// print, and counting it would stamp REPRINT on a receipt the customer is seeing for the first time.
func (r *Repository) CountPrintedForRef(ctx context.Context, docType, refID string) (int, error) {
	if r.pool == nil {
		return 0, errNoPool
	}
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM print_queue WHERE doc_type = $1 AND ref_id = $2::uuid AND status = 'PRINTED'`,
		docType, refID).Scan(&n)
	return n, err
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// compile-time assertion: *Repository satisfies the queue store seam.
var _ queueStore = (*Repository)(nil)
