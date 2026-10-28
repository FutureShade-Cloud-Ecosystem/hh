//go:build integration

// syncbound_gone_replay_integration_test.go, UX-103 round 3: the OWNER's replay of an applied key when
// the entity is gone for everyone. The ledger is the one record of what a key first applied, so the
// replay answers applied with no second application whether the row still stands, is tombstoned, or is
// gone entirely: the engine's bound key gate must fail toward idempotency, never toward a second
// create. A Load miss alone must never spend the bound answer; only a live row the caller's fence
// hides may drop it. Red at 52802de2, where any Load miss dropped the bound answer and the replay
// re-entered Create. Run with DATABASE_URL set to a migrated and seeded throwaway database.
package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// pushScope pushes through the door's engine under another registered scope name, the ledger shared
// with the pick legs exactly as the composition root shares one engine and one ledger across scopes.
func pushScope(d *pickDoor, scope string, ops ...apigen.SyncPushOperation) []apigen.SyncPushResult {
	d.t.Helper()
	info, ok := hhsync.Lookup(scope)
	if !ok {
		d.t.Fatalf("scope %s missing from the registry", scope)
	}
	resp, err := d.engine.Push(context.Background(), info, d.f, ops)
	if err != nil {
		d.t.Fatalf("push %s: %v", scope, err)
	}
	return resp.Results
}

// errString is a result's error words, empty when it carries none.
func errString(r apigen.SyncPushResult) string {
	if r.Error == nil {
		return ""
	}
	return *r.Error
}

// boundCreateOp is one bound create over the door's order double: the payload id is the row the
// ledger will bind the key to.
func boundCreateOp(key, rowID string, fields map[string]any) apigen.SyncPushOperation {
	payload := map[string]any{"id": rowID}
	for k, v := range fields {
		payload[k] = v
	}
	return apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: payload}
}

// TestPickReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer: an owner's replay of an applied key when
// the task row is hard gone answers applied with no seq and applies nothing twice; the same key with
// another body is still key_reused.
func TestPickReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	rowID, key := uuid.NewString(), uuid.NewString()
	op := boundCreateOp(key, rowID, map[string]any{"order_id": uuid.NewString()})
	if first := d.push(op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the bound create applies: %+v (%s)", first[0], errString(first[0]))
	}
	// The row is gone for everyone, hard: no tombstone answers Load, and the task_events history
	// the dedup consults goes with it.
	if _, err := d.pool.Exec(ctx, `DELETE FROM task_events WHERE task_id = $1::uuid`, rowID); err != nil {
		t.Fatalf("drop the events: %v", err)
	}
	if _, err := d.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1::uuid`, rowID); err != nil {
		t.Fatalf("drop the task: %v", err)
	}
	again := d.push(op)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay of an applied key when the row is gone answers applied with no seq: got %+v (%s)", again[0], errString(again[0]))
	}
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = $1::uuid`, rowID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if n != 0 {
		t.Fatalf("the replay applies nothing twice: %d task rows, want 0", n)
	}
	other := boundCreateOp(key, rowID, map[string]any{"order_id": uuid.NewString()})
	if reused := d.push(other); reused[0].Status != "rejected" || !strings.Contains(errString(reused[0]), "key_reused") {
		t.Fatalf("the same key with another body is still key_reused: %+v (%s)", reused[0], errString(reused[0]))
	}
}

// TestPickReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq: the module's own delete tombstones
// the row and the row stays, so the owner's replay answers applied with the tombstone's seq, the
// pre-gate bound answer the round must keep.
func TestPickReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	rowID, key := uuid.NewString(), uuid.NewString()
	op := boundCreateOp(key, rowID, map[string]any{"order_id": uuid.NewString()})
	if first := d.push(op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the bound create applies: %+v (%s)", first[0], errString(first[0]))
	}
	if _, err := d.pool.Exec(ctx, `UPDATE tasks SET deleted_at = now() WHERE id = $1::uuid`, rowID); err != nil {
		t.Fatalf("tombstone the task: %v", err)
	}
	seq := d.taskSeq(rowID)
	again := d.push(op)
	if again[0].Status != "applied" || again[0].Seq == nil || *again[0].Seq != seq {
		t.Fatalf("the owner's replay over a tombstoned row answers applied with the tombstone's seq %d: got %+v (%s)", seq, again[0], errString(again[0]))
	}
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = $1::uuid`, rowID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the tombstone stands alone: %d rows (%v)", n, err)
	}
}

// newWeightEngineDoor builds the weights scope over the pick door's world: the same pool, org,
// authority and engine ledger, the order double answering for one order of the org on branch A,
// exactly the shape the weights fence legs run.
func newWeightEngineDoor(t *testing.T, d *pickDoor, order string) {
	t.Helper()
	orders := weightDoorOrders{owner: map[string]string{order: d.org}, branch: d.branchA}
	w := NewWeightSyncStore(d.pool, orders, nil).WithStaffAuthority(
		func(context.Context, string, string) (identity.StaffContext, bool, error) { return d.authority() })
	if err := d.engine.RegisterScope("weights", w); err != nil {
		t.Fatalf("register weights: %v", err)
	}
}

// TestWeightReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer: the same owner guarantee on the
// weights scope's NEED_WEIGHT rows.
func TestWeightReplayGone_ARowGoneForEveryoneKeepsTheBoundAnswer(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	order := uuid.NewString()
	newWeightEngineDoor(t, d, order)
	rowID, key := uuid.NewString(), uuid.NewString()
	op := boundCreateOp(key, rowID, map[string]any{"order_id": order, "order_line_no": float64(1)})
	if first := pushScope(d, "weights", op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the bound weight create applies: %+v (%s)", first[0], errString(first[0]))
	}
	if _, err := d.pool.Exec(ctx, `DELETE FROM task_events WHERE task_id = $1::uuid`, rowID); err != nil {
		t.Fatalf("drop the events: %v", err)
	}
	if _, err := d.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1::uuid`, rowID); err != nil {
		t.Fatalf("drop the task: %v", err)
	}
	again := pushScope(d, "weights", op)
	if again[0].Status != "applied" || again[0].Seq != nil {
		t.Fatalf("the owner's replay of an applied key when the weight row is gone answers applied with no seq: got %+v (%s)", again[0], errString(again[0]))
	}
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = $1::uuid`, rowID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if n != 0 {
		t.Fatalf("the replay applies nothing twice: %d weight rows, want 0", n)
	}
	other := boundCreateOp(key, rowID, map[string]any{"order_id": order, "order_line_no": float64(2)})
	if reused := pushScope(d, "weights", other); reused[0].Status != "rejected" || !strings.Contains(errString(reused[0]), "key_reused") {
		t.Fatalf("the same key with another body is still key_reused: %+v (%s)", reused[0], errString(reused[0]))
	}
}

// TestWeightReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq: the weights tombstone guard.
func TestWeightReplayGone_ATombstonedRowKeepsTheBoundAnswerWithItsSeq(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	order := uuid.NewString()
	newWeightEngineDoor(t, d, order)
	rowID, key := uuid.NewString(), uuid.NewString()
	op := boundCreateOp(key, rowID, map[string]any{"order_id": order, "order_line_no": float64(1)})
	if first := pushScope(d, "weights", op); first[0].Status != "applied" || first[0].Seq == nil {
		t.Fatalf("setup: the bound weight create applies: %+v (%s)", first[0], errString(first[0]))
	}
	if _, err := d.pool.Exec(ctx, `UPDATE tasks SET deleted_at = now() WHERE id = $1::uuid`, rowID); err != nil {
		t.Fatalf("tombstone the task: %v", err)
	}
	seq := d.taskSeq(rowID)
	again := pushScope(d, "weights", op)
	if again[0].Status != "applied" || again[0].Seq == nil || *again[0].Seq != seq {
		t.Fatalf("the owner's replay over a tombstoned weight row answers applied with the tombstone's seq %d: got %+v (%s)", seq, again[0], errString(again[0]))
	}
}
