//go:build integration

package main

// rt2_holdback_withhold_integration_test.go, wave 3 RT-2: an invoice on a job withholds the job's
// holdback, and the books, the aging and the holdback ledger agree about it.
//
// Every leg runs the production wiring on a fresh migrated and seeded database of its own, through the
// invoice writer exactly as cmd/core builds it (newInvoiceWriter, the credit memo poster), with the GL
// consumer's posting loop drained after each step, and drops that database when it ends.
//
//	Run: DATABASE_URL=… go test -tags integration ./cmd/core -run RT2 -count=1 -v

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/lien"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type rt2Leg struct {
	t      *testing.T
	pool   *pgxpool.Pool
	money  *moneySpine
	branch string
	cust   string
	job    string // the customer job
	lien   string // the lien job linked to it
	n      int
	// base is the variance each tie shows before the leg writes anything: the seed's open invoices
	// carry no journals, so the ties are held to the leg's own movement and not to the seed.
	base map[string]int64
	// agingBase is the aging total before the leg writes anything.
	agingBase int64
	// The served mux and the staff bearer the leg's HTTP calls ride (the Controller's global authority).
	mux   *http.ServeMux
	token string
	slug  string
	// sg is the staff guard double: SetAuthority swaps the role every bearer resolves to.
	sg *testkit.StaffGuards
}

// rt2Source answers one fixed billable order, so the withholding is the only thing under test.
type rt2Source struct {
	customer, job, branch string
	subtotal              int64
	tax                   int64
}

func (s rt2Source) InvoiceableOrder(_ context.Context, _ string, _ []string) (billing.OrderInvoiceFacts, error) {
	tax := s.tax
	return billing.OrderInvoiceFacts{
		CustomerID: s.customer, BranchID: s.branch, JobID: s.job, ClosesOrder: true, TermsDays: 30,
		AgreedTaxCents: &tax,
		Lines: []billing.OrderInvoiceLine{{
			LineNo: 1, Description: "Pavers", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: s.subtotal, PriceUOM: "EA", LineTotalCents: s.subtotal,
		}},
	}, nil
}

func (rt2Source) MarkGroupsInvoiced(context.Context, []string, string, time.Time) error { return nil }

func rt2Setup(t *testing.T, suffix string) *rt2Leg {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "rt2"+suffix)
	t.Cleanup(func() {
		pool.Close()
		admin, err := pgxpool.New(context.Background(), swapDatabase(mustEnv(t, "DATABASE_URL"), "postgres"))
		if err != nil {
			t.Errorf("rt2: connect to drop the leg database: %v", err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS demoseed_it_rt2"+suffix+" WITH (FORCE)"); err != nil {
			t.Errorf("rt2: drop the leg database: %v", err)
		}
	})
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	orgID := "88a20000-0000-4000-8000-0000000000c2"
	slug := "rt2-" + suffix
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'RT2 Org') ON CONFLICT (id) DO NOTHING`, orgID, slug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgID)
	mustExec(t, ctx, pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)
	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, slug, true)
	sg.Harness.Resolver.Set(slug, orgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})
	log := slog.New(slog.NewTextHandler(fw109dWriter{t}, nil))
	vaultKey := dashVaultKey(t)
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		t.Fatalf("the leg's vault: %v", err)
	}
	if err := (payment.NewStore(pool, vault)).Configure(ctx, "run_payments", "MID-RT2"+strings.ToUpper(suffix), "CAD", "", 0, false,
		map[string]string{"api_key": "rt2-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the leg's card rail: %v", err)
	}
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "rt2-fx-ticket", belt.outbox)
	wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)

	leg := &rt2Leg{t: t, pool: pool, money: money, mux: mux, token: sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager), slug: slug, sg: sg}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' ORDER BY id LIMIT 1`).Scan(&leg.branch); err != nil {
		t.Fatalf("the seeded branch: %v", err)
	}
	leg.drain()
	leg.base = map[string]int64{}
	leg.ties("baseline")
	leg.agingBase = leg.agingTotal()
	return leg
}

func mustEnv(t *testing.T, k string) string {
	t.Helper()
	v, ok := os.LookupEnv(k)
	if !ok {
		t.Fatalf("%s is unset", k)
	}
	return v
}

// newJob plants a customer, one open job at the leg's branch and a lien job linked to it (link false
// leaves the lien job out). Each call is a fresh customer, so the legs never share an invoice.
func (l *rt2Leg) newJob(link bool) { l.newJobIn(link, "") }

