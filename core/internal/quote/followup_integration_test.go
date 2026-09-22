//go:build integration

// Integration cover for the quote follow-up cadence (DIB-58) against a real migrated Postgres (0110).
// Run with: DATABASE_URL=... go test -tags integration -count=1 -v ./internal/quote/
//
// WHAT ONLY A DATABASE CAN PROVE HERE:
//   - the cadence is IDEMPOTENT, a second sweep over the same day enqueues nothing, because
//     UNIQUE (quote_id, step_day, channel) says so rather than a loop variable;
//   - only the steps that are DUE fire (day 3 today does not also fire day 7 and day 14);
//   - an ACCEPTED quote cancels its own remaining steps, with the reason recorded;
//   - an opted-out customer's SMS step is SKIPPED with a reason while the EMAIL step still goes;
//   - the one-click disable stops future steps and leaves the history intact.
//
// FIXTURES RESTORE WHAT THEY TOUCH: each test creates its own customer + quote and deletes everything
// it caused on cleanup, so the package is order-independent under -shuffle=on.
//
// DIB-396: nothing here asserts or transiently breaks a whole-DB invariant, every read and write is
// keyed to this test's own quote, so no advisory lease is taken.

package quote

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func followupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("no database: %v", err)
		return nil
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("database unreachable: %v", err)
		return nil
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeQueue is a NoticeQueue that records rather than enqueues. The DB-backed queue is proven in
// internal/messagingprovider; what THIS suite proves is the cadence's decisions, and a fake makes
// "which steps fired" readable without draining a shared table.
type fakeQueue struct {
	consent  map[string]bool
	notBefor time.Time
	sms      []FollowUpNotice
	email    []FollowUpNotice
	shares   []ShareNotice
	n        int
}

func (q *fakeQueue) MaySendSMS(_ context.Context, customerID string, at time.Time) (bool, time.Time, error) {
	if !q.consent[customerID] {
		return false, time.Time{}, nil
	}
	if q.notBefor.IsZero() {
		return true, at, nil
	}
	return true, q.notBefor, nil
}

func (q *fakeQueue) EnqueueQuoteShare(_ context.Context, n ShareNotice, _ time.Time) (string, error) {
	q.shares = append(q.shares, n)
	return q.id(), nil
}

func (q *fakeQueue) EnqueueFollowUpSMS(_ context.Context, n FollowUpNotice, _ time.Time) (string, error) {
	q.sms = append(q.sms, n)
	return q.id(), nil
}

func (q *fakeQueue) EnqueueFollowUpEmail(_ context.Context, n FollowUpNotice) (string, error) {
	q.email = append(q.email, n)
	return q.id(), nil
}

// id returns a syntactically valid uuid so the event_id column (a real uuid) accepts it.
func (q *fakeQueue) id() string {
	q.n++
	return "00000000-0000-4000-9000-" + pad12(q.n)
}

func pad12(n int) string {
	s := ""
	for i := 0; i < 12; i++ {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

type fakeContacts struct{ email string }

func (c fakeContacts) PrimaryContact(_ context.Context, _ string) (string, string, error) {
	return "Fixture Contact", c.email, nil
}

// cadenceFixture is one test's isolated quote.
type cadenceFixture struct {
	pool       *pgxpool.Pool
	repo       *FollowUpRepository
	QuoteID    string
	CustomerID string
}

func newCadenceFixture(t *testing.T, status string, validUntil time.Time) *cadenceFixture {
	t.Helper()
	pool := followupPool(t)
	ctx := context.Background()

	var marker string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&marker); err != nil {
		t.Fatalf("marker: %v", err)
	}
	f := &cadenceFixture{pool: pool, repo: NewFollowUpRepository(pool)}

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY created_at LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no branch in this database (run the seed first): %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name) VALUES ($1, $2) RETURNING id::text`,
		"W3DQ-"+marker[:8], "W3D cadence "+marker[:8]).Scan(&f.CustomerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO quotes (quote_no, customer_id, branch_id, status, valid_until, subtotal_cents, tax_cents, total_cents)
		VALUES ($1, $2, $3, $4, $5, 400000, 52000, 452000) RETURNING id::text`,
		"QW3D-"+marker[:8], f.CustomerID, branchID, status, validUntil).Scan(&f.QuoteID); err != nil {
		t.Fatalf("create quote: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM quote_followup_events WHERE quote_id = $1`, f.QuoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_followup_plans WHERE quote_id = $1`, f.QuoteID)
		_, _ = pool.Exec(c, `DELETE FROM quote_share_links WHERE quote_id = $1`, f.QuoteID)
		_, _ = pool.Exec(c, `DELETE FROM quotes WHERE id = $1`, f.QuoteID)
		_, _ = pool.Exec(c, `DELETE FROM customer_sms_prefs WHERE customer_id = $1`, f.CustomerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, f.CustomerID)
	})
	return f
}

