//go:build integration

// Integration cover for the Wave 3 comms bundle against a real migrated Postgres (0109 + 0110).
// Run with: DATABASE_URL=... go test -tags integration -count=1 -v ./internal/messagingprovider/
//
// WHAT ONLY A DATABASE CAN PROVE HERE:
//   - the AR cadence is IDEMPOTENT, re-running the sweep over an unchanged ledger enqueues nothing,
//     because the UNIQUE (invoice_id, bucket_days) constraint says so, not because a loop said so;
//   - an OPTED-OUT customer resolves to nobody, through the real consent table;
//   - the per-customer COOLDOWN really does turn eleven overdue invoices into one text;
//   - a notice enqueued outside the send window carries a not_before that keeps it invisible to the
//     drain, and a held row accrues NO delivery attempts;
//   - the whole path runs FLAG-OFF: rows are enqueued, the STUB notifier records them, and no live
//     provider is ever constructed.
//
// FIXTURES RESTORE WHAT THEY TOUCH. Every test creates its own customer + invoices under a per-test
// marker and deletes them (and every row they caused) on cleanup, so the package is order-independent
// under -shuffle=on and safe beside every other package running in parallel.
//
// DIB-396: no test in this file asserts or transiently breaks a WHOLE-DB invariant. They read and
// write only rows they created, keyed by a per-test marker, so no advisory lease is required. That is
// a deliberate design of the fixtures, not an omission, see arFixture.

package messagingprovider

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// arFixture is one test's isolated world: a customer, that customer's SMS consent, and N invoices at
// chosen ages. Everything it creates is removed on cleanup, in FK-safe order.
type arFixture struct {
	pool       *pgxpool.Pool
	CustomerID string
	BranchID   string
	Marker     string
	invoiceIDs []string
}

func newARFixture(t *testing.T) *arFixture {
	t.Helper()
	pool := smsPool(t)
	ctx := context.Background()

	var marker string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&marker); err != nil {
		t.Fatalf("mint marker: %v", err)
	}
	f := &arFixture{pool: pool, Marker: marker}

	// A branch to hang invoices off. Any existing one will do, invoices need a real FK and this test
	// asserts nothing about branches.
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY created_at LIMIT 1`).Scan(&f.BranchID); err != nil {
		t.Skipf("no branch in this database (run the seed first): %v", err)
	}

	// A customer of this test's own, so no other suite's customer is ever texted or counted.
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, payment_terms_days)
		VALUES ($1, $2, 30) RETURNING id::text`,
		"W3D-"+marker[:8], "W3D fixture "+marker[:8]).Scan(&f.CustomerID); err != nil {
		t.Fatalf("create fixture customer: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		// Order matters: the ledgers and the queue first, then the invoices, then the customer.
		_, _ = pool.Exec(c, `DELETE FROM ar_notice_log WHERE customer_id = $1`, f.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM sms_outbox WHERE payload->>'customer_id' = $1`, f.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, f.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM customer_sms_prefs WHERE customer_id = $1`, f.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, f.CustomerID)
	})
	return f
}

// consent gives the fixture customer a verified, opted-in number.
func (f *arFixture) consent(t *testing.T, optedOut bool) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO customer_sms_prefs (customer_id, phone_e164, sms_verified, verified_at, opted_out, opted_out_at)
		VALUES ($1, '+16135550199', TRUE, now(), $2, CASE WHEN $2 THEN now() END)
		ON CONFLICT (customer_id) DO UPDATE SET opted_out = EXCLUDED.opted_out, opted_out_at = EXCLUDED.opted_out_at`,
		f.CustomerID, optedOut)
	if err != nil {
		t.Fatalf("seed consent: %v", err)
	}
}

// invoice creates one FINALIZED invoice `daysPastDue` days late with the given balance.
//
// Ages are anchored to businessHours(t), the SAME pinned clock the sweep runs at, not to
// CURRENT_DATE. Anchoring to CURRENT_DATE was a dated time bomb: the fixture aged with the wall
// clock while the sweep's "now" stayed at 2026-08-05, so every invoice drifted one day younger
// per real day until it slid under its bucket boundary (armed 2026-08-11; found by the W-CAT
// gate lane, fixed at the union per its prescription).
func (f *arFixture) invoice(t *testing.T, daysPastDue int, balanceCents int64) string {
	t.Helper()
	var id string
	asOf := businessHours(t).Format("2006-01-02")
	err := f.pool.QueryRow(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, total_cents, balance_cents)
		VALUES ($1, $2, $3, 'FINALIZED', $6::date - $4::int - 30, $6::date - $4::int, $5, $5, $5)
		RETURNING id::text`,
		fmt.Sprintf("W3D-%s-%d", f.Marker[:8], len(f.invoiceIDs)+1), f.CustomerID, f.BranchID, daysPastDue, balanceCents, asOf).
		Scan(&id)
	if err != nil {
		t.Fatalf("create fixture invoice: %v", err)
	}
	f.invoiceIDs = append(f.invoiceIDs, id)
	return id
}