// newJobIn is newJob with the job's ship to province (empty leaves it unnamed, the dealer's own).
func (l *rt2Leg) newJobIn(link bool, province string) {
	l.t.Helper()
	ctx := context.Background()
	l.n++
	if err := l.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		fmt.Sprintf("RT2-%d", l.n), fmt.Sprintf("RT2 Customer %d", l.n), l.branch).Scan(&l.cust); err != nil {
		l.t.Fatalf("plant the customer: %v", err)
	}
	var shipTo map[string]any
	if province != "" {
		shipTo = map[string]any{"province": province}
	}
	repo := customer.NewRepository(l.pool)
	job, err := repo.InsertJob(ctx, customer.JobWrite{
		CustomerID: l.cust, BranchID: l.branch, JobNo: fmt.Sprintf("RT2-JOB-%d", l.n),
		Fields: customer.JobFields{Name: "Driveway", ShipToAddress: shipTo},
	})
	if err != nil {
		l.t.Fatalf("plant the job: %v", err)
	}
	l.job = job.JobID
	l.lien = ""
	if link {
		if err := l.pool.QueryRow(ctx, `
			INSERT INTO lien_jobs (job_no, customer_id, branch_id, customer_job_id)
			VALUES ($1, $2::uuid, $3::uuid, $4::uuid) RETURNING id::text`,
			fmt.Sprintf("RT2-LIEN-%d", l.n), l.cust, l.branch, l.job).Scan(&l.lien); err != nil {
			l.t.Fatalf("plant the lien job: %v", err)
		}
	}
}

func (l *rt2Leg) writer(jobID string, subtotal, tax int64) *billing.InvoiceWriter {
	return newInvoiceWriter(l.pool).
		WithOrderSource(rt2Source{customer: l.cust, job: jobID, branch: l.branch, subtotal: subtotal, tax: tax}).
		WithCreditMemoPoster(w6cCreditMemoPoster{svc: l.money.glSvc, rules: gl.NewRepository(l.pool), words: newTaxWords(l.pool)})
}

func (l *rt2Leg) drain() {
	l.t.Helper()
	d := notify.NewDispatcher(l.money.outboxStore, slog.New(slog.NewTextHandler(fw109dWriter{l.t}, nil)))
	l.money.glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 30; i++ {
		if d.DrainOnce(context.Background()) == 0 {
			return
		}
	}
	l.t.Fatalf("the posting queue never drained")
}

func (l *rt2Leg) invoice(subtotal, tax int64) billing.InvoiceDoc {
	l.t.Helper()
	l.n++
	doc, err := l.writer(l.job, subtotal, tax).InvoiceOrder(context.Background(), fmt.Sprintf("rt2-order-%d", l.n))
	if err != nil {
		l.t.Fatalf("raise the invoice: %v", err)
	}
	l.drain()
	return doc
}

func (l *rt2Leg) holdbackOf(invoiceID string) int64 {
	l.t.Helper()
	var h int64
	if err := l.pool.QueryRow(context.Background(), `SELECT holdback_cents FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&h); err != nil {
		l.t.Fatalf("read the invoice's holdback: %v", err)
	}
	return h
}

// legs reads a source document's journal as account code -> {debit, credit}.
func (l *rt2Leg) legs(sourceRef string) map[string][2]int64 {
	l.t.Helper()
	rows, err := l.pool.Query(context.Background(), `
		SELECT a.code::text, COALESCE(SUM(ln.debit_cents),0), COALESCE(SUM(ln.credit_cents),0)
		  FROM gl_journals j JOIN gl_journal_lines ln ON ln.journal_id = j.id JOIN gl_accounts a ON a.id = ln.account_id
		 WHERE j.source_ref_id = $1::uuid AND j.status = 'POSTED'
		 GROUP BY a.code ORDER BY a.code`, sourceRef)
	if err != nil {
		l.t.Fatalf("read the journal legs: %v", err)
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var code string
		var d, c int64
		if err := rows.Scan(&code, &d, &c); err != nil {
			l.t.Fatalf("scan: %v", err)
		}
		out[code] = [2]int64{d, c}
	}
	return out
}

func (l *rt2Leg) ledgerNet() int64 {
	l.t.Helper()
	var n int64
	if err := l.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(CASE entry_type WHEN 'RETENTION' THEN amount_cents ELSE -amount_cents END), 0) FROM holdback_ledger`).Scan(&n); err != nil {
		l.t.Fatalf("read the ledger: %v", err)
	}
	return n
}

