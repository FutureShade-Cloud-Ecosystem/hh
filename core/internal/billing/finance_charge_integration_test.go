//go:build integration

// finance_charge_integration_test.go, wave 3 FC-1: the monthly finance charge run, proved against
// the real schema, the real invoice writer and the real GL consumer.
//
// The run charges each customer the balance more than 30 days past due as of the month's last house
// day, at one and a half percent rounded half to even, with the two dollar minimum, one charge
// invoice per customer through the writer (DR 1100 / CR finance charge income through the posting
// rule), once per customer and month by the key migration 0473 makes unique, only once the house day
// after the month's last day has begun, skipping the customers the Controller waived, scoped to one
// branch, and after it the aging ties to GL 1100.
//
// Every money literal below is integer cents: 1_000_000 is ten thousand dollars, 15_000 is the one
// and a half percent charge on it.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1
package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The world's constants: the house month under test is a PAST month, so the run's begun judgement
// holds on the real clock the legs run at, exactly as the FX-2 legs fix theirs.
const (
	fc1Month = "2026-09"
	// fc1LastDay is the month's last house day, the day the balance is measured as of.
	fc1LastDay = "2026-09-30"
	// fc1NextDay is the first house day after the month, the day the run is first allowed.
	fc1NextDay = "2026-10-01"
	// fc1Due31 is 31 days before the last day: charged. fc1Due30 is exactly 30 days before: not
	// charged, more than 30 days past due means strictly more.
	fc1Due31 = "2026-08-30"
	fc1Due30 = "2026-08-31"

	fc1OrgID         = "6c100000-0000-4000-9000-0000000000a1"
	fc1BranchID      = "6c100000-0000-4000-9000-0000000000b1"
	fc1OtherBranchID = "6c100000-0000-4000-9000-0000000000b2"
	// fc1Controller is the test Controller's user id, a real users row the rig plants (the waiver's
	// who references users).
	fc1Controller = "6c100000-0000-4000-9000-0000000000c1"
)

// fc1Loc is the dealer's zone the legs run in.
func fc1Loc() *time.Location {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		panic(err)
	}
	return loc
}

// fc1At is an instant on a day of the month under test, in the dealer's zone.
func fc1At(day, hour, min int) time.Time {
	return time.Date(2026, 9, day, hour, min, 0, 0, fc1Loc())
}

// fcRig is one test owned org with two branches of its own, so no seeded or neighbouring test's
// document at a shared branch can fall into a run's candidates.
type fcRig struct {
	pool   *pgxpool.Pool
	t      *testing.T
	ctx    context.Context
	tag    string
	branch string // the run's branch
	other  string // the branch scope leg's branch
}

// fcDelete runs one cleanup delete and fails the test, naming the table, when it errors.
func fcDelete(t *testing.T, pool *pgxpool.Pool, table, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Errorf("cleanup: delete from %s: %v", table, err)
	}
}

