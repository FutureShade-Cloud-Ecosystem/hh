//go:build integration

package pos

// job_exposure_integration_test.go, wave 3 JA-3: the counter's holds by exposure and by job.
//
// Driven through the real services: the real pos service, the real hold-aware gate reading the
// billing module's one exposure reader, the customer module's real job fence. Every ring is an
// account sale (COLLECT_LATER on ACCOUNT), so each one draws the same figure T on the account.
//
// THE ROW'S EXIT TEST: a sale that fits the job but breaks the customer's limit is held; a sale
// over a job's own limit is held; holds tie to the AR aging.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/pos -run TestIntegration_JobExposure -count=1

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

type allowPosition struct{}

func (allowPosition) AccountPositionAllowed(context.Context, string) bool { return true }

func exposureSvc(t *testing.T, w *jobSaleWorld, pool pgxPoolAlias) *Service {
	t.Helper()
	repo := customer.NewRepository(pool)
	gate := till.NewHoldAwareCreditGate(till.NewRepository(pool)).
		WithExposure(billing.NewExposureReader(pool, repo))
	return realPosSvc(t, pool, stubCharger{}).
		WithJobFence(posJobFenceForTest{svc: customer.NewService(repo).WithJobs(repo)}).
		WithCreditGate(gate).
		WithPositionGate(allowPosition{})
}

func ringOn(t *testing.T, svc *Service, ctx context.Context, w *jobSaleWorld, job string) (Sale, error) {
	t.Helper()
	return svc.Sale(ctx, SaleInput{
		IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
		SaleType: SaleCollectLater, Tender: TenderAccount, JobID: job,
		Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
	})
}

func blockedBy(t *testing.T, err error, scope, words string) ErrCreditBlocked {
	t.Helper()
	var cb ErrCreditBlocked
	if !errors.As(err, &cb) {
		t.Fatalf("want the sale held by the credit gate, got %v", err)
	}
	if cb.LimitScope != scope {
		t.Fatalf("held by the %q limit, want the %q limit (detail %q)", cb.LimitScope, scope, cb.Detail)
	}
	if !strings.Contains(cb.Detail, words) {
		t.Fatalf("the hold's words %q do not name %q", cb.Detail, words)
	}
	return cb
}

