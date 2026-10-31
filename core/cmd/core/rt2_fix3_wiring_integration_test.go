//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	rt2f3Requester = "ce110000-0000-4000-8000-0000000000f1"
	rt2f3Decider   = "ce110000-0000-4000-8000-0000000000f2"
)

// plantApprovalUsers plants the requester and the decider the four eyes needs, and takes them back
// at the end, loudly.
func (l *rt2Leg) plantApprovalUsers() {
	l.t.Helper()
	ctx := context.Background()
	if _, err := l.pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status, password_hash, platform_user_id)
		VALUES ($1::uuid, 'rt2f3-requester@test.invalid', 'RT2F3 Requester', 'ACTIVE', NULL, 'rt2f3-requester'),
		       ($2::uuid, 'rt2f3-decider@test.invalid', 'RT2F3 Decider', 'ACTIVE', NULL, 'rt2f3-decider')`,
		rt2f3Requester, rt2f3Decider); err != nil {
		l.t.Fatalf("plant the requester and decider: %v", err)
	}
	l.t.Cleanup(func() {
		if _, err := l.pool.Exec(context.Background(), `DELETE FROM permission_approval_requests WHERE requested_by = $1::uuid`, rt2f3Requester); err != nil {
			l.t.Errorf("delete from permission_approval_requests: %v", err)
		}
		if _, err := l.pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1::uuid, $2::uuid)`, rt2f3Requester, rt2f3Decider); err != nil {
			l.t.Errorf("delete from users: %v", err)
		}
	})
}

// parkThroughTheRail parks a credit through the production parker adapter and returns its approval.
func (l *rt2Leg) parkThroughTheRail(invoiceID string, amount int64, key string) billing.CreditMemoParked {
	l.t.Helper()
	ctx := context.Background()
	rules := gl.NewRepository(l.pool)
	parked, err := (creditMemoParkerAdapter{pool: l.pool, w: l.writer(l.job, 100_000, 0), gl: l.money.glSvc, rules: rules, approvals: l.money.approvals}).
		ParkCreditMemo(ctx, billing.CreditMemoParkRequest{
			Memo: billing.CreditMemoInput{CustomerID: l.cust, BranchID: l.branch, ReversesInvoiceID: invoiceID,
				AmountCents: amount, Memo: "rt2f3", IdempotencyKey: key},
			AmountCents: amount, RequestedBy: rt2f3Requester, Reason: "rt2f3",
		})
	if err != nil {
		l.t.Fatalf("park the credit through the rail: %v", err)
	}
	return parked
}

func rt2f3DeciderCtx() identity.StaffContext {
	return identity.StaffContext{
		UserID: rt2f3Decider, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, Branches: map[string]bool{},
	}
}

// The reader's probe D (PR 762 delta read 2): a parked credit approved on the production rail, with
// the adapter exactly as wireMoney builds it, lowers the column AND the ledger, and the 1150 tie holds.
func TestRT2Fix3_AParkedCreditApprovedOnTheProductionRailMovesTheLedgerWithTheColumn(t *testing.T) {
	l := rt2Setup(t, "f3rail")
	l.newJob(true)
	l.plantApprovalUsers()
	doc := l.invoice(100_000, 0)
	eq(t, "holdback_cents", l.holdbackOf(doc.InvoiceID), int64(10_000))

	parked := l.parkThroughTheRail(doc.InvoiceID, 50_000, "rt2f3-rail")
	if err := l.money.approvals.DecideApproval(context.Background(), parked.ApprovalID, rt2f3DeciderCtx(), true, "agreed"); err != nil {
		t.Fatalf("approve on the production rail: %v", err)
	}
	l.drain()
	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(5_000))
	eq(t, "the ledger", l.ledgerNet(), int64(5_000))
	l.ties("after the parked credit approved on the production rail")
}

// A writer with no seam cannot lower the column: it refuses in words and changes nothing.
func TestRT2Fix3_ABareWriterRefusesToLowerTheColumn(t *testing.T) {
	l := rt2Setup(t, "f3bare")
	l.newJob(true)
	l.plantApprovalUsers()
	doc := l.invoice(100_000, 0)
	parked := l.parkThroughTheRail(doc.InvoiceID, 50_000, "rt2f3-bare")
	var memoID string
	memoID = parked.CreditMemoID
	ctx := context.Background()
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = billing.NewInvoiceWriter(l.pool).FinalizeParkedCreditMemoTx(ctx, tx, memoID)
	_ = tx.Rollback(ctx)
	if err == nil {
		t.Fatal("a writer with no holdback seam finalized a credit that lowers an invoice's holdback")
	}
	eq(t, "the column", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "the ledger", l.ledgerNet(), int64(10_000))
}
