package customer

// job_repository.go, the pgx persistence of the job accounts (wave 3 JA-1, decision 1.1 A).
//
// The table is this module's own (customer_jobs, migration 0460), so every statement here reads or
// writes customer_jobs and nothing else: the branch fence is a predicate on the job's own
// branch_id, the account's own limit and terms are read through this module's own customers table
// when a rule needs them, and no other module's table is touched.
//
// THE FENCE IS THE FENCE. Every read and every write is scoped by the caller's reach
// (AccountReach, account_lookup.go): a job of a branch the caller's grants do not reach is NOT
// FOUND, never forbidden, and a write can never touch one either. The write statements carry the
// predicate themselves rather than trusting a read that happened a moment earlier.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// ErrDuplicateJobNo is returned when a job number is already taken under the same customer (the
// customer_jobs_job_no_per_customer constraint, migration 0460). Exported so the handler maps it
// to a 409 with clear words, the same shape ErrDuplicateAccountNo already has in this module.
var ErrDuplicateJobNo = errors.New("customer: job number already in use for this customer")

// JobFields is the resolved set of a job's own editable columns: what a write persists.
//
// The service resolves the edit against the job as it stands (a field left out keeps the job's own
// value) and hands the resolved set here, so the write never re-decides what an absent field means
// and the two halves cannot drift apart.
type JobFields struct {
	Name                       string
	ShipToAddress              map[string]any
	POReference                string
	ContactName                string
	ContactEmail               string
	ContactPhone               string
	CreditLimitCents           *int64
	TermsDays                  *int
	SubstantialPerformanceDate *time.Time
	LastSupplyDate             *time.Time
}

// JobWrite is what an insert persists: the job's own columns, plus the customer, the branch and
// the job number it is filed under, and the staff member who filed it.
type JobWrite struct {
	CustomerID string
	BranchID   string
	JobNo      string
	CreatedBy  string
	Fields     JobFields
}

// jobColumns is the canonical SELECT list for a Job.
//
// credit_limit_cents and terms_days are nullable on purpose (a job with no own figure shares the
// customer's), so they scan into pointers. effective_terms_days is the one figure in the list that
// is not stored: it is the job's own terms when the job carries any and the customer's when it does
// not, and it is computed in the read so a job that shares the customer's terms follows the
// customer when the customer edits them. substantial_performance_date and last_supply_date are the
// job's own lien dates, kept for wave 3 JA-2. created_by is nullable (a staff member with no
// linked ERP row cannot be attributed), so it is COALESCEd to an empty string, and the write
// refuses an unattributable write rather than attributing it to nobody.
const jobColumns = `j.id::text, j.customer_id::text, j.branch_id::text, j.job_no::text, j.name,
	j.ship_to_address, j.po_reference, j.contact_name, COALESCE(j.contact_email::text, ''), j.contact_phone,
	j.credit_limit_cents, j.terms_days, COALESCE(j.terms_days, c.payment_terms_days), j.status,
	j.substantial_performance_date, j.last_supply_date, COALESCE(j.created_by::text, ''),
	j.created_at, j.updated_at`

// jobFrom is the job's own row beside its customer's, which is where the effective terms come
// from. The join is an inner one: a job always belongs to a customer (the FK says so), so there is
// no job whose customer is missing.
const jobFrom = `FROM customer_jobs j JOIN customers c ON c.id = j.customer_id`

// scanJob reads one job row.
func scanJob(row interface{ Scan(dest ...any) error }) (Job, error) {
	var (
		j          Job
		addr       []byte
		perf       *time.Time
		lastSupply *time.Time
	)
	if err := row.Scan(
		&j.JobID, &j.CustomerID, &j.BranchID, &j.JobNo, &j.Name,
		&addr, &j.POReference, &j.ContactName, &j.ContactEmail, &j.ContactPhone,
		&j.CreditLimitCents, &j.TermsDays, &j.EffectiveTermsDays, &j.Status,
		&perf, &lastSupply, &j.CreatedBy, &j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		return Job{}, err
	}
	if len(addr) > 0 {
		if err := json.Unmarshal(addr, &j.ShipToAddress); err != nil {
			return Job{}, fmt.Errorf("customer: decode the job's ship to address: %w", err)
		}
	}
	if j.ShipToAddress == nil {
		j.ShipToAddress = map[string]any{}
	}
	j.SubstantialPerformanceDate = perf
	j.LastSupplyDate = lastSupply
	return j, nil
}

