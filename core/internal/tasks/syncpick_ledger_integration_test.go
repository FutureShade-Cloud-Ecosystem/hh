//go:build integration

// syncpick_ledger_integration_test.go: the pick record door inside the engine's whole flow, the key
// ledger (sync_op_hashes) in front of the door's own idempotency stamp (the task row's key and the idem
// task_events note). The two agree: a replay of the same record answers applied and tells the order
// nothing twice, whether the task still holds the key or has since taken another; a key reused with
// another body is refused and records nothing; a refused record binds no key, so the corrected body may
// retry under it.
package tasks

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

func TestPickDoor_LedgerSameKeyDifferentBodyIsRefusedAndRecordsNothingNew(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	key := uuid.NewString()
	if res := d.push(recordOp(key, id, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("first: %+v %v", res[0], res[0].Error)
	}
	other := pickLine(1)
	other["qty_picked"] = "9"
	res := d.push(recordOp(key, id, other))
	if res[0].Status != "rejected" || res[0].Error == nil || !strings.Contains(*res[0].Error, "key_reused") {
		t.Fatalf("the same key with another body is key_reused: %+v", res[0])
	}
	if rows := d.lines(id); len(rows) != 1 || rows[0].picked != "7.0000" || len(d.yard.picks) != 1 {
		t.Fatalf("the first record stands and the order was told once: %+v, told %d", rows, len(d.yard.picks))
	}
}

func TestPickDoor_LedgerReplayAfterTheTaskTookAnotherKeyAnswersApplied(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	op := recordOp(uuid.NewString(), id, pickLine(1))
	if res := d.push(op); res[0].Status != "applied" {
		t.Fatalf("first: %+v %v", res[0], res[0].Error)
	}
	// A later status op stamps the task row with its own key; the record's key now lives in the event
	// history and the ledger.
	if res := d.push(apigen.SyncPushOperation{ClientId: uuid.NewString(), Op: "update", Payload: map[string]any{"id": id, "status": "ACCEPTED"}}); res[0].Status != "applied" {
		t.Fatalf("a later status op: %+v %v", res[0], res[0].Error)
	}
	res := d.push(op)
	if res[0].Status != "applied" {
		t.Fatalf("a replay answers applied: %+v %v", res[0], res[0].Error)
	}
	if len(d.yard.picks) != 1 || len(d.lines(id)) != 1 {
		t.Fatalf("nothing runs twice: order told %d times, %d lines", len(d.yard.picks), len(d.lines(id)))
	}
}

func TestPickDoor_LedgerRefusedRecordBindsNoKey(t *testing.T) {
	d := newPickDoor(t)
	id := d.task(d.branchA)
	key := uuid.NewString()
	bad := pickLine(1)
	bad["short_note"] = "dock\x00two"
	if res := d.push(recordOp(key, id, bad)); res[0].Status != "rejected" {
		t.Fatalf("the NUL note is refused: %+v", res[0])
	}
	if res := d.push(recordOp(key, id, pickLine(1))); res[0].Status != "applied" {
		t.Fatalf("the corrected record retries under the refused key: %+v %v", res[0], res[0].Error)
	}
}
