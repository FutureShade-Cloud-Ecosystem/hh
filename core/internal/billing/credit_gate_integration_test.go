//go:build integration

// credit_gate_integration_test.go, DIB-65 / DIB-52 against the real database.
//
// The unit tests argue with the RULE; these prove the two things only a database can: that the aging
// buckets are computed from the right column, and that a hold placed by billing lands in the exact
// table the POS ring and the convert gate already read (0035 credit_holds), with its keystone
// invariant intact.
//
// DIB-396 note: these fixtures break no whole-DB invariant at any point, they create and destroy
// their own customers, invoices and holds, so neither the shared nor the exclusive advisory lease is
// taken. They are order-independent under -shuffle=on: every row is namespaced to the test.
package billing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
)

// creditRig owns one customer plus whatever invoices a test asks for, and unwinds all of it.
type creditRig struct {
	repo       *Repository
	svc        *CreditService
	customerID string
	branchID   string
	orderID    string
}

func newCreditRig(t *testing.T, termsDays int, creditLimit int64) *creditRig {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)

	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		 VALUES ('CRED-'||$1, 'Credit Gate '||$1, 'CONTRACTOR', $2::uuid, $3, $4) RETURNING id::text`,
		suffix, branchID, termsDays, creditLimit).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var orderID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		 VALUES ('ORD-CRED-'||$1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
		suffix, customerID, branchID).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	repo := NewRepository(pool)
	return &creditRig{repo: repo, svc: NewCreditService(repo), customerID: customerID, branchID: branchID, orderID: orderID}
}

// invoiceDueDaysAgo seeds a FINALIZED invoice whose due date is `daysAgo` days in the past (negative
// = due in the future). linkOrder attaches it to the rig's order so the dispatch gate can see it.
func (r *creditRig) invoiceDueDaysAgo(t *testing.T, tag string, cents int64, daysAgo int, linkOrder bool) string {
	t.Helper()
	pool := intPool(t)
	var order any
	if linkOrder {
		order = r.orderID
	}
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, order_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ('INV-CRED-'||$1, $2::uuid, $3::uuid, $6::uuid, 'FINALIZED',
		         CURRENT_DATE - $4::int - 30, CURRENT_DATE - $4::int, $5, 0, $5, $5, now())
		 RETURNING id::text`,
		fmt.Sprintf("%s-%d", tag, time.Now().UnixNano()%1_000_000_000),
		r.customerID, r.branchID, daysAgo, cents, order).Scan(&id); err != nil {
		t.Fatalf("seed invoice %s: %v", tag, err)
	}
	return id
}

// findRow picks this rig's customer out of the aging worklist.
func findRow(rows []AgingRow, customerID string) (AgingRow, bool) {
	for _, r := range rows {
		if r.CustomerID == customerID {
			return r, true
		}
	}
	return AgingRow{}, false
}

// TestIntegration_Credit_AgingBucketsFromDueDate pins the choice that decides whether the whole
// policy is right or a month out: buckets age from DUE date, not issue date. On net-30 terms those
// differ by a month, and a policy that confuses them holds accounts that are perfectly current.
func TestIntegration_Credit_AgingBucketsFromDueDate(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 10_000_00)

	rig.invoiceDueDaysAgo(t, "CURRENT", 10000, -5, false) // due in 5 days → CURRENT
	rig.invoiceDueDaysAgo(t, "D15", 20000, 15, false)     // 15 days late   → 1-30
	rig.invoiceDueDaysAgo(t, "D45", 30000, 45, false)     // 45 days late   → 31-60
	rig.invoiceDueDaysAgo(t, "D95", 40000, 95, false)     // 95 days late   → 90+

	rows, err := rig.repo.AgingRows(ctx, time.Now(), 60, "")
	if err != nil {
		t.Fatalf("aging rows: %v", err)
	}
	got, ok := findRow(rows, rig.customerID)
	if !ok {
		t.Fatal("the account with an open balance is missing from the aging worklist")
	}
	if got.CurrentCents != 10000 {
		t.Fatalf("CURRENT = %d, want 10000, an invoice not yet due is not aged", got.CurrentCents)
	}
	if got.Days30Cents != 20000 || got.Days60Cents != 30000 || got.Days90PlusCents != 40000 {
		t.Fatalf("buckets = 30:%d 60:%d 90+:%d, want 20000/30000/40000",
			got.Days30Cents, got.Days60Cents, got.Days90PlusCents)
	}
	if got.TotalCents != 100000 {
		t.Fatalf("total = %d, want 100000", got.TotalCents)
	}
	// The buckets must sum to the total. A bucketing bug that loses a dollar is exactly the kind of
	// thing a statement customer finds before we do.
	if sum := got.CurrentCents + got.Days30Cents + got.Days60Cents + got.Days60To90Cents + got.Days90PlusCents; sum != got.TotalCents {
		t.Fatalf("buckets sum to %d but the total is %d", sum, got.TotalCents)
	}
	// PastBucketCents is what the 60-day policy trigger reads: the 45- and 95-day invoices.
	if got.PastBucketCents != 40000 {
		t.Fatalf("past-60 balance = %d, want 40000 (only the 95-day invoice is more than 60 days late)", got.PastBucketCents)
	}
}