// InsertJob inserts one job under its customer and returns it as stored.
func (r *Repository) InsertJob(ctx context.Context, w JobWrite) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	addr, err := json.Marshal(orEmptyObject(w.Fields.ShipToAddress))
	if err != nil {
		return Job{}, fmt.Errorf("customer: encode the job's ship to address: %w", err)
	}
	var id string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, ship_to_address, po_reference,
		                           contact_name, contact_email, contact_phone, credit_limit_cents, terms_days,
		                           substantial_performance_date, last_supply_date, created_by)
		SELECT $1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11, $12, $13, NULLIF($14, '')::uuid
		 WHERE $10::bigint IS NULL
		    OR $10::bigint <= (SELECT credit_limit_cents FROM customers WHERE id = $1)
		RETURNING id::text`,
		w.CustomerID, w.BranchID, w.JobNo, w.Fields.Name, addr, w.Fields.POReference,
		w.Fields.ContactName, w.Fields.ContactEmail, w.Fields.ContactPhone,
		w.Fields.CreditLimitCents, w.Fields.TermsDays,
		w.Fields.SubstantialPerformanceDate, w.Fields.LastSupplyDate, w.CreatedBy).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, httpx.BadRequestf("a job's own credit limit cannot exceed the customer's own credit limit")
		}
		// The job number is unique PER CUSTOMER (migration 0460): a collision is the caller's
		// mistake, not a server fault, so it is the sentinel the handler maps to a 409.
		if isUniqueViolation(err) {
			return Job{}, ErrDuplicateJobNo
		}
		return Job{}, fmt.Errorf("customer: insert the job: %w", err)
	}
	return r.GetJobByID(ctx, id)
}

// GetJobByID reads one job by its own id, with no fence. It is the read that follows a write the
// caller has already been let through, so it is not a door onto another branch's job: it is never
// reached from a route without the fence in front of it.
func (r *Repository) GetJobByID(ctx context.Context, jobID string) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	q := fmt.Sprintf("SELECT %s %s WHERE j.id = $1", jobColumns, jobFrom)
	j, err := scanJob(r.pool.QueryRow(ctx, q, jobID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("customer: read the job %q: %w", jobID, err)
	}
	return j, nil
}

// GetJob reads one job of one customer inside the caller's reach.
//
// The predicate is the fence: a job of another customer, a job of another branch, and a job that
// nobody has all answer not found. A caller with no branch grant at all (an empty, non-nil reach)
// matches nothing, which is the opposite of what passing no filter would do.
func (r *Repository) GetJob(ctx context.Context, customerID, jobID string, reach AccountReach) (Job, bool, error) {
	if r.pool == nil {
		return Job{}, false, errNoDB
	}
	q := fmt.Sprintf(`SELECT %s
	  %s
	 WHERE j.id = $1
	   AND j.customer_id = $2
	   AND ($3::uuid[] IS NULL OR j.branch_id = ANY($3::uuid[]))`, jobColumns, jobFrom)
	j, err := scanJob(r.pool.QueryRow(ctx, q, jobID, customerID, reach.branchFilter()))
	switch {
	case err == nil:
		return j, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return Job{}, false, nil
	default:
		return Job{}, false, fmt.Errorf("customer: read the job %q: %w", jobID, err)
	}
}

// ListJobs lists a customer's jobs inside the caller's reach, oldest first.
//
// The list is fenced the same way the single read is: a job of a branch the caller cannot reach is
// not in the list. A list that answered every job would leak the other branches' job names and
// numbers, which is exactly what the single read refuses to answer.
func (r *Repository) ListJobs(ctx context.Context, customerID string, reach AccountReach) ([]Job, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	q := fmt.Sprintf(`SELECT %s
	  %s
	 WHERE j.customer_id = $1
	   AND ($2::uuid[] IS NULL OR j.branch_id = ANY($2::uuid[]))
	 ORDER BY j.created_at, j.job_no`, jobColumns, jobFrom)
	rows, err := r.pool.Query(ctx, q, customerID, reach.branchFilter())
	if err != nil {
		return nil, fmt.Errorf("customer: list the jobs of %q: %w", customerID, err)
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

// UpdateJob writes the resolved columns of one job of one customer, inside the caller's reach.
//
// The fence is in the WHERE, not in a read that happened before it: a job of another branch is
// never written, even by a caller who got the row into their hands a moment ago.
func (r *Repository) UpdateJob(ctx context.Context, customerID, jobID string, f JobFields, reach AccountReach) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	addr, err := json.Marshal(orEmptyObject(f.ShipToAddress))
	if err != nil {
		return Job{}, fmt.Errorf("customer: encode the job's ship to address: %w", err)
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE customer_jobs
		   SET name = $1, ship_to_address = $2, po_reference = $3, contact_name = $4,
		       contact_email = NULLIF($5, ''), contact_phone = $6, credit_limit_cents = $7,
		       terms_days = $8, substantial_performance_date = $9, last_supply_date = $10,
		       updated_at = now()
		 WHERE id = $11
		   AND customer_id = $12
		   AND ($13::uuid[] IS NULL OR branch_id = ANY($13::uuid[]))
		   AND ($7::bigint IS NULL OR $7::bigint IS NOT DISTINCT FROM credit_limit_cents OR $7::bigint <= (SELECT credit_limit_cents FROM customers WHERE id = $12))`,
		f.Name, addr, f.POReference, f.ContactName, f.ContactEmail, f.ContactPhone,
		f.CreditLimitCents, f.TermsDays, f.SubstantialPerformanceDate, f.LastSupplyDate,
		jobID, customerID, reach.branchFilter())
	if err != nil {
		return Job{}, fmt.Errorf("customer: update the job %q: %w", jobID, err)
	}
	if tag.RowsAffected() == 0 {
		if _, found, err := r.GetJob(ctx, customerID, jobID, reach); err != nil {
			return Job{}, err
		} else if !found {
			return Job{}, ErrNotFound
		}
		return Job{}, httpx.BadRequestf("a job's own credit limit cannot exceed the customer's own credit limit")
	}
	return r.GetJobByID(ctx, jobID)
}

