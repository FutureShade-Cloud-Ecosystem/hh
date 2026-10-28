//go:build integration

package main

// fw1_month_walk_integration_test.go, FW1-00: the HH finance wave 1 exit test. One month of
// trading across Trenton (TRT) and Kingston (KGN) on a real migrated database seeded with the
// default build's Ontario Dibbits seed (never the -tags demo world), every step through the
// real services wired exactly as cmd/core wires them (wireMoney, wireOperations, wireTops over
// one mux; no fakes for money), and the GL consumer's posting loop run synchronously after each
// step, exactly as worker.go registers it, so every outbox event becomes a journal before the
// next assertion.
//
// The eight assertions (the wave brief section 2, items 1 to 8) are twelve named subtests (the
// bank story grew into three, and the periods roll forward and the month close are their own). A
// subtest whose name is a key of fw1WalkRed (fw1_walk_red.go) skips with "red until FW1-NN";
// each item deletes its own line when its assertion goes green. A second test guards the map:
// every key must name a real subtest, so a stale skip line fails.
//
// The walk carries two months (the split frame, FROM-PRINCIPAL 209 and 221): the TRADING month is the
// current, still running month in the house zone, and the BANK month is the last ended month,
// because a bank month signs off only once it has ended (a running month's sign off is refused).
// AR_aging_ties_to_GL_1100, customer_statement_equals_aging_and_subledger,
// stock_value_ties_to_GL_1200, HST_reconcile_ties,
// every_journal_line_carries_a_branch_and_branch_trial_balances_sum,
// walk_month_and_next_year_periods_open and
// month_close_passes_its_pre_flight_and_seals_the_month run in the trading month: AR, stock, HST and statements
// stay where their database stamps and period keys put them, and the trial balance, period and
// month close reads are keyed to the trading month. till_lodgement_posts_bank_1000_at_lodgement,
// bank_lodgement_match_posts_no_second_journal and
// bank_month_signs_off_at_zero_with_outstanding_items run in the bank month: the walk pins the
// till and payments clocks into it and the statement, the outstanding cheque and the bank charge
// carry its days, so the reconciliation opens and signs off an ended month rather than a running
// one. no_journal_waiting_or_dead_lettered is month free (the outbox carries both months'
// events); year_end_close_opens_next_year_and_keeps_income_statement works the year ends around
// the frame, refusing the running year's close and closing the last year before it that the books
// have not moved in, which the leg plants into a year of its own so the close does real work.
//
//	Run: DATABASE_URL='postgres://colton:fw1@127.0.0.1:57602/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run FW1MonthWalk -count=1 -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/portal"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// The walk's fixed facts: org and branches. The walk's two months (the trading month and the
// bank month, the last ended one) are derived per run in newFW1Walk.
const (
	fw1OrgID   = "88a20000-0000-4000-8000-0000000000f1"
	fw1OrgSlug = "fw1-walk"
	fw1TRT     = "5d1b3f6e-0000-4000-9000-000000000001"
	fw1KGN     = "5d1b3f6e-0000-4000-9000-000000000002"
)

// fw1AssertionNames is the walk's eight assertions in the wave brief's order. The parent test
// runs them from this list, and the skip map guard checks its keys against it.
func fw1AssertionNames() []string {
	return []string{
		"AR_aging_ties_to_GL_1100",
		"customer_statement_equals_aging_and_subledger",
		"stock_value_ties_to_GL_1200",
		"HST_reconcile_ties",
		"till_lodgement_posts_bank_1000_at_lodgement",
		"bank_lodgement_match_posts_no_second_journal",
		"bank_month_signs_off_at_zero_with_outstanding_items",
		"every_journal_line_carries_a_branch_and_branch_trial_balances_sum",
		"no_journal_waiting_or_dead_lettered",
		"walk_month_and_next_year_periods_open",
		"year_end_close_opens_next_year_and_keeps_income_statement",
		"month_close_passes_its_pre_flight_and_seals_the_month",
	}
}

type fw1Walk struct {
	t     *testing.T
	pool  *pgxpool.Pool
	mux   *http.ServeMux
	sg    *testkit.StaffGuards
	money *moneySpine
	ops   *operationsSpine
	tops  *topsSpine
	disp  *notify.Dispatcher

	prod map[string]string // sku -> product id
	cust map[string]string // account_no -> customer id

	tillTRT, tillKGN string
	year             int
	month            int
	monthEnd         time.Time

	// The bank month is the LAST ENDED month in the house zone (FROM-PRINCIPAL 169). The walk's
	// bank story trades there, so its reconciliation signs off an ended month rather than a
	// running one, while the AR, stock, HST and statement legs keep trading in the current month.
	bankYear, bankMonth int
	bankMonthEnd        time.Time // the bank month's own last instant, in the house zone
	bankDay             time.Time // the instant the walk pins the till and payments clocks to

	// figures assertion 5 names in its failure message
	outstandingChequeCents           int64
	depositInTransitCents            int64
	statementBalanceCents            int64
	bankAccountID                    string
	lodgedChequePaymentID            string
	lodgedChequeInvoiceID            string
	lodgedChequeMovementID           string
	lodgedChequeCents                int64
	journals1000BeforeLodgementMatch int
	journals1000AfterLodgementMatch  int

	nullBranchLines int // snapshot taken before the year end close posts its own lines
	seededInvoiceID []string
}

// fw1TB is the testing surface each assertion writes to. *testing.T satisfies it directly, and
// fw1Probe captures failures without failing the parent test when checking that a skipped
// assertion is still red.
type fw1TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Fatal(args ...any)
}

type fw1Probe struct {
	errs []string
}

type fw1ProbeAbort struct{}

func (p *fw1Probe) Helper() {}
func (p *fw1Probe) Errorf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf(format, args...))
}
func (p *fw1Probe) Fatalf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf(format, args...))
	panic(fw1ProbeAbort{})
}
func (p *fw1Probe) Fatal(args ...any) {
	p.errs = append(p.errs, fmt.Sprint(args...))
	panic(fw1ProbeAbort{})
}
func (p *fw1Probe) run(fn func(fw1TB)) (failed bool, msgs []string) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fw1ProbeAbort); !ok {
				panic(r)
			}
		}
		failed = len(p.errs) > 0
		msgs = p.errs
	}()
	fn(p)
	return len(p.errs) > 0, p.errs
}

func fw1Dollars(c int64) string { return fmt.Sprintf("$%.2f", float64(c)/100) }

// fw1StaffCtx is the global staff context the GL's close and tie reads stamp their actor from.
func fw1StaffCtx(ctx context.Context) context.Context {
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000006", Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// fw1ControllerCtx is the walk's Financial Controller. FW1-09c (FROM-PRINCIPAL 222) narrowed the year
// end close to that role, so the leg that seals a year runs as the Controller, the way the service
// demands; the walk's other legs keep the plain staff context above (they read).
func fw1ControllerCtx(ctx context.Context) context.Context {
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000006", RoleCode: "FINANCIAL_CONTROLLER",
		Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// fw1TestWriter routes the wired services' logs into the test log, so a step's 500 names its
// cause without printing outside the test run.
type fw1TestWriter struct{ t *testing.T }

func (w fw1TestWriter) Write(b []byte) (int, error) {
	w.t.Logf("%s", bytes.TrimRight(b, "\n"))
	return len(b), nil
}

// newFW1Walk builds the throwaway database (migrated from zero, then the Ontario Dibbits seed),
// the org the seed's branches carry, and the full served wiring over one mux with the kit's
// staff guards double (the only double; every money path is the real one).
func newFW1Walk(t *testing.T) *fw1Walk {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw1walk")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	// The seed runs before any org exists; give the branches one org (as the door walk's fixture
	// does) and link the caller's platform subject to the seeded financial controller login.
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'FW1 Walk Org') ON CONFLICT (id) DO NOTHING`, fw1OrgID, fw1OrgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, fw1OrgID)
	mustExec(t, ctx, pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)

	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, fw1OrgSlug, true)
	sg.Harness.Resolver.Set(fw1OrgSlug, fw1OrgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	log := slog.New(slog.NewTextHandler(fw1TestWriter{t}, nil))
	// The card rail needs a configured merchant before any card capture is dispatched; the
	// deterministic stub processor over a throwaway key, exactly as the card wiring test plants
	// one. The key is the same one the wiring hands the vault.
	vaultKey := dashVaultKey(t)
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		t.Fatalf("the walk's vault: %v", err)
	}
	if err := (payment.NewStore(pool, vault)).Configure(ctx, "run_payments", "MID-FW1WALK", "CAD", "", 0, false,
		map[string]string{"api_key": "fw1-walk-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the walk's card rail: %v", err)
	}
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "fw1-walk-fx-ticket", belt.outbox)
	tops := wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)

	// The GL consumer and its neighbours, registered exactly as worker.go registers them, then
	// drained synchronously by drain() after every step.
	d := notify.NewDispatcher(money.outboxStore, log)
	portal.RegisterNotificationHandlers(d, tops.notifier, "", log)
	money.glSvc.RegisterPostingHandlers(d)
	money.inventorySvc.RegisterPostingHandlers(d)
	order.NewLifecycleConsumer(money.orderSvc, tops.billingRepo, log).RegisterHandlers(d)
	registerOrderEventOwners(d, log)
	messaging.NewConsumer(ops.messagingSvc, log).RegisterHandlers(d)
	messaging.NewPortalBridge(ops.messagingSvc, log).RegisterHandlers(d)

	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("loading dealer zone America/Toronto: %v", err)
	}
	now := time.Now().In(zone)
	// The last ended month in the house zone: the current month's first instant less one second,
	// so on a month's own first day the bank month is still the month before. The bank month has
	// fully ended however early in the month the walk runs.
	bankMonthEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone).Add(-time.Second)
	w := &fw1Walk{
		t: t, pool: pool, mux: mux, sg: sg, money: money, ops: ops, tops: tops, disp: d,
		prod: map[string]string{}, cust: map[string]string{},
		year: now.Year(), month: int(now.Month()),
		monthEnd: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, zone).
			AddDate(0, 1, 0).Add(-time.Second),
		bankYear:     bankMonthEnd.Year(),
		bankMonth:    int(bankMonthEnd.Month()),
		bankMonthEnd: bankMonthEnd,
		bankDay:      time.Date(bankMonthEnd.Year(), bankMonthEnd.Month(), 5, 10, 0, 0, 0, zone),
	}

	// Pin the till and payments clocks through their existing WithClock setters (FROM-PRINCIPAL
	// 209). The instances are the wiring's own, carried on the operations spine as unexported
	// fields; production builds and registers both exactly as before, and the walk alone moves
	// their clocks into the bank month, so the till's count and close stamps and the payment
	// reversal's stamps land in the month the bank story trades in.
	w.ops.tillSvc.WithClock(func() time.Time { return w.bankDay })
	w.ops.paymentsReverseSvc.WithClock(func() time.Time { return w.bankDay })
	for _, sku := range []string{"DBS-GATOR-SAND", "DBS-INLITE-LED", "DBS-UL-BRUSSELS", "DBS-TB-BLU60", "DBS-SIKA-SEALER"} {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM products WHERE sku = $1`, sku).Scan(&id); err != nil {
			t.Fatalf("seed product %s: %v", sku, err)
		}
		w.prod[sku] = id
	}
	for _, acct := range []string{"CUST-1001", "CUST-1002", "CUST-1003", "CUST-1004", "CUST-1005", "CUST-1007"} {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE account_no = $1`, acct).Scan(&id); err != nil {
			t.Fatalf("seed customer %s: %v", acct, err)
		}
		w.cust[acct] = id
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '1000'`).Scan(&w.bankAccountID); err != nil {
		t.Fatalf("the operating bank account: %v", err)
	}
	return w
}

