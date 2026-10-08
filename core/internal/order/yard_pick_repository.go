package order

// yard_pick_repository.go, the transaction behind ApplyYardPicks (see yard_pick.go for the rules).

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// yardLineChange is a line that moved, kept for the audit rows written once the header is known.
type yardLineChange struct{ before, after LineSnapshot }

// ApplyYardPicksTx applies one recorded pick in ONE transaction under the order row lock: the lines,
// their shortfall records, the order header money and the audit rows commit together or not at all.
// The precheck runs after the row lock and the status window check, joining
// the transaction through the context (the cancel predicate's own shape), so
// the pay before release hold reads under the lock the write holds (item 9
// Opus P3-2).
func (r *Repository) ApplyYardPicksTx(ctx context.Context, in YardPickInput, precheck func(context.Context) error, audit yardPickAudit) (YardPickOutcome, error) {
	if r.pool == nil {
		return YardPickOutcome{}, errNoDB
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return YardPickOutcome{}, fmt.Errorf("yard pick: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status                        string
		subtotal, tax, total, freight int64
		out                           YardPickOutcome
	)
	f := &out.Order
	err = tx.QueryRow(ctx, `
SELECT status, subtotal_cents, tax_cents, total_cents, COALESCE(quoted_freight_cents, 0),
       order_no::text, customer_id::text, branch_id::text, COALESCE(salesperson_id::text, '')
  FROM orders WHERE id = $1 FOR UPDATE`, in.OrderID).
		Scan(&status, &subtotal, &tax, &total, &freight, &f.OrderNo, &f.CustomerID, &f.BranchID, &f.SalespersonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return YardPickOutcome{}, ErrNotFound
		}
		return YardPickOutcome{}, fmt.Errorf("yard pick: lock order: %w", err)
	}
	if !slices.Contains(yardPickStatuses, status) {
		return YardPickOutcome{}, &NotEditableError{Field: FieldLineQty, Status: status}
	}
	if precheck != nil {
		if err := precheck(withPrecheckTx(ctx, tx)); err != nil {
			return YardPickOutcome{}, err
		}
	}
	f.TotalBeforeCents, f.TotalAfterCents = total, total

	// The header's ordered basis: refreshed while the order is whole (no line on backorder), kept
	// while any line is short, so the tax is always pro-rated from the originals.
	var onBackorder int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM order_lines WHERE order_id = $1 AND backorder_base > 0`, in.OrderID).Scan(&onBackorder); err != nil {
		return YardPickOutcome{}, fmt.Errorf("yard pick: backorder count: %w", err)
	}
	if onBackorder == 0 {
		if _, err := tx.Exec(ctx, `
INSERT INTO order_shortfall_basis (order_id, ordered_subtotal_cents, ordered_tax_cents)
VALUES ($1, $2, $3)
ON CONFLICT (order_id) DO UPDATE SET ordered_subtotal_cents = EXCLUDED.ordered_subtotal_cents,
                                     ordered_tax_cents = EXCLUDED.ordered_tax_cents`,
			in.OrderID, subtotal, tax); err != nil {
			return YardPickOutcome{}, fmt.Errorf("yard pick: basis: %w", err)
		}
		if err := snapshotLineTaxTx(ctx, tx, in.OrderID, true); err != nil {
			return YardPickOutcome{}, err
		}
	}

	var changes []yardLineChange
	for _, l := range sortYardLines(in.Lines) {
		eff, ch, err := applyYardLine(ctx, tx, in, l)
		if err != nil {
			return YardPickOutcome{}, err
		}
		out.Lines = append(out.Lines, eff)
		if ch != nil {
			changes = append(changes, *ch)
		}
	}

	if len(changes) > 0 {
		var basisSub, basisTax, newSub int64
		if err := tx.QueryRow(ctx, `
SELECT b.ordered_subtotal_cents, b.ordered_tax_cents,
       COALESCE((SELECT SUM(line_total_cents) FROM order_lines WHERE order_id = $1), 0)
  FROM order_shortfall_basis b WHERE b.order_id = $1`, in.OrderID).Scan(&basisSub, &basisTax, &newSub); err != nil {
			return YardPickOutcome{}, fmt.Errorf("yard pick: basis read: %w", err)
		}
		newTax := proratedTax(basisSub, basisTax, newSub)
		newTotal := newSub + newTax + freight
		if _, err := tx.Exec(ctx, `
UPDATE orders SET subtotal_cents = $2, tax_cents = $3, total_cents = $4, updated_at = now() WHERE id = $1`,
			in.OrderID, newSub, newTax, newTotal); err != nil {
			return YardPickOutcome{}, fmt.Errorf("yard pick: order money: %w", err)
		}
		if err := reallocLineTaxTx(ctx, tx, in.OrderID, basisTax, newTax); err != nil {
			return YardPickOutcome{}, err
		}
		totals := OrderTotals{SubtotalCents: newSub, TotalCents: newTotal}
		for _, ch := range changes {
			if audit != nil {
				if err := audit(ctx, tx, ch.before, ch.after, totals, tax, newTax); err != nil {
					return YardPickOutcome{}, err
				}
			}
		}
		f.TotalAfterCents = newTotal
	}
	if err := tx.Commit(ctx); err != nil {
		return YardPickOutcome{}, fmt.Errorf("yard pick: commit: %w", err)
	}
	return out, nil
}

// applyYardLine moves one order line to the picked quantity under the order lock. A nil change
// means the line already held exactly this record.
func applyYardLine(ctx context.Context, tx pgx.Tx, in YardPickInput, l YardPickLineInput) (YardLineOutcome, *yardLineChange, error) {
	var (
		id, desc, uom, productID                string
		qtyE, qtyB, backE, backB, fulfilledText string
		lineTotal, unitPrice                    int64
	)
	err := tx.QueryRow(ctx, `
SELECT id::text, description, uom_entered::text, qty_entered::text, qty_base::text,
       backorder_entered::text, backorder_base::text, line_total_cents, fulfilled_base::text,
       COALESCE(product_id::text, ''), unit_price_cents
  FROM order_lines WHERE order_id = $1 AND line_no = $2 FOR UPDATE`, in.OrderID, l.LineNo).
		Scan(&id, &desc, &uom, &qtyE, &qtyB, &backE, &backB, &lineTotal, &fulfilledText, &productID, &unitPrice)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return YardLineOutcome{}, nil, ErrLineNotFound
		}
		return YardLineOutcome{}, nil, fmt.Errorf("yard pick: lock line %d: %w", l.LineNo, err)
	}
	curE, _ := ratOf(qtyE)
	curB, _ := ratOf(qtyB)
	boE, _ := ratOf(backE)
	boB, _ := ratOf(backB)
	fulfilled, _ := ratOf(fulfilledText)
	orderedE := new(big.Rat).Add(curE, boE)
	orderedB := new(big.Rat).Add(curB, boB)

	picked, ok := ratOf(l.QtyPicked)
	if !ok || picked.Sign() < 0 {
		return YardLineOutcome{}, nil, httpx.BadRequestf("line %d: the picked quantity must be a number, zero or more", l.LineNo)
	}
	if orderedE.Sign() <= 0 {
		return YardLineOutcome{}, nil, httpx.BadRequestf("line %d has no ordered quantity to pick against", l.LineNo)
	}
	if picked.Cmp(orderedE) > 0 {
		return YardLineOutcome{}, nil, fmt.Errorf("line %d: %w", l.LineNo, ErrAboveOrdered)
	}

	// The ordered money: what the line is worth whole. A line that is whole right now is its own
	// answer (an edit may have repriced it since); a short line's is frozen on its shortfall row,
	// which also holds the reason and note already recorded and the revision of the last change.
	orderedTotal := lineTotal
	var storedReason, storedNote string
	storedRev := 0
	if boE.Sign() > 0 {
		err := tx.QueryRow(ctx, `SELECT ordered_total_cents, reason, note, revision FROM order_line_shortfalls WHERE order_line_id = $1`, id).
			Scan(&orderedTotal, &storedReason, &storedNote, &storedRev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return YardLineOutcome{}, nil, fmt.Errorf("yard pick: ordered total of line %d: %w", l.LineNo, err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			orderedTotal = lineTotal
		}
	}

	// The reason rule, decided HERE against the order line's own ordered quantity and never the
	// ticket's (the ticket is rebuilt from the line after a short, so it understates the ordered
	// quantity): a line that ends short needs a reason when it changes, a re-save that sends none
	// keeps what is stored, and a full pick carries none.
	short := picked.Cmp(orderedE) < 0
	reason, note := strings.TrimSpace(l.Reason), strings.TrimSpace(l.Note)
	switch {
	case !short:
		reason, note = "", ""
	case reason == "" && picked.Cmp(curE) == 0:
		reason, note = storedReason, storedNote
	case reason == "":
		return YardLineOutcome{}, nil, httpx.BadRequestf("line %d is short, give the reason", l.LineNo)
	case reason == yardReasonOther && note == "":
		return YardLineOutcome{}, nil, httpx.BadRequestf("line %d: say what happened in the note when the reason is OTHER", l.LineNo)
	}

	newB := orderedB
	if picked.Cmp(orderedE) != 0 {
		newB = scaleBase(orderedB, picked, orderedE)
	}
	eff := YardLineOutcome{
		LineNo: l.LineNo, Description: desc, UOM: uom,
		QtyOrdered: rat4(orderedE), QtyPicked: rat4(picked),
		QtyBackordered: rat4(new(big.Rat).Sub(orderedE, picked)),
		Reason:         reason, Note: note, Revision: storedRev,
	}
	if picked.Cmp(curE) == 0 {
		// Already held. A different reason or note on a still short line is a change of its own: it
		// takes the next revision so the rep is told, and moves no money. The same record is not.
		if short && (reason != storedReason || note != storedNote) {
			if err := tx.QueryRow(ctx, `
UPDATE order_line_shortfalls SET reason = $2, note = $3, recorded_by = NULLIF($4, '')::uuid, updated_at = now(),
       revision = revision + 1 WHERE order_line_id = $1
RETURNING revision`, id, reason, note, in.ActorID).Scan(&eff.Revision); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return YardLineOutcome{}, nil, fmt.Errorf("yard pick: shortfall note: %w", err)
			}
			eff.Changed = true
		}
		return eff, nil, nil
	}
	if newB.Cmp(fulfilled) < 0 {
		return YardLineOutcome{}, nil, fmt.Errorf("%w: %s already fulfilled on line %d", ErrLineInUse, rat4(fulfilled), l.LineNo)
	}

	if err := adjustPlanTx(ctx, tx, in.OrderID, id, l.LineNo, new(big.Rat).Sub(newB, curB)); err != nil {
		return YardLineOutcome{}, nil, err
	}
	newTotal := proratedTotal(orderedTotal, picked, orderedE)
	backEntered := new(big.Rat).Sub(orderedE, picked)
	backBase := new(big.Rat).Sub(orderedB, newB)
	if _, err := tx.Exec(ctx, `
UPDATE order_lines SET qty_entered = $3::decimal, qty_base = $4::decimal, line_total_cents = $5,
       backorder_entered = $6::decimal, backorder_base = $7::decimal, updated_at = now()
 WHERE id = $1 AND order_id = $2`,
		id, in.OrderID, rat4(picked), rat4(newB), newTotal, rat4(backEntered), rat4(backBase)); err != nil {
		return YardLineOutcome{}, nil, fmt.Errorf("yard pick: line %d: %w", l.LineNo, err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO order_line_shortfalls (order_line_id, order_id, line_no, ordered_entered, ordered_base,
                                   ordered_total_cents, reason, note, recorded_by)
VALUES ($1, $2, $3, $4::decimal, $5::decimal, $6, $7, $8, NULLIF($9, '')::uuid)
ON CONFLICT (order_line_id) DO UPDATE SET
  ordered_entered = EXCLUDED.ordered_entered, ordered_base = EXCLUDED.ordered_base,
  ordered_total_cents = EXCLUDED.ordered_total_cents, reason = EXCLUDED.reason, note = EXCLUDED.note,
  recorded_by = EXCLUDED.recorded_by, updated_at = now(),
  revision = order_line_shortfalls.revision + 1
RETURNING revision`,
		id, in.OrderID, l.LineNo, rat4(orderedE), rat4(orderedB), orderedTotal, reason, note, in.ActorID).Scan(&eff.Revision); err != nil {
		return YardLineOutcome{}, nil, fmt.Errorf("yard pick: shortfall record: %w", err)
	}
	eff.Changed = true
	return eff, &yardLineChange{
		before: LineSnapshot{LineNo: l.LineNo, ProductID: productID, QtyEntered: qtyE, QtyBase: qtyB, UnitPriceCents: unitPrice, LineTotalCents: lineTotal},
		after:  LineSnapshot{LineNo: l.LineNo, ProductID: productID, QtyEntered: rat4(picked), QtyBase: rat4(newB), UnitPriceCents: unitPrice, LineTotalCents: newTotal},
	}, nil
}
