//go:build integration

// syncscope_create_body_gate_integration_test.go: UX-116 round 3, the K3
// read's P3-2, on the pick_tasks scope. The pick store validates a pushed
// create's fields but did not implement the engine's create body validation
// seam, so an invalid create body met the bound key gate's hidden refusal
// under a spent key and the store's own field refusal under a free one: the
// two full answers differed and the body was an oracle that told the spent
// key from the free one. This leg pins the pair on the real engine and the
// real key ledger: a branch-fenced non binder's full answers to the same
// invalid body under the spent key and a never-bound key must be equal,
// field for field, and the refusal must be the store's own validation
// refusal. A valid body is unchanged (the sibling legs pin it).
//
// Run with DATABASE_URL set to a migrated and seeded throwaway database;
// the leg skips politely without one.
package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// TestUX116PickCreateInvalidBodyAnswersAlikeOnASpentAndAFreeKey: a branch A
// caller binds a key with a record on its own task; a branch B device, which
// the task's fence hides it from, pushes the same invalid create body under
// the spent key and under a key nobody ever applied. Before the fix the
// spent key answered "sync: row not found" (the gate's hidden refusal) and
// the free key the field refusal; after it both answer the field refusal.
func TestUX116PickCreateInvalidBodyAnswersAlikeOnASpentAndAFreeKey(t *testing.T) {
	d := newPickDoor(t)
	ctx := context.Background()
	id := d.task(d.branchA)

	aCaller := scopedTo(d.user, d.branchA)
	bCaller := scopedTo(uuid.NewString(), d.branchB)
	// Each caller rides its own scope filter actor, as the middleware stamps
	// one subject per device: the ledger records who bound the key, so the
	// two callers must not share one actor or the binder rule would answer
	// the branch B replay as the binder's own.
	aF := hhsync.ScopeFilter{OrgID: d.org, Actor: "pick-door-actor-a"}
	bF := hhsync.ScopeFilter{OrgID: d.org, Actor: "pick-door-actor-b"}

	// The branch A caller binds the key with a record on its own task.
	boundKey := uuid.NewString()
	bind := pushAsFilter(d, aF, aCaller, "pick_tasks", recordOp(boundKey, id, pickLine(1)))[0]
	if bind.Status != "applied" || bind.Seq == nil {
		t.Fatalf("setup: the branch A record applies: %+v (%s)", bind, errString(bind))
	}
	freeKey := uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.pool.Exec(context.Background(),
			`DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, []string{boundKey, freeKey})
	})

	// The same invalid create body from the branch B device: a field the
	// create does not admit, beside the row-shaping legs.
	payload := map[string]any{"id": uuid.NewString(), "order_id": uuid.NewString(), "not_writable": "x"}
	spent := pushAsFilter(d, bF, bCaller, "pick_tasks",
		apigen.SyncPushOperation{ClientId: boundKey, Op: "create", Payload: payload})[0]
	free := pushAsFilter(d, bF, bCaller, "pick_tasks",
		apigen.SyncPushOperation{ClientId: freeKey, Op: "create", Payload: payload})[0]

	// The full answers are equal, field for field: the status word, the seq
	// leg, the error words and the envelope.
	same := func(what string, got, want apigen.SyncPushResult) {
		t.Helper()
		if got.Status != want.Status {
			t.Errorf("%s: the status words differ: got %q want %q", what, got.Status, want.Status)
		}
		switch {
		case got.Seq == nil && want.Seq != nil:
			t.Errorf("%s: the seq leg differs: got none want %d", what, *want.Seq)
		case got.Seq != nil && want.Seq == nil:
			t.Errorf("%s: the seq leg differs: got %d want none", what, *got.Seq)
		case got.Seq != nil && *got.Seq != *want.Seq:
			t.Errorf("%s: the seq legs differ: got %d want %d", what, *got.Seq, *want.Seq)
		}
		gotErr, wantErr := "", ""
		if got.Error != nil {
			gotErr = *got.Error
		}
		if want.Error != nil {
			wantErr = *want.Error
		}
		if gotErr != wantErr {
			t.Errorf("%s: the error words differ: got %q want %q", what, gotErr, wantErr)
		}
		if (got.Conflict == nil) != (want.Conflict == nil) {
			t.Errorf("%s: the envelopes differ: got %+v want %+v", what, got.Conflict, want.Conflict)
		}
	}
	same("the spent key and a free key, an invalid create body", spent, free)

	// And the refusal is the store's own validation refusal, on both keys.
	const fieldRefusal = `tasks: field "not_writable" is not writable through sync: sync: operation refused`
	for name, r := range map[string]apigen.SyncPushResult{"the spent key": spent, "a free key": free} {
		if r.Status != "rejected" || r.Error == nil || *r.Error != fieldRefusal {
			t.Errorf("%s: the invalid body's answer is the field refusal: %+v", name, r)
		}
		if r.Seq != nil {
			t.Errorf("%s: the refused create carries no seq: %+v", name, r)
		}
		if r.Conflict != nil {
			t.Errorf("%s: the refused create carries no envelope: %+v", name, r)
		}
	}

	// The replay wrote no task, touched the bound task, and bound nothing
	// new.
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id = $1::uuid`, payload["id"]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the invalid create wrote no task: %d rows (%v)", n, err)
	}
	if rows := d.lines(id); len(rows) != 1 || rows[0].picked != "7.0000" {
		t.Errorf("the replays changed nothing on the bound task: %+v", rows)
	}
	var ledgerRows int
	if err := d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, []string{boundKey, freeKey}).Scan(&ledgerRows); err != nil {
		t.Fatalf("count the probe's ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Errorf("the replays bind nothing new: %d ledger rows, want 1", ledgerRows)
	}
}
