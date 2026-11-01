//go:build integration

// ja4_by_job_doors_integration_test.go, JA-4 fix round 1: the statement by job and the aging by job
// agree per job after a sequence driven through the REAL payment doors (the receipt across two jobs,
// the void, the reversal, the re-application), now and at a past date, and both tie to
// ARBalanceCents. The billing package cannot import payments, so the legs live here.
//
//	Run: DATABASE_URL=... go test -tags integration ./cmd/core/ -run JA4Doors -count=1 -p 1
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ja4World struct {
	pool                *pgxpool.Pool
	cust                string
	jobA, jobB          string
	invA, invB, invAcct string
	pay                 *payments.Repository
	bill                *billing.Repository
	credit              *billing.CreditService
}

func ja4Exec(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// ja4Plant seeds one customer with job A (invoice 30000), job B (invoice 20000) and an account level
// invoice (10000), all issued 45 days ago.
func ja4Plant(t *testing.T) *ja4World {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return ja4PlantOn(t, pool)
}

// ja4PlantOn is ja4Plant on a pool the caller chose.
func ja4PlantOn(t *testing.T, pool *pgxpool.Pool) *ja4World {
	t.Helper()
	w := &ja4World{pool: pool, pay: payments.NewRepository(pool), bill: billing.NewRepository(pool)}
	w.credit = billing.NewCreditService(w.bill)
	ctx := context.Background()
	tag := fmt.Sprintf("JA4D-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 5000000) RETURNING id::text`, tag, "JA-4 doors "+tag, trentonBranchID).Scan(&w.cust); err != nil {
		t.Fatalf("seed the customer: %v", err)
	}
	t.Cleanup(func() { w.cleanup(t) })
	for _, j := range []struct {
		dst *string
		no  string
	}{{&w.jobA, "A"}, {&w.jobB, "B"}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, contact_name, contact_email)
			 VALUES ($1::uuid, $2::uuid, $3, $4, 'Contact', 'job@example.test') RETURNING id::text`,
			w.cust, trentonBranchID, j.no+"-"+tag, "Job "+j.no).Scan(j.dst); err != nil {
			t.Fatalf("seed job %s: %v", j.no, err)
		}
	}
	var running int64
	mk := func(dst *string, suffix string, total int64, job *string) {
		running += total
		var jobArg any
		if job != nil {
			jobArg = *job
		}
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date, subtotal_cents, tax_cents,
                      total_cents, balance_cents, finalized_at, doc_type, job_id)
VALUES ($1, $2::uuid, $3::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid), 'FINALIZED', current_date - 45, current_date - 15, $4, 0, $4, $4,
        now() - interval '45 days', 'INVOICE', $5::uuid)
RETURNING id::text`, tag+suffix, w.cust, trentonBranchID, total, jobArg).Scan(dst); err != nil {
			t.Fatalf("seed invoice %s: %v", suffix, err)
		}
		// The INVOICE ledger row the real writer books with the invoice (invoice_writer.go), dated at
		// the issue date in house time: the statement lists an invoice by it. balance_after is the
		// customer's running open balance, as the writer stores it (no read uses it).
		ja4Exec(t, pool, "seed the INVOICE ledger row "+suffix, `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'INVOICE', $2, $6, 'invoice', $3::uuid, $4::uuid, $5,
        (current_date - 45)::timestamp AT TIME ZONE 'America/Toronto')`,
			w.cust, total, *dst, trentonBranchID, tag+suffix, running)
	}
	mk(&w.invA, "-A", 30000, &w.jobA)
	mk(&w.invB, "-B", 20000, &w.jobB)
	mk(&w.invAcct, "-X", 10000, nil)
	return w
}

