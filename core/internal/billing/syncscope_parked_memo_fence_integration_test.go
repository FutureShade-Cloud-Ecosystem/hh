//go:build integration

// syncscope_parked_memo_fence_integration_test.go: UX-112. The billing
// scopes' two pull feeds hold the PARKED CREDIT MEMO clause (a DRAFT
// credit memo is in no page: no money has moved, so no caller sees it as
// a credit before its approval). Load and FindApplied answered a key
// through the row anyway, so the engine read a row the pull hides as
// visible, the exact disagreement UX-108 closed for quotes and
// approvals. After the fix the clause rides the key reads' SQL and the
// answers agree with the pull:
//
//   - a caller the pull hides the parked memo from: Load and FindApplied
//     answer not found, field for field the answer an absent row gives;
//   - once the rail approves the memo (the real finalize, the subledger
//     row written) the pull sends it, and both reads answer found,
//     unchanged;
//   - through the real engine and the real key ledger, a push whose key
//     is the parked memo's resolves nothing of the hidden row.
//
// The aging scope's FindApplied has no query to fence: the scope has no
// push and the read refuses every key (ErrOpRefused), which this file
// pins so a later push door cannot arrive without the fence.
//
// The memo is parked through the REAL writer (ParkCreditMemoTx, the
// route's own park arm) and approved through the REAL finalize
// (FinalizeParkedCreditMemoTx, which writes the AR subledger row), on
// real transactions, against the real scope stores.
//
// Run with:
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:57434/ux112_gate?sslmode=disable' \
//	  go test -tags=integration -count=1 ./internal/billing/...
package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// ux112World is one seeded world with one parked credit memo raised
// through the real writer, plus the legs that read it back.
type ux112World struct {
	pool    *pgxpool.Pool
	writer  *InvoiceWriter
	memoID  string
	memoKey string
}

// newUx112World builds the fixture, stamps the dealer's own invoices
// into the feed, and parks one credit memo through the real writer on a
// real transaction. The parked row carries the park's idempotency key,
// exactly as the route's park arm leaves it for the rail's decision.
func newUx112World(t *testing.T) *ux112World {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if err := testkit.Build(ctx, testkit.PgxPool(pool)); err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	writer := NewInvoiceWriter(pool)
	// Declared before the cleanup registers: the closure captures the
	// variable, the park below fills it.
	parkedKey := ""
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM sync_op_hashes WHERE client_id::text = $1`, parkedKey)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE source_type = 'invoice' AND source_ref_id IN
			(SELECT id FROM invoices WHERE idempotency_key = $1)`, parkedKey)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE idempotency_key = $1)`, parkedKey)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE idempotency_key = $1`, parkedKey)
		_ = testkit.Cleanup(c, testkit.PgxPool(pool))
	})
	parkedKey = uuid.NewString()
	if _, err := pool.Exec(ctx,
		`WITH s AS (
			INSERT INTO scope_sequences (scope) VALUES ('invoices')
			ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
			RETURNING next_value)
		 UPDATE invoices SET org_id = $2::uuid, seq = (SELECT next_value FROM s)
		  WHERE customer_id = $1::uuid AND doc_type <> 'CREDIT_MEMO'`,
		testkit.TenantAlphaID, testkit.OrgNorthID); err != nil {
		t.Fatalf("stamp the fixture invoices into the feed: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the park transaction: %v", err)
	}
	doc, _, err := writer.ParkCreditMemoTx(ctx, tx, CreditMemoInput{
		CustomerID:     testkit.TenantAlphaID,
		BranchID:       testkit.BranchNorthAID,
		AmountCents:    2500,
		Memo:           "ux112: a parked credit memo no page carries",
		IdempotencyKey: parkedKey,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("park the credit memo through the real writer: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the park transaction: %v", err)
	}
	return &ux112World{pool: pool, writer: writer, memoID: doc.InvoiceID, memoKey: parkedKey}
}

// ux112Alpha is the payer's app filter: the tenant fence the aging scope
// and the invoices scope's tenant arm read (the token's tenant is the
// customer).
func ux112Alpha() sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantAlphaID, Tenant: true, Actor: "ux112"}
}