// scopedStore is the cadence repository narrowed to ONE quote's plan.
//
// It exists because SweepFollowUps is global by design, the nightly job must consider every enabled
// plan in the database. A test that called it unscoped would sweep the plans belonging to every other
// test in this package (and to the walkthrough's demo quote), which is both a false assertion and a
// fixture that writes rows it does not clean up. Narrowing the ONE method that enumerates plans keeps
// the test honest and the package order-independent under -shuffle=on and in parallel.
type scopedStore struct {
	*FollowUpRepository
	quoteID string
}

func (s scopedStore) EnabledPlans(ctx context.Context, limit int) ([]FollowUpPlan, error) {
	all, err := s.FollowUpRepository.EnabledPlans(ctx, limit)
	if err != nil {
		return nil, err
	}
	var out []FollowUpPlan
	for _, p := range all {
		if p.QuoteID == s.quoteID {
			out = append(out, p)
		}
	}
	return out, nil
}

// service builds an AutomationService pinned to `now`, over a queue with the given consent.
func (f *cadenceFixture) service(t *testing.T, now time.Time, consented bool, email string) (*AutomationService, *fakeQueue) {
	t.Helper()
	q := &fakeQueue{consent: map[string]bool{f.CustomerID: consented}}
	svc := NewAutomationService(scopedStore{FollowUpRepository: f.repo, quoteID: f.QuoteID}).
		WithQueue(q).
		WithContacts(fakeContacts{email: email}).
		WithBaseURL("http://localhost:5173").
		WithClock(func() time.Time { return now })
	return svc, q
}

// plan installs a cadence that started `daysAgo` days ago.
func (f *cadenceFixture) plan(t *testing.T, channel string, offsets []int, daysAgo int, now time.Time) {
	t.Helper()
	if _, err := f.repo.EnsurePlan(context.Background(), f.QuoteID, channel, offsets, now.AddDate(0, 0, -daysAgo)); err != nil {
		t.Fatalf("ensure plan: %v", err)
	}
}

func (f *cadenceFixture) events(t *testing.T) []FollowUpEvent {
	t.Helper()
	ev, err := f.repo.Events(context.Background(), f.QuoteID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return ev
}

// cadenceNow pins the cadence clock (edit_test.go already owns the name fixedNow).
var cadenceNow = time.Date(2026, time.August, 5, 14, 0, 0, 0, time.UTC)

// TestIntegration_Cadence_OnlyDueStepsFire_AndReRunningIsANoOp is the DIB-58 acceptance criterion:
// day 3 fires today, days 7 and 14 do not, and running the sweep again changes nothing.
func TestIntegration_Cadence_OnlyDueStepsFire_AndReRunningIsANoOp(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelBoth, DefaultOffsets, 4, cadenceNow) // sent 4 days ago: day 3 is due, 7 and 14 are not
	svc, q := f.service(t, cadenceNow, true, "buyer@example.test")

	first, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first.Enqueued != 2 {
		t.Fatalf("first sweep enqueued %d, want 2 (day 3 on both channels): %+v", first.Enqueued, first)
	}
	if len(q.sms) != 1 || len(q.email) != 1 {
		t.Fatalf("queue got sms=%d email=%d, want 1 each", len(q.sms), len(q.email))
	}
	if q.sms[0].StepDay != 3 {
		t.Fatalf("the fired step is day %d, want 3, a future step fired early", q.sms[0].StepDay)
	}

	second, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.Enqueued != 0 {
		t.Fatalf("re-running the sweep enqueued %d more steps, the cadence is not idempotent", second.Enqueued)
	}
	if len(q.sms) != 1 || len(q.email) != 1 {
		t.Fatalf("after two sweeps the queue holds sms=%d email=%d, want 1 each", len(q.sms), len(q.email))
	}
	if got := len(f.events(t)); got != 2 {
		t.Fatalf("history has %d rows after two sweeps, want 2", got)
	}
}