func (w *ja4World) cleanup(t *testing.T) {
	c := context.Background()
	for _, q := range []struct{ table, sql string }{
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_reapplications", `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE kind = 'REVERSAL' AND payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payments", `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid AND reversed_by_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid AND reverses_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
		{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
		{"finance_charge_runs", `DELETE FROM finance_charge_runs WHERE customer_id = $1::uuid`},
		{"credit_application_keys", `DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
		{"customer_jobs", `DELETE FROM customer_jobs WHERE customer_id = $1::uuid`},
		{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
	} {
		if _, err := w.pool.Exec(c, q.sql, w.cust); err != nil {
			t.Errorf("cleanup %s: %v", q.table, err)
		}
	}
}

// receipt takes a receipt through the real door: 15000 to job A's invoice, 5000 to job B's.
func (w *ja4World) receipt(t *testing.T) payments.Payment {
	t.Helper()
	p, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}, {InvoiceID: w.invB, AmountCents: 5000}})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	return p
}

// bounce takes a receipt back through the real NSF door (till.Service.MarkNSF), dated at `at`.
func (w *ja4World) bounce(t *testing.T, payID string, at time.Time) {
	t.Helper()
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierManager, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})
	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return at })
	if _, err := svc.MarkNSF(ctx, till.NSFInput{PaymentID: payID, Reason: ja4Reason}); err != nil {
		t.Fatalf("the NSF door: %v", err)
	}
	// A bounce taken in the past by a test clock has its ledger rows' created_at at the real now (the
	// column default); live, both are the bounce's instant. Bring them together so the as of reads see
	// what a live bounce on that day would have written.
	ja4Exec(t, w.pool, "date the bounce's ledger rows", `UPDATE customer_transactions SET created_at = occurred_at WHERE customer_id = $1::uuid AND created_at > occurred_at`, w.cust)
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM credit_holds WHERE source_payment_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id = $1::uuid`,
		} {
			if _, err := w.pool.Exec(context.Background(), q, payID); err != nil {
				t.Errorf("cleanup of the bounce: %v", err)
			}
		}
	})
}

// age moves a payment's rows (and the ledger rows that name it) back by d. Only legal before an undo
// exists (the reversal guard refuses to move an application a reversal names).
func (w *ja4World) age(t *testing.T, payID string, d time.Duration) {
	t.Helper()
	secs := d.Seconds()
	ja4Exec(t, w.pool, "age the allocations", `UPDATE payment_allocations SET created_at = created_at - make_interval(secs => $2) WHERE payment_id = $1::uuid`, payID, secs)
	ja4Exec(t, w.pool, "age the payment", `UPDATE payments SET received_at = received_at - make_interval(secs => $2) WHERE id = $1::uuid`, payID, secs)
	ja4Exec(t, w.pool, "age the ledger", `UPDATE customer_transactions SET occurred_at = occurred_at - make_interval(secs => $2), created_at = created_at - make_interval(secs => $2) WHERE source_ref_id = $1::uuid`, payID, secs)
}

// agree reads the statement by job and the aging by job at asOf and fails unless they agree per job
// and both tie to the plain closing balance. It returns the per job aging figures.
func (w *ja4World) agree(t *testing.T, label string, asOf time.Time) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	lines, err := w.bill.StatementLinesByJob(ctx, w.cust, asOf, billing.AllBranches)
	if err != nil {
		t.Fatalf("%s: StatementLinesByJob: %v", label, err)
	}
	stmt := map[string]int64{}
	var stmtSum int64
	jobLinesReadOnlyTheirOwn(t, label, lines, w.invoiceNumbers(t))
	for _, ln := range lines {
		stmt[ln.JobID] += ln.ChargeCents - ln.CreditCents
		stmtSum += ln.ChargeCents - ln.CreditCents
	}
	rows, err := w.credit.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("%s: AgingByJob: %v", label, err)
	}
	age := map[string]int64{}
	var ageSum int64
	for _, r := range rows {
		if r.CustomerID == w.cust {
			age[r.JobID] += r.TotalCents
			ageSum += r.TotalCents
		}
	}
	for _, k := range []string{w.jobA, w.jobB, ""} {
		if stmt[k] != age[k] {
			t.Errorf("%s: job %q statement %d, aging %d (statement %v, aging %v)", label, k, stmt[k], age[k], stmt, age)
		}
	}
	plain, err := w.bill.StatementLines(ctx, w.cust, asOf, billing.AllBranches)
	if err != nil {
		t.Fatalf("%s: StatementLines: %v", label, err)
	}
	if closing := plain[len(plain)-1].BalanceRun; closing != stmtSum || closing != ageSum {
		t.Errorf("%s: plain closing %d, by job statement %d, by job aging %d", label, closing, stmtSum, ageSum)
	}
	return age
}

// ja4Reason is the free text a clerk types at every door, written so that it would leak if it could: it
// names the other job, a figure that is nobody's own and an invoice number. A job line carries none of it.
const (
	ja4Marker = "LEAKMARK"
	ja4Reason = ja4Marker + " keyed 77777 against job B invoice INVSECRET-9 by mistake"
)

var (
	ja4UUID  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	ja4Cents = regexp.MustCompile(`([0-9]+) cents`)
)

// jobLinesReadOnlyTheirOwn is the words half of the by job proof (P3-D3, then P2-1): a statement line,
// at a job or at account level, carries no internal id and no money figure other than its own amount;
// a JOB line also carries no staff reason (the planted marker), no invoice number that is not on the
// same job, and no id. A job contact reads these lines, so no other job's money, name or number may
// reach them. numbers is every invoice number of the customer with the job it sits on ("" for the
// account).
func jobLinesReadOnlyTheirOwn(t *testing.T, label string, lines []billing.StatementLine, numbers map[string]string) {
	t.Helper()
	for _, ln := range lines {
		if ja4UUID.MatchString(ln.Description) || ja4UUID.MatchString(ln.DocNo) {
			t.Errorf("%s: job %q line %q (doc %q) carries an internal id", label, ln.JobID, ln.Description, ln.DocNo)
		}
		own := ln.ChargeCents - ln.CreditCents
		if own < 0 {
			own = -own
		}
		for _, m := range ja4Cents.FindAllStringSubmatch(ln.Description, -1) {
			if n, err := strconv.ParseInt(m[1], 10, 64); err != nil || n != own {
				t.Errorf("%s: job %q line %q (amount %d) names a figure %s that is not its own", label, ln.JobID, ln.Description, own, m[1])
			}
		}
		if ln.JobID == "" {
			continue
		}
		if strings.Contains(ln.Description, ja4Marker) || strings.Contains(ln.Description, "INVSECRET") {
			t.Errorf("%s: job %q line %q carries a staff reason", label, ln.JobID, ln.Description)
		}
		for no, job := range numbers {
			if job == ln.JobID || no == "" {
				continue
			}
			if strings.Contains(ln.Description, no) || strings.Contains(ln.DocNo, no) {
				t.Errorf("%s: job %q line %q (doc %q) names invoice %s, which is not on this job", label, ln.JobID, ln.Description, ln.DocNo, no)
			}
		}
	}
}

// invoiceNumbers reads every invoice number of the customer with its job ("" for the account).
func (w *ja4World) invoiceNumbers(t *testing.T) map[string]string {
	t.Helper()
	rows, err := w.pool.Query(context.Background(), `SELECT invoice_no::text, COALESCE(job_id::text, '') FROM invoices WHERE customer_id = $1::uuid`, w.cust)
	if err != nil {
		t.Fatalf("read the invoice numbers: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var no, job string
		if err := rows.Scan(&no, &job); err != nil {
			t.Fatalf("scan an invoice number: %v", err)
		}
		out[no] = job
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the invoice numbers: %v", err)
	}
	return out
}

func ja4Want(t *testing.T, label string, got map[string]int64, w *ja4World, a, b, acct int64) {
	t.Helper()
	if got[w.jobA] != a || got[w.jobB] != b || got[""] != acct {
		t.Fatalf("%s: A %d, B %d, account %d; want %d, %d, %d", label, got[w.jobA], got[w.jobB], got[""], a, b, acct)
	}
}

// TestIntegration_JA4Doors_ReceiptAcrossTwoJobs: a 20000 receipt, 15000 to job A and 5000 to job B,
// through the real receipt door (one ledger row per allocation).
func TestIntegration_JA4Doors_ReceiptAcrossTwoJobs(t *testing.T) {
	w := ja4Plant(t)
	w.receipt(t)
	got := w.agree(t, "after the receipt", time.Now().Add(time.Hour))
	ja4Want(t, "after the receipt", got, w, 15000, 15000, 10000)
}

// TestIntegration_JA4Doors_ReceiptClosesTwoInvoices (P3-D2): a receipt that pays job A's and job B's
// invoices in full. The statement lists an invoice by its INVOICE ledger row, as live ones have, so a
// planted invoice must carry that row or closing it reads wrong on the plain statement itself.
func TestIntegration_JA4Doors_ReceiptClosesTwoInvoices(t *testing.T) {
	w := ja4Plant(t)
	if _, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 30000}, {InvoiceID: w.invB, AmountCents: 20000}}); err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	got := w.agree(t, "after the closing receipt", time.Now().Add(time.Hour))
	ja4Want(t, "after the closing receipt", got, w, 0, 0, 10000)
}

// TestIntegration_JA4Doors_Void: the same receipt voided through the void door; the figures are back
// to the invoices and the agreement holds.
func TestIntegration_JA4Doors_Void(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	if _, err := w.pay.VoidPayment(context.Background(), payments.VoidInput{PaymentID: p.ID, Reason: ja4Reason, IsManager: true},
		time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("the void door: %v", err)
	}
	got := w.agree(t, "after the void", time.Now().Add(time.Hour))
	ja4Want(t, "after the void", got, w, 30000, 20000, 10000)
}

// TestIntegration_JA4Doors_TheWholeSequence: a split receipt taken ten days ago, its reversal two
// days ago, a re-application of the held money to job A's invoice, then a second receipt voided, then a third receipt bounced: the
// statement by job and the aging by job agree per job now and five days ago (receipt taken, not yet
// reversed), and the grand total ties to ARBalanceCents.
func TestIntegration_JA4Doors_TheWholeSequence(t *testing.T) {
	w := ja4Plant(t)
	ctx := context.Background()
	p := w.receipt(t)
	w.age(t, p.ID, 10*24*time.Hour)
	if _, err := w.pay.ReversePayment(ctx, payments.ReverseInput{PaymentID: p.ID, Reason: ja4Reason, IsManager: true},
		time.Now().Add(-2*24*time.Hour)); err != nil {
		t.Fatalf("the reversal door: %v", err)
	}
	past := w.agree(t, "five days ago", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago", past, w, 15000, 15000, 10000)
	afterRev := w.agree(t, "after the reversal", time.Now().Add(time.Hour))
	ja4Want(t, "after the reversal", afterRev, w, 30000, 20000, 10000)

	if _, err := w.pay.ReapplyHeld(ctx, payments.ReapplyInput{
		PaymentID: p.ID, Reason: ja4Reason, IdempotencyKey: "ja4-" + w.cust, IsManager: true,
		Allocations: []payments.Allocation{{InvoiceID: w.invA, AmountCents: 10000}},
	}, time.Now()); err != nil {
		t.Fatalf("the reapply door: %v", err)
	}
	reapplied := w.agree(t, "after the reapply", time.Now().Add(time.Hour))
	ja4Want(t, "after the reapply", reapplied, w, 20000, 20000, 10000)
	again := w.agree(t, "five days ago, after the reapply", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago, after the reapply", again, w, 15000, 15000, 10000)

	p2 := w.receipt(t)
	if _, err := w.pay.VoidPayment(ctx, payments.VoidInput{PaymentID: p2.ID, Reason: ja4Reason, IsManager: true},
		time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("the void door: %v", err)
	}
	asOf := time.Now().Add(time.Hour)
	final := w.agree(t, "after the void", asOf)
	ja4Want(t, "after the void", final, w, 20000, 20000, 10000)

	// A third receipt taken six days ago and bounced yesterday through the NSF door: before the
	// bounce (three days ago) the receipt credits its jobs, after it (now) the jobs return to what they
	// owed, and the statement equals the aging per job at each read.
	p3 := w.receipt(t)
	w.age(t, p3.ID, 6*24*time.Hour)
	w.agree(t, "three days ago, receipt taken, bounce not yet", time.Now().Add(-3*24*time.Hour))
	w.bounce(t, p3.ID, time.Now().Add(-24*time.Hour))
	w.agree(t, "two days ago, before the bounce", time.Now().Add(-2*24*time.Hour))
	bounced := w.agree(t, "after the bounce", asOf)
	ja4Want(t, "after the bounce", bounced, w, 20000, 20000, 10000+nsfFeeCents(t, w))

	all, err := w.credit.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("AgingByJob: %v", err)
	}
	var grand int64
	for _, r := range all {
		grand += r.TotalCents
	}
	ar, err := w.bill.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("ARBalanceCents: %v", err)
	}
	if grand != ar {
		t.Fatalf("the by job aging totals %d, ARBalanceCents %d", grand, ar)
	}
}

// TestIntegration_JA4Doors_Bounce: the 15000 and 5000 receipt bounced through the NSF door. The jobs
// return to owing what they did before the receipt (the bounce's NSF row is charged back to the jobs
// by the payment's reversal rows), now; and a receipt aged ten days, bounced two days ago, agrees per
// job five days ago (before the bounce), the day before yesterday's bounce was taken, and now.
func TestIntegration_JA4Doors_Bounce(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	w.bounce(t, p.ID, time.Now())
	got := w.agree(t, "after the bounce", time.Now().Add(time.Hour))
	ja4Want(t, "after the bounce", got, w, 30000, 20000, 10000+nsfFeeCents(t, w))
}

func TestIntegration_JA4Doors_BouncePast(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	w.age(t, p.ID, 10*24*time.Hour)
	w.bounce(t, p.ID, time.Now().Add(-2*24*time.Hour))
	before := w.agree(t, "five days ago, before the bounce", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago, before the bounce", before, w, 15000, 15000, 10000)
	after := w.agree(t, "one day ago, after the bounce", time.Now().Add(-24*time.Hour))
	ja4Want(t, "one day ago, after the bounce", after, w, 30000, 20000, 10000+nsfFeeCents(t, w))
	now := w.agree(t, "now, after the bounce", time.Now().Add(time.Hour))
	ja4Want(t, "now, after the bounce", now, w, 30000, 20000, 10000+nsfFeeCents(t, w))
}

// nsfFeeCents is the fee the bounce charged the customer (account level in the aging).
func nsfFeeCents(t *testing.T, w *ja4World) int64 {
	t.Helper()
	var fee int64
	if err := w.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(fee_cents), 0) FROM payment_nsf WHERE customer_id = $1::uuid`, w.cust).Scan(&fee); err != nil {
		t.Fatalf("read the NSF fee: %v", err)
	}
	return fee
}