func (l *rt2Leg) ledgerRows() int {
	l.t.Helper()
	var n int
	if err := l.pool.QueryRow(context.Background(), `SELECT count(*) FROM holdback_ledger`).Scan(&n); err != nil {
		l.t.Fatalf("count the ledger: %v", err)
	}
	return n
}

func (l *rt2Leg) seams() TieSeams {
	glRepo := gl.NewRepository(l.pool)
	return TieSeams{
		Querier: l.pool, GL: l.money.glSvc, GLClock: l.money.glSvc, GLStore: glRepo,
		Billing: billing.NewRepository(l.pool), Holdback: lien.NewRepository(l.pool),
	}
}

// agingTotal is the AR aging's grand total now.
func (l *rt2Leg) agingTotal() int64 {
	l.t.Helper()
	rows, err := billing.NewCreditService(billing.NewRepository(l.pool)).Aging(fw1StaffCtx(context.Background()), time.Now(), "")
	if err != nil {
		l.t.Fatalf("aging: %v", err)
	}
	var due int64
	for _, r := range rows {
		due += r.TotalCents
	}
	return due
}

// ties asserts the two AR ties at the end of the current house month AND at the end of last month:
// aging to 1100 and the holdback ledger to 1150. The first call (the baseline) records the variance the
// seed leaves; every later call holds the variance to that baseline, so the leg's own movement ties.
func (l *rt2Leg) ties(step string) {
	l.t.Helper()
	zone, _ := time.LoadLocation("America/Toronto")
	now := time.Now().In(zone)
	thisEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, 1, 0).Add(-time.Second)
	lastEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone).Add(-time.Second)
	for _, end := range []time.Time{thisEnd, lastEnd} {
		m := TieMonth{Year: end.Year(), Month: int(end.Month()), MonthEnd: end}
		for _, tie := range []func(context.Context, TieSeams, TieMonth) (TieResult, error){TieHoldbackLedgerToGL1150, TieARAgingToGL1100} {
			res, err := tie(context.Background(), l.seams(), m)
			if err != nil {
				l.t.Fatalf("%s: tie at %s: %v", step, end.Format("2006-01-02"), err)
			}
			key := res.Name + end.Format("2006-01-02")
			if step == "baseline" {
				l.base[key] = res.VarianceCents
				continue
			}
			if res.VarianceCents != l.base[key] {
				l.t.Errorf("%s: %s (the variance moved by %d cents from the seed's %d)", step, res.Detail, res.VarianceCents-l.base[key], l.base[key])
			}
		}
	}
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// 1,000.00 at the province's 10 percent: 100.00 in 1150, 900.00 in 1100, a ledger row, the aging at 900.00.
func TestRT2_AThousandDollarInvoiceAtTenPercentWithholdsOneHundred(t *testing.T) {
	l := rt2Setup(t, "a")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	eq(t, "holdback_cents", l.holdbackOf(doc.InvoiceID), int64(10_000))
	j := l.legs(doc.InvoiceID)
	eq(t, "1150 debit", j["1150"][0], int64(10_000))
	eq(t, "1100 debit", j["1100"][0], int64(90_000))
	eq(t, "4000 credit", j["4000"][1], int64(100_000))
	eq(t, "the ledger holds the retention", l.ledgerNet(), int64(10_000))
	eq(t, "the aging shows the net due", l.agingTotal()-l.agingBase, int64(90_000))
	l.ties("after the invoice")
}

// The tax is billed on top and is never part of the holdback: the base is the subtotal.
func TestRT2_TheHoldbackBaseIsTheSubtotalNotTheTotal(t *testing.T) {
	l := rt2Setup(t, "tax")
	l.newJob(true)
	doc := l.invoice(100_000, 13_000)
	eq(t, "holdback_cents on a taxed invoice", l.holdbackOf(doc.InvoiceID), int64(10_000))
	j := l.legs(doc.InvoiceID)
	eq(t, "1150 debit", j["1150"][0], int64(10_000))
	eq(t, "1100 debit (total less holdback)", j["1100"][0], int64(103_000))
	eq(t, "2200 credit", j["2200"][1], int64(13_000))
	l.ties("after the taxed invoice")
}