// drain runs the dispatcher's posting loop until the outbox holds nothing it can claim.
func (w *fw1Walk) drain() {
	w.t.Helper()
	for i := 0; i < 30; i++ {
		if w.disp.DrainOnce(context.Background()) == 0 {
			return
		}
	}
}

// do is one authenticated staff call over the served mux.
func (w *fw1Walk) do(method, path string, body any) (int, []byte) {
	w.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer "+w.sg.Harness.Minter.StaffToken(w.t, testkit.SubSalesManager))
	req.Header.Set(authz.HeaderXOrg, fw1OrgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	w.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.Bytes()
}

// ok is a step call that must succeed; a failed step fails the test immediately so no assertion
// can pass on a reduced scenario.
func (w *fw1Walk) ok(step, method, path string, body any) []byte {
	w.t.Helper()
	code, out := w.do(method, path, body)
	if code >= 300 {
		w.t.Fatalf("%s: %s %s answered %d: %s", step, method, path, code, out)
	}
	return out
}

func (w *fw1Walk) one(q string, args ...any) string {
	w.t.Helper()
	var v string
	if err := w.pool.QueryRow(context.Background(), q, args...).Scan(&v); err != nil {
		w.t.Fatalf("read (%.80s): %v", q, err)
	}
	return v
}

func (w *fw1Walk) sale(step string, body map[string]any) map[string]any {
	w.t.Helper()
	out := w.ok(step, http.MethodPost, "/api/v1/pos/sales", body)
	if out == nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

// countBody builds a till count from the session's own Z report expectations, with a named over
// or short on the cash drawer (FW1-13's screen blind count is the nearest real call today).
func (w *fw1Walk) countBody(sessionID string, overCents int64) map[string]any {
	w.t.Helper()
	out := w.ok("z report "+sessionID, http.MethodGet, "/api/v1/till/sessions/"+sessionID+"/zreport", nil)
	var z struct {
		PerTender []struct {
			Method        string `json:"method"`
			ExpectedCents int64  `json:"expected_cents"`
		} `json:"per_tender"`
		Cash struct {
			ExpectedCents     int64 `json:"expected_cents"`
			OpeningFloatCents int64 `json:"opening_float_cents"`
		} `json:"cash"`
	}
	if out != nil {
		_ = json.Unmarshal(out, &z)
	}
	tenders := []map[string]any{}
	for _, p := range z.PerTender {
		actual := p.ExpectedCents
		if p.Method == "CASH" {
			actual += overCents
		}
		tenders = append(tenders, map[string]any{"method": p.Method, "actual_cents": actual})
	}
	if len(tenders) == 0 {
		tenders = append(tenders, map[string]any{"method": "CASH", "actual_cents": z.Cash.ExpectedCents + overCents})
	}
	return map[string]any{
		"tenders": tenders, "closing_float_cents": z.Cash.OpeningFloatCents,
	}
}

// TestFW1MonthWalk is the wave's exit test: the month script, then the eight assertions.
func TestFW1MonthWalk(t *testing.T) {
	w := newFW1Walk(t)
	ctx := context.Background()

	// -- opening balances -------------------------------------------------------------
	// The seed plants open account invoices with no journal, and opening stock with no 1200
	// balance. Both are loaded as opening trial balance entries against 3000 (opening equity),
	// never into the walk month's 4000 revenue or 2200 HST payable.
	seedInvRows, err := w.pool.Query(ctx, `SELECT id::text FROM invoices`)
	if err != nil {
		t.Fatalf("reading seeded invoices: %v", err)
	}
	for seedInvRows.Next() {
		var id string
		if err := seedInvRows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		w.seededInvoiceID = append(w.seededInvoiceID, id)
	}
	seedInvRows.Close()

	// The walk's opening AR posting covers ONLY documents marked as opening balances (the seed's
	// metadata.opening_balance marker, FROM-PRINCIPAL 177 and 180): a document the migrations gave
	// a finalize journal of its own (the 0055 demo pair) is already in the ledger and is not posted
	// again here.
	openRows, err := w.pool.Query(ctx, `
		SELECT branch_id::text, COALESCE(SUM(balance_cents),0)
		  FROM invoices
		 WHERE status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents > 0
		   AND COALESCE(metadata->>'opening_balance','') = 'true'
		 GROUP BY branch_id`)
	if err != nil {
		t.Fatalf("the opening AR read: %v", err)
	}
	type openAR struct {
		branch string
		bal    int64
	}
	var opens []openAR
	for openRows.Next() {
		var o openAR
		if err := openRows.Scan(&o.branch, &o.bal); err != nil {
			t.Fatal(err)
		}
		opens = append(opens, o)
	}
	openRows.Close()
	var firstJournalID string
	for i, o := range opens {
		jid, err := w.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
			Source: "MANUAL", SourceRefID: "88a20000-0000-4000-8000-0000000000a2",
			IdempotencyKey: fmt.Sprintf("fw1:opening:ar:%d", i),
			Memo:           "opening accounts receivable, the seeded open invoices", BranchID: o.branch,
			Lines: []gl.ExternalLine{
				{AccountCode: "1100", DebitCents: o.bal, Memo: "opening AR"},
				{AccountCode: "3000", CreditCents: o.bal, Memo: "opening equity"},
			},
		})
		if err != nil {
			if item, red := fw1WalkRed["walk_month_and_next_year_periods_open"]; red {
				t.Skipf("red until %s: the walk month %04d-%02d refused the first opening posting (%v); this skip hides the other lines' probes for that month", item, w.year, w.month, err)
			}
			t.Fatalf("the opening AR journal for %s: %v", o.branch, err)
		}
		if firstJournalID == "" {
			firstJournalID = jid
		}
	}
	var walkPeriodOpen int
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year = $1 AND month = $2 AND status = 'OPEN'`, w.year, w.month).Scan(&walkPeriodOpen); err != nil {
		t.Fatalf("the walk month period read: %v", err)
	}
	if walkPeriodOpen == 0 {
		if item, red := fw1WalkRed["walk_month_and_next_year_periods_open"]; red {
			t.Skipf("red until %s: the walk month %04d-%02d has no OPEN fiscal period after the opening journals posted; this skip hides the other lines' probes for that month", item, w.year, w.month)
		}
		t.Fatalf("the walk month %04d-%02d has no OPEN fiscal period after the opening journals posted", w.year, w.month)
	}
	// Unskipped frame guard: the walk's first posted journal's period_id must resolve to (w.year, w.month).
	var firstYear, firstMonth int
	if err := w.pool.QueryRow(ctx, `
		SELECT fp.year, fp.month
		  FROM gl_journals j
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.id = $1`, firstJournalID).Scan(&firstYear, &firstMonth); err != nil {
		t.Fatalf("reading the walk's first journal period: %v", err)
	}
	if firstYear != w.year || firstMonth != w.month {
		t.Fatalf("walk frame guard: first journal posted into %04d-%02d, want walk frame %04d-%02d", firstYear, firstMonth, w.year, w.month)
	}
	// Load the seeded opening AR into the customer_transactions subledger alongside the GL 1100
	// opening journals (one INVOICE row per seeded open invoice, matching ReceivablesLoadWriter).
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO customer_transactions (
		    customer_id, txn_type, amount_cents, balance_after,
		    source_type, source_ref_id, branch_id, memo, occurred_at
		)
		SELECT
		    i.customer_id,
		    'INVOICE',
		    i.balance_cents,
		    SUM(i.balance_cents) OVER (
		        PARTITION BY i.customer_id
		        ORDER BY COALESCE(i.finalized_at, i.created_at), i.id
		    ),
		    'invoice',
		    i.id,
		    i.branch_id,
		    'Opening AR invoice ' || i.invoice_no,
		    COALESCE(i.finalized_at, i.created_at)
		FROM invoices i
		WHERE i.status IN ('FINALIZED', 'PARTIALLY_PAID') AND i.balance_cents > 0
		ORDER BY COALESCE(i.finalized_at, i.created_at), i.id`); err != nil {
		t.Fatalf("the opening AR customer_transactions load: %v", err)
	}
	stockValue := func(branch string) int64 {
		var c int64
		if err := w.pool.QueryRow(ctx, `
			SELECT COALESCE(ROUND(SUM(sb.on_hand_base * c.avg_unit_cost_cents))::bigint, 0)
			  FROM stock_balances sb
			  JOIN product_branch_costs c ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
			 WHERE sb.on_hand_base != 0 AND sb.branch_id = $1`, branch).Scan(&c); err != nil {
			t.Fatalf("stock value: %v", err)
		}
		return c
	}
	for _, b := range []string{fw1TRT, fw1KGN} {
		v := stockValue(b)
		if _, err := w.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
			Source: "MANUAL", SourceRefID: "88a20000-0000-4000-8000-0000000000a1", IdempotencyKey: "fw1:opening:" + b,
			Memo: "opening balances", BranchID: b,
			Lines: []gl.ExternalLine{
				{AccountCode: "1200", DebitCents: v, Memo: "opening stock"},
				{AccountCode: "3000", CreditCents: v, Memo: "opening equity"},
			},
		}); err != nil {
			t.Fatalf("the opening stock journal for %s: %v", b, err)
		}
	}
	w.drain()

	// -- the month --------------------------------------------------------------------
	// Till sessions at both branches.
	w.ok("open till TRT", http.MethodPost, "/api/v1/till/sessions", map[string]any{"branch_id": fw1TRT, "station": "MAIN", "float_cents": 20000})
	w.tillTRT = w.one(`SELECT id::text FROM till_sessions WHERE branch_id = $1 AND status = 'OPEN' ORDER BY opened_at DESC LIMIT 1`, fw1TRT)
	w.ok("open till KGN", http.MethodPost, "/api/v1/till/sessions", map[string]any{"branch_id": fw1KGN, "station": "MAIN", "float_cents": 15000})
	w.tillKGN = w.one(`SELECT id::text FROM till_sessions WHERE branch_id = $1 AND status = 'OPEN' ORDER BY opened_at DESC LIMIT 1`, fw1KGN)
	w.drain()

	// Counter cash sale at Trenton (a retail homeowner, gator sand by the bag).
	w.sale("counter cash sale TRT", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b1", "branch_id": fw1TRT, "till_session_id": w.tillTRT,
		"customer_id": w.cust["CUST-1007"], "tender": "CASH", "sale_type": "COLLECT_NOW",
		"lines": []map[string]any{{"product_id": w.prod["DBS-GATOR-SAND"], "qty_entered": 2, "uom_entered": "BAG"}},
	})
	w.drain()

	// Counter card sale at Kingston (a walk-in, card on the simulated rail).
	w.sale("counter card sale KGN", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b2", "branch_id": fw1KGN, "till_session_id": w.tillKGN,
		"tender": "CARD", "payment_token": "tok_fw1_walk", "sale_type": "COLLECT_NOW",
		"lines": []map[string]any{{"product_id": w.prod["DBS-INLITE-LED"], "qty_entered": 1, "uom_entered": "EA"}},
	})
	w.drain()

	// A delivered order on account at Trenton (Quinte Hardscapes).
	delivered := w.sale("delivered order TRT", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b3", "branch_id": fw1TRT, "till_session_id": w.tillTRT,
		"customer_id": w.cust["CUST-1001"], "tender": "ACCOUNT", "sale_type": "DELIVERED",
		"lines": []map[string]any{{"product_id": w.prod["DBS-UL-BRUSSELS"], "qty_entered": 100, "uom_entered": "SF"}},
	})
	w.drain()

	// A collect later order on account at Trenton (Bay of Quinte).
	w.sale("collect later order TRT", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b4", "branch_id": fw1TRT, "till_session_id": w.tillTRT,
		"customer_id": w.cust["CUST-1002"], "tender": "ACCOUNT", "sale_type": "COLLECT_LATER",
		"lines": []map[string]any{{"product_id": w.prod["DBS-TB-BLU60"], "qty_entered": 50, "uom_entered": "SF"}},
	})
	w.drain()

	// A Cash On Demand order at Kingston (Cataraqui, terms set to same day for the posture).
	mustExec(t, ctx, w.pool, `UPDATE customers SET payment_terms_days = 0 WHERE id = $1`, w.cust["CUST-1004"])
	w.sale("cash on demand order KGN", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b5", "branch_id": fw1KGN, "till_session_id": w.tillKGN,
		"customer_id": w.cust["CUST-1004"], "tender": "CHEQUE", "sale_type": "COD", "fulfilment": "PICKUP",
		"lines": []map[string]any{{"product_id": w.prod["DBS-GATOR-SAND"], "qty_entered": 4, "uom_entered": "BAG"}},
	})
	w.drain()

	// The delivered order's account invoice, paid half at the Money Desk and the rest through
	// Counter Till Pay balance (the pay balance receipt is FW1-02's own journal).
	deliveredInv, _ := delivered["invoice_id"].(string)
	deliveredOrder, _ := delivered["order_id"].(string)
	if deliveredInv == "" || deliveredOrder == "" {
		t.Fatalf("delivered order returned no invoice_id or order_id: %v", delivered)
	}
	var total int64
	if err := w.pool.QueryRow(ctx, `SELECT total_cents FROM invoices WHERE id = $1`, deliveredInv).Scan(&total); err != nil {
		t.Fatal(err)
	}
	w.ok("money desk part payment", http.MethodPost, "/api/v1/ar/payments/apply", map[string]any{
		"customer_id": w.cust["CUST-1001"], "method": "EFT", "amount_cents": total / 2,
		"invoice_ids": []string{deliveredInv}, "idempotency_key": "fw1-desk-part",
	})
	w.drain()
	w.ok("counter pay balance remainder", http.MethodPost, "/api/v1/pos/pay-balance", map[string]any{
		"order_id": deliveredOrder, "method": "CASH", "amount_cents": total - total/2,
		"till_session_id": w.tillTRT, "idempotency_key": "fw1-pay-balance",
	})
	w.drain()

	// An untaxed invoice: the counter's First Nations point of sale relief (the reason code the
	// tax engine owns; FW1-02's untaxed posting and FW1-08's branch on the relief legs).
	w.sale("untaxed invoice TRT", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b6", "branch_id": fw1TRT, "till_session_id": w.tillTRT,
		"customer_id": w.cust["CUST-1007"], "tender": "CASH", "sale_type": "COLLECT_NOW",
		"relief": map[string]any{"purchaser_type": "FIRST_NATIONS", "declared_use": "delivery to reserve",
			"fulfilment": "PICKUP", "reason_code": "ON_FN_POS_8"},
		"lines": []map[string]any{{"product_id": w.prod["DBS-GATOR-SAND"], "qty_entered": 1, "uom_entered": "BAG"}},
	})
	w.drain()

	// A cheque lodged against the collect later invoice, then bounced (NSF).
	var invC2 string
	var balC2 int64
	if err := w.pool.QueryRow(ctx, `SELECT id::text, balance_cents FROM invoices
		WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')
		ORDER BY issue_date DESC, invoice_no DESC LIMIT 1`, w.cust["CUST-1002"]).Scan(&invC2, &balC2); err != nil {
		t.Fatalf("cheque lodged: no open invoice for CUST-1002: %v", err)
	}
	chequeOut := w.ok("cheque lodged", http.MethodPost, "/api/v1/ar/payments/apply", map[string]any{
		"customer_id": w.cust["CUST-1002"], "method": "CHEQUE", "amount_cents": balC2,
		"invoice_ids": []string{invC2}, "idempotency_key": "fw1-cheque", "reference": "CHQ-101",
	})
	w.drain()
	var chequeAns struct {
		PaymentID string `json:"payment_id"`
	}
	if err := json.Unmarshal(chequeOut, &chequeAns); err != nil || chequeAns.PaymentID == "" {
		t.Fatalf("cheque lodged returned no payment_id: %s", chequeOut)
	}
	w.ok("cheque bounced", http.MethodPost, "/api/v1/ar/payments/"+chequeAns.PaymentID+"/nsf", map[string]any{"reason": "NSF, drawer's bank refused"})
	w.drain()

	// Beside the desk cheque (CHQ-101, never lodged through a till lodgement): a cheque payment
	// taken on the Trenton drawer, lodged through a till lodgement (posting DR 1000 / CR 1020 dated
	// by its business day in the house zone and marking the payment DEPOSITED in the same
	// transaction under FW1-13), and then bounced BEFORE the bank match (FROM-PRINCIPAL 120).
	lodgedChequeSale := w.sale("cheque taken on till TRT", map[string]any{
		"idempotency_key": "88a20000-0000-4000-8000-0000000000b7", "branch_id": fw1TRT, "till_session_id": w.tillTRT,
		"customer_id": w.cust["CUST-1003"], "tender": "CHEQUE", "sale_type": "COLLECT_LATER",
		"lines": []map[string]any{{"product_id": w.prod["DBS-GATOR-SAND"], "qty_entered": 2, "uom_entered": "BAG"}},
	})
	w.drain()
	w.lodgedChequePaymentID, _ = lodgedChequeSale["payment_id"].(string)
	w.lodgedChequeInvoiceID, _ = lodgedChequeSale["invoice_id"].(string)
	if w.lodgedChequePaymentID == "" || w.lodgedChequeInvoiceID == "" {
		t.Fatalf("cheque taken on till TRT returned no payment_id or invoice_id: %v", lodgedChequeSale)
	}
	if err := w.pool.QueryRow(ctx, `SELECT amount_cents FROM payments WHERE id = $1`, w.lodgedChequePaymentID).Scan(&w.lodgedChequeCents); err != nil {
		t.Fatalf("reading lodged cheque payment amount: %v", err)
	}
	w.ok("till cheque lodgement TRT", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/movements", map[string]any{
		"movement_type": "LODGEMENT", "method": "CHEQUE", "amount_cents": w.lodgedChequeCents,
		"reason": "fw1 walk cheque lodgement", "reference": "DEP-TRT-CHQ-1",
		"payment_ids": []string{w.lodgedChequePaymentID},
	})
	w.drain()
	w.lodgedChequeMovementID = w.one(`SELECT id::text FROM till_movements WHERE reference = 'DEP-TRT-CHQ-1' ORDER BY id LIMIT 1`)
	w.ok("lodged cheque bounced before match", http.MethodPost, "/api/v1/ar/payments/"+w.lodgedChequePaymentID+"/nsf", map[string]any{
		"reason": "NSF after lodgement, before bank match",
	})
	w.drain()

	// A reversal and a re-application of held money: Frontenac pays $2,000.00 of the seeded
	// open invoice, the payment is reversed (the money holds in 2310), then re-applied.
	seedInv := w.one(`SELECT id::text FROM invoices WHERE invoice_no = 'INV-2026-0002'`)
	heldOut := w.ok("payment to reverse", http.MethodPost, "/api/v1/ar/payments/apply", map[string]any{
		"customer_id": w.cust["CUST-1005"], "method": "EFT", "amount_cents": 200000,
		"invoice_ids": []string{seedInv}, "idempotency_key": "fw1-held",
	})
	w.drain()
	var heldAns struct {
		PaymentID string `json:"payment_id"`
	}
	if err := json.Unmarshal(heldOut, &heldAns); err != nil || heldAns.PaymentID == "" {
		t.Fatalf("payment to reverse returned no payment_id: %s", heldOut)
	}
	w.ok("reversal of held money", http.MethodPost, "/api/v1/ar/payments/"+heldAns.PaymentID+"/reverse", map[string]any{"reason": "fw1 walk reversal"})
	w.drain()
	w.ok("re-application of held money", http.MethodPost, "/api/v1/ar/payments/"+heldAns.PaymentID+"/reapply", map[string]any{
		"reason": "fw1 walk reapplication", "idempotency_key": "fw1-reapply",
		"allocations": []map[string]any{{"invoice_id": seedInv, "amount_cents": 200000}},
	})
	w.drain()

	// A finance issued credit memo (FW1-12 gives this its route and screen; today the desk's
	// own writer is the nearest real call, decorated exactly as worker.go decorates it).
	writer := newInvoiceWriter(w.pool).
		WithCreditMemoPoster(w6cCreditMemoPoster{svc: w.money.glSvc, rules: gl.NewRepository(w.pool), words: newTaxWords(w.pool)}).
		WithDeliveredOrders(w.money.orderSvc).
		WithJournalKeyReader(w.money.glSvc)
	if _, err := writer.IssueCreditMemo(ctx, billing.CreditMemoInput{
		CustomerID: w.cust["CUST-1003"], BranchID: fw1KGN, AmountCents: 5000,
		Memo: "FW1 walk finance credit", IdempotencyKey: "fw1-credit-memo",
	}); err != nil {
		t.Fatalf("finance credit memo: %v", err)
	}
	w.drain()

	// Till movements at Trenton: paid in, paid out, a lodgement that reaches the statement; and
	// one at Kingston that stays in transit (the deposit in transit).
	w.ok("till paid in", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/movements", map[string]any{"movement_type": "PAID_IN", "method": "CASH", "amount_cents": 5000, "reason": "fw1 walk"})
	w.ok("till paid out", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/movements", map[string]any{"movement_type": "PAID_OUT", "method": "CASH", "amount_cents": 3000, "reason": "fw1 walk"})
	w.ok("till lodgement TRT", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/movements", map[string]any{"movement_type": "LODGEMENT", "method": "CASH", "amount_cents": 40000, "reason": "fw1 walk", "reference": "DEP-TRT-1"})
	w.ok("till lodgement KGN in transit", http.MethodPost, "/api/v1/till/sessions/"+w.tillKGN+"/movements", map[string]any{"movement_type": "LODGEMENT", "method": "CASH", "amount_cents": 25000, "reason": "fw1 walk", "reference": "DEP-KGN-1"})
	w.drain()

	// A posted vendor bill: PO to the seeded Techo Bloc vendor, received, billed, posted.
	vendor := w.one(`SELECT id::text FROM vendors WHERE vendor_no = 'V-TECHO'`)
	poExpected := fmt.Sprintf("%04d-%02d-20", w.year, w.month)
	billDate := fmt.Sprintf("%04d-%02d-15", w.year, w.month)
	billDue := time.Date(w.year, time.Month(w.month), 15, 0, 0, 0, 0, w.monthEnd.Location()).AddDate(0, 0, 30).Format("2006-01-02")
	poOut := w.ok("purchase order", http.MethodPost, "/api/v1/purchasing/pos", map[string]any{
		"vendor_id": vendor, "branch_id": fw1TRT, "expected_at": poExpected,
		"lines": []map[string]any{{"product_id": w.prod["DBS-TB-BLU60"], "description": "Blu 60", "qty_ordered_base": 100, "uom": "PC", "unit_cost_cents": 500}},
	})
	var poResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(poOut, &poResp); err != nil || poResp.ID == "" {
		t.Fatalf("purchase order returned no id: %s", poOut)
	}
	w.ok("place the purchase order", http.MethodPost, "/api/v1/purchasing/pos/"+poResp.ID+"/transition", map[string]any{"to": "ORDERED"})
	lineID := w.one(`SELECT id::text FROM purchasing_po_lines WHERE po_id = $1 ORDER BY line_no LIMIT 1`, poResp.ID)
	w.ok("receive the purchase order", http.MethodPost, "/api/v1/purchasing/pos/"+poResp.ID+"/receive", map[string]any{
		"lines": []map[string]any{{"po_line_id": lineID, "qty_base": 100}},
	})
	w.drain()
	billOut := w.ok("vendor bill", http.MethodPost, "/api/v1/purchasing/bills", map[string]any{
		"bill_no": "TECHO-FW1-1", "po_id": poResp.ID, "bill_date": billDate, "due_date": billDue, "tax_cents": 650,
		"lines": []map[string]any{{"po_line_id": lineID, "description": "Blu 60", "qty_billed_base": 100, "unit_cost_cents": 500}},
	})
	var billResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(billOut, &billResp); err != nil || billResp.ID == "" {
		t.Fatalf("vendor bill returned no id: %s", billOut)
	}
	w.ok("post the vendor bill", http.MethodPost, "/api/v1/purchasing/bills/"+billResp.ID+"/post", map[string]any{"accept_variance": true, "note": "fw1 walk"})
	w.drain()

	// A stock transfer between the branches (Sika sealer, zero stock at Kingston by design).
	trOut := w.ok("stock transfer", http.MethodPost, "/api/v1/inventory/transfers", map[string]any{
		"from_branch_id": fw1TRT, "to_branch_id": fw1KGN, "notes": "fw1 walk",
		"lines": []map[string]any{{"product_id": w.prod["DBS-SIKA-SEALER"], "qty_base": 20}},
	})
	var trResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(trOut, &trResp); err != nil || trResp.ID == "" {
		t.Fatalf("stock transfer returned no id: %s", trOut)
	}
	w.ok("ship the stock transfer", http.MethodPost, "/api/v1/inventory/transfers/"+trResp.ID+"/ship", map[string]any{})
	w.drain()
	w.ok("receive the stock transfer", http.MethodPost, "/api/v1/inventory/transfers/"+trResp.ID+"/receive", map[string]any{})
	w.drain()

	// The bank month. The walk's bank story trades in the LAST ENDED month in the house zone
	// (FROM-PRINCIPAL 169), so its reconciliation signs off an ended month rather than a running
	// one: the statement's lines and the walk's own bank journals carry the bank month's days.
	// An outstanding cheque (a vendor payment the bank has not seen); a deposit in transit (the
	// Kingston lodgement, not on the statement); a statement that carries the Trenton lodgement
	// and a bank charge, with the bank charge booked on the ledger and matched to its statement
	// line. The walk's own journals post through the GL provider seam with the document's own
	// day (FROM-PRINCIPAL 209 keeps the manual journal route dateless), and posted_at rides the
	// document date, so the bank month's own as-of read sees them.
	w.outstandingChequeCents = 12500
	w.depositInTransitCents = 25000
	if _, err := w.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "fw1:outstanding:cheque",
		Memo: "fw1 walk vendor cheque, not yet presented", BranchID: fw1TRT,
		DocumentDate: time.Date(w.bankYear, time.Month(w.bankMonth), 5, 0, 0, 0, 0, w.bankMonthEnd.Location()),
		Lines: []gl.ExternalLine{
			{AccountCode: "2100", DebitCents: w.outstandingChequeCents, Memo: "vendor payment"},
			{AccountCode: "1000", CreditCents: w.outstandingChequeCents, Memo: "cheque written"},
		},
	}); err != nil {
		t.Fatalf("the outstanding cheque journal: %v", err)
	}
	w.drain()
	importBody := "Date,Description,Amount,Reference\n" +
		fmt.Sprintf("%04d-%02d-10,DEPOSIT TRT,400.00,DEP-TRT-1\n", w.bankYear, w.bankMonth) +
		fmt.Sprintf("%04d-%02d-12,BANK CHARGE,-6.50,SVC\n", w.bankYear, w.bankMonth)
	w.ok("bank statement import", http.MethodPost, "/api/v1/bankrec/imports", map[string]any{
		"bank_account_id": w.bankAccountID, "profile_code": "rbc-default", "filename": "fw1-rbc.csv", "content": importBody,
	})
	w.drain()
	// Match the deposit to the lodgement by hand, recording the journal count touching 1000
	// before and after the match so assertion 5b can assert that the match creates no journal.
	count1000Journals := func(step string) int {
		var n int
		if err := w.pool.QueryRow(ctx, `
			SELECT count(DISTINCT j.id)
			  FROM gl_journals j
			  JOIN gl_journal_lines l ON l.journal_id = j.id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.status = 'POSTED' AND a.code = '1000'`).Scan(&n); err != nil {
			t.Fatalf("%s: count 1000 journals: %v", step, err)
		}
		return n
	}
	lodgementTRT := w.one(`SELECT id::text FROM till_movements WHERE reference = 'DEP-TRT-1' ORDER BY id LIMIT 1`)
	stmtDepositLine := w.one(`SELECT id::text FROM bank_statement_lines WHERE bank_account_id = $1 AND reference = 'DEP-TRT-1' LIMIT 1`, w.bankAccountID)
	w.journals1000BeforeLodgementMatch = count1000Journals("before lodgement match")
	w.ok("match the deposit", http.MethodPost, "/api/v1/bankrec/match", map[string]any{
		"mode": "manual", "bank_account_id": w.bankAccountID,
		"statement_line_id": stmtDepositLine, "target_type": "LODGEMENT", "target_ref_id": lodgementTRT,
		"amount_cents": 40000,
	})
	w.drain()
	w.journals1000AfterLodgementMatch = count1000Journals("after lodgement match")
	// Book the monthly bank charge on the ledger, dated on its statement line's own day in the
	// bank month, and match the statement line to that journal.
	feeID, err := w.money.glSvc.PostExternal(ctx, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "fw1:bank:charge",
		Memo: "fw1 walk monthly bank service charge", BranchID: fw1TRT,
		DocumentDate: time.Date(w.bankYear, time.Month(w.bankMonth), 12, 0, 0, 0, 0, w.bankMonthEnd.Location()),
		Lines: []gl.ExternalLine{
			{AccountCode: "6300", DebitCents: 650, Memo: "bank service charge"},
			{AccountCode: "1000", CreditCents: 650, Memo: "bank service charge"},
		},
	})
	if err != nil {
		t.Fatalf("bank charge journal: %v", err)
	}
	w.drain()
	stmtChargeLine := w.one(`SELECT id::text FROM bank_statement_lines WHERE bank_account_id = $1 AND reference = 'SVC' LIMIT 1`, w.bankAccountID)
	w.ok("match the bank charge", http.MethodPost, "/api/v1/bankrec/match", map[string]any{
		"mode": "manual", "bank_account_id": w.bankAccountID,
		"statement_line_id": stmtChargeLine, "target_type": "JOURNAL", "target_ref_id": feeID,
		"amount_cents": -650,
	})
	w.drain()
	// The statement balance comes from the imported bank statement lines, a real external
	// figure (+$400.00 deposit less $6.50 bank charge = $393.50), never from the ledger.
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0)
		  FROM bank_statement_lines
		 WHERE bank_account_id = $1`, w.bankAccountID).Scan(&w.statementBalanceCents); err != nil {
		w.t.Fatalf("bank statement balance from lines: %v", err)
	}

	// Till close with an over at Trenton (counted cash $1.50 over) and an exact close at
	// Kingston (FW1-13's screen is the item; the till service's own count and close are the
	// nearest real calls today).
	w.ok("count till TRT over", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/count", w.countBody(w.tillTRT, 150))
	w.ok("complete till TRT", http.MethodPost, "/api/v1/till/sessions/"+w.tillTRT+"/complete", map[string]any{"override_notes": "fw1 walk over"})
	w.ok("count till KGN", http.MethodPost, "/api/v1/till/sessions/"+w.tillKGN+"/count", w.countBody(w.tillKGN, 0))
	w.ok("complete till KGN", http.MethodPost, "/api/v1/till/sessions/"+w.tillKGN+"/complete", map[string]any{})
	w.drain()

	// Snapshot the branchless line count BEFORE the year end close (assertion 8) posts its own
	// unbranched closing lines, so assertion 6 speaks about the month's writers alone.
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		 WHERE l.branch_id IS NULL AND j.status = 'POSTED'`).Scan(&w.nullBranchLines); err != nil {
		t.Fatalf("branchless journal lines: %v", err)
	}

	// -- the eight assertions ----------------------------------------------------------
	assertions := map[string]func(t fw1TB){
		"AR_aging_ties_to_GL_1100":                                          w.assertARAgingTies,
		"customer_statement_equals_aging_and_subledger":                     w.createStatementTies,
		"stock_value_ties_to_GL_1200":                                       w.assertStockTies,
		"HST_reconcile_ties":                                                w.assertHSTTies,
		"till_lodgement_posts_bank_1000_at_lodgement":                       w.assertTillLodgementPostsAtLodgement,
		"bank_lodgement_match_posts_no_second_journal":                      w.assertBankLodgementMatchPostsNothing,
		"bank_month_signs_off_at_zero_with_outstanding_items":               w.assertBankMonth,
		"every_journal_line_carries_a_branch_and_branch_trial_balances_sum": w.assertBranchOnEveryLine,
		"no_journal_waiting_or_dead_lettered":                               w.assertNothingWaiting,
		"walk_month_and_next_year_periods_open":                             w.assertWalkMonthAndNextYearPeriodsOpen,
		"year_end_close_opens_next_year_and_keeps_income_statement":         w.assertYearEndClose,
		"month_close_passes_its_pre_flight_and_seals_the_month":             w.assertMonthClose,
	}
	for _, name := range fw1AssertionNames() {
		name := name
		t.Run(name, func(t *testing.T) {
			if item, red := fw1WalkRed[name]; red {
				p := &fw1Probe{}
				failed, msgs := p.run(assertions[name])
				if !failed {
					t.Fatalf("green but still skipped: %s passed while listed in fw1_walk_red.go under %s: delete its line in this PR", name, item)
				}
				t.Skipf("red until %s: %s", item, strings.Join(msgs, "; "))
			}
			assertions[name](t)
		})
	}
}