// ja4PlantFresh is ja4Plant on a database of the leg's own, migrated and seeded as CI does, with the
// branches under one org: the finance charge run charges every customer of a branch and the credit
// memo door judges the branch's org, so neither can run on the shared gate database without leaving
// rows behind. The database is dropped when the leg ends.
func ja4PlantFresh(t *testing.T, suffix string) *ja4World {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, suffix)
	t.Cleanup(func() {
		pool.Close()
		admin, err := pgxpool.New(context.Background(), swapDatabase(mustEnv(t, "DATABASE_URL"), "postgres"))
		if err != nil {
			t.Errorf("ja4: connect to drop the leg database: %v", err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS demoseed_it_"+suffix+" WITH (FORCE)"); err != nil {
			t.Errorf("ja4: drop the leg database: %v", err)
		}
	})
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, 'ja4-'||$2::text, 'JA4 Org')`, cmRoundNorthOrg, suffix)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, cmRoundNorthOrg)
	return ja4PlantOn(t, pool)
}

// ageInvoices moves the planted invoices and their INVOICE ledger rows back by days.
func (w *ja4World) ageInvoices(t *testing.T, days int) {
	t.Helper()
	ja4Exec(t, w.pool, "age the invoices", `
UPDATE invoices SET issue_date = issue_date - $2::int, due_date = due_date - $2::int,
       finalized_at = finalized_at - make_interval(days => $2::int), created_at = created_at - make_interval(days => $2::int)
 WHERE customer_id = $1::uuid`, w.cust, days)
	ja4Exec(t, w.pool, "age the INVOICE rows", `
