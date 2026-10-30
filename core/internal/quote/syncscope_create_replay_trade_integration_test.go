//go:build integration

// syncscope_create_replay_trade_integration_test.go: UX-114 round 3, the
// accepted create trade (principal item 342, the K3 read's P3-1). A create
// replay on a withdrawn quote, for a caller who cannot read it, answers the
// spent key the absent key's refusal, "sync: row not found", and a free key
// creates a new quote ("applied", a fresh seq). The pair is intentionally
// NOT equal: a non binder's create under a spent key must never reach
// store.Create (PR 640's rule, no key creates twice), while a free key must
// reach it, and Create answers "applied" for a caller who may create. The
// trade is PR 640's settled design, stated per scope in the PR body; this
// leg records today's answers exactly, so the trade is pinned, not assumed.
package quote

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestUX114R3QuotesGoneRowCreateReplayRecordsTheTrade: the real engine over
// the real key ledger. The manager creates a DRAFT quote through sync under
// one key and withdraws it. Inside Sales, at the Work rung, may not read the
// manager's quote and its pull carries neither the quote nor its tombstone.
// The create replay under the spent key is the gate's refusal for a non
// binder; the same body under a free key creates a new quote. The two full
// answers are the accepted trade: recorded here, not equal by design.
func TestUX114R3QuotesGoneRowCreateReplayRecordsTheTrade(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	qd1cPublish(t, qd1Cfg)

	engine, info := ux108R2Engine(t, f)

	bound := uuid.NewString()
	fresh := uuid.NewString()
	quoteIDs := []string{}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(ctx, `DELETE FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
			[]string{bound, fresh})
		if len(quoteIDs) > 0 {
			_, _ = f.pool.Exec(ctx, `DELETE FROM quotes WHERE id = ANY($1::uuid[])`, quoteIDs)
		}
	})

	// The manager creates a DRAFT quote through sync under the key: the
	// create binds the key to him, with the quote as the key's entity.
	createOp := apigen.SyncPushOperation{ClientId: bound, Op: "create", Payload: map[string]any{
		"customer_id": testkit.TenantAlphaID,
		"branch_id":   testkit.BranchNorthAID,
		"valid_until": ux108Date(30)}}
	first := ux108R2Push(t, engine, info, ux108As(testkit.SubSalesManager), createOp)
	if first.Status != "applied" || first.Seq == nil {
		t.Fatalf("the manager creates its draft through sync: %+v", first)
	}
	var quoteID string
	if err := f.pool.QueryRow(ctx,
		`SELECT id::text FROM quotes WHERE idempotency_key = $1::uuid`, bound).Scan(&quoteID); err != nil {
		t.Fatalf("read the created quote back: %v", err)
	}
	quoteIDs = append(quoteIDs, quoteID)

	// The manager withdraws the draft.
	if _, err := f.store.Delete(ctx, ux108As(testkit.SubSalesManager), quoteID, uuid.NewString()); err != nil {
		t.Fatalf("the manager withdraws its own draft: %v", err)
	}

	inside := ux108As(testkit.SubInsideSales)

	// The create replay under the spent key: the gate refuses a non binder
	// the absent key's answer, whole.
	boundAnswer := ux108R2Push(t, engine, info, inside, createOp)
	if boundAnswer.Status != "rejected" || boundAnswer.Error == nil || *boundAnswer.Error != ux108R2Refusal {
		t.Errorf("the spent key's create replay on the gone row is the absent key's refusal: %+v", boundAnswer)
	}
	if boundAnswer.Seq != nil {
		t.Errorf("the spent key's create replay carries no seq: %+v", boundAnswer)
	}
	if boundAnswer.Conflict != nil {
		t.Errorf("the spent key's create replay carries no envelope: %+v", boundAnswer.Conflict)
	}

	// The same body under a free key: a new quote is created. This answer is
	// deliberately not the spent key's: the trade, recorded.
	freeOp := createOp
	freeOp.ClientId = fresh
	freeAnswer := ux108R2Push(t, engine, info, inside, freeOp)
	if freeAnswer.Status != "applied" || freeAnswer.Seq == nil {
		t.Errorf("the free key's create replay applies a new quote: %+v", freeAnswer)
	}
	var freeQuote string
	if err := f.pool.QueryRow(ctx,
		`SELECT id::text FROM quotes WHERE idempotency_key = $1::uuid`, fresh).Scan(&freeQuote); err != nil {
		t.Fatalf("read the free key's quote back: %v", err)
	}
	quoteIDs = append(quoteIDs, freeQuote)
	if freeQuote == quoteID {
		t.Errorf("the free key's create applied a new quote, not the withdrawn one")
	}

	// The ledger holds exactly the two creates: the manager's and the free
	// key's.
	var ledgerRows int
	if err := f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sync_op_hashes WHERE client_id = ANY($1::uuid[])`,
		[]string{bound, fresh}).Scan(&ledgerRows); err != nil {
		t.Fatalf("count the probe's ledger rows: %v", err)
	}
	if ledgerRows != 2 {
		t.Errorf("the trade binds one key per create: %d ledger rows, want 2", ledgerRows)
	}
}
