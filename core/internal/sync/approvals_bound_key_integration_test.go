//go:build integration

// approvals_bound_key_integration_test.go: UX-108 round 2. The engine's bound
// key gate asks the store one more question when the caller's own Load cannot
// see the row a key applied: does the entity still stand, for every caller of
// the scope at once (sync.BoundEntityChecker.BoundEntityExists)? A LIVE entity
// the caller cannot see is answered the seam's own refusal, "sync: row not
// found", so the caller learns nothing of the row. The approvals store did not
// answer that question, so the gate kept the bound answer ("applied") for a
// key it could not attribute: the caller learned its key was spent, and on the
// update path (where the caller holds the card's id in its own payload) that
// the card exists.
//
// The leg: the real engine over the real key ledger. A fenced caller's answer
// on another caller's bound key is the answer on a never-bound key with the
// same body, field for field: the status word, the seq, the error words and
// the envelope. The binder keeps its bound answer, and a card that is gone
// keeps its bound answer too (PR 640's rule).
package sync_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108r2Refusal is the seam's own indistinguishable refusal, the answer the
// sibling scopes give (pick_tasks, attachments, messages).
const ux108r2Refusal = "sync: row not found"

// ux108r2Engine is the approvals scope over the real engine and the real key
// ledger (sync.NewPgKeyLedger), the seam the bound key gate reads.
func ux108r2Engine(t *testing.T, f *theGapFixture, store sync.Store) (*sync.Engine, sync.ScopeInfo) {
	t.Helper()
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("approvals", store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("approvals")
	if !ok {
		t.Fatal("approvals missing from the registry")
	}
	return engine, info
}

// ux108r2Push pushes one operation and answers the result, failing the leg
// only when the push itself failed.
func ux108r2Push(t *testing.T, engine *sync.Engine, info sync.ScopeInfo, f sync.ScopeFilter, op apigen.SyncPushOperation) apigen.SyncPushResult {
	t.Helper()
	res, err := engine.Push(context.Background(), info, f, []apigen.SyncPushOperation{op})
	if err != nil {
		t.Fatalf("push %s: %v", op.Op, err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("push %s: %d results for one operation", op.Op, len(res.Results))
	}
	return res.Results[0]
}

// ux108r2SameAnswer compares two push answers the way a device sees them: the
// status word, the seq, the error words and the envelope. The client id is the
// caller's own key and is not compared: the two answers are about two keys.
func ux108r2SameAnswer(t *testing.T, what string, got, want apigen.SyncPushResult) {
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

// TestUX108R2ApprovalsBoundKeyAnswersLikeANeverBoundKey: the real engine over
// the real key ledger. The dealer wide manager files a credit exception at
// branch B and notes it under a key, so the key is bound to him by that
// write. The branch A clerk reaches branch A alone and filed nothing, so the
// pull shows him no card.
//
// The failing caller's answer on the manager's bound key must be the answer on
// a key nobody ever applied, field for field: the same status word, no seq,
// the same error words and no envelope. Before the fix the approvals store
// did not answer the gate's existence read, so the bound key answered
// "applied" (the caller learned its key was spent) and another body under that
// key answered "key_reused" (the caller learned the key named a different
// operation), where a never-bound key answered the seam's refusal both times.
func TestUX108R2ApprovalsBoundKeyAnswersLikeANeverBoundKey(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	engine, info := ux108r2Engine(t, f, store)

	bound := uuid.NewString()
	fresh := uuid.NewString()
	freshOther := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{bound, fresh, freshOther})
	})

	// The manager files the card and notes it: the note binds the key to him,
	// with the card as the key's entity.
	rs, err := store.Create(ctx, gm, uuid.NewString(), map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108r2: a card the clerk may not read"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": rs.ID, "detail": "ux108r2: the manager's own note"}}
	first := ux108r2Push(t, engine, info, gm, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager notes his own card: %+v", first)
	}

	// The same body: the bound key and a key nobody ever applied must answer
	// alike.
	boundAnswer := ux108r2Push(t, engine, info, clerk, op)
	neverOp := op
	neverOp.ClientId = fresh
	neverAnswer := ux108r2Push(t, engine, info, clerk, neverOp)
	ux108r2SameAnswer(t, "the bound key and a never-bound key, the same body", boundAnswer, neverAnswer)
	if boundAnswer.Status != "rejected" || boundAnswer.Error == nil || *boundAnswer.Error != ux108r2Refusal {
		t.Errorf("a fenced caller's answer on another caller's key is the indistinguishable refusal: %+v", boundAnswer)
	}
	if boundAnswer.Seq != nil {
		t.Errorf("a fenced caller's answer on another caller's key carries no seq: %+v", boundAnswer)
	}
	if boundAnswer.Conflict != nil {
		t.Errorf("a fenced caller's answer on another caller's key carries no envelope: %+v", boundAnswer.Conflict)
	}

	// Another body under the same key: the gate answers before the hash arm,
	// so the answer is the refusal too, never the hash confirmation.
	other := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": rs.ID, "detail": "ux108r2: another body"}}
	otherBound := ux108r2Push(t, engine, info, clerk, other)
	otherNever := other
	otherNever.ClientId = freshOther
	otherNeverAnswer := ux108r2Push(t, engine, info, clerk, otherNever)
	ux108r2SameAnswer(t, "the bound key and a never-bound key, another body", otherBound, otherNeverAnswer)
	if otherBound.Status != "rejected" || otherBound.Error == nil || *otherBound.Error != ux108r2Refusal {
		t.Errorf("another body under another caller's key is the indistinguishable refusal: %+v", otherBound)
	}

	// The card's own facts never travelled: the note is the manager's.
	var detail string
	if err := f.pool.QueryRow(ctx,
		`SELECT detail FROM permission_approval_requests WHERE id = $1::uuid`, rs.ID).Scan(&detail); err != nil {
		t.Fatalf("read the card back: %v", err)
	}
	if detail != "ux108r2: the manager's own note" {
		t.Errorf("the fenced caller's push moved the card: the detail is %q", detail)
	}
}

