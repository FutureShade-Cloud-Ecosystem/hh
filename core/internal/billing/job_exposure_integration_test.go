//go:build integration

package billing

// job_exposure_integration_test.go, wave 3 JA-3 item 1: the one reader of exposure.
//
// A customer's exposure sums every job (its running AR balance plus every open order); a job's own
// exposure is its documents only. The legs plant a customer with two jobs, an invoice on each, and
// an open order on one, and read both figures through the reader.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

type exposureWorld struct {
	cust, other     string
	jobA, jobB      string
	otherJob        string
	orderOnA, order string
}

func plantExposureWorld(t *testing.T, pool *pgxpool.Pool) *exposureWorld {
	t.Helper()
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	w := &exposureWorld{}
	tag := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	mk := func(acct string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
			VALUES ($1, 'Exposure Yard', 'CONTRACTOR', $2::uuid, 30, 1000000) RETURNING id::text`,
			acct+tag, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("plant the customer: %v", err)
		}
		return id
	}
	w.cust, w.other = mk("EXP-"), mk("EXPO-")
	job := func(cust, no string, limit *int64) string {
		j, err := repo.InsertJob(ctx, customer.JobWrite{CustomerID: cust, BranchID: trentonBranch, JobNo: no + tag,
			Fields: customer.JobFields{Name: "Site", CreditLimitCents: limit}})
		if err != nil {
			t.Fatalf("plant the job %s: %v", no, err)
		}
		return j.JobID
	}
	lim := int64(300000)
	w.jobA, w.jobB, w.otherJob = job(w.cust, "EXP-A-", &lim), job(w.cust, "EXP-B-", nil), job(w.other, "EXP-O-", nil)
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{w.cust, w.other} {
			for _, stmt := range []string{
				`DELETE FROM customer_transactions WHERE customer_id = $1`,
				`DELETE FROM invoices WHERE customer_id = $1`,
				`DELETE FROM orders WHERE customer_id = $1`,
				`DELETE FROM customer_jobs WHERE customer_id = $1`,
				`DELETE FROM customers WHERE id = $1`,
			} {
				if _, err := pool.Exec(c, stmt, id); err != nil {
					t.Errorf("clean up %q: %v", stmt, err)
				}
			}
		}
	})
	return w
}

func plantInvoice(t *testing.T, pool *pgxpool.Pool, cust, job, no string, cents int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, job_id)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', current_date, current_date + 30, $4, 0, $4, $4, now(), $5::uuid)`,
		no, cust, trentonBranch, cents, expNullUUID(job)); err != nil {
		t.Fatalf("plant the invoice %s: %v", no, err)
	}
}

func plantLedger(t *testing.T, pool *pgxpool.Pool, cust string, amount, after int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, occurred_at)
		VALUES ($1::uuid, 'INVOICE', $2, $3, 'invoice', gen_random_uuid(), $4::uuid, clock_timestamp())`,
		cust, amount, after, trentonBranch); err != nil {
		t.Fatalf("plant the ledger row: %v", err)
	}
}

func plantOrder(t *testing.T, pool *pgxpool.Pool, cust, job, no, status string, cents int64) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents, job_id, org_id)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid))
		RETURNING id::text`, no, cust, trentonBranch, status, cents, expNullUUID(job)).Scan(&id); err != nil {
		t.Fatalf("plant the order %s: %v", no, err)
	}
	return id
}

func expNullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestIntegration_Exposure_TheCustomerSumsEveryJobAndTheJobReadsItsOwn(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	rd := NewExposureReader(pool, repo)
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)

	plantInvoice(t, pool, w.cust, w.jobA, "EXP-IA-"+tag, 100000)
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-IB-"+tag, 70000)
	plantInvoice(t, pool, w.cust, "", "EXP-IN-"+tag, 5000) // the account's own document, no job
	plantLedger(t, pool, w.cust, 175000, 175000)
	w.orderOnA = plantOrder(t, pool, w.cust, w.jobA, "EXP-OA-"+tag, "DRAFT", 20000)
	// not open: cancelled, and one that already has an invoice (counted in AR, never twice)
	plantOrder(t, pool, w.cust, w.jobA, "EXP-OC-"+tag, "CANCELLED", 999)
	invoiced := plantOrder(t, pool, w.cust, w.jobB, "EXP-OI-"+tag, "CONFIRMED", 70000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid WHERE invoice_no = $2`, invoiced, "EXP-IB-"+tag); err != nil {
		t.Fatalf("link the invoice to its order: %v", err)
	}

	got, err := rd.CustomerExposure(ctx, nil, w.cust)
	if err != nil {
		t.Fatalf("customer exposure: %v", err)
	}
	if got != 175000+20000 {
		t.Fatalf("customer exposure = %d, want the ledger 175000 plus the one open order 20000", got)
	}
	a, err := rd.JobPosition(ctx, nil, w.cust, w.jobA)
	if err != nil {
		t.Fatalf("job A position: %v", err)
	}
	if a.ExposureCents != 100000+20000 || a.OwnLimitCents == nil || *a.OwnLimitCents != 300000 || a.CustomerLimitCents != 1000000 {
		t.Fatalf("job A position = %+v, want exposure 120000, own limit 300000, customer limit 1000000", a)
	}
	b, err := rd.JobPosition(ctx, nil, w.cust, w.jobB)
	if err != nil {
		t.Fatalf("job B position: %v", err)
	}
	if b.ExposureCents != 70000 || b.OwnLimitCents != nil {
		t.Fatalf("job B position = %+v, want exposure 70000 and no own limit (it reads the customer's)", b)
	}
	// a job of another customer is not found, never an empty position
	if _, err := rd.JobPosition(ctx, nil, w.cust, w.otherJob); err == nil {
		t.Fatal("another customer's job read as a position, want not found")
	}
}
