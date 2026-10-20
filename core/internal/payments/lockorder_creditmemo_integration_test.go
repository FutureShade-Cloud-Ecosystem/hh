//go:build integration

// lockorder_creditmemo_integration_test.go: a credit memo takes the parent rows its invoice insert points at
// BEFORE the invoices counter (PR 556 review, round 3 sweep).
//
// The memo's invoice insert draws the invoices counter (the sync seq trigger, 0422) and only then, at its
// foreign key checks, takes FOR KEY SHARE on the order it names. A writer that holds that order row FOR UPDATE
// (the order cancel's settlement, which then draws the invoices counter to clear a deposit document) and the
// memo cross: the memo holds the counter and wants the order, the cancel holds the order and wants the
// counter. LOCK-ORDER.md's step 1 (the order row first) and step 3 (an existing invoice row) are what every
// other invoice writer already does; the memo now takes them too, FOR NO KEY UPDATE, ahead of its insert.
//
// The memo is the parked session: a trigger parks the sessions of a pool whose application name is sf1-park
// right after they touch the invoices counter, on an advisory lock the test holds. The holder (the order row
// FOR UPDATE, then the counter) starts behind it; the test releases only when the holder is seen waiting.
package payments_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
)

func TestIntegration_LockOrder_CreditMemoBesideAnOrderHolderDrawingTheInvoicesCounter(t *testing.T) {
	r := newLORig(t, 4)
	ctx := context.Background()
	orderID := r.order()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sf1_park_inv() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.scope = 'invoices' AND current_setting('application_name') = 'sf1-park' THEN
		     PERFORM pg_advisory_xact_lock(4242004);
		   END IF;
		   RETURN NULL;
		 END $$`,
		`CREATE TRIGGER sf1_park_inv AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION sf1_park_inv()`,
	} {
		if _, err := r.obs.Exec(ctx, q); err != nil {
			t.Fatalf("park trigger: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = r.obs.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_inv ON scope_sequences`)
		_, _ = r.obs.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_inv()`)
		_, _ = r.obs.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, r.custID)
		_, _ = r.obs.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, r.custID)
	})

	cfg := r.pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["application_name"] = "sf1-park"
	cfg.MaxConns = 2
	parkPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("park pool: %v", err)
	}
	defer parkPool.Close()

	hold, err := r.obs.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer hold.Release()
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock(4242004)`); err != nil {
		t.Fatalf("hold: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock(4242004)`)
		}
	}
	defer release()

	errMemo := make(chan error, 1)
	go func() {
		_, err := billing.NewInvoiceWriter(parkPool).IssueCreditMemo(ctx, billing.CreditMemoInput{
			CustomerID: r.custID, BranchID: r.branchID, OrderID: orderID, AmountCents: 500,
			Memo: "lock order leg", IdempotencyKey: fmt.Sprintf("lo-cm-%d", time.Now().UnixNano()),
		})
		errMemo <- err
	}()
	r.waitSessions("the credit memo parked on its invoices counter touch", 1, `wait_event_type = 'Lock' AND wait_event = 'advisory'`)

	errHolder := make(chan error, 1)
	go func() {
		// The order cancel's shape: the order row FOR UPDATE, then the invoices counter (a cleared document).
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			errHolder <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT 1 FROM orders WHERE id = $1::uuid FOR UPDATE`, orderID); err != nil {
			errHolder <- fmt.Errorf("holder: lock order: %w", err)
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('invoices')
		                           ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1`); err != nil {
			errHolder <- fmt.Errorf("holder: invoices counter: %w", err)
			return
		}
		errHolder <- tx.Commit(ctx)
	}()
	r.waitSessions("the order holder waiting behind the parked memo", 1, `wait_event_type = 'Lock' AND wait_event <> 'advisory'`)

	release()
	for who, ch := range map[string]chan error{"credit memo": errMemo, "order holder": errHolder} {
		err := loAnswer(t, who, ch)
		if loIsDeadlock(err) || (err != nil && strings.Contains(err.Error(), "40P01")) {
			t.Fatalf("%s was killed as a deadlock victim: %v", who, err)
		}
		if err != nil {
			t.Fatalf("%s failed: %v", who, err)
		}
	}
}