// TestIntegration_Cadence_AcceptedQuoteCancelsRemainingSteps, the cadence stops on ACCEPTED, and the
// steps that will now never happen are RECORDED as cancelled rather than silently vanishing.
func TestIntegration_Cadence_AcceptedQuoteCancelsRemainingSteps(t *testing.T) {
	f := newCadenceFixture(t, "ACCEPTED", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelSMS, DefaultOffsets, 10, cadenceNow)
	svc, q := f.service(t, cadenceNow, true, "buyer@example.test")

	res, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Enqueued != 0 {
		t.Fatalf("an ACCEPTED quote enqueued %d follow-ups, want 0", res.Enqueued)
	}
	if len(q.sms) != 0 {
		t.Fatalf("an ACCEPTED quote sent %d texts, want 0", len(q.sms))
	}
	if res.Cancelled != 3 {
		t.Fatalf("cancelled %d steps, want 3 (the whole cadence)", res.Cancelled)
	}
	for _, e := range f.events(t) {
		if e.Status != StepCancelled || e.Reason == "" {
			t.Fatalf("step %d/%s = %s (%q), want CANCELLED with a reason", e.StepDay, e.Channel, e.Status, e.Reason)
		}
	}
	plan, err := f.repo.Plan(context.Background(), f.QuoteID)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Enabled {
		t.Fatalf("an ACCEPTED quote's plan must be disabled")
	}
}

// TestIntegration_Cadence_ExpiredQuoteStops, expiry is COMPUTED from valid_until, because nothing in
// this program flips a quote's status on a timer.
func TestIntegration_Cadence_ExpiredQuoteStops(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, -5)) // valid_until five days ago
	f.plan(t, ChannelSMS, DefaultOffsets, 10, cadenceNow)
	svc, q := f.service(t, cadenceNow, true, "buyer@example.test")

	res, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Enqueued != 0 || len(q.sms) != 0 {
		t.Fatalf("an expired quote followed up (enqueued=%d, texts=%d)", res.Enqueued, len(q.sms))
	}
	if res.Cancelled == 0 {
		t.Fatalf("an expired quote must cancel its remaining steps")
	}
}

// TestIntegration_Cadence_OptOutKillsTheSMSStepOnly, the customer said stop TEXTING, not stop
// emailing, and the history says which one was skipped and why.
func TestIntegration_Cadence_OptOutKillsTheSMSStepOnly(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelBoth, DefaultOffsets, 4, cadenceNow)
	svc, q := f.service(t, cadenceNow, false /* opted out */, "buyer@example.test")

	res, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(q.sms) != 0 {
		t.Fatalf("an opted-out customer was texted %d times", len(q.sms))
	}
	if len(q.email) != 1 {
		t.Fatalf("the email step must still go: got %d", len(q.email))
	}
	if res.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the SMS step)", res.Skipped)
	}
	var sawSkip bool
	for _, e := range f.events(t) {
		if e.Channel == ChannelSMS {
			if e.Status != StepSkipped || e.Reason == "" {
				t.Fatalf("the SMS step is %s (%q), want SKIPPED with a reason a salesperson can read", e.Status, e.Reason)
			}
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatalf("the skipped SMS step must be RECORDED, an absent row tells the salesperson nothing")
	}
}

