//go:build integration

package pos

// job_sale_integration_test.go, the counter's sale takes a job (wave 3 JA-2).
//
// The till picks a job: the ring's order is opened on it, the ring's invoice carries it, and an
// invoice on a job with its own terms is due by THOSE terms on the house day, the same rule the
// billing writer's documents follow. The fence is the customer module's own read, bound through the
// same seam shape the composition root binds, and the three refusals arrive in words.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/pos -run TestIntegration_JobSale -count=1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// pgxPoolAlias names the pool the world planner takes.
type pgxPoolAlias = *pgxpool.Pool

// posJobFenceForTest is the customer module's job read in the counter's shape, the composition
// root's adapter written inline for the leg.
type posJobFenceForTest struct{ svc *customer.Service }

func (f posJobFenceForTest) JobForNewDocument(ctx context.Context, customerID, jobID string) (SaleJob, error) {
	job, err := f.svc.JobForNewDocument(ctx, customerID, jobID)
	if err != nil {
		return SaleJob{}, err
	}
	return SaleJob{TermsDays: job.TermsDays}, nil
}

// jobSaleWorld is one credit customer at Trenton with an OPEN job there, a CLOSED job there, an
// OPEN job at Kingston, and a job under another customer.
type jobSaleWorld struct {
	customer   string
	open, far  string
	closed     string
	otherCust  string
	otherJob   string
	productID  string
	branchID   string
	termsJobID string
}