// sweep builds an ARSweep whose aging source sees ONLY this fixture's invoices, at a pinned clock.
func (f *arFixture) sweep(t *testing.T, now time.Time) (*ARSweep, *ConsentResolver) {
	t.Helper()
	resolver := NewConsentResolver(NewPrefsStore(f.pool), slog.Default())
	s := NewARSweep(f.pool, NewOutbox(f.pool), resolver, slog.Default()).
		WithSource(scopedAging{pool: f.pool, customerID: f.CustomerID}).
		WithClock(func() time.Time { return now })
	return s, resolver
}

// scopedAging is the real aging projection narrowed to one customer, so a test can never enqueue a
// notice about somebody else's invoice on a shared database.
type scopedAging struct {
	pool       *pgxpool.Pool
	customerID string
}

func (a scopedAging) OverdueInvoices(ctx context.Context, asOf time.Time, minDays, limit int) ([]AgingInvoice, error) {
	all, err := NewPgARAging(a.pool).OverdueInvoices(ctx, asOf, minDays, 5000)
	if err != nil {
		return nil, err
	}
	var out []AgingInvoice
	for _, inv := range all {
		if inv.CustomerID == a.customerID {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (f *arFixture) countQueued(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sms_outbox WHERE topic = $1 AND payload->>'customer_id' = $2`,
		TopicARNotice, f.CustomerID).Scan(&n); err != nil {
		t.Fatalf("count queued: %v", err)
	}
	return n
}

func (f *arFixture) ledger(t *testing.T) map[int]bool {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT bucket_days, suppressed FROM ar_notice_log WHERE customer_id = $1`, f.CustomerID)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var b int
		var sup bool
		if err := rows.Scan(&b, &sup); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		out[b] = sup
	}
	return out
}

// businessHours is a fixed instant inside the send window, so the tests below are not implicitly
// testing quiet hours (that has its own test).
func businessHours(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(DefaultZone)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return time.Date(2026, time.August, 5, 10, 0, 0, 0, loc)
}

// TestIntegration_ARCadence_IsIdempotent_ReRunEnqueuesNothing is the acceptance criterion for DIB-76:
// running the job twice must not text a customer twice.
func TestIntegration_ARCadence_IsIdempotent_ReRunEnqueuesNothing(t *testing.T) {
	f := newARFixture(t)
	f.consent(t, false)
	f.invoice(t, 35, 250000) // one invoice, comfortably into the 30-day bucket

	sweep, _ := f.sweep(t, businessHours(t))
	ctx := context.Background()

	first, err := sweep.Run(ctx)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first.Enqueued != 1 {
		t.Fatalf("first sweep enqueued %d, want 1 (result %+v)", first.Enqueued, first)
	}
	if got := f.countQueued(t); got != 1 {
		t.Fatalf("sms_outbox has %d rows after the first sweep, want 1", got)
	}

	// The SAME sweep again, and a THIRD run from a fresh instance, the guarantee must not depend on
	// in-process state.
	second, err := sweep.Run(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	fresh, _ := f.sweep(t, businessHours(t).Add(2*time.Hour))
	third, err := fresh.Run(ctx)
	if err != nil {
		t.Fatalf("third sweep: %v", err)
	}

	if second.Enqueued != 0 || third.Enqueued != 0 {
		t.Fatalf("re-running the sweep enqueued more notices (second=%d, third=%d), the cadence is not idempotent",
			second.Enqueued, third.Enqueued)
	}
	if got := f.countQueued(t); got != 1 {
		t.Fatalf("sms_outbox has %d AR rows after three sweeps, want exactly 1", got)
	}
}

// TestIntegration_ARCadence_OneNoticePerBucket_AndBacklogIsBackfilled. An invoice that is already 95
// days late when the job first runs gets the 90-day message ONCE, and the 30/60 buckets are recorded
// as superseded so tomorrow's run does not walk the ladder sending three texts on three nights.
func TestIntegration_ARCadence_OneNoticePerBucket_AndBacklogIsBackfilled(t *testing.T) {
	f := newARFixture(t)
	f.consent(t, false)
	f.invoice(t, 95, 500000)

	sweep, _ := f.sweep(t, businessHours(t))
	if _, err := sweep.Run(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	ledger := f.ledger(t)
	for _, b := range []int{30, 60, 90} {
		if _, ok := ledger[b]; !ok {
			t.Fatalf("bucket %d has no ledger row, the backlog was not backfilled, so tomorrow's run will send again", b)
		}
	}
	if ledger[90] {
		t.Fatalf("the 90-day bucket must be the one that SENT (suppressed=false)")
	}
	if !ledger[30] || !ledger[60] {
		t.Fatalf("the 30 and 60 buckets must be recorded as SUPPRESSED, got 30=%v 60=%v", ledger[30], ledger[60])
	}
	if got := f.countQueued(t); got != 1 {
		t.Fatalf("a 95-day invoice queued %d notices, want exactly 1 (the 90-day message)", got)
	}
}

// TestIntegration_ARCadence_CooldownTurnsElevenInvoicesIntoOneText. Without this a contractor with a
// dozen overdue invoices gets a dozen messages in one second and the sending number is filtered.
func TestIntegration_ARCadence_CooldownTurnsElevenInvoicesIntoOneText(t *testing.T) {
	f := newARFixture(t)
	f.consent(t, false)
	for i := 0; i < 11; i++ {
		f.invoice(t, 40+i, int64(10000*(i+1)))
	}

	sweep, _ := f.sweep(t, businessHours(t))
	res, err := sweep.Run(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Enqueued != 1 {
		t.Fatalf("sweep enqueued %d notices for one customer, want 1, the cooldown did not hold (%+v)", res.Enqueued, res)
	}
	if got := f.countQueued(t); got != 1 {
		t.Fatalf("sms_outbox has %d AR rows for one customer, want 1", got)
	}
	if res.Suppressed == 0 {
		t.Fatalf("the other ten invoices must be RECORDED as suppressed, not silently ignored")
	}
}

// TestIntegration_ARCadence_OptedOutCustomerIsNeverQueued, the kill switch, through the real table.
func TestIntegration_ARCadence_OptedOutCustomerIsNeverQueued(t *testing.T) {
	f := newARFixture(t)
	f.consent(t, true) // opted out
	f.invoice(t, 45, 300000)

	sweep, resolver := f.sweep(t, businessHours(t))
	if _, err := resolver.CustomerRecipient(context.Background(), f.CustomerID); err == nil {
		t.Fatalf("the resolver returned a recipient for an OPTED-OUT customer")
	}

	res, err := sweep.Run(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Enqueued != 0 {
		t.Fatalf("sweep enqueued %d notices for an opted-out customer, want 0", res.Enqueued)
	}
	if got := f.countQueued(t); got != 0 {
		t.Fatalf("sms_outbox has %d rows for an opted-out customer, want 0", got)
	}
	// The decision is still RECORDED, so tomorrow's run does not reconsider it and an operator can see
	// why nothing was sent.
	if len(f.ledger(t)) == 0 {
		t.Fatalf("a suppressed-for-consent decision must still be written to the ledger")
	}
}

// TestIntegration_ARCadence_NoConsentRowMeansNoText. The absent state is silence: a customer we have
// never asked is a customer we may not text.
func TestIntegration_ARCadence_NoConsentRowMeansNoText(t *testing.T) {
	f := newARFixture(t) // deliberately NO consent row
	f.invoice(t, 45, 300000)

	sweep, _ := f.sweep(t, businessHours(t))
	res, err := sweep.Run(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Enqueued != 0 || f.countQueued(t) != 0 {
		t.Fatalf("a customer with NO consent row was queued a notice (enqueued=%d), absence of a row must mean no",
			res.Enqueued)
	}
}

// TestIntegration_ARCadence_QuietHoursHoldTheNotice_AndItAccruesNoAttempts. A sweep that runs at 22:00
// stamps not_before at 08:00 the next morning; the row is invisible to the drain until then, and
// waiting must never count as a delivery failure.
func TestIntegration_ARCadence_QuietHoursHoldTheNotice_AndItAccruesNoAttempts(t *testing.T) {
	loc, err := time.LoadLocation(DefaultZone)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	f := newARFixture(t)
	f.consent(t, false)
	f.invoice(t, 33, 125000)

	// Anchored to TODAY at 22:00 local, not to a fixed calendar date.
	//
	// This was `time.Date(2026, time.August, 5, 22, 0, 0, 0, loc)`, which made not_before a real
	// instant, 2026-08-06 08:00 EDT, and the last assertion in this test compares that instant
	// against Postgres's now() through dueClause (`not_before <= now()`). The test therefore passed
	// only while the wall clock was behind that timestamp, and at 08:00 on 2026-08-06 it began
	// failing permanently. Not a flake: a fixed-date test that expired. The W6 DIB-419 anchor sweep
	// caught it 5/5 red on the very day it went off.
	//
	// Deriving the evening from time.Now keeps the FACT under test identical (a notice decided at
	// 22:00 is held to 08:00 the next morning, and is not yet due) while making not_before always
	// genuinely in the future, at any hour the suite runs.
	nowLocal := time.Now().In(loc)
	lateEvening := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 22, 0, 0, 0, loc)
	wantMorning := lateEvening.AddDate(0, 0, 1)
	sweep, _ := f.sweep(t, lateEvening)
	if _, err := sweep.Run(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var notBefore *time.Time
	var attempts int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT not_before, attempts FROM sms_outbox WHERE payload->>'customer_id' = $1`, f.CustomerID).
		Scan(&notBefore, &attempts); err != nil {
		t.Fatalf("read queued notice: %v", err)
	}
	if notBefore == nil {
		t.Fatalf("a notice decided at 22:00 must carry a not_before, it was queued for immediate send")
	}
	if local := notBefore.In(loc); local.Hour() != 8 || local.Day() != wantMorning.Day() {
		t.Fatalf("not_before = %s local, want 08:00 on %s", local.Format(time.RFC3339), wantMorning.Format("2006-01-02"))
	}
	if attempts != 0 {
		t.Fatalf("a held notice has %d attempts, want 0, waiting is not a delivery failure", attempts)
	}

	// And the drain genuinely cannot see it yet.
	//
	// This asserts the claim PREDICATE rather than calling ProcessBatch, deliberately: ProcessBatch
	// claims the oldest due row in the WHOLE table, so on a shared database it would reach into
	// another package's fixture. The predicate below is the one processOne uses, narrowed to this
	// test's own row, same guarantee, no cross-suite reach.
	var visible bool
	if err := f.pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM sms_outbox
			WHERE payload->>'customer_id' = $1 AND `+dueClause+`
		)`, f.CustomerID).Scan(&visible); err != nil {
		t.Fatalf("due check: %v", err)
	}
	if visible {
		t.Fatalf("a notice held until tomorrow morning is visible to the drain right now")
	}
}

// TestIntegration_ARCadence_CooldownSurvivesAcrossRuns is the arm the eleven-invoice test above does
// NOT reach, and finding that out was the point of mutation-checking it.
//
// In a single run the two suppression arms, the in-process "already texted this customer" map and the
// ar_notice_log cooldown query, are redundant: each invoice is committed before the next is
// considered, so the DB query alone would catch every duplicate. Disabling either one on its own left
// the eleven-invoice test green, which means that test pins neither arm specifically.
//
// THIS test isolates the durable one. Two separate runs, a day apart, with a second overdue invoice
// appearing in between: the in-process map is empty on the second run, so the only thing that can hold
// the notice back is the ledger.
func TestIntegration_ARCadence_CooldownSurvivesAcrossRuns(t *testing.T) {
	f := newARFixture(t)
	f.consent(t, false)
	f.invoice(t, 40, 100000)

	day1, _ := f.sweep(t, businessHours(t))
	if res, err := day1.Run(context.Background()); err != nil || res.Enqueued != 1 {
		t.Fatalf("day 1 sweep = %+v / %v, want one notice", res, err)
	}

	// A second invoice for the SAME customer crosses a bucket the next day, well inside the cooldown.
	f.invoice(t, 65, 200000)
	day2, _ := f.sweep(t, businessHours(t).Add(24*time.Hour))
	res, err := day2.Run(context.Background())
	if err != nil {
		t.Fatalf("day 2 sweep: %v", err)
	}
	if res.Enqueued != 0 {
		t.Fatalf("day 2 enqueued %d notices, the customer was texted twice in two days, which is what the cooldown exists to prevent", res.Enqueued)
	}
	if got := f.countQueued(t); got != 1 {
		t.Fatalf("sms_outbox holds %d AR notices for this customer, want 1 across both runs", got)
	}
	if res.Suppressed != 1 {
		t.Fatalf("the second invoice's bucket must be RECORDED as suppressed, got %+v", res)
	}
	var why string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT suppress_why FROM ar_notice_log WHERE customer_id = $1 AND suppressed ORDER BY decided_at DESC LIMIT 1`,
		f.CustomerID).Scan(&why); err != nil {
		t.Fatalf("read suppression reason: %v", err)
	}
	if !contains(why, "cooldown") {
		t.Fatalf("suppression reason = %q, want it to name the cooldown so an operator can read it", why)
	}
}