// TestIntegration_Credit_AutoHoldLandsInTheTableThePOSAlreadyReads is the design claim, made
// executable: billing places the hold, and it appears in credit_holds with the shape till's
// HoldAwareCreditGate looks for, which is why the POS ring and convert honour it with no change.
func TestIntegration_Credit_AutoHoldLandsInTheTableThePOSAlreadyReads(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 50000) // $500 limit
	rig.invoiceDueDaysAgo(t, "OVER", 150000, 5, false)

	id, placed, err := rig.repo.PlaceHold(ctx, rig.customerID, HoldCreditLimit, "open AR exceeds the limit", "")
	if err != nil || !placed {
		t.Fatalf("place hold: id=%s placed=%v err=%v", id, placed, err)
	}

	// This is the EXACT predicate till.HoldAwareCreditGate evaluates (internal/till/holds.go).
	var posSees bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_holds WHERE customer_id = $1 AND status = 'ACTIVE')`,
		rig.customerID).Scan(&posSees); err != nil {
		t.Fatalf("read the POS gate's predicate: %v", err)
	}
	if !posSees {
		t.Fatal("an AR-placed hold is invisible to the predicate the POS credit gate evaluates, the whole " +
			"design of DIB-65 is that these two are the same table")
	}
	// Provenance: an operator must be able to tell a machine's hold from a colleague's.
	var kind string
	var auto bool
	if err := pool.QueryRow(ctx, `SELECT hold_kind, auto_placed FROM credit_holds WHERE id = $1::uuid`, id).
		Scan(&kind, &auto); err != nil {
		t.Fatalf("read hold provenance: %v", err)
	}
	if kind != HoldCreditLimit || !auto {
		t.Fatalf("hold provenance = %s/auto=%v, want CREDIT_LIMIT/auto=true", kind, auto)
	}

	// The keystone invariant survives: a second placement is a no-op, not a second ACTIVE row.
	_, placedAgain, err := rig.repo.PlaceHold(ctx, rig.customerID, HoldARAging, "also aged", "")
	if err != nil {
		t.Fatalf("second place: %v", err)
	}
	if placedAgain {
		t.Fatal("a second ACTIVE hold was placed, ux_credit_holds_one_active exists precisely to make that impossible")
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM credit_holds WHERE customer_id = $1::uuid AND status='ACTIVE'`, rig.customerID).Scan(&n)
	if n != 1 {
		t.Fatalf("ACTIVE holds = %d, want exactly 1", n)
	}
}