func fcRigNew(t *testing.T) *fcRig {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	r := &fcRig{pool: pool, t: t, ctx: ctx, tag: ns(t), branch: fc1BranchID, other: fc1OtherBranchID}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, 'FC1 Org')
		ON CONFLICT (id) DO NOTHING`, fc1OrgID, "fc1-"+r.tag); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id) VALUES
		  ($1::uuid, $1::uuid, NULL, 'BRANCH', 'FC1RUN', 'FC1 Run Branch', '{}', $3::uuid),
		  ($2::uuid, $2::uuid, NULL, 'BRANCH', 'FC1OTH', 'FC1 Other Branch', '{}', $3::uuid)
		ON CONFLICT (id) DO NOTHING`, fc1BranchID, fc1OtherBranchID, fc1OrgID); err != nil {
		t.Fatalf("plant the branches: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status) VALUES
		  ($1::uuid, $2, 'FC1 Controller', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING`, fc1Controller, "fc1-controller-"+r.tag+"@example.invalid"); err != nil {
		t.Fatalf("plant the controller: %v", err)
	}
	t.Cleanup(func() {
		// Dependency order: the journals the charges and memos posted at the two branches (lines
		// first, a reversing journal before the one it reverses), then the rig's own rows, the
		// branches, the org. Every delete's error fails the test, naming the table: a delete that
		// fails leaves the branches behind for the next package to count (PR 748, run 41188047610).
		branches := []string{fc1BranchID, fc1OtherBranchID}
		fcDelete(t, pool, "gl_journal_lines", `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE branch_id = ANY($1::uuid[]))`, branches)
		fcDelete(t, pool, "gl_journals (reversals)", `DELETE FROM gl_journals WHERE branch_id = ANY($1::uuid[]) AND reverses_journal_id IS NOT NULL`, branches)
		fcDelete(t, pool, "gl_journals", `DELETE FROM gl_journals WHERE branch_id = ANY($1::uuid[])`, branches)
		fcDelete(t, pool, "finance_charge_runs", `DELETE FROM finance_charge_runs WHERE branch_id = ANY($1::uuid[])`, branches)
		fcDelete(t, pool, "finance_charge_waivers", `DELETE FROM finance_charge_waivers WHERE waived_by = $1::uuid OR cleared_by = $1::uuid`, fc1Controller)
		fcDelete(t, pool, "users", `DELETE FROM users WHERE id = $1::uuid`, fc1Controller)
		fcDelete(t, pool, "locations", `DELETE FROM locations WHERE id = ANY($1::uuid[])`, branches)
		fcDelete(t, pool, "org", `DELETE FROM org WHERE id = $1::uuid`, fc1OrgID)
	})
	return r
}

// customer plants one test owned customer at a branch of the rig and removes it (and anything hung
// off it) at test end.
func (r *fcRig) customer(branchID, label string) string {
	r.t.Helper()
	var id string
	if err := r.pool.QueryRow(r.ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days, terms_code)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0, 30, 'NET30') RETURNING id::text`,
		"BILV-"+r.tag+"-"+label, "FC1 "+label, branchID).Scan(&id); err != nil {
		r.t.Fatalf("seed customer %s: %v", label, err)
	}
	r.t.Cleanup(func() {
		fcDelete(r.t, r.pool, "finance_charge_runs", `DELETE FROM finance_charge_runs WHERE customer_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "finance_charge_waivers", `DELETE FROM finance_charge_waivers WHERE customer_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "credit_application_keys", `DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, id)
		fcDelete(r.t, r.pool, "invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, id)
		fcDelete(r.t, r.pool, "invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "customers", `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
	return id
}

// fcInv is one test owned invoice header with its due day, source kind and finalization instant
// under the test's control, the facts the run's candidates read.
type fcInv struct {
	no         string
	status     string
	sourceKind string
	issue      string
	due        string
	total      int64
	finalized  time.Time
}

func (r *fcRig) invoice(customerID, branchID string, s fcInv) string {
	r.t.Helper()
	var id string
	if err := r.pool.QueryRow(r.ctx,
		`INSERT INTO invoices
		   (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents,
		    total_cents, balance_cents, finalized_at, doc_type, source_kind)
		 VALUES ($1, $2::uuid, $3::uuid, $4, $5::date, $6::date, $7, 0, $7, $7, $8, 'INVOICE', $9)
		 RETURNING id::text`,
		r.tag+"-"+s.no, customerID, branchID, s.status, s.issue, s.due, s.total, s.finalized, s.sourceKind,
	).Scan(&id); err != nil {
		r.t.Fatalf("seed invoice %s: %v", s.no, err)
	}
	r.t.Cleanup(func() {
		fcDelete(r.t, r.pool, "credit_application_keys", `DELETE FROM credit_application_keys WHERE invoice_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, id)
		fcDelete(r.t, r.pool, "invoices", `DELETE FROM invoices WHERE id = $1::uuid`, id)
	})
	return id
}

// tradeInvoice posts one overdue trade invoice at the named branch: total cents due on the named
// day, finalized inside the month under test.
func (r *fcRig) tradeInvoice(customerID, branchID, due string, total int64, label string) {
	r.t.Helper()
	r.invoice(customerID, branchID, fcInv{
		no: label, status: "FINALIZED", sourceKind: "ORDER",
		issue: "2026-08-01", due: due, total: total, finalized: fc1At(10, 9, 0),
	})
}

// writer is the run's writer: the real one over the pool, dated by NET30 terms through the terms
// seam, on the clock named (nil is the real clock).
func (r *fcRig) writer(clock func() time.Time) *InvoiceWriter {
	w := NewInvoiceWriter(r.pool).
		WithTerms(termsReaderStub{term: customer.Terms{Code: "NET30", Name: "Net 30", NetDays: 30}})
	if clock != nil {
		w = w.WithClock(clock)
	}
	return w
}

// chargeRows reads the run's rows at a branch for the month under test.
func (r *fcRig) chargeRows(branchID string) map[string]int64 {
	r.t.Helper()
	rows, err := r.pool.Query(r.ctx, `
		SELECT customer_id::text, charge_cents FROM finance_charge_runs
		 WHERE branch_id = $1::uuid AND month = $2`, branchID, fc1Month)
	if err != nil {
		r.t.Fatalf("read the charge rows: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var cents int64
		if err := rows.Scan(&id, &cents); err != nil {
			r.t.Fatalf("scan a charge row: %v", err)
		}
		out[id] = cents
	}
	if err := rows.Err(); err != nil {
		r.t.Fatalf("charge rows rows: %v", err)
	}
	return out
}

// chargeInvoiceCount counts the charge documents the run raised at a branch.
func (r *fcRig) chargeInvoiceCount(branchID string) int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRow(r.ctx, `
		SELECT COUNT(*) FROM invoices
		 WHERE branch_id = $1::uuid AND source_kind = 'FINANCE_CHARGE' AND idempotency_key LIKE 'finance-charge:%'`,
		branchID).Scan(&n); err != nil {
		r.t.Fatalf("count the charge invoices: %v", err)
	}
	return n
}

// THE LEGS OF ITEM 6: the exact one and a half percent, the two dollar minimum, zero past due
// charges nothing, the 30 and 31 day boundary, the words on the document, the exclusion of earlier
// charge documents, and the branch scope.
func TestIntegration_W3FC1_TheRunChargesTheBalanceMoreThan30DaysPastDue(t *testing.T) {
	r := fcRigNew(t)

	// One customer per leg, each with exactly one invoice, so the charged set answers by name.
	pastDue := r.customer(r.branch, "PASTDUE")    // 31 days late: charged
	exact30 := r.customer(r.branch, "EXACT30")    // exactly 30 days late: not charged
	minimum := r.customer(r.branch, "MINIMUM")    // 31 days late on a small balance: the minimum
	current := r.customer(r.branch, "CURRENT")    // inside the month's terms: nothing due
	chargeTx := r.customer(r.branch, "CHARGETX")  // an earlier run's charge document: never recharged
	otherBr := r.customer(r.other, "OTHERBRANCH") // another branch's books: not this run's

	// $10,000.00 due 31 days before the month's last day: the charge is exactly $150.00.
	r.tradeInvoice(pastDue, r.branch, fc1Due31, 1_000_000, "PASTDUE")
	// $5,000.00 due exactly 30 days before: more than 30 days means strictly more, no charge.
	r.tradeInvoice(exact30, r.branch, fc1Due30, 500_000, "EXACT30")
	// $100.00 due 31 days before: one and a half percent is $1.50, the minimum $2.00 applies.
	r.tradeInvoice(minimum, r.branch, fc1Due31, 10_000, "MINIMUM")
	// $7,000.00 due inside the month: not past due at the month's last day, no charge.
	r.tradeInvoice(current, r.branch, "2026-09-15", 700_000, "CURRENT")
	// An earlier run's charge document of $5,000.00, itself long past due: a charge is never charged.
	r.invoice(chargeTx, r.branch, fcInv{
		no: "CHARGETX", status: "FINALIZED", sourceKind: "FINANCE_CHARGE",
		issue: "2026-06-01", due: "2026-07-01", total: 500_000, finalized: fc1At(10, 9, 0),
	})
	// $10,000.00 at the other branch, as late as the charged one: another branch's books.
	r.tradeInvoice(otherBr, r.other, fc1Due31, 1_000_000, "OTHERBRANCH")

	res, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{
		BranchID: r.branch, Month: fc1Month,
	})
	if err != nil {
		t.Fatalf("run the finance charges: %v", err)
	}
	if res.Charged != 2 {
		t.Fatalf("the run charged %d customers, want 2 (the 31 day past due balance and the minimum)", res.Charged)
	}
	if res.ChargedCents != 15_000+200 {
		t.Fatalf("the run charged %d cents, want %d ($150.00 and the $2.00 minimum)", res.ChargedCents, 15_000+200)
	}
	rows := r.chargeRows(r.branch)
	if got := rows[pastDue]; got != 15_000 {
		t.Fatalf("the past due customer's charge is %d cents, want exactly 15000 (one and a half percent of $10,000.00)", got)
	}
	if got := rows[minimum]; got != 200 {
		t.Fatalf("the small balance's charge is %d cents, want the 200 cent minimum", got)
	}
	for name, id := range map[string]string{
		"the exactly 30 day past due balance": exact30,
		"the current invoice":                 current,
		"the earlier charge document":         chargeTx,
	} {
		if _, charged := rows[id]; charged {
			t.Fatalf("%s was charged, it must not be", name)
		}
	}
	if otherRows := r.chargeRows(r.other); len(otherRows) != 0 {
		t.Fatalf("the other branch was charged by this run's branch: %v", otherRows)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 2 {
		t.Fatalf("the run raised %d charge documents, want 2", n)
	}

	// The charge document's words say what it is and which month it covers: on its line and on the
	// AR subledger row beside the invoice number.
	var words string
	if err := r.pool.QueryRow(r.ctx, `
		SELECT l.description FROM invoices i
		 JOIN invoice_lines l ON l.invoice_id = i.id
		 WHERE i.customer_id = $1::uuid AND i.source_kind = 'FINANCE_CHARGE'`, pastDue,
	).Scan(&words); err != nil {
		t.Fatalf("read the charge document's line words: %v", err)
	}
	if want := "Finance charge for " + fc1Month; words != want {
		t.Fatalf("the charge line's words are %q, want %q", words, want)
	}
	var memo string
	if err := r.pool.QueryRow(r.ctx, `
		SELECT memo FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_type = 'invoice'
		   AND source_ref_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid AND source_kind = 'FINANCE_CHARGE')`,
		pastDue).Scan(&memo); err != nil {
		t.Fatalf("read the charge document's subledger words: %v", err)
	}
	if want := ", finance charge for " + fc1Month; !strings.HasSuffix(memo, want) {
		t.Fatalf("the charge's AR row memo is %q, want it to end with %q", memo, want)
	}

	// The charge is its own invoice, raised FINALIZED at its full amount with no assessed tax.
	var total, balance, tax int64
	var source string
	if err := r.pool.QueryRow(r.ctx, `
		SELECT total_cents, balance_cents, tax_cents, source_kind FROM invoices
		 WHERE customer_id = $1::uuid AND source_kind = 'FINANCE_CHARGE'`, pastDue,
	).Scan(&total, &balance, &tax, &source); err != nil {
		t.Fatalf("read the raised charge document: %v", err)
	}
	if total != 15_000 || balance != 15_000 || tax != 0 || source != "FINANCE_CHARGE" {
		t.Fatalf("the charge document is total %d balance %d tax %d source %s, want 15000 15000 0 FINANCE_CHARGE", total, balance, tax, source)
	}
}

// A second run for the same month charges nothing, however it is pressed again.
func TestIntegration_W3FC1_ASecondRunChargesNothing(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "SECOND")
	r.tradeInvoice(cust, r.branch, fc1Due31, 200_000, "SECOND")

	first, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the first run: %v", err)
	}
	if first.Charged != 1 || first.ChargedCents != 3_000 {
		t.Fatalf("the first run charged %d for %d cents, want 1 for 3000", first.Charged, first.ChargedCents)
	}
	second, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the second run: %v", err)
	}
	if second.Charged != 0 || second.ChargedCents != 0 {
		t.Fatalf("the second run charged %d for %d cents, want nothing", second.Charged, second.ChargedCents)
	}
	if second.AlreadyCharged != 1 {
		t.Fatalf("the second run reports %d already charged, want 1", second.AlreadyCharged)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 1 {
		t.Fatalf("the two runs raised %d charge documents, want 1", n)
	}
	if rows := r.chargeRows(r.branch); len(rows) != 1 {
		t.Fatalf("the two runs wrote %d charge rows, want 1", len(rows))
	}
}

// Two runs pressed at the same moment charge every customer exactly once between them.
func TestIntegration_W3FC1_TwoConcurrentRunsChargeOnce(t *testing.T) {
	r := fcRigNew(t)
	custA := r.customer(r.branch, "CONCA")
	custB := r.customer(r.branch, "CONCB")
	r.tradeInvoice(custA, r.branch, fc1Due31, 1_000_000, "CONCA")
	r.tradeInvoice(custB, r.branch, fc1Due31, 400_000, "CONCB")

	const pressers = 2
	results := make([]FinanceChargeRunResult, pressers)
	errs := make([]error, pressers)
	var wg sync.WaitGroup
	for i := 0; i < pressers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent run %d: %v", i, err)
		}
	}
	var charged int
	for _, res := range results {
		charged += res.Charged
	}
	if charged != 2 {
		t.Fatalf("the two concurrent runs charged %d customers between them, want exactly 2", charged)
	}
	rows := r.chargeRows(r.branch)
	if len(rows) != 2 {
		t.Fatalf("the two concurrent runs wrote %d charge rows, want 2", len(rows))
	}
	if rows[custA] != 15_000 {
		t.Fatalf("customer A's charge is %d, want 15000", rows[custA])
	}
	if rows[custB] != 6_000 {
		t.Fatalf("customer B's charge is %d, want 6000", rows[custB])
	}
	if n := r.chargeInvoiceCount(r.branch); n != 2 {
		t.Fatalf("the two concurrent runs raised %d charge documents, want 2", n)
	}
}

// The run waits for its month to end: at half past eleven in the evening of the month's last house
// day it is refused in words, at the midnight after it, the first instant of the next house day, it
// runs.
func TestIntegration_W3FC1_TheRunWaitsForItsMonthToEnd(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "WAIT")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "WAIT")

	// Half past eleven in the evening of the last house day: the month has not ended.
	_, err := r.writer(func() time.Time { return time.Date(2026, 9, 30, 23, 30, 0, 0, fc1Loc()) }).
		RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	var notEnded *FinanceChargeMonthNotEndedError
	if !errors.As(err, &notEnded) {
		t.Fatalf("the evening run was not refused with the month not ended error: %v", err)
	}
	for _, want := range []string{fc1Month, fc1LastDay, fc1NextDay} {
		if !strings.Contains(notEnded.Error(), want) {
			t.Fatalf("the refusal %q does not name %s", notEnded.Error(), want)
		}
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("the refused run wrote charge rows: %v", rows)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 0 {
		t.Fatalf("the refused run raised %d charge documents", n)
	}

	// The midnight after it, the exact first instant of the next house day: accepted.
	res, err := r.writer(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc()) }).
		RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the midnight run: %v", err)
	}
	if res.Charged != 1 || res.ChargedCents != 1_500 {
		t.Fatalf("the midnight run charged %d for %d cents, want 1 for 1500", res.Charged, res.ChargedCents)
	}
}

// A waived customer is skipped: the Controller's flag is recorded with who set it and when, its
// clear is recorded too, and only the Controller's role may do either.
func TestIntegration_W3FC1_TheRunSkipsAWaivedCustomer(t *testing.T) {
	r := fcRigNew(t)
	waived := r.customer(r.branch, "WAIVED")
	kept := r.customer(r.branch, "KEPT")
	r.tradeInvoice(waived, r.branch, fc1Due31, 1_000_000, "WAIVED")
	r.tradeInvoice(kept, r.branch, fc1Due31, 1_000_000, "KEPT")

	waivers := NewFinanceChargeWaiverService(NewRepository(r.pool))

	// A caller without the controller role is refused, and the flag stays unset.
	_, err := waivers.SetWaiver(r.ctx, FinanceChargeWaiverInput{
		CustomerID: waived, Reason: "long standing account, paying to plan", ActorID: fc1Controller,
	})
	var roleErr *FinanceChargeWaiverRoleError
	if !errors.As(err, &roleErr) {
		t.Fatalf("a non controller set the waiver: %v", err)
	}

	// The Controller sets it, and the record names who and when.
	setAt := time.Date(2026, 9, 20, 10, 0, 0, 0, fc1Loc())
	view, err := waivers.WithClock(func() time.Time { return setAt }).SetWaiver(r.ctx, FinanceChargeWaiverInput{
		CustomerID: waived, Reason: "long standing account, paying to plan", ActorID: fc1Controller,
		IsController: true,
	})
	if err != nil {
		t.Fatalf("the controller set the waiver: %v", err)
	}
	if !view.Active || view.WaivedBy != fc1Controller {
		t.Fatalf("the waiver record is %+v, want active and the controller's id on it", view)
	}
	if view.WaivedAt != setAt.UTC().Format(time.RFC3339) {
		t.Fatalf("the waiver's when is %s, want %s", view.WaivedAt, setAt.UTC().Format(time.RFC3339))
	}

	// The run charges the kept customer and skips the waived one.
	res, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the run beside the waiver: %v", err)
	}
	if res.Charged != 1 || res.ChargedCents != 15_000 {
		t.Fatalf("the run charged %d for %d cents, want the kept customer alone for 15000", res.Charged, res.ChargedCents)
	}
	rows := r.chargeRows(r.branch)
	if _, charged := rows[waived]; charged {
		t.Fatal("the waived customer was charged")
	}
	if _, charged := rows[kept]; !charged {
		t.Fatal("the kept customer was not charged")
	}

	// The Controller clears the flag, recorded with who and when, and the next month's run has the
	// customer back.
	clearedAt := time.Date(2026, 10, 5, 9, 0, 0, 0, fc1Loc())
	view, err = waivers.WithClock(func() time.Time { return clearedAt }).ClearWaiver(r.ctx, FinanceChargeWaiverInput{
		CustomerID: waived, Reason: "back on terms", ActorID: fc1Controller,
		IsController: true,
	})
	if err != nil {
		t.Fatalf("the controller cleared the waiver: %v", err)
	}
	if view.Active || view.ClearedBy != fc1Controller {
		t.Fatalf("the cleared waiver record is %+v, want inactive and the controller's id on it", view)
	}
	// The following month's run: both of the customer's invoices ($20,000.00 together) are more
	// than 30 days past due at October's last day, and the September charge document never counts.
	r.tradeInvoice(waived, r.branch, fc1Due31, 1_000_000, "WAIVED2")
	res, err = r.writer(func() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, fc1Loc()) }).
		RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: "2026-10"})
	if err != nil {
		t.Fatalf("the october run: %v", err)
	}
	octRows := map[string]int64{}
	for _, c := range res.Charges {
		octRows[c.CustomerID] = c.ChargeCents
	}
	if got := octRows[waived]; got != 30_000 {
		t.Fatalf("the restored customer's october charge is %d, want 30000 (one and a half percent of $20,000.00)", got)
	}
}

// After a run, the aging ties to GL 1100: the charge documents are AR on the aging's own read, and
// the GL journal the run's documents post moves 1100 by the same cents, DR 1100 against CR finance
// charge income through the posting rule.
func TestIntegration_W3FC1_TheRunTiesTheAgingToGL1100(t *testing.T) {
	r := fcRigNew(t)
	repo := NewRepository(r.pool)
	cust := r.customer(r.branch, "TIE")
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "TIE")

	now := time.Now().Add(time.Hour)
	arBefore, err := repo.ARBalanceCents(r.ctx, now, r.branch)
	if err != nil {
		t.Fatalf("read AR before the run: %v", err)
	}
	agingBefore := r.agingTotal()
	gl1100Before := r.glNet("1100")

	res, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month})
	if err != nil {
		t.Fatalf("the run: %v", err)
	}
	if res.Charged != 1 || res.ChargedCents != 15_000 {
		t.Fatalf("the run charged %d for %d cents, want 1 for 15000", res.Charged, res.ChargedCents)
	}

	// Drain the outbox through the real GL consumer until the charge document's journal posts.
	glSvc := gl.NewService(gl.NewRepository(r.pool)).
		WithInvoiceReader(repo).
		WithPaymentReader(till.NewRepository(r.pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(r.pool), nil)
	glSvc.RegisterPostingHandlers(d)
	key := "ar.invoice.finalized:" + res.Charges[0].InvoiceID
	for i := 0; i < 200; i++ {
		d.DrainOnce(r.ctx)
		var posted int
		if err := r.pool.QueryRow(r.ctx,
			`SELECT COUNT(*) FROM gl_journals WHERE idempotency_key = $1 AND status = 'POSTED'`, key).
			Scan(&posted); err != nil {
			t.Fatalf("watch the charge journal: %v", err)
		}
		if posted > 0 {
			break
		}
		if i == 199 {
			t.Fatalf("the charge journal on key %s never posted", key)
		}
	}

	// The journal's legs: DR 1100 / CR 4220 finance charge income, the whole charge.
	var dr1100, cr4220 int64
	if err := r.pool.QueryRow(r.ctx, `
		SELECT COALESCE(SUM(CASE WHEN a.code = '1100' THEN l.debit_cents ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN a.code = '4220' THEN l.credit_cents ELSE 0 END), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND j.status = 'POSTED'`, key).Scan(&dr1100, &cr4220); err != nil {
		t.Fatalf("read the charge journal's legs: %v", err)
	}
	if dr1100 != 15_000 || cr4220 != 15_000 {
		t.Fatalf("the charge journal is DR 1100 %d / CR 4220 %d, want 15000 on each", dr1100, cr4220)
	}

	// The tie: the aging, the AR subledger and GL 1100 all moved by the charge.
	arAfter, err := repo.ARBalanceCents(r.ctx, now, r.branch)
	if err != nil {
		t.Fatalf("read AR after the run: %v", err)
	}
	if delta := arAfter - arBefore; delta != 15_000 {
		t.Fatalf("the AR subledger moved %d cents, want the 15000 charge", delta)
	}
	if delta := r.agingTotal() - agingBefore; delta != 15_000 {
		t.Fatalf("the aging moved %d cents, want the 15000 charge", delta)
	}
	if delta := r.glNet("1100") - gl1100Before; delta != 15_000 {
		t.Fatalf("GL 1100 moved %d cents, want the 15000 charge", delta)
	}
}

// agingTotal sums the branch's aging rows, the aging's own read.
func (r *fcRig) agingTotal() int64 {
	r.t.Helper()
	svc := NewCreditService(NewRepository(r.pool))
	rows, err := svc.Aging(r.ctx, time.Now().Add(time.Hour), r.branch)
	if err != nil {
		r.t.Fatalf("read the aging: %v", err)
	}
	var sum int64
	for _, row := range rows {
		sum += row.TotalCents
	}
	return sum
}

// glNet reads the branch's net debit balance on a GL account across every posted journal.
func (r *fcRig) glNet(code string) int64 {
	r.t.Helper()
	var net int64
	if err := r.pool.QueryRow(r.ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED' AND j.branch_id = $1::uuid AND a.code = $2`, r.branch, code).Scan(&net); err != nil {
		r.t.Fatalf("read the branch net on %s: %v", code, err)
	}
	return net
}

// A run for a month whose shape is not a house month, and one with no branch or a branch that is
// not a branch, are refused in words before anything is read.
func TestIntegration_W3FC1_TheRunRefusesAShapelessRequest(t *testing.T) {
	r := fcRigNew(t)
	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: "2026-9"}); err == nil {
		t.Fatal("a month without its two digits was accepted")
	}
	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: "2026-13"}); err == nil {
		t.Fatal("a thirteenth month was accepted")
	}
	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{Month: fc1Month}); err == nil {
		t.Fatal("a run without a branch was accepted")
	}
	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: "6c100000-0000-4000-9000-00000000ff", Month: fc1Month}); err == nil {
		t.Fatal("a run for a branch that is not a branch was accepted")
	}
}
