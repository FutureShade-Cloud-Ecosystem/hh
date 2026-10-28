//go:build integration

// ar_evening_age_integration_test.go, the SQL reads of the AR family in the house evening window
// (the unit twin is ar_evening_age_test.go). Pinned at houseday.JustAfterUTCDayChange, so each leg
// is red on a read that follows the UTC day at any wall clock hour.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/billing/... -run EveningAge -count=1
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// An invoice due on the house day is CURRENT on the house day, whatever the UTC day says: the aging
// the credit desk and the month end statements read cuts its date parameter from the clock's house day.
func TestIntegrationEveningAge_AgingRowsReadTheClockOnTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "AG")
	rig.doc(t, cust, "INV-AG", "INVOICE", "FINALIZED", 5000, 0)

	rows, err := NewRepository(rig.pool).AgingRows(context.Background(), rig.clock, 60, "")
	if err != nil {
		t.Fatalf("aging rows: %v", err)
	}
	got, ok := findRow(rows, cust)
	if !ok {
		t.Fatalf("customer missing from the aging")
	}
	if got.CurrentCents != 5000 || got.Days30Cents != 0 {
		t.Errorf("invoice due on the house day: current %d, 1-30 %d; want 5000, 0 (it is not late yet)",
			got.CurrentCents, got.Days30Cents)
	}
}

// The register's overdue test reads the same date parameter.
func TestIntegrationEveningAge_RegisterCountsReadTheClockOnTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "RG")
	rig.doc(t, cust, "INV-RG", "INVOICE", "FINALIZED", 5000, 0)

	counts, err := NewRepository(rig.pool).RegisterCountsFor(context.Background(), rig.clock, 0, RegisterFilter{CustomerID: cust})
	if err != nil {
		t.Fatalf("register counts: %v", err)
	}
	if counts.Overdue != 0 {
		t.Errorf("overdue = %d, want 0: the invoice is due on the house day", counts.Overdue)
	}
}

// A statement cut for the house day carries a transaction rung that house evening, though its
// instant is already the next UTC day.
func TestIntegrationEveningAge_StatementLinesFileAnEveningUnderTheHouseDay(t *testing.T) {
	rig := newHH195Rig(t)
	cust := rig.customer(t, "ST")
	evening := rig.clock // the house evening of rig's day, already the next UTC day
	if _, err := rig.pool.Exec(context.Background(),
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                    source_type, source_ref_id, branch_id, occurred_at, created_at)
		 VALUES ($1::uuid,'INVOICE',700,700,'invoice',gen_random_uuid(),$2::uuid,$3,$3)`,
		cust, rig.branchID, evening); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = rig.pool.Exec(context.Background(), `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cust)
	})
	houseDay := houseClock(evening).Format("2006-01-02")
	asOf, err := parseAsOf(houseDay)
	if err != nil {
		t.Fatalf("as_of: %v", err)
	}
	lines, err := NewRepository(rig.pool).StatementLines(context.Background(), cust, asOf, AllBranches)
	if err != nil {
		t.Fatalf("statement lines: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("%d lines on the statement for %s, want the evening transaction", len(lines), houseDay)
	}
}

