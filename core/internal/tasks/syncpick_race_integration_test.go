//go:build integration

// syncpick_race_integration_test.go: a status move that lands while the order is being told must not
// leave the order moved with no lines behind it. Every status move takes the task row FOR UPDATE, so the
// record holds that row across the order call: a cancel or a complete that arrives during ApplyPicks
// waits and lands after the record, which keeps its lines and tells the rep.
package tasks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// movingOrder moves the task to a terminal status from another connection while ApplyPicks runs, as a
// counter cancel or a complete would, and gives the move a moment to land before the order call ends.
type movingOrder struct {
	*fakeYardOrder
	d      *pickDoor
	taskID string
	status string
	// movedBefore is true when the move had landed by the time ApplyPicks ended.
	movedBefore atomic.Bool
	done        chan struct{}
}

func (m *movingOrder) ApplyPicks(ctx context.Context, in YardPickChange) (YardPickResult, error) {
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		_, _ = m.d.pool.Exec(context.Background(), `UPDATE tasks SET status = $2 WHERE id = $1::uuid`, m.taskID, m.status)
	}()
	select {
	case <-m.done:
		m.movedBefore.Store(true)
	case <-time.After(400 * time.Millisecond):
	}
	return m.fakeYardOrder.ApplyPicks(ctx, in)
}

func TestRecordPick_AStatusMoveDuringTheOrderCallWaitsAndLandsAfter(t *testing.T) {
	for _, status := range []string{StatusCancelled, StatusDone} {
		t.Run(status, func(t *testing.T) {
			d := newPickDoor(t)
			id := d.task(d.branchA)
			d.setStatus(id, StatusInProgress)
			mo := &movingOrder{fakeYardOrder: &fakeYardOrder{}, d: d, taskID: id, status: status}
			svc := NewService(NewRepository(d.pool)).WithYardOrderChanges(mo)
			in := PickedLineInput{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "7", UOM: "EA", ShortReason: ShortReasonNotInYard}
			_, err := svc.RecordPick(context.Background(), id, d.user, []PickedLineInput{in})
			<-mo.done
			if mo.movedBefore.Load() {
				t.Fatal("the status move landed while the order was being told: the record does not hold the task row")
			}
			if err != nil {
				t.Fatalf("a record that began on an open pick completes: %v", err)
			}
			if len(mo.picks) != 1 || len(d.lines(id)) != 1 {
				t.Fatalf("the order was told once and the lines are kept: told %d, lines %d", len(mo.picks), len(d.lines(id)))
			}
			var got string
			if err := d.pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1::uuid`, id).Scan(&got); err != nil || got != status {
				t.Fatalf("the move lands after the record: status %q %v", got, err)
			}
		})
	}
}
