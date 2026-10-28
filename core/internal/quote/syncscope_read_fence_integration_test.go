//go:build integration

// syncscope_read_fence_integration_test.go: UX-108. The quotes scope's pull
// holds the org predicate and the caller's read rung (syncReadScope): the
// branches the caller reaches and, at the Work rung, the quotes it owns. Load
// and FindApplied held the org predicate alone and answered FOUND with the
// projection dropped, so the engine read an unreadable row as visible: the
// gate's Load saw it, and the replayed answer carried the row's seq.
//
// The leg: a quote the caller may not read answers NOT FOUND, field for field
// the answer a key nobody applied gives, and the caller who may read it keeps
// the row. The real engine over the real key ledger answers a refused
// caller's replay with nothing of the row.
package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux108As is one caller's scope filter (org plus the platform subject).
func ux108As(sub string) sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: sub}
}

// ux108Plant plants one quote of the north org, owned by the owner, in the
// branch, carrying the key when one is named, with a real seq so the pull
// sees it.
func ux108Plant(t *testing.T, f *syncFixture, owner, branchID, key string) (id string, seq int64) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `
		WITH qseq AS (
			INSERT INTO scope_sequences (scope) VALUES ('quotes')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value
		)
		INSERT INTO quotes (quote_no, customer_id, branch_id, org_id, status, subtotal_cents, tax_cents,
		                    total_cents, salesperson_id, created_by, seq, idempotency_key)
		VALUES ('#Q-UX108-' || nextval('quote_no_seq'), $1::uuid, $2::uuid, $3::uuid, 'SENT', 2000, 0, 2000,
		        $4::uuid, $4::uuid, (SELECT next_value FROM qseq), NULLIF($5,'')::uuid)
		RETURNING id::text, seq`,
		testkit.TenantAlphaID, branchID, testkit.OrgNorthID, owner, key).Scan(&id, &seq); err != nil {
		t.Fatalf("plant quote: %v", err)
	}
	return id, seq
}

// ux108Date is a date ahead of the module's own clock, so the expiry guard
// lets the edit stand.
func ux108Date(days int) string {
	return time.Now().AddDate(0, 0, days).Format(dateLayout)
}

// TestQD108_SyncLoadAndFindAppliedAnswerNotFoundForAQuoteTheCallerCannotRead:
// a quote the caller may not read is not found, field for field the answer a
// key nobody applied gives. Before the fix both reads answered FOUND with
// the projection dropped, and the engine read an unreadable row as visible.
func TestQD108_SyncLoadAndFindAppliedAnswerNotFoundForAQuoteTheCallerCannotRead(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	key := uuid.NewString()
	quoteID, quoteSeq := ux108Plant(t, f, managerID, testkit.BranchNorthAID, key)

	// The caller who may read it keeps the row, with its projection.
	rs, found, err := f.store.Load(ctx, ux108As(testkit.SubSalesManager), quoteID)
	if err != nil || !found || rs.Data == nil {
		t.Fatalf("the owner reads its own quote: %+v found %v err %v", rs, found, err)
	}
	rs, found, err = f.store.FindApplied(ctx, ux108As(testkit.SubSalesManager), key)
	if err != nil || !found || rs.Seq != quoteSeq {
		t.Fatalf("the owner resolves its own key: %+v found %v err %v", rs, found, err)
	}

	// The caller who may not read it: Inside Sales, at the Work rung, reads
	// its own quotes only, so another seller's quote is out of its reach.
	inside := ux108As(testkit.SubInsideSales)
	absent := uuid.NewString()
	want, wantFound, err := f.store.FindApplied(ctx, inside, absent)
	if err != nil || wantFound {
		t.Fatalf("the absent key answers not found: found %v err %v", wantFound, err)
	}
	got, gotFound, err := f.store.FindApplied(ctx, inside, key)
	if err != nil {
		t.Fatalf("the caller's find applied failed: %v", err)
	}
	if gotFound {
		t.Errorf("a quote the caller may not read resolves the key: %+v, want the absent key's answer", got)
	}
	if got.ID != want.ID || got.Seq != want.Seq || got.Deleted != want.Deleted || got.Data != nil {
		t.Errorf("the caller's answer differs from the absent key's answer: got %+v want %+v", got, want)
	}
	got, gotFound, err = f.store.Load(ctx, inside, quoteID)
	if err != nil {
		t.Fatalf("the caller's load failed: %v", err)
	}
	if gotFound {
		t.Errorf("a quote the caller may not read loads as found: %+v, want not found", got)
	}
	if got.ID != "" || got.Seq != 0 || got.Data != nil || got.Deleted {
		t.Errorf("the caller's load answer differs from the absent row's answer: got %+v", got)
	}
}