UPDATE customer_transactions SET occurred_at = occurred_at - make_interval(days => $2::int),
       created_at = created_at - make_interval(days => $2::int)
 WHERE customer_id = $1::uuid AND txn_type = 'INVOICE'`, w.cust, days)
}

// memo issues a credit memo of cents against invoiceID through the real memo door (ISSUED).
func (w *ja4World) memo(t *testing.T, invoiceID string, cents int64, key string) string {
	t.Helper()
	svc, _ := cmRoundService(t, w.pool)
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	res, err := svc.Issue(staffcontext.ContextWithStaffUserID(context.Background(), userID), billing.CreditMemoRouteRequest{
		CustomerID: w.cust, BranchID: trentonBranchID, InvoiceID: invoiceID, AmountCents: cents,
		Reason: ja4Reason, IdempotencyKey: key,
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: staffcontext.ScopeGlobal})
	if err != nil {
		t.Fatalf("the credit memo door: %v", err)
	}
	if res.Status != billing.CreditMemoIssued {
		t.Fatalf("the memo is %q, want ISSUED", res.Status)
	}
	t.Cleanup(func() {
		ja4Exec(t, w.pool, "memo journals", `DELETE FROM gl_journals WHERE idempotency_key = $1`, billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
	return res.CreditMemoID
}

// financeCharge runs the prior month's finance charge through the real run and returns this customer's
// charge invoice and its cents.
func (w *ja4World) financeCharge(t *testing.T) (string, int64) {
	t.Helper()
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load the house zone: %v", err)
	}
	n := time.Now().In(zone)
	prior := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, -1, 0)
	res, err := newInvoiceWriter(w.pool).RunFinanceCharges(context.Background(), billing.FinanceChargeRequest{
		BranchID: trentonBranchID, Month: prior.Format("2006-01"),
	})
	if err != nil {
		t.Fatalf("the finance charge run: %v", err)
	}
	for _, c := range res.Charges {
		if c.CustomerID == w.cust {
			return c.InvoiceID, c.ChargeCents
		}
	}
	t.Fatalf("the finance charge run charged nothing to the planted customer: %+v", res)
	return "", 0
}

// tie fails unless the by job aging totals ARBalanceCents at asOf.
func (w *ja4World) tie(t *testing.T, label string, asOf time.Time) {
	t.Helper()
	all, err := w.credit.AgingByJob(context.Background(), asOf, "", "")
	if err != nil {
		t.Fatalf("%s: AgingByJob: %v", label, err)
	}
	var mine, grand int64
	for _, r := range all {
		grand += r.TotalCents
		if r.CustomerID == w.cust {
			mine += r.TotalCents
		}
	}
	ar, err := w.bill.ARBalanceCents(context.Background(), asOf, "")
	if err != nil {
		t.Fatalf("%s: ARBalanceCents: %v", label, err)
	}
	if grand != ar {
		t.Fatalf("%s: the by job aging totals %d, ARBalanceCents %d (this customer %d)", label, grand, ar, mine)
	}
}

// TestIntegration_JA4Doors_MemoChargeReceiptBounceNow (P3-D1): a job memo, a real finance charge run
// and an overpaid receipt paying both jobs and the charge invoice, then bounced. Every step reads the
// statement by job equal to the aging by job per job, and the total ties through ARBalanceCents.
func TestIntegration_JA4Doors_MemoChargeReceiptBounceNow(t *testing.T) {
	w := ja4PlantFresh(t, "ja4d1now")
	w.ageInvoices(t, 90)
	soon := time.Now().Add(time.Hour)

	w.memo(t, w.invA, 4000, "ja4-memo-now-"+w.cust)
	ja4Want(t, "after the memo", w.agree(t, "after the memo", soon), w, 26000, 20000, 10000)
	w.tie(t, "after the memo", soon)

	chargeInv, charge := w.financeCharge(t)
	ja4Want(t, "after the finance charge", w.agree(t, "after the finance charge", soon), w, 26000, 20000, 10000+charge)
	w.tie(t, "after the finance charge", soon)

	// 60000 for 50000 + the charge: the rest is change on account.
	ans, err := w.pay.ApplyReceived(context.Background(), payments.ApplyRequest{
		CustomerID: w.cust, Method: "CHEQUE", IdempotencyKey: "ja4-receipt-now-" + w.cust,
		InvoiceIDs: []string{w.invA, w.invB, chargeInv}, AmountCents: 60000,
	})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	change := 60000 - 50000 - charge
	if ans.OnAccountCents != change {
		t.Fatalf("the receipt left %d on account, want %d", ans.OnAccountCents, change)
	}
	got := w.agree(t, "after the receipt", soon)
	ja4Want(t, "after the receipt", got, w, -4000, 0, 10000-change)
	w.tie(t, "after the receipt", soon)

	w.bounce(t, ans.PaymentID, time.Now())
	got = w.agree(t, "after the bounce", soon)
	ja4Want(t, "after the bounce", got, w, 26000, 20000, 10000+charge+nsfFeeCents(t, w))
	w.tie(t, "after the bounce", soon)
}

// TestIntegration_JA4Doors_MemoChargeReceiptBouncePast: a receipt aged six days and bounced yesterday,
// then a memo on job B and the finance charge run now. The statement equals the aging per job three
// days ago (receipt live), twelve hours ago (bounced, before the memo and the charge) and now.
func TestIntegration_JA4Doors_MemoChargeReceiptBouncePast(t *testing.T) {
	w := ja4PlantFresh(t, "ja4d1past")
	w.ageInvoices(t, 90)
	p, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}, {InvoiceID: w.invB, AmountCents: 5000}})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	w.age(t, p.ID, 6*24*time.Hour)
	w.bounce(t, p.ID, time.Now().Add(-24*time.Hour))
	w.memo(t, w.invB, 3000, "ja4-memo-past-"+w.cust)
	_, charge := w.financeCharge(t)
	fee := nsfFeeCents(t, w)

	for _, c := range []struct {
		label   string
		at      time.Time
		a, b, x int64
	}{
		{"three days ago", time.Now().Add(-3 * 24 * time.Hour), 15000, 15000, 10000},
		{"twelve hours ago", time.Now().Add(-12 * time.Hour), 30000, 20000, 10000 + fee},
		{"now", time.Now().Add(time.Hour), 30000, 17000, 10000 + fee + charge},
	} {
		ja4Want(t, c.label, w.agree(t, c.label, c.at), w, c.a, c.b, c.x)
		w.tie(t, c.label, c.at)
	}
}

// TestIntegration_JA4Doors_CreditApplyAcrossJobs (P2-1, the credit rows): a credit memo issued on job
// A's invoice is applied through the real apply door to job B's invoice with a staff reason, then the
// application is reversed through the real reverse door. Job A's lines show neither job B's invoice
// number nor the reason (agree runs the words check at every step), and the statement still equals the
// aging per job.
func TestIntegration_JA4Doors_CreditApplyAcrossJobs(t *testing.T) {
	w := ja4PlantFresh(t, "ja4credit")
	w.ageInvoices(t, 90)
	soon := time.Now().Add(time.Hour)
	memoID := w.memo(t, w.invA, 4000, "ja4-credit-memo-"+w.cust)
	ja4Want(t, "after the memo", w.agree(t, "after the memo", soon), w, 26000, 20000, 10000)

	app, err := w.bill.ApplyCredit(context.Background(), billing.ApplyCreditInput{
		CustomerID: w.cust, CreditKind: billing.CreditKindMemo, CreditID: memoID, InvoiceID: w.invB,
		AmountCents: 4000, Reason: ja4Reason, IdempotencyKey: "ja4-credit-apply-" + w.cust, OrgID: cmRoundNorthOrg,
	}, time.Now())
	if err != nil {
		t.Fatalf("the credit apply door: %v", err)
	}
	w.agree(t, "after the credit applied to job B", soon)
	w.tie(t, "after the credit applied to job B", soon)

	if _, err := w.bill.ReverseCreditApplication(context.Background(), billing.ReverseCreditApplicationInput{
		InvoiceID: w.invB, ApplicationID: app.ApplicationID, Reason: ja4Reason, OrgID: cmRoundNorthOrg,
	}, time.Now()); err != nil {
		t.Fatalf("the credit reverse door: %v", err)
	}
	ja4Want(t, "after the application is reversed", w.agree(t, "after the application is reversed", soon), w, 26000, 20000, 10000)
	w.tie(t, "after the application is reversed", soon)
}

// TestIntegration_JA4Doors_UnknownMemoReadsFixedWords (P061 fix round 2, P3-1): a ledger row that no writer
// this statement knows wrote (an unknown memo, in every transaction type, on a document of job A) reads the
// fixed words of its type on the job's line, never the memo. A new or reworded writer is safe by default.
func TestIntegration_JA4Doors_UnknownMemoReadsFixedWords(t *testing.T) {
	w := ja4Plant(t)
	ctx := context.Background()
	for _, txn := range []struct {
		typ   string
		cents int64
	}{{"ADJUSTMENT", -100}, {"CREDIT_MEMO", -200}, {"WRITE_OFF", -300}, {"NSF", 400}, {"PAYMENT", -500}, {"INVOICE", 600}} {
		ja4Exec(t, w.pool, "plant an unknown "+txn.typ+" row", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, $2, $3, 0, 'invoice', $4::uuid, $5::uuid, $6, now() - interval '1 hour')`,
			w.cust, txn.typ, txn.cents, w.invA, trentonBranchID, "a future writer says: "+ja4Reason)
	}
	lines, err := w.bill.StatementLinesByJob(ctx, w.cust, time.Now().Add(time.Hour), billing.AllBranches)
	if err != nil {
		t.Fatalf("StatementLinesByJob: %v", err)
	}
	jobLinesReadOnlyTheirOwn(t, "unknown memo", lines, w.invoiceNumbers(t))
	want := map[int64]string{
		-100: "Adjustment", -200: "Credit memo", -300: "Write-off", 400: "Returned payment", -500: "Payment received, thank you",
	}
	seen := 0
	for _, ln := range lines {
		if ln.JobID != w.jobA {
			continue
		}
		cents := ln.ChargeCents - ln.CreditCents
		if w8, ok := want[cents]; ok {
			seen++
			if !strings.HasPrefix(ln.Description, w8) || strings.Contains(ln.Description, "future writer") {
				t.Errorf("job A line of %d reads %q; want the fixed words %q", cents, ln.Description, w8)
			}
		}
		if cents == 600 && strings.Contains(ln.Description, "future writer") {
			t.Errorf("job A's unknown INVOICE row reads %q; want the invoice number", ln.Description)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of the %d planted lines on job A: %+v", seen, len(want), lines)
	}
}
