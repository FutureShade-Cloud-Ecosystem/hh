package main

// needs_changes_test.go, the adapter behind the Needs You row for the change orders waiting on the manager
// (0411). It counts through the order module's own cross order queue, a row when the caller's next_actions on
// it offer sign_apply or apply, so the count follows the same changeActions rule the queue and the doors use.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
)

// fakeChangeQueue answers the queue per status filter, in pages of pageSize, and records every ask.
type fakeChangeQueue struct {
	byStatus map[string][]order.ChangeQueueItem
	pageSize int
	asked    []string
	err      error
}

func (f *fakeChangeQueue) ListChangeQueue(_ context.Context, in order.ChangeQueueRequest) (order.ChangeQueuePage, error) {
	f.asked = append(f.asked, in.Status)
	if f.err != nil {
		return order.ChangeQueuePage{}, f.err
	}
	rows := f.byStatus[in.Status]
	start := 0
	if in.Cursor != "" {
		for i, r := range rows {
			if r.ID == in.Cursor {
				start = i + 1
			}
		}
	}
	end := len(rows)
	page := order.ChangeQueuePage{}
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
		page.NextKey = rows[end-1].ID
	}
	page.Items = rows[start:end]
	return page, nil
}

// changeEpoch is a fixed instant the rows are aged from (an epoch number, so no calendar text rides the file).
var changeEpoch = time.Unix(1_700_000_000, 0).UTC()

// at is the queue's requested_at wording for an instant hours after the epoch.
func at(hours int) string {
	return changeEpoch.Add(time.Duration(hours) * time.Hour).Format("2006-01-02T15:04:05Z")
}

func queueRow(id, status, at string, actions ...string) order.ChangeQueueItem {
	return order.ChangeQueueItem{ID: id, Status: status, RequestedAt: at, NextActions: actions}
}

func TestChangesWaiting_CountsWhatTheCallerMaySignOrApply(t *testing.T) {
	q := &fakeChangeQueue{byStatus: map[string][]order.ChangeQueueItem{
		"PENDING_FINANCE": {
			queueRow("a", "PENDING_FINANCE", at(50), "sign_apply", "finance_approve", "reject", "cancel"),
			// An unnamed approver, or the requester: the row is listed but the caller may not sign it.
			queueRow("b", "PENDING_FINANCE", at(20), "reject", "cancel"),
		},
		"APPROVED": {
			queueRow("c", "APPROVED", at(70), "sign_apply", "apply", "reject", "cancel"),
			// A frontline caller sees an approved change but may only withdraw their own.
			queueRow("d", "APPROVED", at(5), "cancel"),
		},
		"PENDING_YARD": {
			queueRow("e", "PENDING_YARD", at(1), "yard_ack", "reject"),
		},
	}}
	got, err := ordersChangesWaiting(q)(context.Background(), dashboard.Scope{})
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if got.Count != 2 {
		t.Fatalf("count = %d, want 2: the PENDING_FINANCE change the caller may sign and the APPROVED one they may apply", got.Count)
	}
	// The age is the oldest COUNTED row's, not the oldest listed row's.
	want := changeEpoch.Add(50 * time.Hour)
	if !got.Oldest.Equal(want) {
		t.Fatalf("oldest = %v, want %v (the oldest counted change, not a row the caller cannot act on)", got.Oldest, want)
	}
	for _, s := range q.asked {
		if s == "PENDING_YARD" || s == "" {
			t.Fatalf("the adapter asked the queue for status %q: only PENDING_FINANCE and APPROVED wait on a manager", s)
		}
	}
}

func TestChangesWaiting_ApplyAloneCountsAndSignApplyAloneCounts(t *testing.T) {
	q := &fakeChangeQueue{byStatus: map[string][]order.ChangeQueueItem{
		"APPROVED": {
			queueRow("x", "APPROVED", at(70), "apply"),
			queueRow("y", "APPROVED", at(71), "sign_apply"),
		},
	}}
	got, err := ordersChangesWaiting(q)(context.Background(), dashboard.Scope{})
	if err != nil || got.Count != 2 {
		t.Fatalf("count = %d, %v, want 2: either word on the row counts it", got.Count, err)
	}
}

func TestChangesWaiting_ReadsEveryPage(t *testing.T) {
	var rows []order.ChangeQueueItem
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5"} {
		rows = append(rows, queueRow(id, "PENDING_FINANCE", at(50), "sign_apply"))
	}
	q := &fakeChangeQueue{pageSize: 2, byStatus: map[string][]order.ChangeQueueItem{"PENDING_FINANCE": rows}}
	got, err := ordersChangesWaiting(q)(context.Background(), dashboard.Scope{})
	if err != nil || got.Count != 5 {
		t.Fatalf("count = %d, %v, want 5 across three pages", got.Count, err)
	}
}

func TestChangesWaiting_NothingWaitingIsAZeroMeasureNotAnError(t *testing.T) {
	got, err := ordersChangesWaiting(&fakeChangeQueue{})(context.Background(), dashboard.Scope{})
	if err != nil || got.Count != 0 || !got.Oldest.IsZero() {
		t.Fatalf("measure = %+v, %v, want an empty measure", got, err)
	}
}

func TestChangesWaiting_AFailedReadIsAnErrorSoTheRowIsAbsent(t *testing.T) {
	q := &fakeChangeQueue{err: errors.New("queue down")}
	if _, err := ordersChangesWaiting(q)(context.Background(), dashboard.Scope{}); err == nil {
		t.Fatal("a failed queue read must surface as an error: the service turns it into an absent row, never a zero")
	}
}