// CloseJob closes one job of one customer, inside the caller's reach.
//
// A job is closed, never reopened by this write: the close is the door, and a job that is finished
// takes no new document. The status is the only column this write touches.
func (r *Repository) CloseJob(ctx context.Context, customerID, jobID string, reach AccountReach) (Job, error) {
	if r.pool == nil {
		return Job{}, errNoDB
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE customer_jobs SET status = 'CLOSED', updated_at = now()
		 WHERE id = $1
		   AND customer_id = $2
		   AND status = 'OPEN'
		   AND ($3::uuid[] IS NULL OR branch_id = ANY($3::uuid[]))`,
		jobID, customerID, reach.branchFilter())
	if err != nil {
		return Job{}, fmt.Errorf("customer: close the job %q: %w", jobID, err)
	}
	if tag.RowsAffected() == 0 {
		// Either the job is not there for this caller, or it is already closed. The second is a
		// conflict the caller can see, so it is told apart by reading the row back.
		j, found, err := r.GetJob(ctx, customerID, jobID, reach)
		if err != nil {
			return Job{}, err
		}
		if !found {
			return Job{}, ErrNotFound
		}
		return j, ErrJobAlreadyClosed
	}
	return r.GetJobByID(ctx, jobID)
}

// ErrJobAlreadyClosed is returned when a close is asked of a job that is already closed. The
// close is idempotent in its effect (the job stays closed) but it is not silent: a caller that
// closes the same job twice is told the second time.
var ErrJobAlreadyClosed = errors.New("customer: the job is already closed")

// JobTerms is what the effective terms read answers: the job's own terms when it carries them, the
// customer's when it does not, and the customer the terms came from.
type JobTerms struct {
	// TermsDays is the effective number of days: the job's own when it carries any, the
	// customer's otherwise.
	TermsDays int
	// Own is true when the terms are the job's own, false when they are the customer's.
	Own bool
}

// JobEffectiveTerms reads the effective terms of one job: the job's own when it carries them, the
// customer's when it does not.
//
// IT IS ONE READ. The customer's terms and the job's own terms live in two tables this module owns,
// and the later waves (JA-2 dates an invoice by the job's terms, JA-3 checks a sale against the
// job's limit) need the answer without reading both themselves. A job's own terms are a pointer
// for the same reason they are on the row: a job with no own terms shares the customer's, and null
// is not zero (a job that read as Cash On Demand because nobody filled the terms in would date its
// invoices on issue).
//
// A job that does not exist, or a customer that does not exist under it, is not found. A job whose
// customer carries no terms at all reads zero: the customers table's own default is 30 days, so a
// customer with no terms row has terms, and a zero here would mean the row was read wrong.
func (r *Repository) JobEffectiveTerms(ctx context.Context, customerID, jobID string) (JobTerms, error) {
	if r.pool == nil {
		return JobTerms{}, errNoDB
	}
	var (
		own   *int
		terms int
	)
	err := r.pool.QueryRow(ctx, `
		SELECT j.terms_days, COALESCE(j.terms_days, c.payment_terms_days)
		  FROM customer_jobs j
		  JOIN customers c ON c.id = j.customer_id
		 WHERE j.id = $1 AND j.customer_id = $2`, jobID, customerID).Scan(&own, &terms)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JobTerms{}, ErrNotFound
		}
		return JobTerms{}, fmt.Errorf("customer: read the effective terms of the job %q: %w", jobID, err)
	}
	return JobTerms{TermsDays: terms, Own: own != nil}, nil
}

// JobLimitForCustomer reads a job's own credit limit together with the customer's own limit.
//
// Kept for wave 3 JA-3 (where the POS and order credit gate checks a job-tagged sale against
// min(job limit, customer limit) and sums exposure across the customer's jobs). Both figures are
// read in one statement so the pair cannot be read at two different instants.
func (r *Repository) JobLimitForCustomer(ctx context.Context, customerID, jobID string) (jobLimit *int64, customerLimit int64, err error) {
	if r.pool == nil {
		return nil, 0, errNoDB
	}
	err = r.pool.QueryRow(ctx, `
		SELECT j.credit_limit_cents, c.credit_limit_cents
		  FROM customer_jobs j
		  JOIN customers c ON c.id = j.customer_id
		 WHERE j.id = $1 AND j.customer_id = $2`, jobID, customerID).Scan(&jobLimit, &customerLimit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("customer: read the limits of the job %q: %w", jobID, err)
	}
	return jobLimit, customerLimit, nil
}

// JobStatusForCustomer reads the status of one job under one customer.
//
// It is the read behind JobOpenForCustomer: a closed job is refused as the job of any new
// document, and the refusal is the job's own status rather than a guess. It is scoped to the
// customer on purpose: a job of another customer is not the customer's job at all, and the two
// answers are different (a closed job, and a job that is not there).
func (r *Repository) JobStatusForCustomer(ctx context.Context, customerID, jobID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT status FROM customer_jobs WHERE id = $1 AND customer_id = $2`, jobID, customerID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("customer: read the status of the job %q: %w", jobID, err)
	}
	return status, nil
}
