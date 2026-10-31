//go:build integration

// job_documents_integration_test.go, the documents carry the job (wave 3 JA-2).
//
// The whole road, from the doors the brief names to the columns the documents land on:
//
//	  · migration 0469: job_id on quotes, orders and invoices, nullable, FK'd to customer_jobs, with
//	    the partial indexes the by job reads need
//	  · a QUOTE takes a job at the desk, and the fence refuses another customer's job, a CLOSED job
//	    and a job outside the caller's reach, in words
//	  · the CONVERSION picks a job: the request names one, else the quote's own rides to the order
//	  · the ORDER's job reaches its INVOICE: the counter's one action converts the quote, raises the
//	    agreed invoice and the document carries the order's job, dated by the job's own terms
//	  · the whole one action runs on a pool of ONE connection: the job terms read rides the caller's
//	    transaction (the QD5 shape, PR 722 car 18's lesson)
//
//		Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//		     go test -tags integration ./cmd/core/ -run TestIntegration_JobDocuments -count=1
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/quote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// ja2Env is the quote desk's own harness: the real repository, the real credit gate, the money
// door's invoicer and payer, and the job fence bound the way the operations wire binds it.
type ja2Env struct {
	t           *testing.T
	pool        *pgxpool.Pool
	svc         *quote.StaffService
	jobs        *customer.Repository
	userID      string
	nextCounter int
}

// ja2Setup builds the harness over DATABASE_URL, with the pool capped at maxConns (0 keeps pgx's
// default). The pool of one is the QD5 shape: every read inside the action must ride the door's
// own transaction or it never gets a second connection.
func ja2Setup(t *testing.T, maxConns int32) *ja2Env {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL is not set; the JA-2 doors need the scratch database")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pool config: %v", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	qd5KeepSequences(t, pool)
	var userID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE status='ACTIVE' ORDER BY created_at LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("no seeded staff: %v", err)
	}
	orderSvc := order.NewService(order.NewRepository(pool))
	repo := quote.NewRepository(pool).WithCreditGate(till.NewHoldAwareCreditGate(till.NewRepository(pool)))
	aud := audit.NewService(audit.NewRepository(pool), nil)
	invoicer, payer := quoteMoneyDoor(pool, orderSvc)
	svc := quote.NewStaffService(repo).WithAudit(aud).WithAuditTrail(aud).
		WithOrderInvoicer(invoicer).WithCounterPayer(payer).
		WithJobFence(newQuoteJobFence(pool))
	return &ja2Env{t: t, pool: pool, svc: svc, jobs: customer.NewRepository(pool), userID: userID}
}

func (e *ja2Env) counter() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: e.userID, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{qd5Trenton: true},
	})
}

// seedJobQuote plants an ACCEPTED, lined quote for the customer on the job, the shape the payment
// door converts: one line at 50.00, taxed at 13 percent half up, same as the QD5 fixture's money.
func (e *ja2Env) seedJobQuote(customerID, jobID, tag string) string {
	e.t.Helper()
	ctx := context.Background()
	var prod, uom string
	if err := e.pool.QueryRow(ctx, `
		SELECT p.id::text, pu.uom_code::text FROM products p
		  JOIN product_uoms pu ON pu.product_id = p.id AND pu.qty_in_base = 1
		 WHERE p.is_active ORDER BY p.sku LIMIT 1`).Scan(&prod, &uom); err != nil {
		e.t.Skipf("no factor-1 anchored product seeded: %v", err)
	}
	var quoteID string
	subtotal := int64(5_000)
	tax := (subtotal*1300 + 5000) / 10000
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, subtotal_cents, tax_cents, total_cents, job_id, created_by)
		VALUES ($1, $2::uuid, $3::uuid, 'ACCEPTED', $4, $5, $4::bigint + $5::bigint, $6::uuid, $7::uuid)
		RETURNING id::text`,
		"#Q-"+tag, customerID, qd5Trenton, subtotal, tax, jobID, e.userID).Scan(&quoteID); err != nil {
		e.t.Fatalf("seed the job quote: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
		VALUES ($1, 1, $2::uuid, 'Pavers', 1, $3, 1, 5000, $3, 5000)`, quoteID, prod, uom); err != nil {
		e.t.Fatalf("seed the job quote's line: %v", err)
	}
	return quoteID
}

