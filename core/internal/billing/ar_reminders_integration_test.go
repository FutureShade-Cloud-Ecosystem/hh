//go:build integration

// ar_reminders_integration_test.go, HH-185 against the real database.
//
// WHY A DATABASE TEST WHEN THE UNIT FILE ALREADY PINS THE SERVICE: because the damage is done in
// SQL. The paper-path sweep counts rows WHERE status = 'DELIVERED', and the question a fake cannot
// answer is whether a send made with NO transport wired lands in that count. On the defect the nil
// sender resolved to the delivering stub, every send came back DELIVERED, the FILTER counted them,
// and the sweep flagged a customer as unreachable by email who had never been contacted at all.
//
// Assertions in the send loop report rather than abort, so a run against the defect shows the whole
// chain at once: the DELIVERED rows, the counted window, and the raised flag.
//
// Run: DATABASE_URL=... go test -tags integration ./internal/billing/... -run ARReminder -count=1
package billing

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestIntegrationARReminder_NoTransportIsNotADeliveredSendOrAFlag drives the REAL repository with
// a nil sender, the unwired default the service ships (the same transport the composition root
// names in words), and proves the phantom deliveries are neither counted by the detector's input
// nor flagged by its sweep.
func TestIntegrationARReminder_NoTransportIsNotADeliveredSendOrAFlag(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// The sweep's floor and window are policy knobs on a shared default row: run on known values
	// and put back what was there.
	repo := NewARLandingRepository(pool)
	stashed, err := repo.ReadARLandingPolicy(ctx, "")
	if err != nil {
		t.Fatalf("read policy: %v", err)
	}
	p := DefaultARLandingPolicy()
	p.Tier = stashed.Tier
	if err := repo.UpsertARLandingPolicy(ctx, p); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	t.Cleanup(func() { _ = repo.UpsertARLandingPolicy(context.Background(), stashed) })

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		// A gated run against a database with no BRANCH row is a broken fixture, not a skip:
		// the migrations plant branches, so reaching here means the gate variable points at a
		// database this suite did not migrate, and a polite skip would let the whole leg go
		// unasserted while the run reads green.
		t.Fatalf("no seeded BRANCH (migrate the gate database before this suite): %v", err)
	}
	suffix := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)
	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id)
		 VALUES ('HH185-'||$1, 'HH 185 Rig '||$1, 'CONTRACTOR', $2::uuid) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// ar_ap_contacts, ar_reminder_sends (and its events) and ar_paper_path_flags all cascade from
	// customers, so one delete unwinds the whole rig.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})

	// THE UNWIRED DEFAULT: no sender injected, the same default the service ships when nothing is
	// passed, which is also what the composition root now names in words at wire_tops. The nil
	// sender is UnwiredSender, so every send below is recorded and marked FAILED.
	landing := NewARLandingService(repo)
	svc := NewARReminderService(repo, landing, nil, "https://dibbits.example")
	if _, err := svc.TagAPContact(ctx, APContact{CustomerID: customerID, Name: "AP Desk", Email: "ap@hh185.example"}, ""); err != nil {
		t.Fatalf("tag AP contact: %v", err)
	}

	// Exactly the policy's send floor: on the defect this many phantom deliveries is a flagged
	// account, which is the reported outcome, a customer marked unreachable who was never emailed.
	for i := 0; i < p.BlackHoleMinSends; i++ {
		got, err := svc.Send(ctx, SendReminderInput{CustomerID: customerID, Channel: ChannelEmail}, "u1")
		if err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
		if got.Status != SendFailed {
			t.Errorf("send %d status = %s, want %s: no transport is wired, nothing left the server", i+1, got.Status, SendFailed)
		}
		if got.DeliveredAt != nil {
			t.Errorf("send %d stamped delivered_at on a message that was never sent", i+1)
		}
	}

	windows, err := repo.EngagementWindows(ctx, p.BlackHoleWindowDays, time.Now())
	if err != nil {
		t.Fatalf("engagement windows: %v", err)
	}
	var mine *EngagementWindow
	for i := range windows {
		if windows[i].CustomerID == customerID {
			mine = &windows[i]
		}
	}
	if mine == nil {
		t.Fatal("the seeded customer has sends in the window but no engagement row")
	}
	if mine.DeliveredSends != 0 {
		t.Errorf("delivered sends = %d, want 0: the FILTER that feeds the paper-path sweep is counting sends that never left the server", mine.DeliveredSends)
	}

	if _, _, err := svc.SweepPaperPath(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	flags, err := repo.OpenPaperPathFlags(ctx)
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	for _, fl := range flags {
		if fl.CustomerID == customerID {
			t.Errorf("flag %s raised on an account that was never contacted: its frozen evidence says %d delivered sends", fl.FlagID, fl.DeliveredSends)
		}
	}

	// Round 2, the cadence leg: a FAILED send must not count as a reminder. The work list's
	// NextAction reads LastReminderAt, and a failed send that advances it moves the account from
	// "Send reminder" to "Call" for a whole cadence period although nobody was contacted, the same
	// phantom the DELIVERED rows were. Only a message that left the server is a reminder.
	eng, err := repo.EngagementFor(ctx, []string{customerID}, p.BlackHoleWindowDays, time.Now())
	if err != nil {
		t.Fatalf("engagement: %v", err)
	}
	in, ok := eng[customerID]
	if !ok {
		t.Fatalf("no engagement row for the seeded customer")
	}
	if in.LastReminderAt != nil {
		t.Errorf("last reminder at %v, want never: a FAILED send is not a reminder and must not advance the cadence", in.LastReminderAt)
	}
	if got := NextAction(in, p, time.Now()); got != "Send reminder" {
		t.Errorf("next action = %q, want %q: the work list moved on a send that never left the server", got, "Send reminder")
	}

	// Round 3, the other direction: a DELIVERED send IS a reminder. The filter is pinned both
	// ways or not at all: a predicate that counted no send (or only QUEUED rows) would pass the
	// leg above while the cadence stopped moving for real deliveries. The delivering stub is
	// injected explicitly, the test seam it exists for, and one delivered send must set
	// LastReminderAt and hold the work list on "Call" inside the cadence window.
	deliveredSvc := NewARReminderService(repo, landing, StubSender{}, "https://dibbits.example")
	dgot, err := deliveredSvc.Send(ctx, SendReminderInput{CustomerID: customerID, Channel: ChannelEmail}, "u1")
	if err != nil {
		t.Fatalf("delivered send: %v", err)
	}
	if dgot.Status != SendDelivered {
		t.Fatalf("delivered send status = %s, want %s: the leg needs a row that actually delivered", dgot.Status, SendDelivered)
	}
	deng, err := repo.EngagementFor(ctx, []string{customerID}, p.BlackHoleWindowDays, time.Now())
	if err != nil {
		t.Fatalf("engagement after delivery: %v", err)
	}
	din, ok := deng[customerID]
	if !ok {
		t.Fatalf("no engagement row for the seeded customer after delivery")
	}
	if din.LastReminderAt == nil {
		t.Errorf("last reminder at never after a DELIVERED send: only delivered sends advance the cadence, and this one did deliver")
	}
	if got := NextAction(din, p, time.Now()); got != "Call" {
		t.Errorf("next action = %q, want %q: inside the cadence window a delivered reminder holds the work list on Call", got, "Call")
	}
}