func TestRT2_AJobOverrideReplacesTheProvincePercent(t *testing.T) {
	l := rt2Setup(t, "ovr")
	l.newJob(true)
	mustExec(t, context.Background(), l.pool,
		`INSERT INTO holdback_job_overrides (customer_job_id, branch_id, holdback_pct_bps) VALUES ($1::uuid, $2::uuid, 500)`, l.job, l.branch)
	doc := l.invoice(100_005, 0)
	// 100,005 cents at 5 percent is 5,000.25: the cent below the half goes down.
	eq(t, "holdback_cents at the job's 5 percent", l.holdbackOf(doc.InvoiceID), int64(5_000))
	eq(t, "1150 debit", l.legs(doc.InvoiceID)["1150"][0], int64(5_000))
	l.ties("after the override invoice")
}

func TestRT2_AZeroRuleAndNoJobWithholdNothing(t *testing.T) {
	l := rt2Setup(t, "zero")
	l.newJob(true)
	mustExec(t, context.Background(), l.pool,
		`INSERT INTO holdback_job_overrides (customer_job_id, branch_id, holdback_pct_bps) VALUES ($1::uuid, $2::uuid, 0)`, l.job, l.branch)
	zero := l.invoice(100_000, 0)
	eq(t, "a zero rule withholds nothing", l.holdbackOf(zero.InvoiceID), int64(0))
	if _, has := l.legs(zero.InvoiceID)["1150"]; has {
		t.Errorf("a zero rule posts no 1150 leg")
	}
	l.n++
	noJob, err := l.writer("", 100_000, 0).InvoiceOrder(context.Background(), fmt.Sprintf("rt2-nojob-%d", l.n))
	if err != nil {
		t.Fatalf("raise the jobless invoice: %v", err)
	}
	l.drain()
	eq(t, "an invoice with no job withholds nothing", l.holdbackOf(noJob.InvoiceID), int64(0))
	eq(t, "the ledger is empty", l.ledgerRows(), 0)
	l.ties("after the zero and jobless invoices")
}

// Withholding is a construction contract's retention: only a job a lien job is linked to withholds. An
// unlinked job (a retail patio) withholds nothing, reads no rule, and leaves the ledger and 1150 alone.
func TestRT2_AnUnlinkedJobWithholdsNothing(t *testing.T) {
	l := rt2Setup(t, "nolien")
	l.newJob(false)
	doc := l.invoice(100_000, 0)
	eq(t, "holdback_cents", l.holdbackOf(doc.InvoiceID), int64(0))
	j := l.legs(doc.InvoiceID)
	if _, has := j["1150"]; has {
		t.Errorf("an unlinked job posts no 1150 leg")
	}
	eq(t, "1100 debit", j["1100"][0], int64(100_000))
	eq(t, "the ledger has no row", l.ledgerRows(), 0)
	l.ties("after the unlinked invoice")
}

// A linked job withholds (the legs above); an unlinked job outside any seeded province is invoiced
// normally, never refused for want of a rule.
func TestRT2_AnUnlinkedJobInAProvinceWithNoRuleInvoicesNormally(t *testing.T) {
	l := rt2Setup(t, "noprov")
	l.newJobIn(false, "XX")
	doc := l.invoice(100_000, 0)
	eq(t, "holdback_cents", l.holdbackOf(doc.InvoiceID), int64(0))
	eq(t, "1100 debit", l.legs(doc.InvoiceID)["1100"][0], int64(100_000))
	l.ties("after the invoice in a province with no rule")
}

// A linked lien job in a province with no rule is refused in words.
func TestRT2_ALinkedJobInAProvinceWithNoRuleIsRefusedInWords(t *testing.T) {
	l := rt2Setup(t, "linkednoprov")
	l.newJobIn(true, "XX")
	l.n++
	_, err := l.writer(l.job, 100_000, 0).InvoiceOrder(context.Background(), fmt.Sprintf("rt2-order-%d", l.n))
	if err == nil || !strings.Contains(err.Error(), "no holdback rule for the province") {
		t.Fatalf("want the refusal in words, got %v", err)
	}
}

func (l *rt2Leg) creditMemo(invoiceID string, amount int64, key string) billing.InvoiceDoc {
	l.t.Helper()
	doc, err := l.writer(l.job, 100_000, 0).IssueCreditMemo(context.Background(), billing.CreditMemoInput{
		CustomerID: l.cust, BranchID: l.branch, ReversesInvoiceID: invoiceID, AmountCents: amount,
		Memo: "rt2", IdempotencyKey: key,
	})
	if err != nil {
		l.t.Fatalf("credit memo: %v", err)
	}
	if doc.GLJournalError != "" {
		l.t.Fatalf("the memo's journal: %s", doc.GLJournalError)
	}
	return doc
}