// TestIntegration_Credit_OverrideIsTimeboxedInSQL proves the override window is evaluated by the
// DATABASE against now(), not by an application clock somebody could skew.
func TestIntegration_Credit_OverrideIsTimeboxedInSQL(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 0, 0) // COD
	rig.invoiceDueDaysAgo(t, "COD", 42500, 1, true)

	id, _, err := rig.repo.PlaceHold(ctx, rig.customerID, HoldCOD, "COD account with an unpaid balance", "")
	if err != nil {
		t.Fatalf("place COD hold: %v", err)
	}

	// Held → the gate stops the truck and says why.
	ok, reason := rig.svc.CanDispatch(ctx, rig.orderID)
	if ok {
		t.Fatal("a COD account on hold must not dispatch")
	}
	if reason == "" {
		t.Fatal("a block must carry a reason, the driver's banner is built from it")
	}

	// A LIVE override lets it through...
	if err := rig.repo.OverrideHold(ctx, id, "cheque collected at the counter", time.Now().Add(2*time.Hour), ""); err != nil {
		t.Fatalf("override: %v", err)
	}
	if ok, reason := rig.svc.CanDispatch(ctx, rig.orderID); !ok {
		t.Fatalf("a live override must permit dispatch; blocked with %q", reason)
	}

	// ... and an EXPIRED one does not. Written straight to the past so the expiry, not the code path,
	// is what is under test.
	if _, err := pool.Exec(ctx,
		`UPDATE credit_holds SET override_until = now() - interval '1 minute' WHERE id = $1::uuid`, id); err != nil {
		t.Fatalf("expire the override: %v", err)
	}
	if ok, _ := rig.svc.CanDispatch(ctx, rig.orderID); ok {
		t.Fatal("an EXPIRED override still permitted dispatch, 'temporarily' has become 'forever'")
	}
}

// TestIntegration_Credit_CODDispatchGate covers the DIB-52 seam end to end without any hold row at
// all: a brand-new COD order has nothing to have placed a hold yet, and it is precisely that order
// which must not leave unpaid.
func TestIntegration_Credit_CODDispatchGate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 0, 0) // COD terms
	invID := rig.invoiceDueDaysAgo(t, "CODNEW", 42500, 0, true)

	ok, reason := rig.svc.CanDispatch(ctx, rig.orderID)
	if ok {
		t.Fatal("an unpaid COD order must not dispatch even with no hold row, the hold sweep runs nightly, " +
			"and a COD order placed this morning must still be stopped this afternoon")
	}
	if want := "PAYMENT DUE"; reason[:len(want)] != want {
		t.Fatalf("reason = %q, want it to start with PAYMENT DUE (the driver banner)", reason)
	}
	if !containsAmount(reason, "$425.00") {
		t.Fatalf("reason = %q must name the amount so the driver knows what to collect", reason)
	}

	// Pay it → the same order dispatches.
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET balance_cents = 0, status = 'PAID' WHERE id = $1::uuid`, invID); err != nil {
		t.Fatalf("settle the invoice: %v", err)
	}
	if ok, reason := rig.svc.CanDispatch(ctx, rig.orderID); !ok {
		t.Fatalf("a paid COD order must dispatch; blocked with %q", reason)
	}
}

// TestIntegration_Credit_ReleaseRefusesNSFHolds pins the ownership boundary in SQL: billing does not
// get to lift a hold whose authorization rule it does not enforce.
func TestIntegration_Credit_ReleaseRefusesNSFHolds(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 100000)

	id, _, err := rig.repo.PlaceHold(ctx, rig.customerID, HoldNSF, "cheque returned", "")
	if err != nil {
		t.Fatalf("place NSF hold: %v", err)
	}
	if _, err := rig.repo.ReleaseHold(ctx, id, "customer says it is fine", ""); err == nil {
		t.Fatal("billing released an NSF hold, that hold carries a Tier-1 requirement this module does " +
			"not enforce, and two release paths with different authorization rules onto one table is a hole")
	}

	// An AR-placed hold, by contrast, releases cleanly.
	if _, err := rig.repo.ReleaseHold(ctx, id, "x", ""); err == nil {
		t.Fatal("the NSF refusal must be stable across calls")
	}
	arRig := newCreditRig(t, 30, 100000)
	arID, _, err := arRig.repo.PlaceHold(ctx, arRig.customerID, HoldARAging, "aged", "")
	if err != nil {
		t.Fatalf("place AR hold: %v", err)
	}
	kind, err := arRig.repo.ReleaseHold(ctx, arID, "paid in full this morning", "")
	if err != nil {
		t.Fatalf("release an AR hold: %v", err)
	}
	if kind != HoldARAging {
		t.Fatalf("released kind = %q, want AR_AGING", kind)
	}
}

// TestIntegration_Credit_PolicyResolvesBranchOverOrg proves the per-branch override actually beats
// the org default, which is the whole reason the policy is a table rather than a constant.
func TestIntegration_Credit_PolicyResolvesBranchOverOrg(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}

	org, err := repo.CreditPolicy(ctx, "")
	if err != nil {
		t.Fatalf("org policy: %v", err)
	}
	if org.AgingBucketDays != 60 || org.AutoHoldEnabled {
		t.Fatalf("the seeded org policy = %d days / auto=%v, want 60 days and auto-hold OFF (0104 ships flag-off)",
			org.AgingBucketDays, org.AutoHoldEnabled)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ar_credit_policy (branch_id, aging_bucket_days, auto_hold_enabled, notes)
		 VALUES ($1::uuid, 90, TRUE, 'test override') ON CONFLICT (branch_id) DO NOTHING`, branchID); err != nil {
		t.Fatalf("seed branch policy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ar_credit_policy WHERE branch_id = $1::uuid`, branchID)
	})

	got, err := repo.CreditPolicy(ctx, branchID)
	if err != nil {
		t.Fatalf("branch policy: %v", err)
	}
	if got.AgingBucketDays != 90 || !got.AutoHoldEnabled {
		t.Fatalf("branch policy = %d days / auto=%v, want the branch row's 90 / true", got.AgingBucketDays, got.AutoHoldEnabled)
	}
	// And the org default is untouched by the branch override.
	if again, _ := repo.CreditPolicy(ctx, ""); again.AgingBucketDays != 60 {
		t.Fatalf("the org default moved to %d days when a branch override was added", again.AgingBucketDays)
	}
}

