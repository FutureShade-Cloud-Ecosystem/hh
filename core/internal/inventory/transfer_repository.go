package inventory

// transfer_repository.go, the pgx half of DIB-71 (stock_transfers + stock_transfer_lines, 0116).
//
// TransferForUpdate takes FOR UPDATE on the header and the lines together. Ship and receive are both
// read-decide-write over the same rows, and two clerks hitting "Receive" on the same truck within a
// second of each other is not hypothetical, it is what happens when a page is left open on two
// screens in a yard office. The lock makes the second one wait and then find the transfer already
// RECEIVED, which is a clean 409, instead of writing a second inbound move for stock that arrived once.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// transferHeader is the ship/receive-relevant projection of a transfer.
type transferHeader struct {
	ID           string
	TransferNo   string
	FromBranchID string
	ToBranchID   string
	Status       string
}

// transferLineRow is the ship/receive-relevant projection of a transfer line.
type transferLineRow struct {
	ID              string
	LineNo          int
	ProductID       string
	UOM             string
	QtyBase         float64
	QtyShippedBase  float64
	QtyReceivedBase float64
	UnitCostCents   int64
}

// InsertTransfer writes the DRAFT header (number minted by next_transfer_no off the source branch)
// and its lines. The line UOM is resolved from the product's base_uom in SQL, matching InsertMove -
// the caller supplies a product and a base quantity, never a unit.
func (r *Repository) InsertTransfer(ctx context.Context, tx pgx.Tx, in CreateTransferInput) (Transfer, error) {
	var t Transfer
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO stock_transfers (transfer_no, from_branch_id, to_branch_id, notes, created_by)
		VALUES (next_transfer_no($1), $1, $2, $3, $4)
		RETURNING id::text, transfer_no::text, from_branch_id::text, to_branch_id::text, status`,
		in.FromBranchID, in.ToBranchID, in.Notes, nullStr(in.CreatedBy)).
		Scan(&t.ID, &t.TransferNo, &t.FromBranchID, &t.ToBranchID, &t.Status)
	if err != nil {
		return Transfer{}, err
	}
	for i, l := range in.Lines {
		if _, err := r.q(tx).Exec(ctx, `
			INSERT INTO stock_transfer_lines (transfer_id, line_no, product_id, uom, qty_base)
			VALUES ($1, $2, $3, (SELECT base_uom FROM products WHERE id = $3), $4)`,
			t.ID, i+1, l.ProductID, l.QtyBase); err != nil {
			return Transfer{}, err
		}
	}
	return t, nil
}

// TransferForUpdate locks the header and lines for a ship/receive decision.
func (r *Repository) TransferForUpdate(ctx context.Context, tx pgx.Tx, transferID string) (transferHeader, []transferLineRow, error) {
	var h transferHeader
	err := r.q(tx).QueryRow(ctx, `
		SELECT id::text, transfer_no::text, from_branch_id::text, to_branch_id::text, status
		  FROM stock_transfers WHERE id = $1 FOR UPDATE`, transferID).
		Scan(&h.ID, &h.TransferNo, &h.FromBranchID, &h.ToBranchID, &h.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return transferHeader{}, nil, errNotFound
	}
	if err != nil {
		return transferHeader{}, nil, err
	}
	rows, err := r.q(tx).Query(ctx, `
		SELECT id::text, line_no, product_id::text, uom::text, qty_base,
		       qty_shipped_base, qty_received_base, unit_cost_cents
		  FROM stock_transfer_lines WHERE transfer_id = $1 ORDER BY line_no FOR UPDATE`, transferID)
	if err != nil {
		return transferHeader{}, nil, err
	}
	defer rows.Close()
	var out []transferLineRow
	for rows.Next() {
		var l transferLineRow
		if err := rows.Scan(&l.ID, &l.LineNo, &l.ProductID, &l.UOM, &l.QtyBase,
			&l.QtyShippedBase, &l.QtyReceivedBase, &l.UnitCostCents); err != nil {
			return transferHeader{}, nil, err
		}
		out = append(out, l)
	}
	return h, out, rows.Err()
}

// MarkLineShipped records the outbound half on a line: quantity, the cost that travelled with it,
// and the move that proves it.
func (r *Repository) MarkLineShipped(ctx context.Context, tx pgx.Tx, lineID string, qty float64, unitCostCents int64, moveID string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_transfer_lines
		   SET qty_shipped_base = $2, unit_cost_cents = $3, ship_move_id = $4, updated_at = now()
		 WHERE id = $1`, lineID, qty, unitCostCents, moveID)
	return err
}

// MarkLineReceived records the inbound half on a line.
func (r *Repository) MarkLineReceived(ctx context.Context, tx pgx.Tx, lineID string, qty float64, moveID string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_transfer_lines
		   SET qty_received_base = $2, recv_move_id = $3, updated_at = now()
		 WHERE id = $1`, lineID, qty, moveID)
	return err
}

// SetTransferShipped advances the header to IN_TRANSIT, stamps who loaded it, and records the promised
// arrival (DIB-472).
//
// eta is a YYYY-MM-DD string already graded by the service; EMPTY binds as SQL NULL through nullStr,
// which is how "no promise was made" is stored. It is written in the SAME statement as shipped_at so
// the 0158 CHECK (an eta requires a ship event) can never see an intermediate state.
func (r *Repository) SetTransferShipped(ctx context.Context, tx pgx.Tx, transferID, actorID, eta string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_transfers
		   SET status = 'IN_TRANSIT', shipped_at = now(), shipped_by = $2,
		       eta = $3::date, updated_at = now()
		 WHERE id = $1`, transferID, nullStr(actorID), nullStr(eta))
	return err
}

