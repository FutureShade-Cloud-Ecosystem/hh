//go:build integration

// syncscope_bound_key_integration_test.go: UX-108 round 2. The engine's bound
// key gate asks the store one more question when the caller's own Load cannot
// see the row a key applied: does the entity still stand, for every caller of
// the scope at once (sync.BoundEntityChecker.BoundEntityExists)? A LIVE entity
// the caller cannot see is answered the seam's own refusal, "sync: row not
// found", so the caller learns nothing of the row. The quotes store did not
// answer that question, so the gate kept the bound answer ("applied") for a
// key it could not attribute: the caller learned its key was spent, and on the
// update path (where the caller holds the row's id in its own payload) that
// the row exists.
//
// The leg: the real engine over the real key ledger. A fenced caller's answer
// on another caller's bound key is the answer on a never-bound key with the
// same body, field for field: the status word, the seq, the error words and
// the envelope. The binder keeps its bound answer, and a row that is gone
// keeps its bound answer too (PR 640's rule).
package quote

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108R2Plant plants one quote of the north org, owned by the owner, in the
// branch, in the given status, carrying the key when one is named, with a real
// seq so the pull sees it.
func ux108R2Plant(t *testing.T, f *syncFixture, owner, branchID, status, key string) (id string, seq int64) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `
		WITH qseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('quotes')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents,
		                    total_cents, salesperson_id, created_by, seq, idempotency_key)
		VALUES ('#Q-UX108R2-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, $4, 2000, 0, 2000,
		        $5::uuid, $5::uuid, (SELECT next_value FROM qseq), NULLIF($6,'')::uuid)
		RETURNING id::text, seq`,
		testkit.TenantAlphaID, branchID, testkit.OrgNorthID, status, owner, key).Scan(&id, &seq); err != nil {
		t.Fatalf("plant quote: %v", err)
	}
	return id, seq
}

// ux108R2Engine is the quotes scope over the real engine and the real key
// ledger (sync.NewPgKeyLedger), the seam the bound key gate reads.
func ux108R2Engine(t *testing.T, f *syncFixture) (*sync.Engine, sync.ScopeInfo) {
	t.Helper()
	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("quotes", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("quotes")
	if !ok {
		t.Fatal("quotes missing from the registry")
	}
	return engine, info
}

// ux108R2Push pushes one operation and answers the result, failing the leg
// only when the push itself failed.
func ux108R2Push(t *testing.T, engine *sync.Engine, info sync.ScopeInfo, f sync.ScopeFilter, op apigen.SyncPushOperation) apigen.SyncPushResult {
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

// ux108R2SameAnswer compares two push answers the way a device sees them: the
// status word, the seq, the error words and the envelope. The client id is the
// caller's own key and is not compared: the two answers are about two keys.
func ux108R2SameAnswer(t *testing.T, what string, got, want apigen.SyncPushResult) {
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

// ux108R2Refusal is the seam's own indistinguishable refusal, the answer the
// sibling scopes give (pick_tasks, attachments, messages).
const ux108R2Refusal = "sync: row not found"

// TestUX108R2QuotesBoundKeyAnswersLikeANeverBoundKey: the real engine over
// the real key ledger. The Sales manager owns a quote and applies an edit
// under a key, so the key is bound to him by that write. Inside Sales, at the
// Work rung, reads his own quotes only, so he may not read the manager's.
//
// The failing caller's answer on the manager's bound key must be the answer on
// a key nobody ever applied, field for field: the same status word, no seq,
// the same error words and no envelope. Before the fix the quotes store did
// not answer the gate's existence read, so the bound key answered "applied"
// (the caller learned its key was spent) and another body under that key
// answered "key_reused" (the caller learned the key named a different
// operation), where a never-bound key answered the seam's refusal both times.
func TestUX108R2QuotesBoundKeyAnswersLikeANeverBoundKey(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	engine, info := ux108R2Engine(t, f)

	// The manager owns the quote and applies an edit under a key: that write
	// binds the key to him, with the quote as the key's entity.
	bound := uuid.NewString()
	fresh := uuid.NewString()
	freshOther := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{bound, fresh, freshOther})
	})
	quoteID, _ := ux108R2Plant(t, f, managerID, testkit.BranchNorthAID, "SENT", "")
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": quoteID, "valid_until": ux108Date(30)}}
	first := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager applies its edit: %+v", first)
	}

	// The caller who may not read the quote.
	inside := ux108As(testkit.SubInsideSales)

	// The same body: the bound key and a key nobody ever applied must answer
	// alike.
	boundAnswer := ux108R2Push(t, engine, info, inside, op)
	neverOp := op
	neverOp.ClientId = fresh
	neverAnswer := ux108R2Push(t, engine, info, inside, neverOp)
	ux108R2SameAnswer(t, "the bound key and a never-bound key, the same body", boundAnswer, neverAnswer)
	if boundAnswer.Status != "rejected" || boundAnswer.Error == nil || *boundAnswer.Error != ux108R2Refusal {
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
		"id": quoteID, "valid_until": ux108Date(60)}}
	otherBound := ux108R2Push(t, engine, info, inside, other)
	otherNever := other
	otherNever.ClientId = freshOther
	otherNeverAnswer := ux108R2Push(t, engine, info, inside, otherNever)
	ux108R2SameAnswer(t, "the bound key and a never-bound key, another body", otherBound, otherNeverAnswer)
	if otherBound.Status != "rejected" || otherBound.Error == nil || *otherBound.Error != ux108R2Refusal {
		t.Errorf("another body under another caller's key is the indistinguishable refusal: %+v", otherBound)
	}

	// The quote's own facts never travelled: its validity is the manager's
	// date, not the other body's.
	var validity string
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).Scan(&validity); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	if validity != ux108Date(30) {
		t.Errorf("the fenced caller's push moved the quote: the validity is %q", validity)
	}
}

