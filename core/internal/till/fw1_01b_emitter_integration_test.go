//go:build integration

// fw1_01b_emitter_integration_test.go, FW1-01b round 2's emitter legs (S3's review,
// FROM-PRINCIPAL 112, P2): the till's events carry their document's own date. The drawer
// close carries the session's business day (the day the drawer was OPENED, even when the
// count happens after midnight), the bounce carries the NSF record's own instant, and the
// payment seam hands the GL the payment's received_at.
//
// The GL's half of the house zone leg (the consumer booking that event in the opening day's
// month) lives in internal/gl/fw1_01b_round2_integration_test.go: the GL's posting consumer
// is package-private and the till imports the GL, so the GL cannot import the till. No
// journal is posted from this file: a journal landed in a shared period would move balances
// other packages' tests read.
package till

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// TestIntegration_FW1_01B_R2_TillSessionCarriesItsOpeningDay is the till's half of the
// house zone leg: a drawer opened at 23:30 house time on the month's last day and counted
// at 00:30 carries its OPENING day on the event, never the count's day.
func TestIntegration_FW1_01B_R2_TillSessionCarriesItsOpeningDay(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	house := houseday.DefaultZone().Location()
	openedAt := time.Date(2026, 1, 31, 23, 30, 0, 0, house) // the drawer's own evening
	countAt := time.Date(2026, 2, 1, 0, 30, 0, 0, house)    // the count, after midnight

	sid, err := svc.OpenTill(cctx, OpenTillInput{BranchID: branchID, Station: "FW1B-R2-HZ", FloatCents: 20000})
	if err != nil {
		t.Fatalf("open the drawer: %v", err)
	}
	t.Cleanup(func() { cleanupSession(pool, sid) })
	// The till stamps the opening when the drawer opens; the leg moves it to the month's last
	// evening, the case the house zone leg is about.
	if _, err := pool.Exec(ctx, `UPDATE till_sessions SET opened_at = $2 WHERE id = $1::uuid`, sid, openedAt); err != nil {
		t.Fatalf("set the drawer's opening: %v", err)
	}
	if err := svc.CountTill(cctx, CountInput{TillSessionID: sid, Tenders: []TenderActual{{Method: MethodCash, ActualCents: 20000}}}); err != nil {
		t.Fatalf("count the drawer: %v", err)
	}
	if err := svc.store.SetCompleted(ctx, sid, branchID, userID, countAt, 0, 20000, ""); err != nil {
		t.Fatalf("close the drawer: %v", err)
	}

	var carried *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = 'till.session.completed' AND (payload->>'session_id')::uuid = $1::uuid`, sid).Scan(&carried); err != nil {
		t.Fatalf("read the session event: %v", err)
	}
	if carried == nil {
		t.Fatalf("the session event carries no occurred_at; the session's business day must ride it")
	}
	if !carried.UTC().Equal(openedAt.UTC()) {
		t.Fatalf("the session event carries %v, want the drawer's opening %v", carried.UTC(), openedAt.UTC())
	}
}

// TestIntegration_FW1_01B_R2_BounceCarriesTheNSFRecordsOwnInstant is the bounce's emitter
// leg: the payment.bounced event carries the NSF record's own created_at, the instant the
// bounce was recorded, and not the day the outbox row is read.
func TestIntegration_FW1_01B_R2_BounceCarriesTheNSFRecordsOwnInstant(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)

	pid, _, _, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: pid, Reason: "the round 2 date proof"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}

	var carried, recorded *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT (payload->>'occurred_at')::timestamptz FROM events_outbox
		  WHERE topic = 'payment.bounced' AND (payload->>'payment_id')::uuid = $1::uuid`, pid).Scan(&carried); err != nil {
		t.Fatalf("read the bounce event: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT created_at FROM payment_nsf WHERE payment_id = $1::uuid`, pid).Scan(&recorded); err != nil {
		t.Fatalf("read the NSF record: %v", err)
	}
	if carried == nil {
		t.Fatalf("the bounce event carries no occurred_at; the NSF record's own instant must ride it")
	}
	if !carried.UTC().Equal(recorded.UTC()) {
		t.Fatalf("the bounce event carries %v, want the NSF record's own instant %v", carried.UTC(), recorded.UTC())
	}
}

// TestIntegration_FW1_01B_R2_PaymentForGLHandsOverTheReceivedAt is the payment seam's leg:
// the till hands the GL the payment's received_at with the payment's other facts, so the
// receipt journal is dated by the payment and not by the day the outbox row was read.
func TestIntegration_FW1_01B_R2_PaymentForGLHandsOverTheReceivedAt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, _ := seedBranchUser(t, pool)
	pid, _, _, cleanup := seedNSFCase(t, pool, branchID, 50000)
	t.Cleanup(cleanup)

	receivedAt := time.Date(2026, 1, 31, 18, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE payments SET received_at = $2 WHERE id = $1::uuid`, pid, receivedAt); err != nil {
		t.Fatalf("set the payment's received_at: %v", err)
	}
	facts, err := NewRepository(pool).PaymentForGL(ctx, pid)
	if err != nil {
		t.Fatalf("the payment facts: %v", err)
	}
	if facts.ReceivedAt.IsZero() {
		t.Fatalf("the payment seam carries no received_at; the receipt's own date must ride it")
	}
	if !facts.ReceivedAt.UTC().Equal(receivedAt) {
		t.Fatalf("the payment seam carries %v, want the payment's received_at %v", facts.ReceivedAt.UTC(), receivedAt)
	}
}
