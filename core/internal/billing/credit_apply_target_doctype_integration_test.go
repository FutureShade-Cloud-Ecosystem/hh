//go:build integration

package billing

// ROUND 9, M3-P2-5's FIRST DOOR: the apply's own TARGET row (M3's leg G).
//
// credit_apply.go locks the named invoice with no doc_type filter, so a hand
// built request can name a CREDIT_MEMO as the invoice a credit lands on. The
// target's status check passes (a memo is FINALIZED) and the ceiling refusal
// runs only AFTER the credit's own row is locked, so the door reaches the
// credit's lock with a memo in hand: its lock order (the target row first, the
// credit's row second) is live against a memo, and a second door holding that
// memo while it wants the target closes the cycle. The door's answer to the
// request is a raw SQLSTATE 40P01 where a refusal in words belongs.
//
// The rule this leg pins: the apply's target must be a doc_type INVOICE
// document, and a memo named as the target is refused in words BEFORE any row
// is locked, so the door never reaches the credit's lock with a memo in hand.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIntegration_FW1_12b_TheApplyRefusesACreditMemoNamedAsItsTarget pins the
// apply's target door. The staging is M3's leg G, deterministic: one
// transaction of our own holds the credit's row (the lock the apply wants
// second), a real apply names the other memo as its target and parks on the
// held credit row, and then our transaction asks for the row the apply is
// holding as its target. That is a lock cycle, and on the unfixed door
// Postgres answers SQLSTATE 40P01 somewhere in it. After the fix the apply
// refuses the memo target in words while our credit row lock is still held,
// which is itself the proof it never reached the credit's row: it answered
// before the row it would have needed was free.
func TestIntegration_FW1_12b_TheApplyRefusesACreditMemoNamedAsItsTarget(t *testing.T) {
	pool := intPool(t)
	applySvc := NewCreditApplyService(NewRepository(pool))
	ctx := context.Background()
	n := ns(t)

	custID := seedCustomer(t, pool, n+"-CUST")

	// Two credit memos, both the customer's, both at one branch (M3's leg G seeding).
	seedMemo := func(tag string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE, CURRENT_DATE, -5000, 0, -5000, -5000, now(), 'CREDIT_MEMO')
			RETURNING id::text`, n+tag, custID, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("seed the memo %s: %v", tag, err)
		}
		return id
	}
	memoA := seedMemo("-MEMOA")
	memoB := seedMemo("-MEMOB")

	// The credit's row, held by a transaction of our own: the lock the apply
	// wants second, after it has taken the memo it was handed as its target.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, memoA); err != nil {
		t.Fatalf("hold the credit's row: %v", err)
	}

	// The hand built request: the other memo named as the TARGET, the held one
	// as its credit. Nothing here can move money whichever way the door
	// answers: a memo target can take at most 0 cents, so the door's own
	// ceiling refusal is the backstop.
	applyErr := make(chan error, 1)
	go func() {
		_, err := applySvc.Apply(ctx, ApplyCreditInput{
			CustomerID: custID, CreditKind: CreditKindMemo, CreditID: memoA, InvoiceID: memoB,
			AmountCents: 100, Reason: "round 9: the crossing", IdempotencyKey: n + "-key-1",
		}, true)
		applyErr <- err
	}()

	// Wait until the apply either answers (the fixed door refuses at once,
	// before any lock beyond the empty target read) or parks on a lock (the
	// unfixed door has taken the memo target and wants the credit's row we
	// hold), so the leg is deterministic and not a sleep race. The answer, when
	// it has landed, is kept here: a second receive on the channel would wait
	// for an answer that has already been taken.
	applyAnswered := false
	var applyAnswer error
	deadline := time.Now().Add(15 * time.Second)
	for !applyAnswered {
		select {
		case applyAnswer = <-applyErr:
			applyAnswered = true
		default:
		}
		if applyAnswered {
			break
		}
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("read the lock waiters: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the apply neither answered nor reached a lock wait")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Close the cycle only when the apply is parked: ask for the row it holds
	// as its target. On the fixed door this never runs with the apply holding
	// anything (it answered instead), and the ask cannot deadlock.
	ourErr := error(nil)
	if !applyAnswered {
		_, ourErr = tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, memoB)
	}

	if !applyAnswered {
		select {
		case applyAnswer = <-applyErr:
		case <-time.After(30 * time.Second):
			st := pool.Stat()
			t.Logf("the apply never answered: pool conns total %d acquired %d idle %d",
				st.TotalConns(), st.AcquiredConns(), st.IdleConns())
			rows, _ := pool.Query(ctx, `
				SELECT pid, COALESCE(wait_event_type, ''), COALESCE(wait_event, ''), state, left(query, 140)
				  FROM pg_stat_activity WHERE datname = current_database()`)
			for rows.Next() {
				var pid int
				var waitType, waitEvent, state, query string
				if rows.Scan(&pid, &waitType, &waitEvent, &state, &query) == nil {
					t.Logf("the apply never answered: pid %d %s/%s %s: %s", pid, waitType, waitEvent, state, query)
				}
			}
			rows.Close()
			t.Fatal("the apply never answered")
		}
	}

	if ourErr != nil {
		t.Fatalf("the two doors' lock orders still cross: asking for the apply's target row answered %v, want no cycle at all", ourErr)
	}
	var noSuch NoSuchInvoiceError
	if !errors.As(applyAnswer, &noSuch) {
		t.Fatalf("the apply answered %v, want the no such invoice refusal in words for a memo named as its target", applyAnswer)
	}
}