func TestIntegration_JobExposure_TheRowsTwoHoldsAndTheTie(t *testing.T) {
	pool := intPool(t)
	w := plantJobSaleWorld(t, pool, "")
	ctx := staffCtx(t, pool, "GLOBAL", w.branchID, kingstonBranchID)
	svc := exposureSvc(t, w, pool)
	bg := context.Background()
	setCustomerLimit := func(cents int64) {
		if _, err := pool.Exec(bg, `UPDATE customers SET credit_limit_cents = $2 WHERE id = $1`, w.customer, cents); err != nil {
			t.Fatalf("set the customer's limit: %v", err)
		}
	}
	setJobLimit := func(job string, cents *int64) {
		if _, err := pool.Exec(bg, `UPDATE customer_jobs SET credit_limit_cents = $2 WHERE id = $1`, job, cents); err != nil {
			t.Fatalf("set the job's limit: %v", err)
		}
	}

	// Ring 1, on the open job, no job limit and a wide customer limit: within both, it passes, and
	// its invoice total is the figure T every later ring draws.
	sale, err := ringOn(t, svc, ctx, w, w.open)
	if err != nil {
		t.Fatalf("a sale within both limits: %v", err)
	}
	var T int64
	if err := pool.QueryRow(bg, `SELECT total_cents FROM invoices WHERE id = $1::uuid`, sale.InvoiceID).Scan(&T); err != nil || T <= 0 {
		t.Fatalf("read the ring's total: %v (%d)", err, T)
	}

	// A sale over the job's OWN limit is held, in words naming the job's limit and the figures,
	// while the customer's limit is wide.
	own := T + T/2
	setJobLimit(w.open, &own)
	_, err = ringOn(t, svc, ctx, w, w.open)
	cb := blockedBy(t, err, "job", "the job's own credit limit")
	for _, fig := range []string{dollarsForTest(T), dollarsForTest(2 * T), dollarsForTest(own)} {
		if !strings.Contains(cb.Detail, fig) {
			t.Fatalf("the job hold's words %q do not carry the figure %s", cb.Detail, fig)
		}
	}

	// A sale that FITS the job but breaks the customer's limit is held, naming the customer's.
	// The customer's limit is 2.5T, the open job's own limit is 2T (it never raises the
	// customer's), and the other job carries one T of its own.
	setCustomerLimit(2*T + T/2)
	two := 2 * T
	setJobLimit(w.open, &two)
	if _, err := ringOn(t, svc, ctx, w, w.termsJobID); err != nil {
		t.Fatalf("the second job's sale fits both limits: %v", err)
	}
	_, err = ringOn(t, svc, ctx, w, w.open) // job: T + T = 2T <= 2T fits; customer: 2T + T = 3T > 2.5T
	cb = blockedBy(t, err, "customer", "the customer's credit limit")
	for _, fig := range []string{dollarsForTest(2 * T), dollarsForTest(T), dollarsForTest(2*T + T/2)} {
		if !strings.Contains(cb.Detail, fig) {
			t.Fatalf("the customer hold's words %q do not carry the figure %s", cb.Detail, fig)
		}
	}

	// A job with no limit of its own reads the customer's: the same breach, from the job that has none.
	_, err = ringOn(t, svc, ctx, w, w.termsJobID)
	blockedBy(t, err, "customer", "the customer's credit limit")
	// And no job at all is held by the customer's limit the same way.
	_, err = ringOn(t, svc, ctx, w, "")
	blockedBy(t, err, "customer", "the customer's credit limit")

	// A job of another customer is refused by the fence, in words, before any limit is read.
	_, err = ringOn(t, svc, ctx, w, w.otherJob)
	if err == nil || !strings.Contains(err.Error(), "another customer's job") {
		t.Fatalf("another customer's job: %v, want the fence's words", err)
	}
	// Branch fenced: a caller whose grants miss the job's branch is refused, not held.
	_, err = ringOn(t, svc, staffCtx(t, pool, "BRANCH", w.branchID), w, w.far)
	if err == nil || !strings.Contains(err.Error(), "outside your reach") {
		t.Fatalf("a job outside the reach: %v, want the fence's words", err)
	}

	// HOLDS TIE TO THE AGING. Two rings stand (T on each job): the exposure the gate reads equals
	// the AR aging's figure for the customer, and the job's own exposure equals its invoices.
	rd := billing.NewExposureReader(pool, customer.NewRepository(pool))
	exp, err := rd.CustomerExposure(bg, nil, w.customer)
	if err != nil {
		t.Fatalf("read the exposure: %v", err)
	}
	rows, err := billing.NewRepository(pool).AgingRowsIn(bg, time.Now(), 60, "", billing.AllBranches)
	if err != nil {
		t.Fatalf("read the aging: %v", err)
	}
	var aging int64
	for _, r := range rows {
		if r.CustomerID == w.customer {
			aging = r.TotalCents
		}
	}
	if exp != 2*T || aging != exp {
		t.Fatalf("exposure %d, aging %d, want both %d (two rings of %d)", exp, aging, 2*T, T)
	}
	_, jobExp, _, err := rd.JobPosition(bg, nil, w.customer, w.open)
	if err != nil || jobExp != T {
		t.Fatalf("the open job's own exposure = %d (%v), want its one invoice of %d", jobExp, err, T)
	}
	// An open order (a converted quote not yet invoiced) is exposure the aging does not yet carry:
	// the exposure is the aging plus the open orders.
	var orderID string
	if err := pool.QueryRow(bg, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents, job_id, org_id)
		VALUES ('JA3-OPEN-' || substr(gen_random_uuid()::text, 1, 8), $1::uuid, $2::uuid, 'CONFIRMED', 7000, $3::uuid,
		        (SELECT org_id FROM locations WHERE id = $2::uuid)) RETURNING id::text`,
		w.customer, w.branchID, w.open).Scan(&orderID); err != nil {
		t.Fatalf("plant the open order: %v", err)
	}
	exp, err = rd.CustomerExposure(bg, nil, w.customer)
	if err != nil || exp != aging+7000 {
		t.Fatalf("exposure with an open order = %d (%v), want the aging %d plus 7000", exp, err, aging)
	}
}

// dollarsForTest renders cents as the gate's words do, "$1,234.56".
func dollarsForTest(cents int64) string {
	whole := fmt.Sprintf("%d", cents/100)
	var out []byte
	for i, c := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return fmt.Sprintf("$%s.%02d", out, cents%100)
}