// TestQD108_SyncEngineReplayOfAnotherCallersKeyCarriesNothingOfTheRow: the
// real engine over the real key ledger. The Sales manager applies an edit
// under a key; a replay of that key by a caller who may not read the quote
// carries nothing of the row: not its seq, nothing of it. The manager's own
// replay still answers applied with the row's seq, and nothing ran twice.
//
// The status word stays the engine's own bound answer: the quotes store
// cannot answer the gate's existence read, so the gate keeps the bound
// answer for a key it cannot attribute, and what the scope owes is the row.
func TestQD108_SyncEngineReplayOfAnotherCallersKeyCarriesNothingOfTheRow(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	key := uuid.NewString()
	// The quote carries no key: the manager's edit below is what applies the
	// key, so the ledger binds it to the manager and the replay below is a
	// replay of a key somebody else applied.
	quoteID, _ := ux108Plant(t, f, managerID, testkit.BranchNorthAID, "")

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(f.pool))
	if err := engine.RegisterScope("quotes", f.store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("quotes")
	if !ok {
		t.Fatal("quotes missing from the registry")
	}
	op := apigen.SyncPushOperation{ClientId: key, Op: "update", Payload: map[string]any{
		"id": quoteID, "valid_until": ux108Date(30)}}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, key)
	})

	res, err := engine.Push(ctx, info, ux108As(testkit.SubSalesManager), []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil {
		t.Fatalf("the manager applies its edit: %+v %v", res.Results[0], err)
	}
	first := res.Results[0]
	var validity string
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).Scan(&validity); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	if validity != ux108Date(30) {
		t.Fatalf("the manager's edit did not land: the quote's validity is %q", validity)
	}

	// The caller who may not read the quote replays the key: nothing of the
	// row travels, the seq included.
	res, err = engine.Push(ctx, info, ux108As(testkit.SubInsideSales), []apigen.SyncPushOperation{op})
	if err != nil {
		t.Fatalf("the refused caller's replay failed the push: %v", err)
	}
	replay := res.Results[0]
	if replay.Seq != nil {
		t.Errorf("a replay by a caller who may not read the quote carries nothing of the row: seq %v status %q error %v",
			replay.Seq, replay.Status, replay.Error)
	}
	if replay.Conflict != nil {
		t.Errorf("a replay by a caller who may not read the quote carries no conflict: %+v", replay.Conflict)
	}

	// Another body under the same key: the engine's own answer (the scope is
	// not consulted on the hash path), and it carries nothing of the row.
	other := apigen.SyncPushOperation{ClientId: key, Op: "update", Payload: map[string]any{
		"id": quoteID, "valid_until": ux108Date(60)}}
	res, err = engine.Push(ctx, info, ux108As(testkit.SubInsideSales), []apigen.SyncPushOperation{other})
	if err != nil {
		t.Fatalf("the refused caller's other body failed the push: %v", err)
	}
	if res.Results[0].Status != "rejected" || res.Results[0].Seq != nil {
		t.Errorf("another body under the same key is refused and carries nothing of the row: %+v", res.Results[0])
	}

	// The key's owner keeps the bound answer, with the row's seq.
	res, err = engine.Push(ctx, info, ux108As(testkit.SubSalesManager), []apigen.SyncPushOperation{op})
	if err != nil || res.Results[0].Status != "applied" || res.Results[0].Seq == nil || *res.Results[0].Seq != *first.Seq {
		t.Errorf("the owner's own replay still answers applied with the row's seq: %+v %v", res.Results[0], err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(valid_until::text,'') FROM quotes WHERE id = $1`, quoteID).Scan(&validity); err != nil {
		t.Fatalf("read the quote back: %v", err)
	}
	if validity != ux108Date(30) {
		t.Errorf("the replay ran nothing twice: the quote's validity is %q", validity)
	}
}

// TestQD108_SyncCreateCollisionAnswersRowExistsForAnotherCallersQuote: the
// client-chosen id is taken by a quote the caller may not read. The
// collision is inside the caller's own org, so it is the caller's own fact
// and answers ErrRowExists, the pick door's rule (its create's collision
// probe reads the row under the org predicate alone). A fence that held the
// probe to the read rung answered ErrRowNotFound, a write-path answer the
// fence did not owe.
func TestQD108_SyncCreateCollisionAnswersRowExistsForAnotherCallersQuote(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	managerID, ok := linkedSyncUser(f.pool)(ctx, testkit.SubSalesManager)
	if !ok {
		t.Fatal("no linked user for the Sales manager")
	}
	taken, _ := ux108Plant(t, f, managerID, testkit.BranchNorthAID, uuid.NewString())

	inside := ux108As(testkit.SubInsideSales)
	_, err := f.store.Create(ctx, inside, uuid.NewString(), map[string]any{
		"id": taken, "customer_id": testkit.TenantAlphaID, "branch_id": testkit.BranchNorthAID})
	if !errors.Is(err, sync.ErrRowExists) {
		t.Errorf("a taken id answers ErrRowExists, the collision is inside the caller's own org: %v", err)
	}
}
