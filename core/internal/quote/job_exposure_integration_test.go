//go:build integration

package quote

// job_exposure_integration_test.go, wave 3 JA-3: the quote conversion's holds by job.
//
// The conversion consults the same hold-aware gate the counter does, reading the same exposure
// reader. A conversion on a job over the job's own limit is held, in words naming the job's limit; a
// conversion that fits the job but breaks the customer's limit over the customer's whole exposure
// (open orders included, since a converted quote is committed money no invoice carries yet) is held
// naming the customer's.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/quote -run TestIntegration_JobExposure -count=1

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

func exposureGatedRepo(t *testing.T, pool *pgxpool.Pool) *Repository {
	t.Helper()
	crepo := customer.NewRepository(pool)
	gate := till.NewHoldAwareCreditGate(till.NewRepository(pool)).
		WithExposure(billing.NewExposureReader(pool, crepo))
	return NewRepository(pool).WithCreditGate(gate)
}

func TestIntegration_JobExposure_ConversionIsHeldByTheJobAndByTheCustomer(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	cust, quoteID, _, _ := seedQuote(t, pool, "ACCEPTED", nil)
	cleanupConvertedOrders(t, pool, quoteID)
	setCreditLimit(t, pool, cust, 1000000)
	crepo := customer.NewRepository(pool)
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)
	ownLimit := int64(1500) // the quote totals 2000 cents
	limited, err := crepo.InsertJob(ctx, customer.JobWrite{CustomerID: cust, BranchID: intTrenton, JobNo: "J-JE-L-" + tag,
		Fields: customer.JobFields{Name: "Limited", CreditLimitCents: &ownLimit}})
	if err != nil {
		t.Fatalf("plant the limited job: %v", err)
	}
	plain, err := crepo.InsertJob(ctx, customer.JobWrite{CustomerID: cust, BranchID: intTrenton, JobNo: "J-JE-P-" + tag,
		Fields: customer.JobFields{Name: "Plain"}})
	if err != nil {
		t.Fatalf("plant the plain job: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`,
			`DELETE FROM orders WHERE customer_id = $1`,
			`DELETE FROM customer_jobs WHERE customer_id = $1`,
		} {
			if _, err := pool.Exec(context.Background(), stmt, cust); err != nil {
				t.Errorf("clean up %q: %v", stmt, err)
			}
		}
	})
	repo := exposureGatedRepo(t, pool)

	// Over the job's own limit: held, naming the job's limit; nothing written.
	_, err = repo.Convert(ctx, cust, quoteID, ConvertOptions{JobID: limited.JobID})
	var blocked ErrCreditBlocked
	if !errors.As(err, &blocked) || blocked.Blocked == nil || blocked.Blocked.Data["limit_scope"] != till.LimitScopeJob {
		t.Fatalf("a conversion over the job's own limit: %v, want held by the job's limit", err)
	}
	msg := CreditRefusalMessage(blocked)
	if !strings.Contains(msg, "the job's own") || !strings.Contains(msg, "$15.00") || !strings.Contains(msg, "$20.00") {
		t.Fatalf("the job hold's words %q do not name the job's limit and the figures", msg)
	}
	if n := ordersForQuote(t, pool, quoteID); n != 0 {
		t.Fatalf("a held conversion wrote %d orders", n)
	}

	// Fits the job (no limit of its own) but breaks the customer's limit over the customer's whole
	// exposure: another job already has an open order of 9000 cents, the customer's limit is 10000.
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents, job_id, org_id)
		VALUES ('JE-OPEN-' || $3, $1::uuid, $2::uuid, 'DRAFT', 9000, $4::uuid, (SELECT org_id FROM locations WHERE id = $2::uuid))`,
		cust, intTrenton, tag, limited.JobID); err != nil {
		t.Fatalf("plant the other job's open order: %v", err)
	}
	setCreditLimit(t, pool, cust, 10000)
	_, err = repo.Convert(ctx, cust, quoteID, ConvertOptions{JobID: plain.JobID})
	if !errors.As(err, &blocked) || blocked.Blocked == nil || blocked.Blocked.Data["limit_scope"] != till.LimitScopeCustomer {
		t.Fatalf("a conversion over the customer's whole exposure: %v, want held by the customer's limit", err)
	}
	msg = CreditRefusalMessage(blocked)
	if !strings.Contains(msg, "$90.00") || !strings.Contains(msg, "$100.00") {
		t.Fatalf("the customer hold's words %q do not carry the exposure and the limit", msg)
	}

	// Within both: the customer's limit lifted, the plain job converts.
	setCreditLimit(t, pool, cust, 1000000)
	if _, err := repo.Convert(ctx, cust, quoteID, ConvertOptions{JobID: plain.JobID}); err != nil {
		t.Fatalf("a conversion within both limits: %v", err)
	}
}