// SetTransferReceived closes the pair.
func (r *Repository) SetTransferReceived(ctx context.Context, tx pgx.Tx, transferID, actorID string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_transfers
		   SET status = 'RECEIVED', received_at = now(), received_by = $2, updated_at = now()
		 WHERE id = $1`, transferID, nullStr(actorID))
	return err
}

// SetTransferStatus is the plain status write (cancellation).
func (r *Repository) SetTransferStatus(ctx context.Context, tx pgx.Tx, transferID, status string) error {
	_, err := r.q(tx).Exec(ctx, `
		UPDATE stock_transfers SET status = $2, updated_at = now() WHERE id = $1`, transferID, status)
	return err
}

const transferSelect = `
	SELECT t.id::text, t.transfer_no::text, t.from_branch_id::text, t.to_branch_id::text, t.status,
	       t.notes,
	       COALESCE(to_char(t.shipped_at,  ` + tsFmt + `), ''),
	       COALESCE(to_char(t.received_at, ` + tsFmt + `), ''),
	       to_char(t.created_at, ` + tsFmt + `),
	       -- DIB-472. A DATE, so it is formatted as a bare day and NOT with tsFmt: an eta rendered as a
	       -- timestamp would invite a surface to show an arrival hour nobody promised. NULL → '', which
	       -- is how the frontend reads "no promised date" (an absent field and an empty one must not be
	       -- different facts on this wire).
	       COALESCE(to_char(t.eta, 'YYYY-MM-DD'), '')
	  FROM stock_transfers t`

func scanTransfers(rows pgx.Rows) ([]Transfer, error) {
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(&t.ID, &t.TransferNo, &t.FromBranchID, &t.ToBranchID, &t.Status,
			&t.Notes, &t.ShippedAt, &t.ReceivedAt, &t.CreatedAt, &t.ETA); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TransferByID returns one transfer with its lines (SKU + name joined so the surface renders a
// document without a second round trip per line).
func (r *Repository) TransferByID(ctx context.Context, transferID string) (Transfer, error) {
	if r.pool == nil {
		return Transfer{}, errNoDB
	}
	rows, err := r.pool.Query(ctx, transferSelect+` WHERE t.id = $1`, transferID)
	if err != nil {
		return Transfer{}, err
	}
	list, err := scanTransfers(rows)
	if err != nil {
		return Transfer{}, err
	}
	if len(list) == 0 {
		return Transfer{}, errNotFound
	}
	t := list[0]
	t.Lines, err = r.TransferLines(ctx, transferID)
	return t, err
}

// TransferLines returns the lines of one transfer.
func (r *Repository) TransferLines(ctx context.Context, transferID string) ([]TransferLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT tl.id::text, tl.line_no, tl.product_id::text, p.sku::text, p.name, tl.uom::text,
		       tl.qty_base, tl.qty_shipped_base, tl.qty_received_base, tl.unit_cost_cents,
		       COALESCE(tl.ship_move_id::text, ''), COALESCE(tl.recv_move_id::text, '')
		  FROM stock_transfer_lines tl
		  JOIN products p ON p.id = tl.product_id
		 WHERE tl.transfer_id = $1 ORDER BY tl.line_no`, transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TransferLine
	for rows.Next() {
		var l TransferLine
		if err := rows.Scan(&l.ID, &l.LineNo, &l.ProductID, &l.SKU, &l.ProductName, &l.UOM,
			&l.QtyBase, &l.QtyShippedBase, &l.QtyReceivedBase, &l.UnitCostCents,
			&l.ShipMoveID, &l.RecvMoveID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListTransfers lists transfers touching a branch at EITHER end, newest first. A yard office cares
// about "what is coming and what is going", so filtering on one direction would be the wrong list.
func (r *Repository) ListTransfers(ctx context.Context, branchID, status string, limit int) ([]Transfer, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, transferSelect+`
		 WHERE ($1 = '' OR t.from_branch_id = $1::uuid OR t.to_branch_id = $1::uuid)
		   AND ($2 = '' OR t.status = $2)
		 ORDER BY t.created_at DESC LIMIT $3`, branchID, status, limit)
	if err != nil {
		return nil, err
	}
	list, err := scanTransfers(rows)
	if err != nil {
		return nil, err
	}
	// Lines are fetched per transfer. The list is capped at 50 by default and a yard rarely has more
	// than a handful open, so N+1 here is a bounded handful of fast indexed reads, and the
	// alternative (one join, then regroup in Go) costs more code than it saves round trips at this size.
	for i := range list {
		lines, err := r.TransferLines(ctx, list[i].ID)
		if err != nil {
			return nil, err
		}
		list[i].Lines = lines
	}
	return list, nil
}