// TestUX108R2QuotesBinderKeepsItsBoundAnswer: the binder replays its own key
// and keeps the bound answer, applied with the row's seq, whatever the gate
// would have said to anybody else.
func TestUX108R2QuotesBinderKeepsItsBoundAnswer(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	engine, info := ux108R2Engine(t, f)

	bound := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`, []string{bound})
	})
	quoteID, _ := ux108R2Plant(t, f, managerID, testkit.BranchNorthAID, "SENT", "")
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": quoteID, "valid_until": ux108Date(30)}}
	first := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager applies its edit: %+v", first)
	}

	replay := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), op)
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq != *first.Seq {
		t.Errorf("the binder's own replay keeps the bound answer, applied with the row's seq: %+v", replay)
	}
	var validity string
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).Scan(&validity); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	if validity != ux108Date(30) {
		t.Errorf("the replay ran nothing twice: the quote's validity is %q", validity)
	}
}

// TestUX114QuotesGoneRowRefusesANonBinder: UX-114 on the quotes scope, the
// leg UX-108's merge made runnable. A withdrawn quote is gone for everyone,
// and with BoundEntityExists on the store the engine refuses a non binder
// replaying the bound key with the absent key's answer, whole: the status,
// the seq, the error words and the envelope are the never-bound key's. The
// binder keeps the bound answer, and nothing writes twice.
func TestUX114QuotesGoneRowRefusesANonBinder(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	engine, info := ux108R2Engine(t, f)

	bound := uuid.NewString()
	fresh := uuid.NewString()
	freshDelete := uuid.NewString()
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{bound, fresh, freshDelete})
	})
	// A DRAFT quote, the only kind that may be withdrawn through sync.
	quoteID, _ := ux108R2Plant(t, f, managerID, testkit.BranchNorthAID, "DRAFT", "")
	op := apigen.SyncPushOperation{ClientId: bound, Op: "update", Payload: map[string]any{
		"id": quoteID, "valid_until": ux108Date(30)}}
	first := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), op)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager applies its edit: %+v", first)
	}
	if _, err := f.store.Delete(ctx, ux108As(testkit.SubSalesManager), quoteID, uuid.NewString()); err != nil {
		t.Fatalf("the manager withdraws its own draft: %v", err)
	}

	inside := ux108As(testkit.SubInsideSales)

	// The update replay: the bound key and a never-bound key with the same
	// body answer alike, field for field, and the refusal is the seam's.
	boundAnswer := ux108R2Push(t, engine, info, inside, op)
	neverOp := op
	neverOp.ClientId = fresh
	neverAnswer := ux108R2Push(t, engine, info, inside, neverOp)
	ux108R2SameAnswer(t, "the bound key and a never-bound key on a gone row, the update", boundAnswer, neverAnswer)
	if boundAnswer.Status != "rejected" || boundAnswer.Error == nil || *boundAnswer.Error != ux108R2Refusal {
		t.Errorf("a non binder's update replay on a gone row is the indistinguishable refusal: %+v", boundAnswer)
	}

	// The delete replay: the same pair, the same equality.
	delOp := apigen.SyncPushOperation{ClientId: bound, Op: "delete", Payload: map[string]any{"id": quoteID}}
	boundDelete := ux108R2Push(t, engine, info, inside, delOp)
	neverDelete := delOp
	neverDelete.ClientId = freshDelete
	neverDeleteAnswer := ux108R2Push(t, engine, info, inside, neverDelete)
	ux108R2SameAnswer(t, "the bound key and a never-bound key on a gone row, the delete", boundDelete, neverDeleteAnswer)

	// The binder keeps the bound answer: applied, the tombstone's seq, no
	// second write, one ledger row.
	replay := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), op)
	if replay.Status != "applied" || replay.Seq == nil || *replay.Seq <= *first.Seq {
		t.Errorf("the binder's own replay keeps the bound answer on a gone row: %+v", replay)
	}
	var deletedAt *time.Time
	var validity string
	if err := f.pool.QueryRow(ctx,
		`SELECT deleted_at, COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).
		Scan(&deletedAt, &validity); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	if deletedAt == nil || validity != ux108Date(30) {
		t.Errorf("the replay wrote nothing: deleted_at %v validity %q", deletedAt, validity)
	}
	var ledgerRows int
	if err := f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
		[]string{bound, fresh, freshDelete}).Scan(&ledgerRows); err != nil {
		t.Fatalf("count the probe's ledger rows: %v", err)
	}
	if ledgerRows != 1 {
		t.Errorf("the replay binds nothing new: %d ledger rows, want 1", ledgerRows)
	}
}