// ux112Staff is the counter's filter: the org fence the invoices scope's
// staff arm reads.
func ux112Staff() sync.ScopeFilter {
	return sync.ScopeFilter{OrgID: testkit.OrgNorthID, Actor: "ux112"}
}

// ux112Push drives the real engine's push and hands back the results.
func ux112Push(t *testing.T, e *sync.Engine, scope sync.ScopeInfo, f sync.ScopeFilter, ops []apigen.SyncPushOperation) []apigen.SyncPushResult {
	t.Helper()
	resp, err := e.Push(context.Background(), scope, f, ops)
	if err != nil {
		t.Fatalf("push %s: %v", scope.Name, err)
	}
	if len(resp.Results) != len(ops) {
		t.Fatalf("push %s: %d results for %d ops", scope.Name, len(resp.Results), len(ops))
	}
	return resp.Results
}

// TestUx112InvoicesScopeParkedMemoAnswersNotFound: the invoices pull
// hides the parked memo from BOTH caller classes (the clause is
// unconditional), so Load and FindApplied answer not found for it, field
// for field the absent row's answer. Before the fix both reads answered
// FOUND with the row's projection and seq.
func TestUx112InvoicesScopeParkedMemoAnswersNotFound(t *testing.T) {
	w := newUx112World(t)
	ctx := context.Background()
	store := NewInvoicesSyncStore(w.pool, w.writer, nil)

	// The pull's own answer is the fence the key reads must agree with:
	// the parked memo is in no page, for the payer and for the counter.
	for name, f := range map[string]sync.ScopeFilter{"tenant": ux112Alpha(), "staff": ux112Staff()} {
		items, _, err := store.Pull(ctx, f, 0, 200)
		if err != nil {
			t.Fatalf("%s pull: %v", name, err)
		}
		for _, it := range items {
			if it.ID == w.memoID {
				t.Fatalf("%s pull carries the parked memo: the fence this test reads through is not the shipped one", name)
			}
		}
	}

	// The absent row's answer, captured field for field.
	absent := uuid.NewString()
	wantLoad, wantLoadFound, err := store.Load(ctx, ux112Alpha(), absent)
	if err != nil || wantLoadFound {
		t.Fatalf("the absent invoice answers not found: found %v err %v", wantLoadFound, err)
	}
	wantKey, wantKeyFound, err := store.FindApplied(ctx, ux112Alpha(), absent)
	if err != nil || wantKeyFound {
		t.Fatalf("the absent key answers not found: found %v err %v", wantKeyFound, err)
	}

	// The payer's app: the pull hides the parked memo from it, so the key
	// reads answer the absent row's answer, nothing of the row.
	gotLoad, gotLoadFound, err := store.Load(ctx, ux112Alpha(), w.memoID)
	if err != nil {
		t.Fatalf("the tenant's load failed: %v", err)
	}
	if gotLoadFound {
		t.Errorf("a parked memo the pull hides loads as found: %+v, want not found", gotLoad)
	}
	if gotLoad.ID != wantLoad.ID || gotLoad.Seq != wantLoad.Seq || gotLoad.Deleted != wantLoad.Deleted || gotLoad.Data != nil {
		t.Errorf("the tenant's load answer differs from the absent row's answer: got %+v want %+v", gotLoad, wantLoad)
	}
	gotKey, gotKeyFound, err := store.FindApplied(ctx, ux112Alpha(), w.memoKey)
	if err != nil {
		t.Fatalf("the tenant's find applied failed: %v", err)
	}
	if gotKeyFound {
		t.Errorf("a parked memo the pull hides resolves its key: %+v, want not found", gotKey)
	}
	if gotKey.ID != wantKey.ID || gotKey.Seq != wantKey.Seq || gotKey.Deleted != wantKey.Deleted || gotKey.Data != nil {
		t.Errorf("the tenant's find applied answer differs from the absent key's answer: got %+v want %+v", gotKey, wantKey)
	}

	// The counter: the same clause hides the row from the org arm too.
	gotLoad, gotLoadFound, err = store.Load(ctx, ux112Staff(), w.memoID)
	if err != nil {
		t.Fatalf("the staff load failed: %v", err)
	}
	if gotLoadFound {
		t.Errorf("a parked memo the pull hides loads as found for the org arm: %+v, want not found", gotLoad)
	}
	if gotLoad.ID != "" || gotLoad.Seq != 0 || gotLoad.Deleted || gotLoad.Data != nil {
		t.Errorf("the staff load answer carries something of the hidden row: got %+v", gotLoad)
	}
	gotKey, gotKeyFound, err = store.FindApplied(ctx, ux112Staff(), w.memoKey)
	if err != nil {
		t.Fatalf("the staff find applied failed: %v", err)
	}
	if gotKeyFound {
		t.Errorf("a parked memo the pull hides resolves its key for the org arm: %+v, want not found", gotKey)
	}
	if gotKey.ID != "" || gotKey.Seq != 0 || gotKey.Deleted || gotKey.Data != nil {
		t.Errorf("the staff find applied answer carries something of the hidden row: got %+v", gotKey)
	}

	// Another payer: the tenant fence holds as before, parked or not.
	beta := sync.ScopeFilter{OrgID: testkit.OrgNorthID, TenantID: testkit.TenantBetaID, Tenant: true, Actor: "ux112"}
	if _, found, err := store.Load(ctx, beta, w.memoID); err != nil || found {
		t.Errorf("another payer loads alpha's parked memo: found %v err %v", found, err)
	}
	if _, found, err := store.FindApplied(ctx, beta, w.memoKey); err != nil || found {
		t.Errorf("another payer resolves alpha's parked memo key: found %v err %v", found, err)
	}
}