// -- the assertions, one per wave brief item -----------------------------------------

// 1. AR aging total equals GL 1100, inception to date, to the cent.
func (w *fw1Walk) assertARAgingTies(t fw1TB) {
	ctx := staffctx.ContextWithStaff(context.Background(), staffctx.StaffContext{
		UserID: "fw1-walk", Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
	rows, err := billing.NewCreditService(billing.NewRepository(w.pool)).Aging(ctx, w.monthEnd, "")
	if err != nil {
		t.Fatalf("the aging read: %v", err)
	}
	var aging int64
	for _, r := range rows {
		aging += r.TotalCents
	}
	gl1100, _, err := gl.NewRepository(w.pool).AccountBalanceAsOf(context.Background(), "1100", w.monthEnd, "")
	if err != nil {
		t.Fatalf("the GL 1100 read: %v", err)
	}
	if aging != gl1100 {
		t.Errorf("AR aging total %s does not tie to GL 1100 %s (inception to date): the aging is %s %s",
			fw1Dollars(aging), fw1Dollars(gl1100),
			shorter(aging, gl1100), byDollars(aging-gl1100))
	}
}

// 2. Each customer's statement balance equals their aging, their customer_transactions
// subledger, and their independent documents and receipts in the walk.
func (w *fw1Walk) createStatementTies(t fw1TB) {
	ctx := context.Background()
	sctx := staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: "fw1-walk", Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
	rows, err := billing.NewCreditService(billing.NewRepository(w.pool)).Aging(sctx, w.monthEnd, "")
	if err != nil {
		t.Fatalf("the aging read: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the walk left no AR to tie")
	}
	_, stmts, err := billing.NewStatementService(billing.NewRepository(w.pool)).GenerateRun(sctx, "MASS", "", w.monthEnd, "d7000000-0000-4000-9000-000000000006")
	if err != nil {
		t.Fatalf("the statement run: %v", err)
	}
	agingByCust := map[string]int64{}
	custSet := map[string]bool{}
	for _, r := range rows {
		agingByCust[r.CustomerID] = r.TotalCents
		custSet[r.CustomerID] = true
	}
	stmtByCust := map[string]int64{}
	for _, s := range stmts {
		stmtByCust[s.CustomerID] = s.ClosingCents
		custSet[s.CustomerID] = true
	}
	unionRows, err := w.pool.Query(ctx, `
		SELECT DISTINCT customer_id::text FROM customer_transactions
		UNION
		SELECT DISTINCT customer_id::text FROM invoices WHERE status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents != 0`)
	if err != nil {
		t.Fatalf("the customer union read: %v", err)
	}
	for unionRows.Next() {
		var cid string
		if err := unionRows.Scan(&cid); err != nil {
			t.Fatal(err)
		}
		custSet[cid] = true
	}
	unionRows.Close()

	type custEntry struct {
		id     string
		acctNo string
	}
	var custs []custEntry
	for cid := range custSet {
		var acctNo string
		if err := w.pool.QueryRow(ctx, `SELECT account_no FROM customers WHERE id = $1`, cid).Scan(&acctNo); err != nil {
			t.Fatalf("customer account_no for %s: %v", cid, err)
		}
		custs = append(custs, custEntry{id: cid, acctNo: acctNo})
	}
	sort.Slice(custs, func(i, j int) bool { return custs[i].acctNo < custs[j].acctNo })

	for _, c := range custs {
		var subledger int64
		if err := w.pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_cents), 0)
			  FROM customer_transactions
			 WHERE customer_id = $1 AND occurred_at <= $2`, c.id, w.monthEnd).Scan(&subledger); err != nil {
			t.Fatalf("the customer_transactions subledger read for %s: %v", c.acctNo, err)
		}
		// Independent net receivable from the customer's documents (invoices and credit memos)
		// and receipts (including bounced cheque NSF fee records in payment_nsf net of live
		// payments.metadata->'nsf_fee_allocations' settlements per FW1-06a) in the walk.
		var docsAndReceipts int64
		if err := w.pool.QueryRow(ctx, `
			SELECT COALESCE((
			    SELECT SUM(balance_cents) FROM invoices
			     WHERE customer_id = $1 AND status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
			), 0) + COALESCE((
			    SELECT SUM(
			        pn.fee_cents - COALESCE((
			            SELECT SUM((p2.metadata->'nsf_fee_allocations'->>pn.id::text)::bigint)
			              FROM payments p2
			             WHERE p2.customer_id = pn.customer_id
			               AND p2.reversed_at IS NULL
			               AND p2.reverses_payment_id IS NULL
			               AND p2.status <> 'BOUNCED'
			               AND (p2.metadata->'nsf_fee_allocations'->>pn.id::text) IS NOT NULL
			        ), 0)
			    )
			      FROM payment_nsf pn
			     WHERE pn.customer_id = $1
			), 0)`, c.id).Scan(&docsAndReceipts); err != nil {
			t.Fatalf("the independent documents and receipts read for %s: %v", c.acctNo, err)
		}
		lines, err := billing.NewRepository(w.pool).StatementLines(ctx, c.id, w.monthEnd, billing.AllBranches)
		if err != nil {
			t.Fatalf("the statement lines for %s: %v", c.acctNo, err)
		}
		var walked int64
		if len(lines) > 0 {
			walked = lines[len(lines)-1].BalanceRun
		}
		stmt := stmtByCust[c.id]
		aging := agingByCust[c.id]
		if stmt != docsAndReceipts || walked != docsAndReceipts || aging != docsAndReceipts || subledger != docsAndReceipts {
			t.Errorf("%s: statement closing %s, printed line walk %s, aging %s, customer_transactions subledger %s do not tie to independent documents and receipts %s (aging vs subledger %s; statement closing vs printed walk %s; subledger vs documents %s)",
				c.acctNo, fw1Dollars(stmt), fw1Dollars(walked), fw1Dollars(aging), fw1Dollars(subledger), fw1Dollars(docsAndReceipts),
				byDollars(aging-subledger), byDollars(stmt-walked), byDollars(subledger-docsAndReceipts))
		}
	}
}

// 3. Stock value equals GL 1200.
func (w *fw1Walk) assertStockTies(t fw1TB) {
	var stock int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT COALESCE(ROUND(SUM(sb.on_hand_base * c.avg_unit_cost_cents))::bigint, 0)
		  FROM stock_balances sb
		  JOIN product_branch_costs c ON c.product_id = sb.product_id AND c.branch_id = sb.branch_id
		 WHERE sb.on_hand_base != 0`).Scan(&stock); err != nil {
		t.Fatalf("stock value: %v", err)
	}
	gl1200, _, err := gl.NewRepository(w.pool).AccountBalanceAsOf(context.Background(), "1200", w.monthEnd, "")
	if err != nil {
		t.Fatalf("the GL 1200 read: %v", err)
	}
	if stock != gl1200 {
		t.Errorf("stock value %s does not tie to GL 1200 %s: the ledger is %s the shelves by %s",
			fw1Dollars(stock), fw1Dollars(gl1200), shorter(stock, gl1200), byDollars(stock-gl1200))
	}
}

