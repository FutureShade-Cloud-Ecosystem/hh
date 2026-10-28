//go:build integration

package main

// credit_memo_withdraw_integration_test.go: THE ABANDONED PARK. A requester who withdraws their own
// pending credit memo request (the rail's Delete, the requester's own act) must not leave the parked
// DRAFT memo and its DRAFT journal holding the invoice's room for good: the withdrawal voids both on
// its own transaction, and the next credit of that invoice goes through.
//
// Red on the branch: the withdrawal succeeded and voided nothing.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

func TestIntegration_CreditMemoRound_AWithdrawnParkVoidsTheMemoAndFreesTheRoom(t *testing.T) {
	pool := cmRoundPool(t)
	world := cmRoundWorldPlant(t, pool)
	svc, _ := cmRoundService(t, pool)

	res := cmRoundParkCredit(t, pool, svc, world.requester, world.cust, world.inv, "cm-rail-withdraw")
	// The withdrawn memo's journal is the round's own row: the withdrawal voids it, it does not
	// delete it, and a voided journal left on the shared gate is still a line no later package
	// can decide.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = $1`,
			billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})

	// The requester takes their request back through the rail's own door. The store carries the same
	// JE bridge the decide path does, as the composition root wires it.
	glRepo := gl.NewRepository(pool)
	store := identity.NewApprovalsSyncStore(pool, linkedUserID(pool)).
		WithJEDecider(jeDeciderAdapter{svc: gl.NewService(glRepo), journals: glRepo, memos: billing.NewInvoiceWriter(pool)})
	if _, err := store.Delete(context.Background(),
		sync.ScopeFilter{OrgID: world.org, Actor: world.requesterPlatform}, res.ApprovalID, ""); err != nil {
		t.Fatalf("withdraw the request: %v", err)
	}

	ctx := context.Background()
	var memoStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1::uuid`, res.CreditMemoID).
		Scan(&memoStatus); err != nil {
		t.Fatalf("read the withdrawn memo: %v", err)
	}
	if memoStatus != "VOID" {
		t.Fatalf("fw1-12a core red: the withdrawn credit's memo reads %s, want VOID (the room must free)", memoStatus)
	}
	var journalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM gl_journals WHERE idempotency_key = $1`,
		billing.CreditMemoJournalKeyPrefix+res.CreditMemoID).Scan(&journalStatus); err != nil {
		t.Fatalf("read the withdrawn journal: %v", err)
	}
	if journalStatus != "VOID" {
		t.Fatalf("fw1-12a core red: the withdrawn credit's journal reads %s, want VOID", journalStatus)
	}
	// The room is freed: a small posted credit of the invoice goes through.
	issueCtx := identity.ContextWithStaffUserID(ctx, world.requester)
	follow, err := svc.Issue(issueCtx, billing.CreditMemoRouteRequest{
		CustomerID: world.cust, BranchID: trentonBranchID, InvoiceID: world.inv,
		AmountCents: 100_000, Reason: "The next credit", IdempotencyKey: "cm-rail-after-withdraw",
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: identity.ScopeGlobal})
	if err != nil {
		t.Fatalf("fw1-12a core red: the withdrawn park held the invoice's room; the next credit answered %v", err)
	}
	if follow.Status != billing.CreditMemoIssued {
		t.Fatalf("the next credit is %q, want ISSUED", follow.Status)
	}
}