// TestUx112InvoicesScopeApprovedMemoAnswersFound: once the rail approves
// the parked memo (the real finalize, the AR subledger row written) the
// pull sends it, and Load and FindApplied answer found with the
// projection, unchanged by the fence. This is the leg that keeps the
// clause from over-hiding: the fence hides PARKED credits, not credits.
func TestUx112InvoicesScopeApprovedMemoAnswersFound(t *testing.T) {
	w := newUx112World(t)
	ctx := context.Background()
	store := NewInvoicesSyncStore(w.pool, w.writer, nil)

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the approve transaction: %v", err)
	}
	if err := w.writer.FinalizeParkedCreditMemoTx(ctx, tx, w.memoID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("approve the parked memo through the real finalize: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the approve transaction: %v", err)
	}

	for name, f := range map[string]sync.ScopeFilter{"tenant": ux112Alpha(), "staff": ux112Staff()} {
		sent := false
		items, _, err := store.Pull(ctx, f, 0, 200)
		if err != nil {
			t.Fatalf("%s pull: %v", name, err)
		}
		for _, it := range items {
			if it.ID == w.memoID {
				sent = true
				if v, _ := it.Data["total_cents"].(float64); v >= 0 {
					t.Errorf("%s pull carries the approved credit with total %v, want a negative credit", name, v)
				}
			}
		}
		if !sent {
			t.Fatalf("%s pull misses the approved credit: the pull this fence agrees with does not send it", name)
		}
		got, found, err := store.Load(ctx, f, w.memoID)
		if err != nil || !found {
			t.Fatalf("%s load of the approved credit: found %v err %v", name, found, err)
		}
		if got.Data == nil || got.Seq == 0 {
			t.Errorf("%s load of the approved credit answers %+v, want the projection and its seq", name, got)
		}
		got, found, err = store.FindApplied(ctx, f, w.memoKey)
		if err != nil || !found {
			t.Fatalf("%s find applied of the approved credit's key: found %v err %v", name, found, err)
		}
		if got.Data == nil || got.Seq == 0 {
			t.Errorf("%s find applied of the approved credit answers %+v, want the projection and its seq", name, got)
		}
	}
}