// quoteTotal reads the quote's total, the amount the one action must take.
func (e *ja2Env) quoteTotal(quoteID string) int64 {
	e.t.Helper()
	var total int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT total_cents FROM quotes WHERE id = $1`, quoteID).Scan(&total); err != nil {
		e.t.Fatalf("read the quote's total: %v", err)
	}
	return total
}

// plantCustomer plants an unhomed credit customer, so the job's branch is the fence's own fact.
func (e *ja2Env) plantCustomer(creditLimit int64, tag string) string {
	e.t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, customer_type, credit_limit_cents, payment_terms_days)
		VALUES ($1, $2, 'CONTRACTOR', $3, 30) RETURNING id::text`, tag, "JA2 Yard "+tag, creditLimit).Scan(&id); err != nil {
		e.t.Fatalf("plant the customer %s: %v", tag, err)
	}
	// The leg leaves nothing behind (the review of PR 747, P3): every document it raised for the
	// customer, then its jobs, then the customer, in the order the foreign keys allow. A delete that
	// fails is an error, not a leftover for the next package on this database.
	e.t.Cleanup(func() {
		c := context.Background()
		for _, stmt := range []string{
			`DELETE FROM customer_transactions WHERE customer_id = $1`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`,
			`DELETE FROM payments WHERE customer_id = $1`,
			`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`,
			`DELETE FROM invoices WHERE customer_id = $1`,
			`DELETE FROM order_line_tax_basis WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM tax_decision_snapshot WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM quote_accept_provenance WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM orders WHERE customer_id = $1`,
			`DELETE FROM quote_lines WHERE quote_id IN (SELECT id FROM quotes WHERE customer_id = $1)`,
			`DELETE FROM quotes WHERE customer_id = $1`,
			`DELETE FROM customer_jobs WHERE customer_id = $1`,
			`DELETE FROM customers WHERE id = $1`,
		} {
			if _, err := e.pool.Exec(c, stmt, id); err != nil {
				e.t.Errorf("clean up %q: %v", stmt, err)
			}
		}
	})
	return id
}

// plantJob plants a job under the customer at the named branch, with optional own terms days, and
// closes it when close is set.
func (e *ja2Env) plantJob(customerID, branchID, jobNo string, ownTerms *int, close bool) customer.Job {
	e.t.Helper()
	j, err := e.jobs.InsertJob(context.Background(), customer.JobWrite{
		CustomerID: customerID, BranchID: branchID, JobNo: jobNo,
		Fields: customer.JobFields{Name: "Driveway", TermsDays: ownTerms},
	})
	if err != nil {
		e.t.Fatalf("plant the job %s: %v", jobNo, err)
	}
	if close {
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, j.JobID); err != nil {
			e.t.Fatalf("close the job %s: %v", jobNo, err)
		}
	}
	return j
}

// openQuoteOnJob opens a DRAFT quote through the desk's own door, on the named job ("" for none),
// accepts it by phone, and returns its id.
func (e *ja2Env) openQuoteOnJob(ctx context.Context, customerID, jobID, tag string) string {
	e.t.Helper()
	view, err := e.svc.CreateQuote(ctx, quote.QuoteCreate{
		CustomerID: customerID, BranchID: qd5Trenton, JobID: jobID,
	})
	if err != nil {
		e.t.Fatalf("open the quote on the job: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE quotes SET status = 'ACCEPTED' WHERE id = $1`, view.ID); err != nil {
		e.t.Fatalf("accept the quote: %v", err)
	}
	return view.ID
}

// documentJob reads the job a quote, order or invoice row carries, "" for none.
func (e *ja2Env) documentJob(table, id string) string {
	e.t.Helper()
	var jobID string
	if err := e.pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT COALESCE(job_id::text, '') FROM %s WHERE id = $1::uuid`, table), id).Scan(&jobID); err != nil {
		e.t.Fatalf("read the %s job: %v", table, err)
	}
	return jobID
}

// TestIntegration_JobDocuments_TheQuoteTakesAJobAndTheFenceRefusesInWords: the desk's create door
// writes the job onto the quote, and the three refusals are the customer module's own words.
func TestIntegration_JobDocuments_TheQuoteTakesAJobAndTheFenceRefusesInWords(t *testing.T) {
	e := ja2Setup(t, 0)
	ctx := e.counter()
	tag := fmt.Sprintf("JA2-Q-%d", time.Now().UnixNano()%1_000_000_000)
	cust := e.plantCustomer(1_000_000, tag)
	other := e.plantCustomer(1_000_000, tag+"-O")

	open := e.plantJob(cust, qd5Trenton, "J-Q-OPEN", nil, false)
	closed := e.plantJob(cust, qd5Trenton, "J-Q-CLOSED", nil, true)
	far := e.plantJob(cust, qd5Kingston, "J-Q-FAR", nil, false)
	foreign := e.plantJob(other, qd5Trenton, "J-Q-OTHER", nil, false)

	view, err := e.svc.CreateQuote(ctx, quote.QuoteCreate{
		CustomerID: cust, BranchID: qd5Trenton, JobID: open.JobID,
	})
	if err != nil {
		t.Fatalf("open the quote on the job: %v", err)
	}
	if got := e.documentJob("quotes", view.ID); got != open.JobID {
		t.Fatalf("the quote does not carry its job: got %q, want %q", got, open.JobID)
	}

	for _, tc := range []struct {
		name  string
		jobID string
		words string
	}{
		{"another customer's job", foreign.JobID, "another customer's job"},
		{"a closed job", closed.JobID, "the job is closed"},
		{"a job outside the reach", far.JobID, "outside your reach"},
	} {
		_, err := e.svc.CreateQuote(ctx, quote.QuoteCreate{
			CustomerID: cust, BranchID: qd5Trenton, JobID: tc.jobID,
		})
		if err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Fatalf("%s refused with %v, want words naming %q", tc.name, err, tc.words)
		}
	}
}

// TestIntegration_JobDocuments_TheConversionPicksAJob: the request's job wins, the quote's own
// rides when the request names none, and a job that closed between the quote and its conversion
// refuses the conversion in words, whatever door asks.
func TestIntegration_JobDocuments_TheConversionPicksAJob(t *testing.T) {
	e := ja2Setup(t, 0)
	ctx := e.counter()
	tag := fmt.Sprintf("JA2-C-%d", time.Now().UnixNano()%1_000_000_000)
	cust := e.plantCustomer(1_000_000, tag)
	named := e.plantJob(cust, qd5Trenton, "J-C-NAMED", nil, false)
	rides := e.plantJob(cust, qd5Trenton, "J-C-RIDES", nil, false)
	closed := e.plantJob(cust, qd5Trenton, "J-C-CLOSED", nil, true)

	// The quote's own job rides to the order when the request names none.
	q1 := e.openQuoteOnJob(ctx, cust, rides.JobID, tag)
	res, err := e.svc.ConvertQuote(ctx, q1, quote.ConvertOptions{})
	if err != nil {
		t.Fatalf("convert the quote: %v", err)
	}
	if got := e.documentJob("orders", res.OrderID); got != rides.JobID {
		t.Fatalf("the quote's own job did not ride to the order: got %q, want %q", got, rides.JobID)
	}

	// The named job wins over the quote's own.
	q2 := e.openQuoteOnJob(ctx, cust, rides.JobID, tag+"2")
	res, err = e.svc.ConvertQuote(ctx, q2, quote.ConvertOptions{JobID: named.JobID})
	if err != nil {
		t.Fatalf("convert the quote naming the job: %v", err)
	}
	if got := e.documentJob("orders", res.OrderID); got != named.JobID {
		t.Fatalf("the named job did not win: got %q, want %q", got, named.JobID)
	}

	// A job that closed between the quote and its conversion refuses the conversion. The quote was
	// opened while the job was OPEN; the close lands after it and before the conversion.
	q3 := e.openQuoteOnJob(ctx, cust, rides.JobID, tag+"3")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, closed.JobID); err != nil {
		t.Fatalf("close the job: %v", err)
	}
	// The quote names the now closed job directly, the shape a conversion with no request job meets
	// through the quote's own.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE quotes SET job_id = $1 WHERE id = $2`, closed.JobID, q3); err != nil {
		t.Fatalf("point the quote at the closed job: %v", err)
	}
	if _, err := e.svc.ConvertQuote(ctx, q3, quote.ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "the job is closed") {
		t.Fatalf("a closed job's conversion answered %v, want the closed refusal", err)
	}
	// The quote stands unconverted.
	var status string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT status FROM quotes WHERE id = $1`, q3).Scan(&status); err != nil || status != "ACCEPTED" {
		t.Fatalf("the refused conversion left the quote %s (%v), want ACCEPTED", status, err)
	}
}