// 4. The HST reconcile ties.
//
// Note on what the walk's own leg proves: buildInvoiceFinalized posts GL 2200 from the same
// invoices.tax_cents column, so comparing GL 2200's month movement against SUM(invoices.tax_cents)
// less vendor bill ITC is a posting completeness check (proving every walk document's tax reached
// GL 2200), not a rate check on its own. Alongside it, svc.ReconcileReport compares GL 2200
// payable against the tax engine's collected tax decisions.
func (w *fw1Walk) assertHSTTies(t fw1TB) {
	ctx := context.Background()
	var gl2200Move, docSalesHST, billITC int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		  JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE a.code = '2200' AND j.status = 'POSTED' AND fp.year = $1 AND fp.month = $2`,
		w.year, w.month).Scan(&gl2200Move); err != nil {
		t.Fatalf("the GL 2200 month movement read: %v", err)
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		  FROM invoices
		 WHERE status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
		   AND NOT (id::text = ANY($1))`, w.seededInvoiceID).Scan(&docSalesHST); err != nil {
		t.Fatalf("the month documents HST read: %v", err)
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tax_cents), 0)
		  FROM purchasing_bills
		 WHERE status = 'POSTED'`).Scan(&billITC); err != nil {
		t.Fatalf("the vendor bill ITC read: %v", err)
	}
	netDocHST := docSalesHST - billITC
	if gl2200Move != netDocHST {
		t.Errorf("GL 2200 month movement %s does not tie to the month's documents HST %s (sales and credit memos HST %s less vendor bill ITC %s): GL 2200 is %s by %s",
			fw1Dollars(gl2200Move), fw1Dollars(netDocHST), fw1Dollars(docSalesHST), fw1Dollars(billITC),
			shorter(gl2200Move, netDocHST), byDollars(gl2200Move-netDocHST))
	}
	// The reconcile reads its document sides through the owning modules' seams, wired here over the
	// real repositories exactly as wire_tops wires them; an unwired seam refuses the reconcile
	// rather than answering a silent zero.
	svc := tax.NewService(tax.NewRepository(w.pool)).
		WithSalesHSTs(billing.NewRepository(w.pool)).
		WithReturnHSTs(pos.NewRepository(w.pool)).
		WithBillITCs(purchasing.NewRepository(w.pool)).
		WithGLBalances(glBalanceAdapter{svc: w.money.glSvc})
	rep, err := svc.ReconcileReport(ctx, w.year, w.month, "")
	if err != nil {
		t.Fatalf("the HST reconcile: %v", err)
	}
	if rep.VarianceCents != 0 || !rep.TiedOut {
		t.Errorf("the HST reconcile does not tie for %04d-%02d: GL 2200 payable %s against %s collected on tax decisions, a variance of %s (%s)",
			w.year, w.month, fw1Dollars(rep.GLPayableCents), fw1Dollars(rep.CollectedCents), fw1Dollars(rep.VarianceCents),
			shorter(rep.GLPayableCents, rep.CollectedCents))
	}
}

// 5. The bank month signs off at a zero difference computed from balances at month end, with
// the outstanding items listed.
//
// THE BANK MONTH IS THE LAST ENDED MONTH in the house zone (FROM-PRINCIPAL 169): the walk's
// bank story (the statement, the outstanding cheque, the bank charge, the till lodgements) all
// carry that month's days, so the reconciliation opens and signs off an ended month. A running
// month cannot sign off (627's guard), which is why the walk moved here.
//
// THE LODGEMENT RULE (FROM-PRINCIPAL 117 and 120), binding: the textbook bank reconciliation:
//   - Lodgement: a till lodgement posts DR 1000 Bank / CR 1020 Undeposited Funds AT LODGEMENT,
//     dated by its business day in the house zone, and marks the lodged payments DEPOSITED in the
//     same transaction. FW1-13 owns this.
//   - Bounce between lodgement and match: a DEPOSITED payment's bounce credits 1000 (not 1020),
//     so a cheque lodged then bounced before the bank match credits 1000 and leaves AR and 1000
//     tying afterwards.
//   - Match: the bank match of that deposit line matches the lodgement journal and posts NOTHING,
//     and does not mark DEPOSITED again. Its undo reverses nothing. FW1-05 (or FW1-13 per FP 132)
//     owns this.
//   - Deposits in transit: a lodgement in 1000 that is not yet on a statement is a deposit in
//     transit, listed on the reconciliation as a reconciling item. FW1-04 owns this.
//   - Older lodgements: a lodgement from before this change has no lodgement journal, so it keeps
//     today's match, which marks DEPOSITED and posts 1020 to 1000. Nothing is booked twice on a
//     box with history. Each owner tests the old case beside the new one.
func (w *fw1Walk) assertTillLodgementPostsAtLodgement(t fw1TB) {
	ctx := context.Background()
	rows, err := w.pool.Query(ctx, `
		SELECT tm.id::text, COALESCE(tm.reference, ''), tm.amount_cents
		  FROM till_movements tm
		 WHERE tm.movement_type = 'LODGEMENT'
		 ORDER BY tm.created_at, tm.id`)
	if err != nil {
		t.Fatalf("the till lodgements read: %v", err)
	}
	defer rows.Close()
	type lodgRow struct {
		id     string
		ref    string
		amount int64
	}
	var lodgements []lodgRow
	for rows.Next() {
		var r lodgRow
		if err := rows.Scan(&r.id, &r.ref, &r.amount); err != nil {
			t.Fatal(err)
		}
		lodgements = append(lodgements, r)
	}
	if len(lodgements) == 0 {
		t.Fatal("the walk left no till lodgements")
	}
	for _, l := range lodgements {
		var posted int
		// The lodgements trade in the bank month (the walk's till clock is pinned into it), so
		// the lodgement journals' period is the bank month's, not the trading month's.
		if err := w.pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM gl_journals j
			  JOIN fiscal_periods fp ON fp.id = j.period_id
			 WHERE j.status = 'POSTED'
			   AND j.source_ref_id = $1::uuid
			   AND fp.year = $3 AND fp.month = $4
			   AND COALESCE(j.idempotency_key, '') NOT LIKE 'bankrec:%'
			   AND EXISTS (
			       SELECT 1 FROM gl_journal_lines dl
			         JOIN gl_accounts da ON da.id = dl.account_id
			        WHERE dl.journal_id = j.id AND da.code = '1000' AND dl.debit_cents = $2
			   )
			   AND EXISTS (
			       SELECT 1 FROM gl_journal_lines cl
			         JOIN gl_accounts ca ON ca.id = cl.account_id
			        WHERE cl.journal_id = j.id AND ca.code = '1020' AND cl.credit_cents = $2
			   )`, l.id, l.amount, w.bankYear, w.bankMonth).Scan(&posted); err != nil {
			t.Fatalf("the lodgement journal read for %s: %v", l.ref, err)
		}
		if posted != 1 {
			t.Errorf("till lodgement %s (%s) posted %d DR 1000 / CR 1020 journals dated in %04d-%02d at lodgement (want 1)",
				l.ref, fw1Dollars(l.amount), posted, w.bankYear, w.bankMonth)
		}
	}

	// The cheque lodged through DEP-TRT-CHQ-1 and then bounced BEFORE the bank match:
	// 1. Because the lodgement marks its payment DEPOSITED in the same transaction as the DR 1000 /
	//    CR 1020 lodgement journal, the bounce must credit 1000 (not 1020).
	// 2. Afterwards, 1000 ties (the lodgement's DR 1000 and the bounce's CR 1000 net to $0.00) and
	//    AR ties to GL 1100 for the cheque's reopened invoice and NSF fee.
	var bounceCr1000, bounceCr1020 int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.credit_cents) FILTER (WHERE a.code = '1000'), 0),
		       COALESCE(SUM(l.credit_cents) FILTER (WHERE a.code = '1020'), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND j.idempotency_key = $1`, "payment.bounced:"+w.lodgedChequePaymentID).Scan(&bounceCr1000, &bounceCr1020); err != nil {
		t.Fatalf("reading the lodged cheque bounce journal legs: %v", err)
	}
	if bounceCr1000 != w.lodgedChequeCents || bounceCr1020 != 0 {
		t.Errorf("lodged cheque DEP-TRT-CHQ-1 (%s) bounced before bank match credited 1000 %s and 1020 %s (want CR 1000 %s and CR 1020 $0.00)",
			fw1Dollars(w.lodgedChequeCents), fw1Dollars(bounceCr1000), fw1Dollars(bounceCr1020), fw1Dollars(w.lodgedChequeCents))
	}
	var lodgDr1000 int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND j.source_ref_id = $1::uuid
		   AND COALESCE(j.idempotency_key, '') NOT LIKE 'bankrec:%'
		   AND a.code = '1000'`, w.lodgedChequeMovementID).Scan(&lodgDr1000); err != nil {
		t.Fatalf("reading the lodged cheque lodgement 1000 debit: %v", err)
	}
	if lodgDr1000 != w.lodgedChequeCents || bounceCr1000 != w.lodgedChequeCents || lodgDr1000-bounceCr1000 != 0 {
		t.Errorf("GL 1000 does not tie after lodged cheque DEP-TRT-CHQ-1 bounced before match: lodgement DR 1000 %s (want %s) less bounce CR 1000 %s (want %s) leaves %s (want $0.00)",
			fw1Dollars(lodgDr1000), fw1Dollars(w.lodgedChequeCents),
			fw1Dollars(bounceCr1000), fw1Dollars(w.lodgedChequeCents),
			byDollars(lodgDr1000-bounceCr1000))
	}
	var chequeGL1100, chequeOpenAR int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND a.code = '1100'
		   AND j.idempotency_key IN ($1, $2, $3)`,
		"ar.invoice.finalized:"+w.lodgedChequeInvoiceID,
		"payment.received:"+w.lodgedChequePaymentID,
		"payment.bounced:"+w.lodgedChequePaymentID).Scan(&chequeGL1100); err != nil {
		t.Fatalf("reading GL 1100 movement for lodged cheque: %v", err)
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT balance_cents FROM invoices WHERE id = $1::uuid), 0)
		     + COALESCE((SELECT SUM(fee_cents) FROM payment_nsf WHERE payment_id = $2::uuid), 0)`,
		w.lodgedChequeInvoiceID, w.lodgedChequePaymentID).Scan(&chequeOpenAR); err != nil {
		t.Fatalf("reading open AR for lodged cheque: %v", err)
	}
	if chequeGL1100 != chequeOpenAR {
		t.Errorf("AR (%s) and GL 1100 (%s) do not tie after lodged cheque DEP-TRT-CHQ-1 bounced before match (%s)",
			fw1Dollars(chequeOpenAR), fw1Dollars(chequeGL1100), byDollars(chequeOpenAR-chequeGL1100))
	}
}

