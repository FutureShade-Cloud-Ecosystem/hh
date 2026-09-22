//go:build integration

// w6c_notice_counts_integration_test.go, DIB-422 item 3: statement notification TURN-ON READINESS.
//
// The flag stays OFF. What changes is that an operator can now answer the question they have to
// answer before flipping it, which is not "does the wiring work" (W5-C proved that) but:
//
//	"if I turn this on tonight, how many text messages go out?"
//
// W5-C's run detail could not answer it. queued_count counts statements that were SENDABLE, and with
// the flag off that number is identical whether the notification path is wired correctly and switched
// off, or broken. Those are very different situations and an operator staring at one number cannot
// tell them apart, so the natural move is to flip the flag in production to find out, which is
// exactly the experiment nobody should run on customers.
//
// 0148 splits the number in two, and these tests pin both halves against real Postgres.
package billing

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationW6C_NoticeCounts_FlagOffMeasuresTheBlastRadius is the central claim: with the outbox
// unbound, the suppressed count is EXACTLY the number of messages the flip would have sent.
func TestIntegrationW6C_NoticeCounts_FlagOffMeasuresTheBlastRadius(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	rig.invoiceDueDaysAgo(t, "W6CNOTICE", 90_000, 40, false)

	// The shipped default, literally: NewStatementNoticeOutbox(nil, …) returns nil.
	svc := NewStatementService(rig.repo).WithOutbox(NewStatementNoticeOutbox(nil, StatementNoticeTopic))

	run, _, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("flag-off run: %v", err)
	}

	if run.NoticesEnabled {
		t.Error("notices_enabled must record that the outbox was UNBOUND for this run")
	}
	if run.NoticeQueuedCount != 0 {
		t.Errorf("an unbound outbox cannot have queued a notice; got %d", run.NoticeQueuedCount)
	}
	// THE NUMBER. One sendable statement ⇒ one message the flip would send.
	if run.NoticeSuppressedCount != 1 {
		t.Errorf("suppressed = %d, want 1, this is the count the operator reads before flipping",
			run.NoticeSuppressedCount)
	}
	// And it is not merely equal to queued_count by coincidence of this fixture; it is the STATED
	// invariant, which is what makes the pair readable as a whole.
	if !run.NoticeAccountingHolds() {
		t.Errorf("0148 invariant broken: queued=%d + suppressed=%d ≠ sendable=%d",
			run.NoticeQueuedCount, run.NoticeSuppressedCount, run.QueuedCount)
	}
	if !run.NoticeAccountingRecorded {
		t.Error("a run whose accounting landed must say so, or the surface shows a dash")
	}

	// Re-reading the run from the database must produce the same numbers. A run is a historical
	// record; if these were derived at display time from the CURRENT flag, the answer would change
	// under the operator between opening the page and acting on it.
	reread, _, err := svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.NoticesEnabled != run.NoticesEnabled ||
		reread.NoticeQueuedCount != run.NoticeQueuedCount ||
		reread.NoticeSuppressedCount != run.NoticeSuppressedCount {
		t.Errorf("a run's notice accounting must be STORED, not re-derived: %+v vs %+v", reread, run)
	}
}

// TestIntegrationW6C_NoticeCounts_FlagOnCountsRealEnqueues is the other side. It also proves the zero
// is MEASURED rather than absent: flag-on with a real queue gives suppressed=0 and queued=N, which is
// the mirror image of the test above from the same fixture.
func TestIntegrationW6C_NoticeCounts_FlagOnCountsRealEnqueues(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	cleanupSMSOutbox(t, rig.customerID)
	rig.invoiceDueDaysAgo(t, "W6CNOTICEON", 120_000, 55, false)

	q := &sqlNoticeQueue{}
	svc := NewStatementService(rig.repo).WithOutbox(NewStatementNoticeOutbox(q, StatementNoticeTopic))

	run, _, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("flag-on run: %v", err)
	}
	if !run.NoticesEnabled {
		t.Error("notices_enabled must record that the outbox WAS bound")
	}
	if run.NoticeQueuedCount != 1 {
		t.Errorf("notice_queued = %d, want 1", run.NoticeQueuedCount)
	}
	if run.NoticeSuppressedCount != 0 {
		t.Errorf("nothing is suppressed when the outbox is bound; got %d", run.NoticeSuppressedCount)
	}
	// The accounting must agree with what physically happened, not merely with itself.
	if q.rows != run.NoticeQueuedCount {
		t.Errorf("notice_queued (%d) must equal the rows actually enqueued (%d)", run.NoticeQueuedCount, q.rows)
	}
	if !run.NoticeAccountingHolds() {
		t.Error("0148 invariant broken on the flag-on path")
	}
}

// TestIntegrationW6C_NoticeCounts_ZeroSendableIsNotAMissingStamp pins the one ambiguity the derived
// flag exists to remove: a run with nothing worth sending is 0/0, which SATISFIES the invariant and
// must therefore read as "recorded, and the answer is zero" rather than as "not recorded".
func TestIntegrationW6C_NoticeCounts_ZeroSendableIsNotAMissingStamp(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)
	// No invoice at all ⇒ a zero balance ⇒ generated but NOT queued (DIB-166's ruling).
	svc := NewStatementService(rig.repo).WithOutbox(NewStatementNoticeOutbox(nil, StatementNoticeTopic))

	run, _, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("zero-balance run: %v", err)
	}
	if run.QueuedCount != 0 {
		t.Fatalf("a zero balance must not be queued; got %d", run.QueuedCount)
	}
	if run.NoticeSuppressedCount != 0 {
		t.Errorf("nothing sendable means nothing suppressed; got %d", run.NoticeSuppressedCount)
	}
	if !run.NoticeAccountingRecorded {
		t.Error("a genuine zero must read as an answer, not as a missing stamp, that distinction is " +
			"the entire reason the derived flag exists")
	}
}
