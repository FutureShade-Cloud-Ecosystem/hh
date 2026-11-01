package billing

// job_exposure.go, wave 3 JA-3: the one reader of what a customer, and one of its jobs, owes and
// has ordered.
//
// THE RULE (FIN-WAVE3-PLAN 1.1): a job shares the customer's limit unless it carries its own; the
// customer's exposure includes EVERY job, and a job limit never raises it. So there are two figures
// and every credit gate reads both from here, never from its own SQL:
//
//   - the customer's exposure: open AR (the running balance the gates have always read, the latest
//     customer_transactions balance_after, which every job's documents move) PLUS open orders;
//   - a job's own exposure: the open balance of the job's invoices PLUS the job's open orders.
//
// An OPEN ORDER is an order that has been written and not yet invoiced in full: not cancelled, not
// closed, and a DRAFT only when it is a converted quote's. It counts what has been committed and
// not yet invoiced (its total less its live invoices), so a converted quote counts the moment it
// is converted, a progress invoice moves its billed part into AR and leaves the rest here, and
// nothing is counted twice.
//
// The job's own limit is the customer module's fact; it arrives through the JobLimits seam so this
// module reads no customer_jobs row of its own.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rowQuerier is the one read the exposure SQL needs. A pool satisfies it, and so does a
// transaction: a gate consulted inside a write transaction passes that transaction, so the read
// sees the same snapshot and asks the pool for no second connection.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// querier picks the caller's transaction when one is given and the pool otherwise.
func (e *ExposureReader) querier(tx pgx.Tx) (rowQuerier, error) {
	if tx != nil {
		return tx, nil
	}
	if e.pool == nil {
		return nil, errNoDB
	}
	return e.pool, nil
}

// JobLimits is the customer module's read of a job's own credit limit beside the customer's.
// *customer.Repository satisfies it (JobLimitForCustomer).
type JobLimits interface {
	JobLimitForCustomer(ctx context.Context, customerID, jobID string) (jobLimit *int64, customerLimit int64, err error)
}

// ExposureReader reads exposure. The zero value is unusable; build it with NewExposureReader.
type ExposureReader struct {
	pool *pgxpool.Pool
	jobs JobLimits
}

// NewExposureReader builds the reader over the pool and the customer module's job limits seam.
func NewExposureReader(pool *pgxpool.Pool, jobs JobLimits) *ExposureReader {
	return &ExposureReader{pool: pool, jobs: jobs}
}

// openOrdersPredicate selects the orders that count as open for alias o: committed and not
// cancelled, invoiced in full or closed. A DRAFT counts only when it is a converted quote's order
// (quote_id set, the order a conversion writes before anything is confirmed); an untouched reorder
// or duplicate draft someone opened and abandoned is not a commitment.
const openOrdersPredicate = `o.status NOT IN ('CANCELLED','INVOICED','CLOSED')
	   AND (o.status <> 'DRAFT' OR o.quote_id IS NOT NULL)`

// openOrderCents is the ONE open order term: what an open order has committed the customer to
// beyond what has been invoiced on it. An order's total less its non void, non credit memo
// invoices, floored at zero, so a progress invoice moves its billed part into AR and leaves the
// unbilled remainder here, and nothing is counted twice. Both figures read this term; a ruling to
// count AR only is a change to this constant alone.
const openOrderCents = `GREATEST(o.total_cents - COALESCE((SELECT SUM(x.total_cents) FROM invoices x
	                  WHERE x.order_id = o.id AND x.status <> 'VOID' AND x.doc_type <> 'CREDIT_MEMO'), 0), 0)`

// CustomerExposure is the customer's whole exposure: the running AR balance (every job included,
// since every job's invoices post to the one ledger) plus every open order.
func (e *ExposureReader) CustomerExposure(ctx context.Context, tx pgx.Tx, customerID string) (int64, error) {
	q, err := e.querier(tx)
	if err != nil {
		return 0, err
	}
	var open, orders int64
	err = q.QueryRow(ctx, `
		SELECT COALESCE((SELECT t.balance_after FROM customer_transactions t
		                  WHERE t.customer_id = $1::uuid ORDER BY t.occurred_at DESC LIMIT 1), 0),
		       COALESCE((SELECT SUM(`+openOrderCents+`) FROM orders o
		                  WHERE o.customer_id = $1::uuid AND `+openOrdersPredicate+`), 0)`,
		customerID).Scan(&open, &orders)
	if err != nil {
		return 0, fmt.Errorf("billing: read the exposure of customer %q: %w", customerID, err)
	}
	return open + orders, nil
}

// JobPosition reads a job's own position for one customer: the job's OWN credit limit (nil when it
// shares the customer's), the job's own exposure (its open invoice balances plus its open orders),
// and the customer's limit it is held under, which a job never raises. A job that is not the
// customer's reads as not found, never as an empty position.
func (e *ExposureReader) JobPosition(ctx context.Context, tx pgx.Tx, customerID, jobID string) (ownLimit *int64, exposure, customerLimit int64, err error) {
	if e.jobs == nil {
		return nil, 0, 0, errors.New("billing: the exposure reader has no job limits bound")
	}
	q, err := e.querier(tx)
	if err != nil {
		return nil, 0, 0, err
	}
	ownLimit, customerLimit, err = e.jobs.JobLimitForCustomer(ctx, customerID, jobID)
	if err != nil {
		return nil, 0, 0, err
	}
	var open, orders int64
	err = q.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(i.balance_cents) FROM invoices i
		                  WHERE i.customer_id = $1::uuid AND i.job_id = $2::uuid
		                    AND i.status IN ('FINALIZED','PARTIALLY_PAID')), 0),
		       COALESCE((SELECT SUM(`+openOrderCents+`) FROM orders o
		                  WHERE o.customer_id = $1::uuid AND o.job_id = $2::uuid AND `+openOrdersPredicate+`), 0)`,
		customerID, jobID).Scan(&open, &orders)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("billing: read the exposure of job %q: %w", jobID, err)
	}
	return ownLimit, open + orders, customerLimit, nil
}