func (w *fw1Walk) assertBankLodgementMatchPostsNothing(t fw1TB) {
	ctx := context.Background()
	var nonLodgementJournalCount int
	var nonLodgementJournalKey string
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(MAX(COALESCE(j.idempotency_key, j.id::text)), '')
		  FROM bank_matches bm
		  JOIN gl_journals j ON j.id = bm.journal_id
		 WHERE bm.target_type = 'LODGEMENT'
		   AND bm.unmatched_at IS NULL
		   AND j.source_ref_id IS DISTINCT FROM bm.target_ref_id`).Scan(&nonLodgementJournalCount, &nonLodgementJournalKey); err != nil {
		t.Fatalf("the lodgement match journal read: %v", err)
	}
	delta1000 := w.journals1000AfterLodgementMatch - w.journals1000BeforeLodgementMatch
	if nonLodgementJournalCount != 0 || delta1000 != 0 {
		t.Errorf("the bank match of a till lodgement created %d journal(s) touching 1000 across the match and linked %d non-lodgement journal(s) (%s) on bank_matches.journal_id instead of matching the lodgement journal and posting nothing",
			delta1000, nonLodgementJournalCount, nonLodgementJournalKey)
	}
}

func (w *fw1Walk) assertBankMonth(t fw1TB) {
	ctx := context.Background()
	// The GL side reads the bank month's own end. The walk's bank journals carry the bank
	// month's document dates and posted_at rides the document date, so the as-of read at the
	// bank month's end sees exactly the month's bank story.
	gl1000, _, err := gl.NewRepository(w.pool).AccountBalanceAsOf(ctx, "1000", w.bankMonthEnd, "")
	if err != nil {
		t.Fatalf("the GL 1000 read: %v", err)
	}
	var depositInTransit, outstandingCheques int64
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(tm.amount_cents), 0)
		  FROM till_movements tm
		 WHERE tm.movement_type = 'LODGEMENT'
		   AND tm.id <> $1::uuid
		   AND NOT EXISTS (
		       SELECT 1 FROM bank_matches bm
		        WHERE bm.target_type = 'LODGEMENT'
		          AND bm.target_ref_id = tm.id
		          AND bm.unmatched_at IS NULL
		   )`, w.lodgedChequeMovementID).Scan(&depositInTransit); err != nil {
		t.Fatalf("the deposits in transit read: %v", err)
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.credit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE a.code = '1000'
		   AND j.status = 'POSTED'
		   AND COALESCE(l.credit_cents, 0) > 0
		   AND COALESCE(j.idempotency_key, '') NOT LIKE 'payment.bounced:%'
		   AND NOT EXISTS (
		       SELECT 1 FROM bank_matches bm
		        WHERE (bm.journal_id = j.id OR (bm.target_type = 'JOURNAL' AND bm.target_ref_id = j.id))
		          AND bm.unmatched_at IS NULL
		   )`).Scan(&outstandingCheques); err != nil {
		t.Fatalf("the outstanding cheques read: %v", err)
	}
	if depositInTransit != w.depositInTransitCents || outstandingCheques != w.outstandingChequeCents {
		t.Errorf("the bank month outstanding items do not match the walk: deposit in transit %s (want %s), outstanding cheques %s (want %s)",
			fw1Dollars(depositInTransit), fw1Dollars(w.depositInTransitCents),
			fw1Dollars(outstandingCheques), fw1Dollars(w.outstandingChequeCents))
	}
	svc := bankrec.NewService(bankrec.NewRepository(w.pool)).
		WithPayments(till.NewRepository(w.pool)).
		WithLodgements(till.NewRepository(w.pool)).
		WithJournalPoster(glPosterAdapter{svc: w.money.glSvc}).
		WithGLBalances(glBalanceAdapter{svc: w.money.glSvc}).
		WithJournals(w.money.glSvc)
	rec, err := svc.OpenReconciliation(ctx, bankrec.ReconInput{
		BankAccountID: w.bankAccountID, Year: w.bankYear, Month: w.bankMonth, StatementBalanceCents: 0,
	})
	if err != nil {
		t.Fatalf("the bank month open: %v", err)
	}
	if rec.StatementBalanceCents != w.statementBalanceCents {
		t.Errorf("the bank month statement balance %s does not match the statement lines %s",
			fw1Dollars(rec.StatementBalanceCents), fw1Dollars(w.statementBalanceCents))
	}
	// Standard bank reconciliation tie: statement balance + deposits in transit - outstanding cheques = book balance (GL 1000).
	adjustedBank := rec.StatementBalanceCents + depositInTransit - outstandingCheques
	if rec.VarianceCents != 0 || adjustedBank != gl1000 {
		t.Errorf("the bank month %04d-%02d does not tie at zero: statement %s plus %s deposit in transit less %s outstanding cheques = %s against GL 1000 at the month's end %s (the ledger side is %s by %s; OpenReconciliation reports variance %s)",
			w.bankYear, w.bankMonth,
			fw1Dollars(rec.StatementBalanceCents), fw1Dollars(depositInTransit),
			fw1Dollars(outstandingCheques), fw1Dollars(adjustedBank), fw1Dollars(gl1000),
			shorter(gl1000, adjustedBank), byDollars(gl1000-adjustedBank), fw1Dollars(rec.VarianceCents))
	}
	completed, err := svc.CompleteReconciliation(ctx, rec.ID, "d7000000-0000-4000-9000-000000000006")
	if err != nil || completed.Status != bankrec.ReconCompleted {
		t.Errorf("the bank month sign off (CompleteReconciliation) did not succeed: err=%v status=%s", err, completed.Status)
	}
	w.drain()
}

// 6. Every journal line carries a branch, and the branch trial balances sum to the consolidated one.
func (w *fw1Walk) assertBranchOnEveryLine(t fw1TB) {
	if w.nullBranchLines != 0 {
		t.Errorf("%d posted journal lines carry no branch (the GL side of the tie is the lines without a branch)", w.nullBranchLines)
	}
	cons, err := w.money.glSvc.TrialBalance(fw1StaffCtx(context.Background()), w.year, w.month, "")
	if err != nil {
		t.Fatalf("the consolidated trial balance: %v", err)
	}
	var sumDebit, sumCredit int64
	for _, b := range []string{fw1TRT, fw1KGN} {
		tb, err := w.money.glSvc.TrialBalance(fw1StaffCtx(context.Background()), w.year, w.month, b)
		if err != nil {
			t.Fatalf("the %s trial balance: %v", b, err)
		}
		sumDebit += tb.TotalDebit
		sumCredit += tb.TotalCredit
	}
	if sumDebit != cons.TotalDebit || sumCredit != cons.TotalCredit {
		t.Errorf("the branch trial balances sum to %s debit and %s credit against the consolidated %s debit and %s credit (the branches are %s the whole by %s)",
			fw1Dollars(sumDebit), fw1Dollars(sumCredit), fw1Dollars(cons.TotalDebit), fw1Dollars(cons.TotalCredit),
			shorter(sumDebit, cons.TotalDebit), byDollars(sumDebit-cons.TotalDebit))
	}
}

// 7. No journal is waiting or dead lettered.
func (w *fw1Walk) assertNothingWaiting(t fw1TB) {
	var pending, dead int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM events_outbox WHERE status = 'PENDING'`).Scan(&pending); err != nil {
		t.Fatalf("the waiting read: %v", err)
	}
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM events_outbox WHERE status = 'DEAD'`).Scan(&dead); err != nil {
		t.Fatalf("the dead letter read: %v", err)
	}
	if pending != 0 || dead != 0 {
		var topics []string
		rows, err := w.pool.Query(context.Background(), `SELECT DISTINCT topic FROM events_outbox WHERE status IN ('PENDING','DEAD')`)
		if err == nil {
			for rows.Next() {
				var topic string
				if err := rows.Scan(&topic); err == nil {
					topics = append(topics, topic)
				}
			}
			rows.Close()
		}
		t.Errorf("%d journal events are waiting and %d are dead lettered after the consumer's pass (topics: %s)",
			pending, dead, strings.Join(topics, ", "))
	}
}

// The walk month's period exists and the next fiscal year's periods roll forward ahead of need (FW1-01).
func (w *fw1Walk) assertWalkMonthAndNextYearPeriodsOpen(t fw1TB) {
	var walkPeriod int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM fiscal_periods WHERE year = $1 AND month = $2 AND status = 'OPEN'`, w.year, w.month).Scan(&walkPeriod); err != nil {
		t.Fatalf("the walk month period read: %v", err)
	}
	if walkPeriod == 0 {
		t.Errorf("the walk month %04d-%02d has no OPEN fiscal period", w.year, w.month)
	}
	// What the box does: the worker runs the roll forward at boot (cmd/core/worker.go,
	// glSvc.EnsureNextFiscalYear), so the year after the current house year exists, all twelve
	// months OPEN, ahead of need and without anyone acting. The walk runs it here before it counts.
	if _, err := w.money.glSvc.EnsureNextFiscalYear(context.Background()); err != nil {
		t.Fatalf("the boot roll forward: %v", err)
	}
	var nextYear int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM fiscal_periods WHERE year = $1 AND status = 'OPEN'`, w.year+1).Scan(&nextYear); err != nil {
		t.Fatalf("the next year period read: %v", err)
	}
	if nextYear < 12 {
		t.Errorf("the fiscal calendar opened %d OPEN periods for next year %d ahead of need (want 12): posting into %d is refused for want of a period", nextYear, w.year+1, w.year+1)
	}
	// The promise kept, asserted by a posting and not by a row count alone: a journal dated in the
	// next year is accepted and lands in an OPEN period of that year. Passing here with PR 612's
	// maxYear+1 arm in insertJournalIn neutered is accepted, because the boot roll forward above
	// makes the year and PR 612's own gl leg
	// (TestIntegration_FW1_01_YearRollForward_PostsOnTheFirstDayAfterTheSeededYear) guards the arm.
	jid, err := w.money.glSvc.PostExternal(context.Background(), gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: fmt.Sprintf("fw1:next-year-posting:%d", w.year+1),
		Memo: "the next year roll forward, a posting dated in the next year", BranchID: fw1TRT,
		DocumentDate: time.Date(w.year+1, time.January, 15, 12, 0, 0, 0, time.UTC),
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: 100, Memo: "a posting dated in the next year"},
			{AccountCode: "3000", CreditCents: 100, Memo: "a posting dated in the next year"},
		},
	})
	if err != nil {
		t.Errorf("a posting dated in %d was refused: %v", w.year+1, err)
		return
	}
	var postedYear, postedMonth int
	var postedStatus string
	if err := w.pool.QueryRow(context.Background(), `
		SELECT fp.year, fp.month, fp.status
		  FROM gl_journals j JOIN fiscal_periods fp ON fp.id = j.period_id
		 WHERE j.id = $1`, jid).Scan(&postedYear, &postedMonth, &postedStatus); err != nil {
		t.Fatalf("the next year posting read: %v", err)
	}
	if postedYear != w.year+1 || postedStatus != "OPEN" {
		t.Errorf("the posting dated in %d landed in %04d-%02d with status %s (want an OPEN period of %d)", w.year+1, postedYear, postedMonth, postedStatus, w.year+1)
	}
}

// 8. The walk's own year has not ended, so its year end close is refused (FW1-09b), and the close
// of the last year before the walk's own that the books have not moved in, which has ended, really
// closes: the closed year carries its own periods and its own P&L, the close posts a closing
// journal carrying that P&L's net, every period of the closed year is sealed, the walk's own year
// is left open, and the closed year's income statement still reads as it did before the close
// (FROM-PRINCIPAL 159, ruled again in 321 after S5's P2 on PR 634).
//
// THE PLANT. On a fresh migrated and seeded database the year before the walk's own year holds
// nothing: no periods, no journals, no P&L. A close of such a year posts no closing journal,
// seals no period and answers success, so every check on it passes for the wrong reason (S5's P2,
// REVIEW-PR634-r7-s5-opus.md). The plant below gives the closed year a book of its own:
//
//   - the year's twelve OPEN fiscal periods, created the way the product's own roll forward creates
//     them (the GL repository's ensureFiscalYearTx), so the close has periods to seal. A posting
//     dated in a year with no period rows files late into the next open month, so the periods come
//     first;
//   - the year's opening stock, the stock the dealer holds today, because the close's pre flight
//     ties the company's stock against the company's 1200 at the month's end and the stock side
//     reads today's value until the inventory module answers an as of read;
//   - one revenue journal and one expense journal through PostExternal, the production posting
//     seam, each stamped with a branch, so the year carries a P&L for the close to sweep;
//   - the current year's mirror of that opening stock, so the stock the closed year opened with is
//     not opened twice and the walk's own month close still ties.
//
// The plant leaves the walk's own books as they were: the mirror cancels the duplicate at 1200 and
// at 3000, so the walk's month close's pre flight still ties and the walk's own figures read the
// same after the plant as before it.
func (w *fw1Walk) assertYearEndClose(t fw1TB) {
	ctx := context.Background()
	// The walk's own year is still running: closing it would seal the months still ahead, so the
	// close is refused until the year's last day has passed in the dealer's zone. Red until FW1-09
	// lands: the refusal does not exist yet.
	if _, err := w.money.glSvc.CloseYear(fw1ControllerCtx(context.Background()), w.year, "FW1 walk, the running year's close must be refused"); err == nil {
		t.Errorf("the year end close of the walk's own year %d succeeded but the year has not ended (want refused)", w.year)
	}
	// The year before the walk's own has ended; its close must do real work.
	closedYear := w.year - 1
	// The year the leg closes has to be a year the books have not already moved in. The close's
	// pre flight ties that year's own balances (the AR subledger against 1100, the stock against
	// 1200) at each of its month ends, so a year that already carries a movement cannot be closed
	// by this leg: the pre flight reads the AR subledger's own dated acts, and a year whose
	// movements sit in the GL but not in the subledger is out of tie by construction. On the seeded
	// database the year before the walk's own is empty, which is the year ruling 159 named; the
	// rule keeps the leg honest on a box whose seed carries a year of history.
	for {
		var moved int
		if err := w.pool.QueryRow(ctx, `
			SELECT count(*) FROM gl_journals WHERE fiscal_year = $1 AND status = 'POSTED'`, closedYear).Scan(&moved); err != nil {
			t.Fatalf("the closed year %d's journal read: %v", closedYear, err)
		}
		if moved == 0 {
			break
		}
		closedYear--
		if closedYear < w.year-100 {
			t.Fatalf("no year before the walk's own year %d carries no movement of its own, so this leg has no year to close", w.year)
		}
	}
	income := func() map[string]int64 {
		out := map[string]int64{}
		rows, err := w.pool.Query(context.Background(), `
			SELECT account_code, SUM(amount_cents) FROM v_income_statement WHERE fiscal_year = $1 GROUP BY account_code`, closedYear)
		if err != nil {
			t.Fatalf("the income statement read: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var code string
			var amt int64
			if err := rows.Scan(&code, &amt); err != nil {
				t.Fatal(err)
			}
			out[code] = amt
		}
		return out
	}
	// -- the plant: the closed year as a year in the books --------------------------------
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, closedYear); err != nil {
		t.Fatalf("planting the closed year %d's fiscal periods: %v", closedYear, err)
	}
	// The stock the dealer holds today, read through the production seam the close's pre flight
	// reads it through, so the plant carries the figure the close's own tie will look for.
	stock, err := w.money.inventorySvc.StockValueCents(ctx, "")
	if err != nil {
		t.Fatalf("the company's stock value: %v", err)
	}
	const plantedRevenue, plantedExpense = int64(100000), int64(40000)
	openedAt := time.Date(closedYear, time.January, 15, 12, 0, 0, 0, time.UTC)
	for _, p := range []gl.ExternalJournal{
		{
			Source: "MANUAL", IdempotencyKey: fmt.Sprintf("fw1:year-end-plant:opening:%d", closedYear),
			Memo: "the closed year's opening stock", BranchID: fw1TRT, DocumentDate: openedAt,
			Lines: []gl.ExternalLine{
				{AccountCode: "1200", DebitCents: stock, Memo: "opening stock"},
				{AccountCode: "3000", CreditCents: stock, Memo: "opening equity"},
			},
		},
		{
			Source: "MANUAL", IdempotencyKey: fmt.Sprintf("fw1:year-end-plant:revenue:%d", closedYear),
			Memo: "the closed year's revenue", BranchID: fw1TRT, DocumentDate: openedAt,
			Lines: []gl.ExternalLine{
				{AccountCode: "1000", DebitCents: plantedRevenue, Memo: "the year's cash sale"},
				{AccountCode: "4000", CreditCents: plantedRevenue, Memo: "the year's revenue"},
			},
		},
		{
			Source: "MANUAL", IdempotencyKey: fmt.Sprintf("fw1:year-end-plant:expense:%d", closedYear),
			Memo: "the closed year's expense", BranchID: fw1TRT, DocumentDate: openedAt,
			Lines: []gl.ExternalLine{
				{AccountCode: "6100", DebitCents: plantedExpense, Memo: "the year's rent"},
				{AccountCode: "1000", CreditCents: plantedExpense, Memo: "the year's cash paid out"},
			},
		},
		{
			// The current year's mirror of that opening stock: the stock the closed year opened
			// with is the stock the walk's own books already carry, so the current year does not
			// open it a second time. It rides the clock, so it lands in the walk's own month.
			Source: "MANUAL", IdempotencyKey: fmt.Sprintf("fw1:year-end-plant:mirror:%d", closedYear),
			Memo: "the current year does not open the stock the closed year carries", BranchID: fw1TRT,
			Lines: []gl.ExternalLine{
				{AccountCode: "3000", DebitCents: stock, Memo: "opening equity"},
				{AccountCode: "1200", CreditCents: stock, Memo: "the stock the closed year opened with"},
			},
		},
	} {
		if _, err := w.money.glSvc.PostExternal(ctx, p); err != nil {
			t.Fatalf("planting the closed year %d's journal %q: %v", closedYear, p.Memo, err)
		}
	}
	// The income statement the close will sweep, read before the close runs.
	before := income()
	// The close has to do real work, or this leg proves nothing. A close of a year with no income
	// statement posts no closing journal, seals no period and answers success, so the comparison
	// below ranges over an empty map and passes for the wrong reason (S5's P2 on PR 634,
	// REVIEW-PR634-r7-s5-opus.md). The closed year must carry a P&L before the close runs.
	if len(before) == 0 {
		t.Errorf("the closed year %d carries no income statement: the year end close of a year with no P&L posts no closing journal and seals no period, so this leg would prove nothing", closedYear)
	}
	// The closed year carries exactly the planted P&L: one revenue and one expense.
	wantIncome := map[string]int64{"4000": plantedRevenue, "6100": plantedExpense}
	for code, want := range wantIncome {
		if before[code] != want {
			t.Errorf("the closed year %d's income statement holds %s on account %s, want the planted %s", closedYear, fw1Dollars(before[code]), code, fw1Dollars(want))
		}
	}
	if len(before) != len(wantIncome) {
		t.Errorf("the closed year %d's income statement holds %d accounts (%v), want the planted %d", closedYear, len(before), before, len(wantIncome))
	}
	// The year end close is the Financial Controller's (FW1-09c), so the leg closes as the Controller.
	res, err := w.money.glSvc.CloseYear(fw1ControllerCtx(context.Background()), closedYear, "FW1 walk year end close")
	if err != nil {
		t.Fatalf("the year end close of %d: %v", closedYear, err)
	}
	// The close posted a closing journal, and it carries exactly the planted net.
	plantedNet := plantedRevenue - plantedExpense
	if len(res.ClosingJournalIDs) == 0 {
		t.Errorf("the year end close of %d returned no closing journal: the year's P&L (%v) was not swept to retained earnings", closedYear, before)
	}
	if res.NetIncomeCents != plantedNet {
		t.Errorf("the year end close of %d reported a net income of %s, want the planted net %s", closedYear, fw1Dollars(res.NetIncomeCents), fw1Dollars(plantedNet))
	}
	// The closing journal itself: it sits in the closed year's own December, every line carries a
	// branch, and its retained earnings leg carries the year's net.
	for _, id := range res.ClosingJournalIDs {
		var retained int64
		var periodYear, periodMonth int
		if err := w.pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0), fp.year, fp.month
			  FROM gl_journals j
			  JOIN gl_journal_lines l ON l.journal_id = j.id
			  JOIN gl_accounts a ON a.id = l.account_id
			  JOIN fiscal_periods fp ON fp.id = j.period_id
			 WHERE j.id = $1::uuid AND a.code = '3900'
			 GROUP BY fp.year, fp.month`, id).Scan(&retained, &periodYear, &periodMonth); err != nil {
			t.Fatalf("the closing journal %s read: %v", id, err)
		}
		if retained != -plantedNet {
			t.Errorf("the closing journal %s carries %s on retained earnings, want the planted net %s credited", id, fw1Dollars(retained), fw1Dollars(plantedNet))
		}
		if periodYear != closedYear || periodMonth != 12 {
			t.Errorf("the closing journal %s sits in %04d-%02d, want the closed year's own December %04d-12", id, periodYear, periodMonth, closedYear)
		}
		var branchlessLines int
		if err := w.pool.QueryRow(ctx, `
			SELECT count(*) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
			 WHERE j.id = $1::uuid AND l.branch_id IS NULL`, id).Scan(&branchlessLines); err != nil {
			t.Fatalf("the closing journal %s branch read: %v", id, err)
		}
		if branchlessLines != 0 {
			t.Errorf("the closing journal %s carries %d line(s) without a branch, want every line stamped", id, branchlessLines)
		}
	}
	// Every period of the closed year is sealed, and the walk's own year is left open.
	var closedOpen, closedTotal int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'OPEN'), count(*) FROM fiscal_periods WHERE year = $1`, closedYear).Scan(&closedOpen, &closedTotal); err != nil {
		t.Fatalf("the closed year %d's period read: %v", closedYear, err)
	}
	if closedTotal == 0 {
		t.Errorf("the year end close of %d left the closed year without a fiscal period: the close sealed nothing", closedYear)
	}
	if closedOpen != 0 {
		t.Errorf("the year end close of %d left %d of the closed year's %d periods OPEN, want every period of the closed year sealed", closedYear, closedOpen, closedTotal)
	}
	var walkOpen, walkTotal int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'OPEN'), count(*) FROM fiscal_periods WHERE year = $1`, w.year).Scan(&walkOpen, &walkTotal); err != nil {
		t.Fatalf("the walk year period read: %v", err)
	}
	if walkTotal == 0 {
		t.Errorf("the year end close of %d left the walk's own year %d without fiscal periods: posting into the walk year is refused for want of a period", closedYear, w.year)
	}
	if walkOpen != walkTotal {
		t.Errorf("the year end close of %d left %d of the walk's own year %d's %d periods OPEN, want the walk's own year left open", closedYear, walkOpen, w.year, walkTotal)
	}
	after := income()
	for code, amt := range before {
		if after[code] != amt {
			t.Errorf("the year end close changed the closed year's income statement: account %s held %s before the close and %s after (%s)",
				code, fw1Dollars(amt), fw1Dollars(after[code]), byDollars(after[code]-amt))
		}
	}
}

