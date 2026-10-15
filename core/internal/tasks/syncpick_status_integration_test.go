//go:build integration

// syncpick_status_integration_test.go: a CANCELLED or DONE pick takes no record through any door. The
// record is refused before the order is told anything (the order's quantities, totals and backorder
// and the rep's notice are money), nothing is written, and the task's seq does not move. Through sync
// it is that op's rejection in the module's words; the direct door answers the module's conflict class.
package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (d *pickDoor) setStatus(id, status string) {
	d.t.Helper()
	if _, err := d.pool.Exec(context.Background(), `UPDATE tasks SET status = $2 WHERE id = $1::uuid`, id, status); err != nil {
		d.t.Fatalf("set status %s: %v", status, err)
	}
}

func TestPickDoor_TerminalPickTakesNoRecord(t *testing.T) {
	for _, status := range []string{StatusCancelled, StatusDone} {
		t.Run("sync door "+status, func(t *testing.T) {
			d := newPickDoor(t)
			id := d.task(d.branchA)
			d.setStatus(id, status)
			before := d.taskSeq(id)
			res := d.push(recordOp(uuid.NewString(), id, pickLine(1)))
			if res[0].Status != "rejected" || res[0].Error == nil || !strings.Contains(*res[0].Error, "its record cannot change") {
				t.Fatalf("want rejected in the module's words, got %+v (%v)", res[0], res[0].Error)
			}
			if len(d.yard.picks) != 0 || len(d.lines(id)) != 0 || d.taskSeq(id) != before {
				t.Fatalf("a refused record moves nothing: order told %d times, %d lines, seq %d to %d", len(d.yard.picks), len(d.lines(id)), before, d.taskSeq(id))
			}
		})
		t.Run("direct door "+status, func(t *testing.T) {
			d := newPickDoor(t)
			id := d.task(d.branchA)
			d.setStatus(id, status)
			before := d.taskSeq(id)
			_, err := d.svc.RecordPick(context.Background(), id, d.user, []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}})
			if err == nil || !strings.Contains(err.Error(), "its record cannot change") {
				t.Fatalf("want the closed pick refusal, got %v", err)
			}
			if len(d.yard.picks) != 0 || len(d.lines(id)) != 0 || d.taskSeq(id) != before {
				t.Fatalf("a refused record moves nothing: order told %d times, %d lines, seq %d to %d", len(d.yard.picks), len(d.lines(id)), before, d.taskSeq(id))
			}
		})
		t.Run("repository "+status, func(t *testing.T) {
			// The last line of defence: a record that reaches the store (the status changed after the
			// service looked) is refused under the task's row lock, with nothing written.
			d := newPickDoor(t)
			id := d.task(d.branchA)
			d.setStatus(id, status)
			before := d.taskSeq(id)
			_, err := NewRepository(d.pool).UpsertPickedLines(context.Background(), id, d.user, []PickedLineInput{{OrderLineNo: 1, QtyOrdered: "10", QtyPicked: "10", UOM: "EA"}})
			if err == nil || !strings.Contains(err.Error(), "its record cannot change") {
				t.Fatalf("want the closed pick refusal, got %v", err)
			}
			if len(d.lines(id)) != 0 || d.taskSeq(id) != before {
				t.Fatalf("a refused record writes nothing: %d lines, seq %d to %d", len(d.lines(id)), before, d.taskSeq(id))
			}
		})
	}
}

// A pick that is still open keeps taking records, so a correction before the pick closes is unchanged.
func TestPickDoor_OpenPicksStillTakeRecords(t *testing.T) {
	for _, status := range []string{StatusQueued, StatusAccepted, StatusInProgress, StatusStaged} {
		d := newPickDoor(t)
		id := d.task(d.branchA)
		d.setStatus(id, status)
		if res := d.push(recordOp(uuid.NewString(), id, pickLine(1))); res[0].Status != "applied" {
			t.Fatalf("%s: %+v (%v)", status, res[0], res[0].Error)
		}
	}
}