// An invoice finalized now is on the books NOW and ties to GL 1100. The ledger dates an invoice's
// journal by its FINALIZE instant (gl/posting.go builds the journal's OccurredAt from FinalizedAt;
// InvoiceForGL reads COALESCE(finalized_at, created_at)), so an invoice planted the way production
// writers plant it, issue day the dealer's day taken through houseday (the repository's own zone)
// and finalized this instant, moves GL 1100, the 1100 tie figure and the aging TOGETHER under the
// evening pin, whatever the UTC day has rolled to. No house day read follows the UTC day: every
// figure in this leg moves by exactly the invoice's total, at the instant it finalized.
func TestIntegrationEveningAge_AnInvoiceFinalizedNowIsOnTheBooksAndTiesToGL1100(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	// The real GL consumer, wired the way the app wires it: billing's invoice reader behind
	// gl.Service, the finalize event drained out of the transactional outbox.
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(repo)
	dispatch := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatch)

	gl1100 := func() int64 {
		var v int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(COALESCE(l.debit_cents,0)-COALESCE(l.credit_cents,0)),0)
		   FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
		  WHERE a.code = '1100' AND j.status = 'POSTED' AND j.posted_at <= now()`).Scan(&v); err != nil {
			t.Fatalf("read GL 1100: %v", err)
		}
		return v
	}
	figures := func() (glBal, tie, aging int64) {
		glBal = gl1100()
		var err error
		if tie, err = repo.ARBalanceCents(ctx, time.Now(), ""); err != nil {
			t.Fatalf("read the tie figure: %v", err)
		}
		rows, err := repo.AgingRowsIn(ctx, time.Now(), 60, "", BranchReach{Global: true})
		if err != nil {
			t.Fatalf("read the aging: %v", err)
		}
		for _, row := range rows {
			aging += row.TotalCents
		}
		return glBal, tie, aging
	}

	// The drain below must move the books by THIS leg's invoice alone: the shared outbox carries
	// other tests' leftover rows, and posting their journals here would reach into windows other
	// legs read (a backdated finalize journal lands inside the June residue probe in gl). So the
	// leg takes FOR UPDATE locks on every other PENDING row and holds them while it drains: the
	// claim reads FOR UPDATE SKIP LOCKED, ordered by occurred_at, so the only row the dispatcher
	// can settle is this leg's own. The lock transaction rolls back and the leftovers stand as
	// they were found.
	gl0, tie0, aging0 := figures()

	houseDay := houseday.Today(time.Now)
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $4::date + 30, 8000, 0, 8000, 8000, now(), 'INVOICE')
		 RETURNING id::text`,
		n+"-EVE", cust, trentonBranch, houseDay).Scan(&id); err != nil {
		t.Fatalf("plant the invoice on the dealer's day %s: %v", houseDay, err)
	}
	key := "ar.invoice.finalized:" + id
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, key)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'invoice_id' = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1::uuid`, id)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ('ar.invoice.finalized', jsonb_build_object('invoice_id', $1::text))`, id); err != nil {
		t.Fatalf("emit the finalize event: %v", err)
	}

	locks, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the lock transaction: %v", err)
	}
	defer func() { _ = locks.Rollback(ctx) }()
	if _, err := locks.Exec(ctx, `SELECT event_id FROM events_outbox WHERE status = 'PENDING' AND payload->>'invoice_id' IS DISTINCT FROM $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock the other pending rows: %v", err)
	}

	var posted int
	for i := 0; i < 300 && posted == 0; i++ {
		dispatch.DrainOnce(ctx)
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&posted); err != nil {
			t.Fatalf("read the journal: %v", err)
		}
	}
	if posted == 0 {
		t.Fatalf("the finalize event never posted its journal")
	}
	var postedAt, dbNow time.Time
	if err := pool.QueryRow(ctx, `SELECT posted_at, now() FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&postedAt, &dbNow); err != nil {
		t.Fatalf("read the journal's posted_at: %v", err)
	}
	if postedAt.After(dbNow) {
		t.Fatalf("the journal posted at %s, after now (%s): an invoice finalized now is on the books now", postedAt.Format(time.RFC3339), dbNow.Format(time.RFC3339))
	}
	if postedDay := houseday.Today(func() time.Time { return postedAt }); postedDay != houseDay {
		t.Fatalf("the journal posted on house day %s, want %s: an invoice finalized now posts on the dealer's day", postedDay, houseDay)
	}

	gl1, tie1, aging1 := figures()
	if moved := gl1 - gl0; moved != 8000 {
		t.Fatalf("GL 1100 moved %d for the invoice finalized now, want 8000: the journal is dated by the finalize instant and the control account carries it now", moved)
	}
	if moved := tie1 - tie0; moved != 8000 {
		t.Fatalf("the 1100 tie figure moved %d for the invoice finalized now, want 8000 (the invoice joins the books at its finalize instant, whatever the UTC day)", moved)
	}
	if moved := aging1 - aging0; moved != 8000 {
		t.Fatalf("the aging moved %d for the invoice finalized now, want 8000 (the aging, the tie figure and GL 1100 read one rule)", moved)
	}
}

// An explicit day asked of the tie-out is the dealer's day, taken at its last instant (the cut the
// GL route hands both arms). An invoice issued on that house day is counted, and one issued the next
// house day is not, at any wall hour.
func TestIntegrationEveningAge_TieOutBalanceCountsAnInvoiceIssuedOnTheExplicitDay(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	const day, nextDay = "1990-03-10", "1990-03-11"
	asOf, err := houseday.EndOfDay(day)
	if err != nil {
		t.Fatal(err)
	}
	base, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("baseline AR: %v", err)
	}
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-EXP", status: "FINALIZED", issue: day, total: 8000, balance: 8000, finalized: true})
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-EXP-NEXT", status: "FINALIZED", issue: nextDay, total: 500, balance: 500, finalized: true})
	got, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("AR after seeding: %v", err)
	}
	if delta := got - base; delta != 8000 {
		t.Fatalf("as of %s the tie-out moved by %d for an invoice issued that day (8000) and one issued the day after (500 must not count)", day, delta)
	}
}