// 12. The walk month closes through the served route: the close pre flight reads the company's
// stock, the AR subledger, the posting queue and the till drawers, and the close seals the month
// (FW1-09d: the walk guards the month close from here on, where before it closed only a year).
func (w *fw1Walk) assertMonthClose(t fw1TB) {
	periodID := w.one(`SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = $2`, w.year, w.month)
	code, out := w.do(http.MethodPost, "/api/v1/gl/periods/"+periodID+"/close",
		map[string]any{"reason": "FW1 walk month close"})
	if code != http.StatusOK {
		t.Fatalf("the walk month %04d-%02d close answered %d: %s", w.year, w.month, code, out)
	}
	var status string
	if err := w.pool.QueryRow(context.Background(), `SELECT status FROM fiscal_periods WHERE id = $1::uuid`, periodID).Scan(&status); err != nil {
		t.Fatalf("the closed period read: %v", err)
	}
	if status != "CLOSED" {
		t.Errorf("the walk month %04d-%02d close answered 200 but the period is %s, want CLOSED", w.year, w.month, status)
	}
}

// TestFW1WalkRedMapNamesRealSubtests guards the skip map: every key must name one of the walk's
// assertions, every value must name a single owning item, and when FW1_WAVE_FINAL=1 is set
// the map must be empty.
func TestFW1WalkRedMapNamesRealSubtests(t *testing.T) {
	real := map[string]bool{}
	for _, name := range fw1AssertionNames() {
		real[name] = true
	}
	for name, owner := range fw1WalkRed {
		if !real[name] {
			t.Errorf("fw1WalkRed names %q, which is not a subtest of the month walk: remove the stale line", name)
		}
		if strings.TrimSpace(owner) == "" || strings.Contains(owner, " and ") || strings.Contains(owner, ",") {
			t.Errorf("fw1WalkRed[%q] = %q must name a single owning item", name, owner)
		}
	}
	if os.Getenv("FW1_WAVE_FINAL") == "1" && len(fw1WalkRed) != 0 {
		t.Fatalf("FW1_WAVE_FINAL=1 requires fw1WalkRed to be empty, but %d line(s) remain: %v", len(fw1WalkRed), fw1WalkRed)
	}
}

// TestFW1WalkRedEmptyOnWaveFinal fails when FW1_WAVE_FINAL=1 is set and fw1WalkRed is not empty,
// so the final wave PR can prove every assertion runs unskipped.
func TestFW1WalkRedEmptyOnWaveFinal(t *testing.T) {
	if os.Getenv("FW1_WAVE_FINAL") != "1" {
		return
	}
	if len(fw1WalkRed) != 0 {
		t.Fatalf("FW1_WAVE_FINAL=1 requires fw1WalkRed to be empty, but %d line(s) remain: %v", len(fw1WalkRed), fw1WalkRed)
	}
}

// shorter says which of two figures is the smaller, in words.
func shorter(a, b int64) string {
	if a < b {
		return "short"
	}
	return "over"
}

// byDollars renders a delta as a signed dollar amount.
func byDollars(c int64) string {
	if c < 0 {
		return "-" + fw1Dollars(-c)
	}
	return fw1Dollars(c)
}