func containsAmount(s, amount string) bool {
	for i := 0; i+len(amount) <= len(s); i++ {
		if s[i:i+len(amount)] == amount {
			return true
		}
	}
	return false
}

// TestIntegration_Credit_SweepRunsUnderTheRealJobRunner proves the DIB-65 job against the REAL
// jobs.Runner and the REAL schedule row from 0104, not a hand-called function.
//
// It uses RunNow (the runner's manual trigger) rather than waiting for 08:00 UTC, so what is under
// test is the registration, the claim, the invocation and the job_runs accounting: everything except
// the clock. The clock is internal/jobs' own tested concern.
func TestIntegration_Credit_SweepRunsUnderTheRealJobRunner(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewCreditService(repo)

	runner := jobs.NewRunner(jobs.NewRepository(pool), slog.New(slog.NewTextHandler(io.Discard, nil))).
		Register(CreditHoldSweepJobName, CreditHoldSweepJob(svc, ""))

	if !runner.Registered(CreditHoldSweepJobName) {
		t.Fatalf("%s did not register", CreditHoldSweepJobName)
	}

	res, err := runner.RunNow(ctx, CreditHoldSweepJobName)
	if err != nil {
		t.Fatalf("run the sweep: %v", err)
	}
	// 0104 ships auto_hold_enabled FALSE, so a run against the shipped policy must be a DRY RUN that
	// places nothing. A SUCCEEDED-and-placed result here would mean the flag-off promise is broken and
	// merging this migration silently started stopping customers at the counter.
	if !res.Skipped {
		t.Fatal("the shipped policy is flag-off, so the run must record SKIPPED, recording SUCCEEDED would " +
			"make job_runs say the credit policy is live when it is not")
	}
	if res.RowsWritten != 0 {
		t.Fatalf("a flag-off sweep placed %d hold(s), 0104 promises it touches nobody", res.RowsWritten)
	}
	if res.Detail["dry_run"] != true {
		t.Fatalf("Detail = %v, want dry_run true (the operator reads would_hold off this)", res.Detail)
	}

	// The attempt is recorded in job_runs, the whole reason internal/jobs exists is that somebody can
	// ask "did the nightly sweep happen last night?" and get an answer from a table.
	var status string
	var trigger string
	if err := pool.QueryRow(ctx,
		`SELECT status, trigger FROM job_runs WHERE job_name = $1 ORDER BY created_at DESC LIMIT 1`,
		CreditHoldSweepJobName).Scan(&status, &trigger); err != nil {
		t.Fatalf("read job_runs: %v", err)
	}
	if status != jobs.StatusSkipped || trigger != jobs.TriggerManual {
		t.Fatalf("job_runs row = %s/%s, want SKIPPED/MANUAL", status, trigger)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM job_runs WHERE job_name = $1`, CreditHoldSweepJobName)
	})
}