// TestUX108R2ApprovalsBinderKeepsItsBoundAnswer: the binder replays its own
// key and keeps the bound answer, applied with the card's seq, whatever the
// gate would have said to anybody else.
func TestUX108R2ApprovalsBinderKeepsItsBoundAnswer(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	engine, info := ux108r2Engine(t, f, store)

	bound := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, []string{bound})
	})
	rs, err := store.Create(ctx, gm, uuid.NewString(), map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108r2: a card the binder owns"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": rs.ID, "detail": "ux108r2: the manager's own note"}}
	first := ux108r2Push(t, engine, info, gm, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager notes his own card: %+v", first)
	}

	replay := ux108r2Push(t, engine, info, gm, op)
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq != *first.Seq {
		t.Errorf("the binder's own replay keeps the bound answer, applied with the card's seq: %+v", replay)
	}
	var detail string
	if err := f.pool.QueryRow(ctx,
		`SELECT detail FROM permission_approval_requests WHERE id = $1::uuid`, rs.ID).Scan(&detail); err != nil {
		t.Fatalf("read the card back: %v", err)
	}
	if detail != "ux108r2: the manager's own note" {
		t.Errorf("the replay ran nothing twice: the card's detail is %q", detail)
	}
}

// TestUX108R2ApprovalsGoneRowKeepsItsBoundAnswer: a card that is gone keeps
// the bound answer (PR 640's rule). The gate hides a key only for a LIVE row
// a caller cannot see; a withdrawn card is gone for everyone in the org, so
// the key keeps its own answer.
func TestUX108R2ApprovalsGoneRowKeepsItsBoundAnswer(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}
	engine, info := ux108r2Engine(t, f, store)

	bound := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, []string{bound})
	})
	rs, err := store.Create(ctx, gm, uuid.NewString(), map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108r2: a card withdrawn after the filing"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": rs.ID, "detail": "ux108r2: the manager's own note"}}
	first := ux108r2Push(t, engine, info, gm, op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager notes his own card: %+v", first)
	}
	if _, err := store.Delete(ctx, gm, rs.ID, uuid.NewString()); err != nil {
		t.Fatalf("the manager withdraws his own card: %v", err)
	}

	replay := ux108r2Push(t, engine, info, clerk, op)
	if replay.Status != "applied" {
		t.Errorf("a gone row keeps the bound answer: %+v", replay)
	}
	if replay.Error != nil && *replay.Error == ux108r2Refusal {
		t.Errorf("a gone row is not refused as unreadable: %+v", replay)
	}
}
