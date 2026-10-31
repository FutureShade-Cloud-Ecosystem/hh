package payments

// discount_giveback.go, TM-2: the one place an undone application gives its early payment discount
// back on the subledger. Every door that undoes an application (the reversal, the void and the NSF
// bounce in the till module) restores the invoice's balance by the money AND the discount, and
// writes the discount's own contra row here, so the next door cannot forget the discount: the
// invoice side, the subledger and (through the event's discount_cents) the GL lines agree.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DiscountGiveBack is one invoice's discount coming back.
type DiscountGiveBack struct {
	CustomerID   string
	BranchID     string
	InvoiceID    string
	Cents        int64
	BalanceAfter int64
	Memo         string
	// At dates the row; zero takes the database's clock like the other undo rows.
	At time.Time
}

// GiveBackDiscountTx writes the discount's contra subledger row, an ADJUSTMENT against the
// invoice (source_type 'invoice') like the discount it undoes, so the account's statement and the
// aging's past date reconstruction read the give back exactly where they read the taking. It is a
// no-op for a zero discount or a receipt with no customer.
func GiveBackDiscountTx(ctx context.Context, tx pgx.Tx, g DiscountGiveBack) error {
	if g.Cents <= 0 || g.CustomerID == "" {
		return nil
	}
	var at any
	if !g.At.IsZero() {
		at = g.At
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'ADJUSTMENT', $2, $3, 'invoice', $4::uuid, $5::uuid, $6, COALESCE($7::timestamptz, now()))`,
		g.CustomerID, g.Cents, g.BalanceAfter, g.InvoiceID, nullIfEmpty(g.BranchID), g.Memo, at); err != nil {
		return fmt.Errorf("payments: insert the discount give back subledger txn on invoice %s: %w", g.InvoiceID, err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
