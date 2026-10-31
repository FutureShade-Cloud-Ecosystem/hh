//go:build integration

package quote

// job_words_integration_test.go, the quote desk answers a job refusal in words (the review of
// PR 747, P3-1). The leg drives the REAL create handler and reads the status and the sentence off
// its response: 400 for the three mistakes the fence names, 404 in words for a job id that is a
// uuid but no job.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/quote -run TestIntegration_QuoteJobWords -count=1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// wordsJobFence is the customer module's job read in the quote desk's shape, the composition
// root's adapter written inline.
type wordsJobFence struct{ svc *customer.Service }

func (f wordsJobFence) JobForNewDocument(ctx context.Context, tx pgx.Tx, customerID, jobID string) error {
	_, err := f.svc.JobForNewDocument(customer.WithCallerTx(ctx, tx), customerID, jobID)
	return err
}

func TestIntegration_QuoteJobWords_TheDeskAnswersAJobRefusalInWords(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	tag := fmt.Sprintf("QJW-%d", time.Now().UnixNano()%1_000_000_000)
	var cust, other string
	for i, dest := range []*string{&cust, &other} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, credit_limit_cents, payment_terms_days)
			VALUES ($1, 'Quote Job Words Yard', 'CONTRACTOR', 1000000, 30) RETURNING id::text`,
			fmt.Sprintf("%s-%d", tag, i)).Scan(dest); err != nil {
			t.Fatalf("plant the customer: %v", err)
		}
	}
	plant := func(customerID, branchID, no string, closed bool) string {
		j, err := repo.InsertJob(ctx, customer.JobWrite{
			CustomerID: customerID, BranchID: branchID, JobNo: no, Fields: customer.JobFields{Name: "Driveway"},
		})
		if err != nil {
			t.Fatalf("plant the job %s: %v", no, err)
		}
		if closed {
			if _, err := pool.Exec(ctx, `UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, j.JobID); err != nil {
				t.Fatalf("close the job: %v", err)
			}
		}
		return j.JobID
	}
	closedJob := plant(cust, intTrenton, "J-QJW-CLOSED", true)
	farJob := plant(cust, intKingston, "J-QJW-FAR", false)
	foreignJob := plant(other, intTrenton, "J-QJW-OTHER", false)
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{cust, other} {
			_, _ = pool.Exec(c, `DELETE FROM customer_jobs WHERE customer_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, id)
		}
	})

	svc := NewStaffService(NewRepository(pool)).WithJobFence(wordsJobFence{svc: customer.NewService(repo).WithJobs(repo)})
	h := NewStaffHandler(svc)
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	rctx := identity.ContextWithStaff(ctx, identity.StaffContext{
		UserID: userID, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{intTrenton: true},
	})
	for _, tc := range []struct {
		name   string
		jobID  string
		status int
		words  string
	}{
		{"another customer's job", foreignJob, http.StatusBadRequest, "that job is another customer's job"},
		{"a closed job", closedJob, http.StatusBadRequest, "the job is closed, so it takes no new document"},
		{"a job outside the reach", farJob, http.StatusBadRequest, "that job sits at a branch outside your reach"},
		{"a uuid that is no job", uuid.NewString(), http.StatusNotFound, "that job was not found"},
	} {
		raw, _ := json.Marshal(map[string]string{"customer_id": cust, "branch_id": intTrenton, "job_id": tc.jobID})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewReader(raw)).WithContext(rctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.createQuote(rec, req, guards.StaffCaller{})
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Error != tc.words {
			t.Fatalf("%s answered %d %q, want %d %q", tc.name, rec.Code, body.Error, tc.status, tc.words)
		}
	}
}