// TestUx112InvoicesScopeEngineReplayOfAParkedMemosKeyCarriesNothingOfTheRow:
// the real engine over the real key ledger. A push whose client key is the
// parked memo's park key resolves nothing of the hidden row: not its seq,
// nothing of it. Before the fix the engine answered applied with the parked
// memo's seq, a row the pull never sent.
func TestUx112InvoicesScopeEngineReplayOfAParkedMemosKeyCarriesNothingOfTheRow(t *testing.T) {
	w := newUx112World(t)
	ctx := context.Background()
	store := NewInvoicesSyncStore(w.pool, w.writer, nil)

	engine := sync.NewEngine().WithKeyLedger(sync.NewPgKeyLedger(w.pool))
	if err := engine.RegisterScope("invoices", store); err != nil {
		t.Fatalf("register: %v", err)
	}
	info, ok := sync.Lookup("invoices")
	if !ok {
		t.Fatal("invoices missing from the registry")
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = $1::uuid`, w.memoKey)
	})

	op := apigen.SyncPushOperation{ClientId: w.memoKey, Op: "create", Payload: map[string]any{}}
	results := ux112Push(t, engine, info, ux112Alpha(), []apigen.SyncPushOperation{op})
	res := results[0]
	if res.Status == "applied" {
		t.Errorf("a push whose key is the parked memo's resolves the hidden row: status %q seq %v, want nothing of it", res.Status, res.Seq)
	}
	if res.Seq != nil {
		t.Errorf("the parked memo's seq escaped through the key resolution: seq %v", *res.Seq)
	}
}

// TestUx112AgingScopeParkedMemoAnswersNotFound: the aging pull hides the
// parked memo (the clause is the aging scope's own), so Load answers the
// absent row's answer. FindApplied has no query to fence: the scope has
// no push and refuses every key, which this leg pins so a later push
// door cannot arrive without the fence.
func TestUx112AgingScopeParkedMemoAnswersNotFound(t *testing.T) {
	w := newUx112World(t)
	ctx := context.Background()
	store := NewAgingStore(w.pool, nil)
	f := ux112Alpha()

	items, _, err := store.Pull(ctx, f, 0, 200)
	if err != nil {
		t.Fatalf("aging pull: %v", err)
	}
	for _, it := range items {
		if it.ID == w.memoID {
			t.Fatalf("aging pull carries the parked memo: the fence this test reads through is not the shipped one")
		}
	}

	want, wantFound, err := store.Load(ctx, f, uuid.NewString())
	if err != nil || wantFound {
		t.Fatalf("the absent invoice answers not found: found %v err %v", wantFound, err)
	}
	got, found, err := store.Load(ctx, f, w.memoID)
	if err != nil {
		t.Fatalf("aging load failed: %v", err)
	}
	if found {
		t.Errorf("a parked memo the aging pull hides loads as found: %+v, want not found", got)
	}
	if got.ID != want.ID || got.Seq != want.Seq || got.Deleted != want.Deleted || got.Data != nil {
		t.Errorf("the aging load answer differs from the absent row's answer: got %+v want %+v", got, want)
	}
	if _, _, err := store.FindApplied(ctx, f, w.memoKey); !errors.Is(err, sync.ErrOpRefused) {
		t.Errorf("the aging scope's find applied refuses every key, err %v, want ErrOpRefused", err)
	}
}

// TestUx112AgingScopeApprovedMemoAnswersFound: after the rail's approval
// the aging pull sends the credit, and Load answers found with the
// projection, unchanged by the fence.
func TestUx112AgingScopeApprovedMemoAnswersFound(t *testing.T) {
	w := newUx112World(t)
	ctx := context.Background()
	store := NewAgingStore(w.pool, nil)
	f := ux112Alpha()

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the approve transaction: %v", err)
	}
	if err := w.writer.FinalizeParkedCreditMemoTx(ctx, tx, w.memoID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("approve the parked memo through the real finalize: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the approve transaction: %v", err)
	}

	sent := false
	items, _, err := store.Pull(ctx, f, 0, 200)
	if err != nil {
		t.Fatalf("aging pull: %v", err)
	}
	for _, it := range items {
		if it.ID == w.memoID {
			sent = true
			if v, _ := it.Data["total_cents"].(float64); v >= 0 {
				t.Errorf("aging pull carries the approved credit with total %v, want a negative credit", v)
			}
		}
	}
	if !sent {
		t.Fatalf("aging pull misses the approved credit: the pull this fence agrees with does not send it")
	}
	got, found, err := store.Load(ctx, f, w.memoID)
	if err != nil || !found {
		t.Fatalf("aging load of the approved credit: found %v err %v", found, err)
	}
	if got.Data == nil || got.Seq == 0 {
		t.Errorf("aging load of the approved credit answers %+v, want the projection and its seq", got)
	}
}
