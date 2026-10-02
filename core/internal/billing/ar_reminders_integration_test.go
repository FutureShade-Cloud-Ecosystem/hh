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

// TestIntegrationARReminder_NoTransportIsNotADeliveredSendOrAFlag drives the REAL repository with a
// nil sender, the exact shape the composition root wires, and proves the phantom deliveries are
// neither counted by the detector's input nor flagged by its sweep.
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
		t.Skipf("no seeded BRANCH: %v", err)
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

	// THE PRODUCTION WIRING SHAPE: a nil sender, exactly the service the composition root builds.
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
}