// TestIntegration_Cadence_NoEmailAddressIsRecordedAsSkipped.
func TestIntegration_Cadence_NoEmailAddressIsRecordedAsSkipped(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelEmail, DefaultOffsets, 4, cadenceNow)
	svc, q := f.service(t, cadenceNow, true, "" /* no email on file */)

	res, err := svc.SweepFollowUps(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(q.email) != 0 || res.Skipped != 1 {
		t.Fatalf("a customer with no email address: emails=%d skipped=%d, want 0 / 1", len(q.email), res.Skipped)
	}
}

// TestIntegration_Cadence_DisableStopsFutureStepsAndKeepsHistory, the one-click switch.
func TestIntegration_Cadence_DisableStopsFutureStepsAndKeepsHistory(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelSMS, DefaultOffsets, 4, cadenceNow)
	svc, q := f.service(t, cadenceNow, true, "buyer@example.test")
	ctx := context.Background()

	if _, err := svc.SweepFollowUps(ctx); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if len(q.sms) != 1 {
		t.Fatalf("day 3 did not fire (%d texts)", len(q.sms))
	}

	// One click.
	if err := f.repo.SetEnabled(ctx, f.QuoteID, false, "", "turned off by staff"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Now move the clock past day 7 and sweep again: nothing more may go out.
	later, _ := f.service(t, cadenceNow.AddDate(0, 0, 10), true, "buyer@example.test")
	res, err := later.SweepFollowUps(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if res.PlansConsidered != 0 {
		t.Fatalf("a disabled plan was considered by the sweep")
	}

	// And the history of what already went out is intact.
	hist, err := svc.History(ctx, f.QuoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist.Events) != 1 || hist.Events[0].StepDay != 3 || hist.Events[0].Status != StepSent {
		t.Fatalf("history after disabling = %+v, want the day-3 SENT row preserved", hist.Events)
	}
	if hist.Automated {
		t.Fatalf("History must report automated=false once the plan is off")
	}
	if len(hist.Upcoming) != 0 {
		t.Fatalf("a disabled plan must show no upcoming steps, got %+v", hist.Upcoming)
	}
}

// TestIntegration_Cadence_HistoryShowsUpcomingSteps, the salesperson sees what is coming, not just
// what happened. Upcoming is computed from the plan, so it never needs a row written in advance.
func TestIntegration_Cadence_HistoryShowsUpcomingSteps(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	f.plan(t, ChannelSMS, DefaultOffsets, 4, cadenceNow)
	svc, _ := f.service(t, cadenceNow, true, "buyer@example.test")
	ctx := context.Background()

	if _, err := svc.SweepFollowUps(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	hist, err := svc.History(ctx, f.QuoteID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(hist.Events))
	}
	if len(hist.Upcoming) != 2 {
		t.Fatalf("upcoming = %+v, want the day-7 and day-14 steps", hist.Upcoming)
	}
	for _, u := range hist.Upcoming {
		if u.DueAt.Before(cadenceNow) {
			t.Fatalf("upcoming step day %d is due in the past (%s)", u.StepDay, u.DueAt)
		}
	}
	if !hist.Automated {
		t.Fatalf("an enabled plan must report automated=true")
	}
}

// TestIntegration_Cadence_EnsurePlanNeverRestartsARunningCadence. Re-sharing a quote on day 6 must not
// reset the clock, otherwise the customer receives the same three messages twice.
func TestIntegration_Cadence_EnsurePlanNeverRestartsARunningCadence(t *testing.T) {
	f := newCadenceFixture(t, "SENT", cadenceNow.AddDate(0, 0, 25))
	original := cadenceNow.AddDate(0, 0, -6)
	if _, err := f.repo.EnsurePlan(context.Background(), f.QuoteID, ChannelSMS, DefaultOffsets, original); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	plan, err := f.repo.EnsurePlan(context.Background(), f.QuoteID, ChannelBoth, []int{1}, cadenceNow)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if plan.StartedAt.UTC().Truncate(time.Second) != original.UTC().Truncate(time.Second) {
		t.Fatalf("started_at moved to %s, want the original %s, re-sharing restarted the cadence",
			plan.StartedAt, original)
	}
	if plan.Channel != ChannelSMS {
		t.Fatalf("channel changed to %s, EnsurePlan must not rewrite a running plan", plan.Channel)
	}
}