func plantJobSaleWorld(t *testing.T, pool *pgxpool.Pool, callerID string) *jobSaleWorld {
	t.Helper()
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	w := &jobSaleWorld{}
	w.productID, w.branchID = aPaver(t, pool)
	suffix := fmt.Sprintf("js-%d", time.Now().UnixNano()%1_000_000_000)
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Job Sale Yard', 'CONTRACTOR', $2::uuid, 30, 5000000) RETURNING id::text`,
		"JS-"+suffix, w.branchID).Scan(&w.customer); err != nil {
		t.Fatalf("plant the credit customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Job Sale Other Yard', 'CONTRACTOR', $2::uuid, 30, 5000000) RETURNING id::text`,
		"JSO-"+suffix, w.branchID).Scan(&w.otherCust); err != nil {
		t.Fatalf("plant the other customer: %v", err)
	}
	plant := func(customerID, branchID, jobNo string, terms *int) string {
		j, err := repo.InsertJob(ctx, customer.JobWrite{
			CustomerID: customerID, BranchID: branchID, JobNo: jobNo,
			Fields: customer.JobFields{Name: "Driveway", TermsDays: terms},
		})
		if err != nil {
			t.Fatalf("plant the job %s: %v", jobNo, err)
		}
		return j.JobID
	}
	w.open = plant(w.customer, w.branchID, "J-JS-OPEN", nil)
	w.far = plant(w.customer, kingstonBranchID, "J-JS-FAR", nil)
	w.closed = plant(w.customer, w.branchID, "J-JS-CLOSED", nil)
	if _, err := pool.Exec(ctx, `UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, w.closed); err != nil {
		t.Fatalf("close the closed job: %v", err)
	}
	w.otherJob = plant(w.otherCust, w.branchID, "J-JS-OTHER", nil)
	seven := 7
	w.termsJobID = plant(w.customer, w.branchID, "J-JS-TERMS", &seven)
	// The leg leaves nothing behind (the review of PR 747, P3): the ring's orders, lines, invoices and
	// sale rows, then the jobs, then the customers, in the order the foreign keys allow, and a delete
	// that fails is an error, not a silent leftover for the next package on this database.
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{w.customer, w.otherCust} {
			for _, stmt := range []string{
				`DELETE FROM customer_transactions WHERE customer_id = $1`,
				`DELETE FROM pos_sale WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
				`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`,
				`DELETE FROM invoices WHERE customer_id = $1`,
				`DELETE FROM order_line_tax_basis WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
				`DELETE FROM tax_decision_snapshot WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
				`DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
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

// TestIntegration_JobSale_TheRingTakesAJobAndTheFenceRefusesInWords: the ring's order and its
// invoice carry the job the fence vouched for, and the three refusals are the customer module's
// own words before anything is rung.
func TestIntegration_JobSale_TheRingTakesAJobAndTheFenceRefusesInWords(t *testing.T) {
	pool := intPool(t)
	w := plantJobSaleWorld(t, pool, "")
	ctx := staffCtx(t, pool, "GLOBAL", w.branchID, kingstonBranchID)
	repo := customer.NewRepository(pool)
	svc := realPosSvc(t, pool, stubCharger{}).
		WithJobFence(posJobFenceForTest{svc: customer.NewService(repo).WithJobs(repo)})

	in := SaleInput{
		IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
		SaleType: SaleCollectLater, Tender: TenderAccount, JobID: w.open,
		Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
	}
	sale, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("ring the sale on the job: %v", err)
	}
	q := `SELECT COALESCE(job_id::text, '') FROM %s WHERE id = $1::uuid`
	var orderJob, invoiceJob string
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(q, "orders"), sale.OrderID).Scan(&orderJob); err != nil {
		t.Fatalf("read the ring's order: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(q, "invoices"), sale.InvoiceID).Scan(&invoiceJob); err != nil {
		t.Fatalf("read the ring's invoice: %v", err)
	}
	if orderJob != w.open || invoiceJob != w.open {
		t.Fatalf("the order and the invoice carry the job: order %q invoice %q, want %q", orderJob, invoiceJob, w.open)
	}

	for _, tc := range []struct {
		name  string
		jobID string
		words string
	}{
		{"another customer's job", w.otherJob, "another customer's job"},
		{"a closed job", w.closed, "the job is closed"},
		{"a job outside the reach", w.far, "outside your reach"},
	} {
		ctxScoped := staffCtx(t, pool, "BRANCH", w.branchID)
		_, err := svc.Sale(ctxScoped, SaleInput{
			IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
			SaleType: SaleCollectLater, Tender: TenderAccount, JobID: tc.jobID,
			Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
		})
		if err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Fatalf("%s refused with %v, want words naming %q", tc.name, err, tc.words)
		}
	}
}

// TestIntegration_JobSale_TheTillAnswersAJobRefusalInWords: the same refusals through the REAL
// handler (the review of PR 747, P2-1). The service error is not the answer the cashier sees; the
// status and the sentence are, so each refusal is read off the handler's own response: 400 and the
// customer module's words, and a job id that is a uuid but no job is a 404 in words.
func TestIntegration_JobSale_TheTillAnswersAJobRefusalInWords(t *testing.T) {
	pool := intPool(t)
	w := plantJobSaleWorld(t, pool, "")
	repo := customer.NewRepository(pool)
	svc := realPosSvc(t, pool, stubCharger{}).
		WithJobFence(posJobFenceForTest{svc: customer.NewService(repo).WithJobs(repo)})
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := staffCtx(t, pool, "BRANCH", w.branchID)

	for _, tc := range []struct {
		name   string
		jobID  string
		status int
		words  string
	}{
		{"another customer's job", w.otherJob, http.StatusBadRequest, "that job is another customer's job"},
		{"a closed job", w.closed, http.StatusBadRequest, "the job is closed, so it takes no new document"},
		{"a job outside the reach", w.far, http.StatusBadRequest, "that job sits at a branch outside your reach"},
		{"a uuid that is no job", uuid.NewString(), http.StatusNotFound, "that job was not found"},
	} {
		raw, err := json.Marshal(SaleInput{
			IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
			SaleType: SaleCollectLater, Tender: TenderAccount, JobID: tc.jobID,
			Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", bytes.NewReader(raw)).WithContext(ctx)
		rec := httptest.NewRecorder()
		h.createSale(rec, req, guards.StaffCaller{})
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Error != tc.words {
			t.Fatalf("%s answered %d %q, want %d %q", tc.name, rec.Code, body.Error, tc.status, tc.words)
		}
	}
}
