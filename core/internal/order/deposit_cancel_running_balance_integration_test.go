//go:build integration

package order

// deposit_cancel_running_balance_integration_test.go, the settlement's effect
// on the account's RUNNING BALANCE: the latest customer_transactions
// balance_after every credit gate reads (the POS sale gate, the quote
// conversion gate, the account picker), the statement's own closing line, and
// the aging figure, against the world the real paths leave behind.
//
// The earlier deposit legs seeded the deposit document with a bare INSERT and
// no ledger rows, so no leg could see what the settlement does to the running
// balance: the account's ledger is what the gates read, not the document.
// These legs plant the INVOICE row a real finalize writes, pay through the
// payments module's own received apply route (the 304 shape, one payment row,
// its allocation and its PAYMENT row), and cancel through the real handler,
// then read the three surfaces every customer facing screen agrees on.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// ledgerRow is one planted or written ledger row a leg compares verbatim.
type ledgerRow struct {
	TxnType    string
	SourceType string
	Amount     int64
}

// seedDepositOrderWithLedger plants the deposit world the real paths leave
// behind: a CONFIRMED order, its 4000 deposit document FINALIZED, the INVOICE
// ledger row a real finalize writes (occurred_at clock_timestamp, the ring's
// own insert shape), and, when payCents is above zero, a real received
// payment applied by the payments module's apply route, which writes the
// payment row, its allocation and its PAYMENT row and moves the document to
// PARTIALLY_PAID. The caller owns the figures this leaves: paid payCents,
// closed 4000 less payCents.
func (w *depositCancelWorld) seedDepositOrderWithLedger(t *testing.T, tag string, payCents int64) (cust, ord, dep, payID string) {
	t.Helper()
	ctx := context.Background()
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'Deposit cancel balance', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("DCB-%s", tag)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 4000, 4000) RETURNING id::text`,
		fmt.Sprintf("#DCB-%s", tag), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at)
		 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', now())
		 RETURNING id::text`,
		fmt.Sprintf("INV-DCB-%s", tag), cust, ord, trentonBranch).Scan(&dep); err != nil {
		t.Fatalf("seed deposit document: %v", err)
	}
	// The INVOICE row the ring's finalize writes: the charge that puts the
	// deposit on the account's ledger, the row every later balance_after
	// chains from. Without it the gates read an account that was never billed.
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
		                                   source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'INVOICE', 4000, 4000, 'invoice', $2::uuid, $3::uuid, 'Supplier deposit', clock_timestamp())`,
		cust, dep, trentonBranch); err != nil {
		t.Fatalf("plant the finalize's ledger row: %v", err)
	}
	if payCents > 0 {
		ans, err := payments.NewRepository(w.pool).ApplyReceived(ctx, payments.ApplyRequest{
			CustomerID: cust, Method: "CHEQUE", Reference: "DCB-" + tag,
			IdempotencyKey: "dcb-" + tag, InvoiceIDs: []string{dep}, AmountCents: payCents,
		})
		if err != nil {
			t.Fatalf("apply the real received payment: %v", err)
		}
		if ans.AppliedCents != payCents {
			t.Fatalf("the apply applied %d, want %d", ans.AppliedCents, payCents)
		}
		payID = ans.PaymentID
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id::uuid = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM payments WHERE id::uuid = $1`, payID)
		_, _ = w.pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id::uuid = $1`, cust)
		_, _ = w.pool.Exec(c, `DELETE FROM audit_log WHERE entity_id::text = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM events_outbox WHERE (payload->>'invoice_id')::uuid = $1::uuid
		 OR (payload->>'payment_id')::uuid = $2::uuid`, dep, payID)
		_, _ = w.pool.Exec(c, `DELETE FROM invoices WHERE id::uuid = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM orders WHERE id::uuid = $1`, ord)
		_, _ = w.pool.Exec(c, `DELETE FROM customers WHERE id::uuid = $1`, cust)
	})
	return cust, ord, dep, payID
}

// ledgerRows reads the account's ledger in order, the kinds and amounts the
// statement and the activity read show.
func ledgerRows(t *testing.T, w *depositCancelWorld, cust string) []ledgerRow {
	t.Helper()
	rows, err := w.pool.Query(context.Background(),
		`SELECT txn_type, source_type, amount_cents FROM customer_transactions
		  WHERE customer_id = $1::uuid ORDER BY occurred_at, created_at`, cust)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ledgerRow
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.TxnType, &r.SourceType, &r.Amount); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// THE RUNNING BALANCE AND THE STATEMENT FOLLOW THE SETTLEMENT: whatever the
// disposition, the account's ledger must end at the figure the aging carries
// for it, or the credit gates refuse sales against money that is not owed.
// Four cases on one 4000 deposit: unpaid and part paid (2500 paid, 1500
// still owed), credit back and kept. The part paid credit back ends at the
// paid part's credit; every other case ends at zero.
func TestIntegration_DepositCancel_RunningBalanceFollowsTheSettlement(t *testing.T) {
	cases := []struct {
		name string
		pay  int64
		keep bool
		want int64
		rows []ledgerRow
	}{
		{"unpaid credit back", 0, false, 0, []ledgerRow{
			{"INVOICE", "invoice", 4000},
			{"ADJUSTMENT", "invoice", -4000},
		}},
		{"part paid credit back", 2500, false, -2500, []ledgerRow{
			{"INVOICE", "invoice", 4000},
			{"PAYMENT", "payment", -2500},
			{"ADJUSTMENT", "invoice", -1500},
			{"ADJUSTMENT", "deposit_cancel", -2500},
		}},
		{"unpaid kept", 0, true, 0, []ledgerRow{
			{"INVOICE", "invoice", 4000},
			{"ADJUSTMENT", "invoice", -4000},
		}},
		{"part paid kept", 2500, true, 0, []ledgerRow{
			{"INVOICE", "invoice", 4000},
			{"PAYMENT", "payment", -2500},
			{"ADJUSTMENT", "invoice", -1500},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newDepositCancelWorld(t)
			ctx := context.Background()
			cust, ord, _, _ := w.seedDepositOrderWithLedger(t, fmt.Sprintf("%d", time.Now().UnixNano()), tc.pay)

			body := `{"reason":"customer walked away"}`
			if tc.keep {
				body = `{"reason":"the supplier already ordered it","keep_deposit":true}`
			}
			if rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, body); rr.Code != http.StatusOK {
				t.Fatalf("the cancel: code=%d body=%s", rr.Code, rr.Body.String())
			}

			// The ledger itself, verbatim: the closed part must be a row.
			got := ledgerRows(t, w, cust)
			if len(got) != len(tc.rows) {
				t.Fatalf("the ledger holds %d rows %v, want %d %v", len(got), got, len(tc.rows), tc.rows)
			}
			for i, r := range tc.rows {
				if got[i] != r {
					t.Fatalf("ledger row %d = %+v, want %+v", i, got[i], r)
				}
			}

			// The running balance every credit gate reads: the latest
			// balance_after on the account.
			var latest int64
			if err := w.pool.QueryRow(ctx,
				`SELECT balance_after FROM customer_transactions
				  WHERE customer_id = $1::uuid ORDER BY occurred_at DESC, created_at DESC LIMIT 1`, cust).
				Scan(&latest); err != nil {
				t.Fatal(err)
			}
			if latest != tc.want {
				t.Errorf("the latest balance_after = %d, want %d (what the credit gates read)", latest, tc.want)
			}

			// The statement's own closing line: the last running figure the
			// customer is shown.
			lines, err := billing.NewRepository(w.pool).StatementLines(ctx, cust, time.Now(), billing.AllBranches)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) == 0 {
				t.Fatal("the statement has no lines")
			}
			if last := lines[len(lines)-1].BalanceRun; last != tc.want {
				t.Errorf("the statement's closing line = %d, want %d", last, tc.want)
			}

			// The aging figure (the v_ar_aging arms: open invoices plus the
			// non invoice movements): the tie out the books answer to.
			var aging int64
			if err := w.pool.QueryRow(ctx,
				`SELECT COALESCE(SUM(amount_cents),0) FROM v_ar_aging WHERE customer_id = $1::uuid`, cust).
				Scan(&aging); err != nil {
				t.Fatal(err)
			}
			if aging != tc.want {
				t.Errorf("the aging figure = %d, want %d", aging, tc.want)
			}
		})
	}
}
