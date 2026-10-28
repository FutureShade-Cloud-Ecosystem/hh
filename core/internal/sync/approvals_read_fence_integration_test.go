//go:build integration

// approvals_read_fence_integration_test.go: UX-108. The approvals scope's
// pull holds the org fence and the reader (readerFor): the list's wall, the
// caller's branch reach and the floor card rule, plus the caller's own
// filings. Load and FindApplied held the org fence alone and answered FOUND
// with the projection dropped, so the engine read a card the caller may not
// read as visible: the gate's Load saw it, and the replayed answer carried
// the card's seq.
//
// A tombstone stays found: the pull sends the tombstone of a withdrawn
// request to every caller that passes the org fence, so a withdrawn request
// stays found for every one of them, and only for them.
//
// Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:55439/hh35syncgap?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/sync/...
package sync_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108ApprovalsStore is the approvals scope over the real database, the
// store the composition root wires.
func ux108ApprovalsStore(t *testing.T, f *theGapFixture) *staffcontext.ApprovalsSyncStore {
	t.Helper()
	return staffcontext.NewApprovalsSyncStore(f.pool, linkedUser(f.pool))
}

// TestUX108ApprovalsReadsAnswerNotFoundForACardTheCallerCannotRead: a card
// the caller may not read is not found, field for field the answer an absent
// card gives. Before the fix both reads answered FOUND with the projection
// dropped, and the engine read an unreadable card as visible.
func TestUX108ApprovalsReadsAnswerNotFoundForACardTheCallerCannotRead(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	// The dealer wide manager files a credit exception at branch B. The
	// clerk reaches branch A only and filed nothing, so the pull shows it to
	// the manager alone.
	key := uuid.NewString()
	rs, err := store.Create(ctx, gm, key, map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108: a card the clerk may not read"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	if rs.ID == "" {
		t.Fatalf("the manager's filing answers no row: %+v", rs)
	}

	// The reader keeps the card, with its projection.
	got, found, err := store.Load(ctx, gm, rs.ID)
	if err != nil || !found || got.Data == nil {
		t.Fatalf("the manager reads its own card: %+v found %v err %v", got, found, err)
	}
	got, found, err = store.FindApplied(ctx, gm, key)
	if err != nil || !found {
		t.Fatalf("the manager resolves its own key: %+v found %v err %v", got, found, err)
	}

	// The caller who may not read it: the absent card's answer, field for
	// field, no id and no seq.
	absent := uuid.NewString()
	want, wantFound, err := store.FindApplied(ctx, clerk, absent)
	if err != nil || wantFound {
		t.Fatalf("the absent key answers not found: found %v err %v", wantFound, err)
	}
	got, gotFound, err := store.FindApplied(ctx, clerk, key)
	if err != nil {
		t.Fatalf("the caller's find applied failed: %v", err)
	}
	if gotFound {
		t.Errorf("a card the caller may not read resolves the key: %+v, want the absent key's answer", got)
	}
	if got.ID != want.ID || got.Seq != want.Seq || got.Deleted != want.Deleted || got.Data != nil {
		t.Errorf("the caller's answer differs from the absent key's answer: got %+v want %+v", got, want)
	}
	got, gotFound, err = store.Load(ctx, clerk, rs.ID)
	if err != nil {
		t.Fatalf("the caller's load failed: %v", err)
	}
	if gotFound {
		t.Errorf("a card the caller may not read loads as found: %+v, want not found", got)
	}
	if got.ID != "" || got.Seq != 0 || got.Data != nil || got.Deleted {
		t.Errorf("the caller's load answer differs from the absent card's answer: got %+v", got)
	}
}