// A partial credit takes its proportion of the holdback from 1150 and the ledger; the rest takes the
// remainder to the cent; the books tie after each.
func TestRT2_CreditMemosReverseTheHoldbackInProportionAndTie(t *testing.T) {
	l := rt2Setup(t, "cm")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	m1 := l.creditMemo(doc.InvoiceID, 25_000, "rt2-cm-1")
	var memoHold int64
	if err := l.pool.QueryRow(context.Background(), `SELECT holdback_cents FROM invoices WHERE id = $1::uuid`, m1.InvoiceID).Scan(&memoHold); err != nil {
		t.Fatal(err)
	}
	eq(t, "the memo carries minus its share", memoHold, int64(-2_500))
	j := l.legs(m1.InvoiceID)
	eq(t, "1150 credit", j["1150"][1], int64(2_500))
	eq(t, "1100 credit", j["1100"][1], int64(22_500))
	eq(t, "4000 debit", j["4000"][0], int64(25_000))
	eq(t, "the ledger after the partial credit", l.ledgerNet(), int64(7_500))
	l.ties("after the partial credit")

	m2 := l.creditMemo(doc.InvoiceID, 0, "rt2-cm-2") // the rest
	j = l.legs(m2.InvoiceID)
	eq(t, "1150 credit on the rest", j["1150"][1], int64(7_500))
	eq(t, "1100 credit on the rest", j["1100"][1], int64(67_500))
	eq(t, "the ledger after the credits", l.ledgerNet(), int64(0))
	var g1150 int64
	g1150, _, err := gl.NewRepository(l.pool).AccountBalanceAsOf(context.Background(), "1150", time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "1150 after both credits", g1150, int64(0))
	l.ties("after the credits in full")
}

// A voided parked credit frees its room and moves nothing; an approved parked credit takes its holdback
// part off the ledger on the approval's transaction.
func TestRT2_AVoidedParkedCreditMovesNothingAndAnApprovedOneReversesTheHoldback(t *testing.T) {
	l := rt2Setup(t, "park")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	w := l.writer(l.job, 100_000, 0)
	ctx := context.Background()
	park := func(key string, amount int64) (billing.InvoiceDoc, billing.CreditMemoJournal) {
		tx, err := l.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		d, j, err := w.ParkCreditMemoTx(ctx, tx, billing.CreditMemoInput{
			CustomerID: l.cust, BranchID: l.branch, ReversesInvoiceID: doc.InvoiceID, AmountCents: amount, Memo: "rt2", IdempotencyKey: key,
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("park: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return d, j
	}
	inTx := func(fn func(tx pgx.Tx) error) {
		tx, err := l.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	d1, _ := park("rt2-park-1", 0) // the whole invoice, parked
	eq(t, "a parked credit moves nothing in the ledger", l.ledgerNet(), int64(10_000))
	inTx(func(tx pgx.Tx) error { return w.VoidParkedCreditMemoTx(ctx, tx, d1.InvoiceID) })
	eq(t, "a voided credit moves nothing in the ledger", l.ledgerNet(), int64(10_000))
	l.ties("after the void")

	d2, j2 := park("rt2-park-2", 50_000) // its room was freed by the void
	inTx(func(tx pgx.Tx) error { return w.FinalizeParkedCreditMemoTx(ctx, tx, d2.InvoiceID) })
	if _, err := (w6cCreditMemoPoster{svc: l.money.glSvc, rules: gl.NewRepository(l.pool), words: newTaxWords(l.pool)}).PostCreditMemoJournal(ctx, j2); err != nil {
		t.Fatalf("post the approved credit's journal: %v", err)
	}
	eq(t, "the approved credit took half the holdback", l.ledgerNet(), int64(5_000))
	l.ties("after the approved parked credit")
}

// The rule is read inside the writer's own transaction: on a pool of one connection the invoice
// completes (a second connection asked for would starve it, the car 18 shape).
func TestRT2_TheRuleReadRidesTheWritersTransactionOnAPoolOfOne(t *testing.T) {
	l := rt2Setup(t, "one")
	l.newJob(true)
	cfg, err := pgxpool.ParseConfig(l.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	w := newInvoiceWriter(one).WithOrderSource(rt2Source{customer: l.cust, job: l.job, branch: l.branch, subtotal: 100_000})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	doc, err := w.InvoiceOrder(ctx, "rt2-pool-of-one")
	if err != nil {
		t.Fatalf("the invoice on a pool of one: %v", err)
	}
	eq(t, "holdback_cents", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "the ledger row rode the same transaction", l.ledgerNet(), int64(10_000))
}
