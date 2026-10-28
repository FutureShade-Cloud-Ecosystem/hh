//go:build integration

// lockorder_creditmemo_walk_integration_test.go: the received payment walk never takes a credit memo.
//
// K3 P2-1: the credit apply locks the target invoice row first and the memo row after it (credit_apply.go's
// THE LOCKS order). The received payment walk sorted every named id into one set and locked each row without
// a document type predicate, so a hand-built request could name a CREDIT_MEMO and its target INVOICE. The
// memo passes the walk's open status check (it is FINALIZED) and its negative balance only produces zero
// payable, so the walk kept the memo's row lock while it paid the invoice beside it.
//
// With the memo's id BELOW the invoice's, the walk takes the memo and then the invoice while the credit
// apply takes the invoice and then the memo: each door holds the row the other needs, and Postgres answers
// one side 40P01. The credit route then answers the desk a raw 400 through credit_apply_handler.go.
//
// The fix filters the walk's targets to doc_type = 'INVOICE' in both the locking and the preview reads, so
// the walk never takes a memo it can never pay and the crossing cannot form.
//
// The staging: the test holds the invoice row, the credit apply is launched and parks on it, the walk is
// launched and (on the unfixed tree) takes the memo and parks on the invoice behind it, and only then is the
// row let go, so both doors are proven parked before anything is released. On the fixed tree the walk
// answers at the walk instead, refusing the memo before it touches a row, and the leg holds that answer as
// the fix's own.
package payments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// TestIntegration_LockOrder_TheReceivedPaymentWalkNeverTakesACreditMemo pins K3 P2-1: the walk's targets
// are invoices, so a request naming a credit memo is refused at the walk and never locks the memo row.
func TestIntegration_LockOrder_TheReceivedPaymentWalkNeverTakesACreditMemo(t *testing.T) {
	r := newLORig(t, 4)
	ctx := context.Background()
	tag := "CMW" + r.suffix

	// One open credit memo of -10000 and one invoice of 10000, the ids forced so the memo's id
	// sorts BELOW the invoice's: the walk takes the memo first, the credit apply takes the invoice
	// first, and the two orders cross.
	var memoID string
	if err := r.obs.QueryRow(ctx, `
		INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES (('10000000' || substr(gen_random_uuid()::text, 9))::uuid, $1, $2::uuid, $3::uuid, 'FINALIZED',
		        CURRENT_DATE, CURRENT_DATE, -10000, 0, -10000, -10000, now(), 'CREDIT_MEMO')
		RETURNING id::text`, tag+"-CM", r.custID, r.branchID).Scan(&memoID); err != nil {
		t.Fatalf("seed the credit memo: %v", err)
	}
	var invID string
	if err := r.obs.QueryRow(ctx, `
		INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		VALUES (('f0000000' || substr(gen_random_uuid()::text, 9))::uuid, $1, $2::uuid, $3::uuid, 'FINALIZED',
		        CURRENT_DATE, CURRENT_DATE + 30, 10000, 0, 10000, 10000, now(), 'INVOICE')
		RETURNING id::text`, tag+"-INV", r.custID, r.branchID).Scan(&invID); err != nil {
		t.Fatalf("seed the invoice: %v", err)
	}
	if !(memoID < invID) {
		t.Fatalf("the staging came out the other sort: memo %s, invoice %s", memoID, invID)
	}

	applySvc := billing.NewCreditApplyService(billing.NewRepository(r.pool))
	// The apply's key is scoped to the caller's org (FW1-12c) and the key table takes a real org
	// (0448), so the leg's caller carries a planted org's id. The plant's own cleanup (the key's
	// rows, then the org row) runs before the rig's, so the invoices free cleanly.
	var applyOrg string
	if err := r.obs.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, $2) RETURNING id::text`,
		"lo-cmw-"+r.suffix, "Lock Order Credit Memo Walk").Scan(&applyOrg); err != nil {
		t.Fatalf("plant the walk leg's org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = r.obs.Exec(c, `DELETE FROM credit_application_keys WHERE org_id = $1::uuid`, applyOrg)
		_, _ = r.obs.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, applyOrg)
	})

	// The test's own transaction holds the invoice row: both doors' first lock step wants that row,
	// so both park here whatever the id sort and whatever the code under test does.
	held, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback(ctx) }()
	if _, err := held.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, invID); err != nil {
		t.Fatalf("hold the invoice row: %v", err)
	}

	applyDone := make(chan error, 1)
	walkDone := make(chan error, 1)

	// The credit apply: the target invoice and the memo.
	go func() {
		_, e := applySvc.Apply(ctx, billing.ApplyCreditInput{
			CustomerID: r.custID, CreditKind: billing.CreditKindMemo, CreditID: memoID, InvoiceID: invID,
			AmountCents: 1000, Reason: "the memo applied to the invoice", IdempotencyKey: tag + "-k-apply",
			OrgID: applyOrg,
		}, true)
		applyDone <- e
	}()
	cmWaitParked(t, r, "the credit apply parking on the invoice row", 1, applyDone, nil)

	// The received payment walk: the same memo and the same invoice, the memo's id first in the
	// walk's own ascending sort.
	go func() {
		_, e := r.repo.ApplyReceived(ctx, payments.ApplyRequest{
			CustomerID: r.custID, Method: "CHEQUE", Reference: "chq " + tag,
			IdempotencyKey: tag + "-k-pay", ReceivedBy: r.userID,
			InvoiceIDs: []string{memoID, invID}, AmountCents: 5000,
		})
		walkDone <- e
	}()
	// The walk parks on the invoice row too, UNLESS it refused the memo at the walk, which is the
	// fix's own answer: a refusal, never a deadlock.
	earlyWalk := cmWaitParked(t, r, "the received payment walk parking on the invoice row", 2, applyDone, walkDone)

	if err := held.Rollback(ctx); err != nil {
		t.Fatalf("release the invoice row: %v", err)
	}

	applyErr := <-applyDone
	walkErr := earlyWalk
	if walkErr == nil {
		walkErr = <-walkDone
	}
	for who, err := range map[string]error{"the credit apply": applyErr, "the received payment walk": walkErr} {
		if loIsDeadlock(err) || (err != nil && strings.Contains(err.Error(), "40P01")) {
			t.Fatalf("%s was killed as a deadlock victim: %v", who, err)
		}
	}

	// The walk's answer is the refusal at the walk: the memo is not a document a payment can land
	// on, so the walk names it as not found. It never locked the memo row, which is what the credit
	// apply then proves: it took the invoice and the memo and completed.
	var noInv payments.NoSuchInvoiceError
	if !errors.As(walkErr, &noInv) {
		t.Fatalf("the received payment walk answered %v, want the walk's not found for the memo", walkErr)
	}
	if noInv.InvoiceID != memoID {
		t.Fatalf("the received payment walk answered not found for %s, want the memo %s", noInv.InvoiceID, memoID)
	}
	if applyErr != nil {
		t.Fatalf("the credit apply answered %v, want it to complete", applyErr)
	}

	// The credit apply landed once: the invoice at 9000 and the memo at -9000.
	for name, want := range map[string]struct {
		invoice string
		balance int64
	}{
		"the invoice": {invID, 9000},
		"the memo":    {memoID, -9000},
	} {
		var balance int64
		if err := r.obs.QueryRow(ctx, `SELECT balance_cents FROM invoices WHERE id = $1::uuid`, want.invoice).Scan(&balance); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if balance != want.balance {
			t.Fatalf("%s balance after the pair = %d, want %d", name, balance, want.balance)
		}
	}
}

// cmWaitParked blocks until at least `want` sessions of this database wait on a lock. The door in `door`
// answering is the fix's own answer (the walk refuses the memo at the walk), so it ends the wait there and
// hands that answer back; a door in `mustPark` answering is a staging failure, because that door's first
// lock step wants the row the test holds.
func cmWaitParked(t *testing.T, r *loRig, what string, want int, mustPark, door chan error) error {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var waiting int
		if err := r.obs.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return nil
		}
		select {
		case err := <-mustPark:
			t.Fatalf("%s: the door answered without parking: %v", what, err)
		default:
		}
		if door != nil {
			select {
			case err := <-door:
				// The fix's answer: the walk refused the memo at the walk, before it took a row.
				if err == nil {
					t.Fatalf("%s: the walk completed instead of refusing the memo at the walk", what)
				}
				return err
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("barrier never reached: %s (want %d sessions waiting on a lock)", what, want)
		}
		time.Sleep(15 * time.Millisecond)
	}
}