// TestUX108ApprovalsWithdrawnCardStaysFoundForEveryCaller: a withdrawn
// request's tombstone goes to every caller that passes the org fence, so a
// caller that may not read the live card still finds the withdrawn one: the
// tombstone names an id alone.
func TestUX108ApprovalsWithdrawnCardStaysFoundForEveryCaller(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	key := uuid.NewString()
	rs, err := store.Create(ctx, gm, key, map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108: a card withdrawn after the filing"})
	if err != nil {
		t.Fatalf("the manager files the credit exception: %v", err)
	}
	if _, err := store.Delete(ctx, gm, rs.ID, uuid.NewString()); err != nil {
		t.Fatalf("the manager withdraws its own card: %v", err)
	}

	got, found, err := store.Load(ctx, clerk, rs.ID)
	if err != nil || !found {
		t.Fatalf("the withdrawn card answers found for every caller: %+v found %v err %v", got, found, err)
	}
	if !got.Deleted || got.Data != nil {
		t.Errorf("the withdrawn card answers as a tombstone: %+v", got)
	}
	got, found, err = store.FindApplied(ctx, clerk, key)
	if err != nil || !found {
		t.Fatalf("the withdrawn card's key answers found for every caller: %+v found %v err %v", got, found, err)
	}
	if !got.Deleted || got.Data != nil {
		t.Errorf("the withdrawn card's key answers as a tombstone: %+v", got)
	}
}

// TestUX108ApprovalsEngineReplayOfAnotherCallersKeyCarriesNothingOfTheRow:
// the real engine over the real key ledger. The manager files a credit
// exception under a key; a replay of that key by a caller who may not read
// the card carries nothing of it: not its seq, nothing of it. The manager's
// own replay still answers applied with the card's seq, and nothing ran
// twice.
//
// The status word stays the engine's own bound answer: the approvals store
// cannot answer the gate's existence read, so the gate keeps the bound
// answer for a key it cannot attribute, and what the scope owes is the row.
func TestUX108ApprovalsEngineReplayOfAnotherCallersKeyCarriesNothingOfTheRow(t *testing.T) {
	f := newGapFixture(t)
	ctx := context.Background()
	store := ux108ApprovalsStore(t, f)
	gm := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubGeneralMgr}
	clerk := sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: testkit.SubInsideSales}

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("approvals", store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("approvals")
	if !ok {
		t.Fatal("approvals missing from the registry")
	}
	key := uuid.NewString()
	payload := map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108: a card the clerk may not read"}
	op := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: payload}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	res := gapPush(t, engine, info, gm, []apigen.SyncPushOperation{op})
	if res[0].Status != "applied" || res[0].Seq == nil {
		t.Fatalf("the manager files the credit exception: %+v", res[0])
	}
	first := res[0]

	// The caller who may not read the card replays the key: nothing of the
	// card travels, the seq included.
	res = gapPush(t, engine, info, clerk, []apigen.SyncPushOperation{op})
	replay := res[0]
	if replay.Seq != nil {
		t.Errorf("a replay by a caller who may not read the card carries nothing of the row: seq %v status %q error %v",
			replay.Seq, replay.Status, replay.Error)
	}
	if replay.Conflict != nil {
		t.Errorf("a replay by a caller who may not read the card carries no conflict: %+v", replay.Conflict)
	}

	// Another body under the same key: the engine's own answer (the scope is
	// not consulted on the hash path), and it carries nothing of the row.
	other := apigen.SyncPushOperation{ClientId: key, Op: "create", Payload: map[string]any{
		"branch_id":   testkit.BranchNorthBID,
		"policy_type": "CREDIT_LIMIT_EXCEEDED",
		"detail":      "ux108: another body"}}
	res = gapPush(t, engine, info, clerk, []apigen.SyncPushOperation{other})
	if res[0].Status != "rejected" || res[0].Seq != nil {
		t.Errorf("another body under the same key is refused and carries nothing of the row: %+v", res[0])
	}

	// The key's owner keeps the bound answer, with the card's seq.
	res = gapPush(t, engine, info, gm, []apigen.SyncPushOperation{op})
	if res[0].Status != "applied" || res[0].Seq == nil || *res[0].Seq != *first.Seq {
		t.Errorf("the filing caller's own replay still answers applied with the card's seq: %+v", res[0])
	}
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM permission_approval_requests WHERE idempotency_key = $1::uuid`, key).Scan(&n); err != nil || n != 1 {
		t.Errorf("the replay ran nothing twice: %d cards under the key, %v", n, err)
	}
}
