package purchasing

// repository.go, the pgx persistence layer over the 0120/0121 purchasing tables, plus the two 0011 tables
// that make up a RECEIVING DOCUMENT (purchase_orders + po_lines, see migration 0120 for why those rows are
// a goods-received note and not a second purchase order).
//
// It touches NO vendor table: the vendor name a list view shows is enriched in the service through the
// VendorDirectory seam, so this file has no JOIN that would quietly make purchasing a second owner of vendor
// data. It touches no stock table either, not stock_moves, not stock_balances, not product_branch_costs.
//
// A nil pool is tolerated (offline/scaffold): every method returns errNoDB.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is the shared read/write surface of both *pgxpool.Pool and pgx.Tx, so a method can run either in the
// caller's transaction (the composed receiving/posting paths) or standalone on the pool (a read).
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (r *Repository) q(tx pgx.Tx) querier {
	if tx != nil {
		return tx
	}
	return r.pool
}

// Begin opens a transaction on the pool.
func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	return r.pool.Begin(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isCheckViolation answers a CHECK constraint refusal (23514), the database's own guard speaking.
func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// - purchase orders -

const poCols = `p.id::text, p.po_no::text, p.vendor_id::text, p.branch_id::text, p.status,
	COALESCE(to_char(p.expected_at,'YYYY-MM-DD'),''), p.ordered_at,
	p.notes, p.created_at, p.updated_at, p.currency::text, p.fx_rate::float8, p.delivery_method,
	COALESCE(p.warranty_credit_applied_cents, 0), p.warranty_claim_id::text, COALESCE(p.net_total_cents, 0)`

func scanPO(row pgx.Row) (PO, error) {
	var p PO
	err := row.Scan(&p.ID, &p.PONo, &p.VendorID, &p.BranchID, &p.Status,
		&p.ExpectedAt, &p.OrderedAt, &p.Notes, &p.CreatedAt, &p.UpdatedAt, &p.Currency, &p.FXRate, &p.DeliveryMethod,
		&p.WarrantyCreditAppliedCents, &p.WarrantyClaimID, &p.NetTotalCents)
	return p, err
}

// MintPONo allocates the branch's next PO number through next_po_no (migration 0120). The counter row is
// bumped inside the caller's transaction, so a draft that rolls back returns its number.
func (r *Repository) MintPONo(ctx context.Context, tx pgx.Tx, branchID string) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var no string
	if err := r.q(tx).QueryRow(ctx, `SELECT next_po_no($1)`, branchID).Scan(&no); err != nil {
		return "", fmt.Errorf("purchasing: mint po_no: %w", err)
	}
	return no, nil
}

type insertPOParams struct {
	PONo       string
	VendorID   string
	BranchID   string
	ExpectedAt string
	Notes      string
	CreatedBy  string
	// DIB-421. Always set by the service through normalizeCurrency, so a caller can never insert a
	// CAD document carrying a rate other than 1.0 (the 0134 CHECK is the floor under that).
	Currency string
	FXRate   float64
}

func (r *Repository) InsertPO(ctx context.Context, tx pgx.Tx, in insertPOParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	// The sync leg (0213, syncscope.go) stamps beside the row: the seq
	// from the purchase_orders scope's allocator, one statement, so a
	// PO the desk drafts is visible to a device's pull the moment it
	// lands. purchasing_pos carries no org column (the fence is the
	// branch's, the adapter's own predicate).
	err := r.q(tx).QueryRow(ctx, poSeqCTE+`
		INSERT INTO purchasing_pos AS p (po_no, vendor_id, branch_id, status, expected_at, notes, created_by,
		                                 currency, fx_rate, seq)
		SELECT $1,$2,$3,'DRAFT',$4::date,$5,$6,$7,$8, (SELECT next_value FROM poseq) RETURNING p.id::text`,
		in.PONo, in.VendorID, in.BranchID, nullStr(in.ExpectedAt), in.Notes,
		nullStr(in.CreatedBy), in.Currency, in.FXRate).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: insert po: %w", err)
	}
	return id, nil
}

// POForUpdate loads a PO header under a row lock, the serialization point for concurrent receipts against
// the same order.
func (r *Repository) POForUpdate(ctx context.Context, tx pgx.Tx, poID string) (PO, error) {
	if r.pool == nil {
		return PO{}, errNoDB
	}
	p, err := scanPO(r.q(tx).QueryRow(ctx,
		`SELECT `+poCols+` FROM purchasing_pos p WHERE p.id = $1 FOR UPDATE`, poID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PO{}, errNotFound
	}
	if err != nil {
		return PO{}, fmt.Errorf("purchasing: po for update: %w", err)
	}
	return p, nil
}

// GetPO reads a PO through the pool, for callers that hold no transaction.
func (r *Repository) GetPO(ctx context.Context, poID string) (PO, error) {
	return r.GetPOTx(ctx, nil, poID)
}

// GetPOTx reads a PO through tx (nil means the pool). A caller inside a transaction must pass it: a read
// through the pool there takes a second connection while the first is held, and enough of those at once
// starve the pool.
func (r *Repository) GetPOTx(ctx context.Context, tx pgx.Tx, poID string) (PO, error) {
	if r.pool == nil {
		return PO{}, errNoDB
	}
	p, err := scanPO(r.q(tx).QueryRow(ctx, `SELECT `+poCols+` FROM purchasing_pos p WHERE p.id = $1`, poID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PO{}, errNotFound
	}
	if err != nil {
		return PO{}, fmt.Errorf("purchasing: get po: %w", err)
	}
	return p, nil
}

// ListPOs lists POs for a branch ("" = all accessible; the service applies the branch fence) and optional
// status. Line count + subtotal are aggregated here so a list view costs one round trip.
func (r *Repository) ListPOs(ctx context.Context, branchID, status string) ([]PO, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+poCols+`,
		       (SELECT count(*) FROM purchasing_po_lines l WHERE l.po_id = p.id),
		       COALESCE((SELECT round(sum(l.qty_ordered_base * l.unit_cost_cents))
		                   FROM purchasing_po_lines l WHERE l.po_id = p.id), 0)::bigint,
		       -- DIB-421: the same total after toCAD. Rounded ONCE over the document total here,
		       -- which is a LIST-VIEW figure only; the money that moves is converted per unit cost at
		       -- the receipt boundary, exactly as inventory does, and never derived from this column.
		       COALESCE((SELECT round(sum(l.qty_ordered_base * l.unit_cost_cents) * p.fx_rate)
		                   FROM purchasing_po_lines l WHERE l.po_id = p.id), 0)::bigint
		  FROM purchasing_pos p
		 WHERE ($1 = '' OR p.branch_id = $1::uuid)
		   AND ($2 = '' OR p.status = $2)
		 ORDER BY p.created_at DESC, p.po_no DESC
		 LIMIT 500`, branchID, status)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list pos: %w", err)
	}
	defer rows.Close()
	var out []PO
	for rows.Next() {
		var p PO
		if err := rows.Scan(&p.ID, &p.PONo, &p.VendorID, &p.BranchID, &p.Status,
			&p.ExpectedAt, &p.OrderedAt, &p.Notes, &p.CreatedAt, &p.UpdatedAt, &p.Currency, &p.FXRate, &p.DeliveryMethod,
			&p.WarrantyCreditAppliedCents, &p.WarrantyClaimID, &p.NetTotalCents,
			&p.LineCount, &p.SubtotalCents, &p.SubtotalCadCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan po: %w", err)
		}
		if p.NetTotalCents == 0 && p.WarrantyCreditAppliedCents == 0 {
			p.NetTotalCents = p.SubtotalCents
		} else if p.WarrantyCreditAppliedCents > 0 && p.NetTotalCents == 0 {
			p.NetTotalCents = p.SubtotalCents - p.WarrantyCreditAppliedCents
			if p.NetTotalCents < 0 {
				p.NetTotalCents = 0
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const poLineCols = `l.id::text, l.line_no, l.product_id::text, COALESCE(pr.sku::text,''), l.description,
	l.qty_ordered_base, l.qty_received_base, l.qty_billed_base, l.uom::text, l.unit_cost_cents, l.billed_cents`

// POLines lists a PO's lines. lock=true takes FOR UPDATE (the receiving path).
func (r *Repository) POLines(ctx context.Context, tx pgx.Tx, poID string, lock bool) ([]POLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	// FOR UPDATE cannot be combined with the LEFT JOIN's outer side, so the lock is taken on the line rows
	// only (OF l), which is exactly the row set being mutated.
	sql := `SELECT ` + poLineCols + `
		  FROM purchasing_po_lines l
		  LEFT JOIN products pr ON pr.id = l.product_id
		 WHERE l.po_id = $1
		 ORDER BY l.line_no`
	if lock {
		sql += ` FOR UPDATE OF l`
	}
	rows, err := r.q(tx).Query(ctx, sql, poID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: po lines: %w", err)
	}
	defer rows.Close()
	var out []POLine
	for rows.Next() {
		var l POLine
		if err := rows.Scan(&l.ID, &l.LineNo, &l.ProductID, &l.SKU, &l.Description, &l.QtyOrderedBase,
			&l.QtyReceivedBase, &l.QtyBilledBase, &l.UOM, &l.UnitCostCents, &l.BilledCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan po line: %w", err)
		}
		l.QtyOutstanding = l.QtyOrderedBase - l.QtyReceivedBase
		if l.QtyOutstanding < 0 {
			l.QtyOutstanding = 0
		}
		l.LineTotalCents = round(l.QtyOrderedBase * float64(l.UnitCostCents))
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) InsertPOLine(ctx context.Context, tx pgx.Tx, poID string, lineNo int, in AddLineParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	// An empty uom resolves to the PRODUCT'S BASE UNIT, in SQL, exactly as inventory's moveParams
	// documents for its own writes ("" = resolve the product's base_uom in SQL). See validateLine
	// for why a caller is allowed to omit it: the DIB-72 create-PO seam speaks base quantities, and
	// the unit a base quantity is in belongs to the product, not to the request.
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_po_lines (po_id, line_no, product_id, description, qty_ordered_base, uom, unit_cost_cents)
		VALUES ($1,$2,$3,$4,$5,
		        COALESCE(NULLIF($6,'')::citext, (SELECT p.base_uom FROM products p WHERE p.id = $3)),
		        $7) RETURNING id::text`,
		poID, lineNo, in.ProductID, in.Description, in.QtyOrderedBase, strings.TrimSpace(in.UOM), in.UnitCostCents).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: insert po line: %w", err)
	}
	if err := bumpPOHeaderSeq(ctx, r.q(tx), poID); err != nil {
		return "", err
	}
	return id, nil
}

// UpdatePOLine corrects the quantity or the cost of one line on its draft, then stamps the header
// seq exactly as the add and the remove do: the line set is the order's meaning, and a device's
// pull must see the header move. A line that is not on this order is a 404 (errNotFound), never a
// silent no-op that reads back as success.
func (r *Repository) UpdatePOLine(ctx context.Context, tx pgx.Tx, poID, lineID string, in UpdateLineParams) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_po_lines SET
			qty_ordered_base = COALESCE($3, qty_ordered_base),
			unit_cost_cents = COALESCE($4, unit_cost_cents),
			updated_at = now()
		WHERE id = $1 AND po_id = $2`,
		lineID, poID, in.QtyOrderedBase, in.UnitCostCents)
	if err != nil {
		return fmt.Errorf("purchasing: update po line: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	if err := bumpPOHeaderSeq(ctx, r.q(tx), poID); err != nil {
		return err
	}
	return nil
}

// bumpPOHeaderSeq stamps the purchase_orders scope's seq on the PO's
// header row (0213): a line change changes the order's meaning (its
// line count, its totals), and a device's pull must see the header
// move even though the lines themselves are not a synced table.
func bumpPOHeaderSeq(ctx context.Context, q querier, poID string) error {
	if _, err := q.Exec(ctx, `
		UPDATE purchasing_pos SET updated_at = now() WHERE id = $1`,
		poID); err != nil {
		return fmt.Errorf("purchasing: bump po header seq: %w", err)
	}
	return nil
}

// ApplyWarrantyCredit records the warranty credit applied to a draft purchase order.
func (r *Repository) ApplyWarrantyCredit(ctx context.Context, tx pgx.Tx, poID string, claimID *string, creditCents, netTotalCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_pos
		   SET warranty_credit_applied_cents = $2,
		       warranty_claim_id = $3::uuid,
		       net_total_cents = $4,
		       updated_at = now()
		 WHERE id = $1::uuid`, poID, creditCents, claimID, netTotalCents)
	if err != nil {
		return fmt.Errorf("purchasing: apply warranty credit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

func (r *Repository) NextLineNo(ctx context.Context, tx pgx.Tx, poID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var n int
	if err := r.q(tx).QueryRow(ctx,
		`SELECT COALESCE(max(line_no),0)+1 FROM purchasing_po_lines WHERE po_id = $1`, poID).Scan(&n); err != nil {
		return 0, fmt.Errorf("purchasing: next line no: %w", err)
	}
	return n, nil
}

func (r *Repository) DeletePOLine(ctx context.Context, tx pgx.Tx, poID, lineID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.q(tx).Exec(ctx, `DELETE FROM purchasing_po_lines WHERE id = $1 AND po_id = $2`, lineID, poID)
	if err != nil {
		return fmt.Errorf("purchasing: delete po line: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	if err := bumpPOHeaderSeq(ctx, r.q(tx), poID); err != nil {
		return err
	}
	return nil
}

// SetPOStatus writes a lifecycle status. stampOrdered sets ordered_at (the DRAFT→ORDERED moment); stampClosed
// sets closed_at. The purchase_orders scope's seq rides the move (0213), so a desk transition is visible
// to a device's pull the moment it lands.
func (r *Repository) SetPOStatus(ctx context.Context, tx pgx.Tx, poID, status string, stampOrdered, stampClosed bool) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_pos
		   SET status = $2,
		       ordered_at = CASE WHEN $3 THEN now() ELSE ordered_at END,
		       closed_at  = CASE WHEN $4 THEN now() ELSE closed_at  END,
		       updated_at = now()
		 WHERE id = $1`, poID, status, stampOrdered, stampClosed)
	if err != nil {
		return fmt.Errorf("purchasing: set po status: %w", err)
	}
	return nil
}

// - the receiving document (0011 purchase_orders + po_lines) -

type receivingDocParams struct {
	GRNNo     string
	VendorID  string
	BranchID  string
	CreatedBy string
	SourcePO  string
}

// InsertReceivingDoc writes the goods-received note inventory will convert into stock. The metadata records
// which purchase order it belongs to, so the linkage is legible from the inventory side too, a person
// reading purchase_orders can see at a glance that this row is a receipt, not an order.
func (r *Repository) InsertReceivingDoc(ctx context.Context, tx pgx.Tx, in receivingDocParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchase_orders (po_no, vendor_id, branch_id, status, currency, fx_rate, created_by, metadata)
		VALUES ($1,$2,$3,'SUBMITTED','CAD',1.0,$4,
		        jsonb_build_object('doc_kind','RECEIVING_DOCUMENT','purchasing_po_id',$5::text,'grn_no',$6::text))
		RETURNING id::text`,
		in.GRNNo, in.VendorID, in.BranchID, nullStr(in.CreatedBy), in.SourcePO, in.GRNNo).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: insert receiving document: %w", err)
	}
	return id, nil
}

// InsertReceivingDocLine writes one line of the goods-received note. qty_base IS the quantity landing now -
// that is what makes a partial receipt expressible through inventory's receive-everything-outstanding path.
func (r *Repository) InsertReceivingDocLine(ctx context.Context, tx pgx.Tx, docID string, lineNo int, productID, uom string, qty float64, unitCostCents int64) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO po_lines (po_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_cost_cents, cost_uom)
		VALUES ($1,$2,$3,$4,$5,$4,$6,$5) RETURNING id::text`,
		docID, lineNo, productID, qty, uom, unitCostCents).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: insert receiving document line: %w", err)
	}
	return id, nil
}

// - receipts -

func (r *Repository) NextReceiptSeq(ctx context.Context, tx pgx.Tx, poID string) (int, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var n int
	if err := r.q(tx).QueryRow(ctx,
		`SELECT COALESCE(max(seq),0)+1 FROM purchasing_receipts WHERE po_id = $1`, poID).Scan(&n); err != nil {
		return 0, fmt.Errorf("purchasing: next receipt seq: %w", err)
	}
	return n, nil
}

type insertReceiptParams struct {
	POID       string
	Seq        int
	GRNNo      string
	StockDocID string
	ValueCents int64
	ReceivedBy string
	Notes      string
}

func (r *Repository) InsertReceipt(ctx context.Context, tx pgx.Tx, in insertReceiptParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_receipts (po_id, seq, grn_no, stock_doc_id, value_cad_cents, received_by, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`,
		in.POID, in.Seq, in.GRNNo, in.StockDocID, in.ValueCents, nullStr(in.ReceivedBy), in.Notes).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("purchasing: insert receipt: %w", err)
	}
	return id, nil
}

func (r *Repository) InsertReceiptLine(ctx context.Context, tx pgx.Tx, receiptID, poLineID, stockPOLineID string, qty float64, unitCostCents, valueCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		INSERT INTO purchasing_receipt_lines (receipt_id, po_line_id, qty_base, unit_cost_cents, value_cents, stock_po_line_id)
		VALUES ($1,$2,$3,$4,$5,$6)`, receiptID, poLineID, qty, unitCostCents, valueCents, nullStr(stockPOLineID))
	if err != nil {
		return fmt.Errorf("purchasing: insert receipt line: %w", err)
	}
	return nil
}

// BumpReceived adds to a PO line's received rollup. The 0120 CHECK (received <= ordered) is the backstop
// under the service's over-receipt guard.
func (r *Repository) BumpReceived(ctx context.Context, tx pgx.Tx, poLineID string, qty float64) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_po_lines SET qty_received_base = qty_received_base + $2, updated_at = now()
		 WHERE id = $1`, poLineID, qty)
	if err != nil {
		return fmt.Errorf("purchasing: bump received: %w", err)
	}
	return nil
}

// SetReceiptValue stamps the CAD value of a receipt once its lines are written. It is a second statement
// rather than a computed insert because the value is the SUM of what the lines converted to, and the lines
// are not known until the receiving document has been accepted by the stock seam.
func (r *Repository) SetReceiptValue(ctx context.Context, tx pgx.Tx, receiptID string, valueCents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx,
		`UPDATE purchasing_receipts SET value_cad_cents = $2 WHERE id = $1`, receiptID, valueCents)
	if err != nil {
		return fmt.Errorf("purchasing: set receipt value: %w", err)
	}
	return nil
}

const receiptCols = `rc.id::text, rc.po_id::text, p.po_no::text, rc.seq, rc.grn_no::text,
	COALESCE(rc.stock_doc_id::text,''), COALESCE(rc.landed_cost_id::text,''), rc.value_cad_cents,
	COALESCE(rc.received_by::text,''), rc.received_at, rc.notes`

func (r *Repository) Receipts(ctx context.Context, poID string) ([]Receipt, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+receiptCols+`
		  FROM purchasing_receipts rc JOIN purchasing_pos p ON p.id = rc.po_id
		 WHERE rc.po_id = $1 ORDER BY rc.seq`, poID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: receipts: %w", err)
	}
	defer rows.Close()
	var out []Receipt
	for rows.Next() {
		var rc Receipt
		if err := rows.Scan(&rc.ID, &rc.POID, &rc.PONo, &rc.Seq, &rc.GRNNo, &rc.StockDocID, &rc.LandedCostID,
			&rc.ValueCadCents, &rc.ReceivedBy, &rc.ReceivedAt, &rc.Notes); err != nil {
			return nil, fmt.Errorf("purchasing: scan receipt: %w", err)
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (r *Repository) GetReceipt(ctx context.Context, id string) (Receipt, error) {
	if r.pool == nil {
		return Receipt{}, errNoDB
	}
	var rc Receipt
	err := r.pool.QueryRow(ctx, `SELECT `+receiptCols+`
		  FROM purchasing_receipts rc JOIN purchasing_pos p ON p.id = rc.po_id
		 WHERE rc.id = $1`, id).Scan(&rc.ID, &rc.POID, &rc.PONo, &rc.Seq, &rc.GRNNo, &rc.StockDocID,
		&rc.LandedCostID, &rc.ValueCadCents, &rc.ReceivedBy, &rc.ReceivedAt, &rc.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, errNotFound
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("purchasing: get receipt: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT rl.id::text, rl.po_line_id::text, l.product_id::text, COALESCE(pr.sku::text,''),
		       rl.qty_base, rl.unit_cost_cents, rl.value_cents
		  FROM purchasing_receipt_lines rl
		  JOIN purchasing_po_lines l ON l.id = rl.po_line_id
		  LEFT JOIN products pr ON pr.id = l.product_id
		 WHERE rl.receipt_id = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return Receipt{}, fmt.Errorf("purchasing: receipt lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rl ReceiptLine
		if err := rows.Scan(&rl.ID, &rl.POLineID, &rl.ProductID, &rl.SKU, &rl.QtyBase, &rl.UnitCostCents, &rl.ValueCents); err != nil {
			return Receipt{}, fmt.Errorf("purchasing: scan receipt line: %w", err)
		}
		rc.Lines = append(rc.Lines, rl)
	}
	return rc, rows.Err()
}

func (r *Repository) SetReceiptLandedCost(ctx context.Context, receiptID, landedCostID string) error {
	if r.pool == nil {
		return errNoDB
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE purchasing_receipts SET landed_cost_id = $2 WHERE id = $1`, receiptID, landedCostID)
	if err != nil {
		return fmt.Errorf("purchasing: set receipt landed cost: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// Worklist is the receiving queue: placed orders with something still outstanding, soonest-expected first.
func (r *Repository) Worklist(ctx context.Context, branchID string) ([]WorklistItem, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.po_no::text, p.vendor_id::text, p.branch_id::text, p.status,
		       COALESCE(to_char(p.expected_at,'YYYY-MM-DD'),''),
		       count(*) FILTER (WHERE l.qty_ordered_base - l.qty_received_base > 0.0001),
		       COALESCE(sum(GREATEST(l.qty_ordered_base - l.qty_received_base, 0)), 0),
		       COALESCE(round(sum(GREATEST(l.qty_ordered_base - l.qty_received_base, 0) * l.unit_cost_cents)), 0)::bigint
		  FROM purchasing_pos p
		  JOIN purchasing_po_lines l ON l.po_id = p.id
		 WHERE p.status IN ('ORDERED','PARTIAL')
		   AND ($1 = '' OR p.branch_id = $1::uuid)
		 GROUP BY p.id
		HAVING sum(GREATEST(l.qty_ordered_base - l.qty_received_base, 0)) > 0.0001
		 ORDER BY p.expected_at NULLS LAST, p.po_no
		 LIMIT 200`, branchID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: worklist: %w", err)
	}
	defer rows.Close()
	var out []WorklistItem
	for rows.Next() {
		var w WorklistItem
		if err := rows.Scan(&w.POID, &w.PONo, &w.VendorID, &w.BranchID, &w.Status, &w.ExpectedAt,
			&w.OpenLines, &w.QtyOutstanding, &w.ValueOpenCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan worklist: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// - tolerance -

// Tolerance resolves the branch row if there is one, else the org default. A deployment with neither (the
// 0121 default row deleted) gets the conservative zero-tolerance answer rather than an error: a missing
// configuration must not silently widen a control.
func (r *Repository) Tolerance(ctx context.Context, branchID string) (Tolerance, error) {
	return r.ToleranceTx(ctx, nil, branchID)
}

// ToleranceTx is Tolerance through tx (nil means the pool), for a caller that already holds a transaction.
func (r *Repository) ToleranceTx(ctx context.Context, tx pgx.Tx, branchID string) (Tolerance, error) {
	if r.pool == nil {
		return Tolerance{}, errNoDB
	}
	var t Tolerance
	err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(branch_id::text,''), qty_tolerance_pct, price_tolerance_pct, price_tolerance_abs_cents
		  FROM purchasing_match_tolerance
		 WHERE branch_id IS NULL OR branch_id = NULLIF($1, '')::uuid
		 ORDER BY (branch_id IS NOT NULL) DESC
		 LIMIT 1`, branchID).Scan(&t.BranchID, &t.QtyPct, &t.PricePct, &t.PriceAbsCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tolerance{}, nil
	}
	if err != nil {
		return Tolerance{}, fmt.Errorf("purchasing: tolerance: %w", err)
	}
	t.IsBranchOverride = t.BranchID != ""
	t.ResolvedFromDefault = !t.IsBranchOverride
	return t, nil
}

func (r *Repository) UpsertTolerance(ctx context.Context, in SetToleranceRequest, userID string) error {
	if r.pool == nil {
		return errNoDB
	}
	var err error
	if in.BranchID == "" {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO purchasing_match_tolerance (branch_id, qty_tolerance_pct, price_tolerance_pct, price_tolerance_abs_cents, updated_by)
			VALUES (NULL,$1,$2,$3,$4)
			ON CONFLICT ((branch_id IS NULL)) WHERE branch_id IS NULL
			DO UPDATE SET qty_tolerance_pct = EXCLUDED.qty_tolerance_pct,
			              price_tolerance_pct = EXCLUDED.price_tolerance_pct,
			              price_tolerance_abs_cents = EXCLUDED.price_tolerance_abs_cents,
			              updated_by = EXCLUDED.updated_by, updated_at = now()`,
			in.QtyPct, in.PricePct, in.PriceAbsCents, nullStr(userID))
	} else {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO purchasing_match_tolerance (branch_id, qty_tolerance_pct, price_tolerance_pct, price_tolerance_abs_cents, updated_by)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (branch_id) WHERE branch_id IS NOT NULL
			DO UPDATE SET qty_tolerance_pct = EXCLUDED.qty_tolerance_pct,
			              price_tolerance_pct = EXCLUDED.price_tolerance_pct,
			              price_tolerance_abs_cents = EXCLUDED.price_tolerance_abs_cents,
			              updated_by = EXCLUDED.updated_by, updated_at = now()`,
			in.BranchID, in.QtyPct, in.PricePct, in.PriceAbsCents, nullStr(userID))
	}
	if err != nil {
		return fmt.Errorf("purchasing: upsert tolerance: %w", err)
	}
	return nil
}

// - bills -

const billCols = `b.id::text, b.bill_no::text, b.vendor_id::text, b.branch_id::text,
	COALESCE(b.po_id::text,''), COALESCE(p.po_no::text,''), b.status, b.match_state,
	to_char(b.bill_date,'YYYY-MM-DD'), COALESCE(to_char(b.due_date,'YYYY-MM-DD'),''),
	b.subtotal_cents, b.tax_cents, b.total_cents,
	COALESCE(b.gl_journal_id::text,''), b.posted_at, b.variance_accepted_note,
	COALESCE(b.variance_accepted_by::text,''), b.created_at, b.currency::text, b.fx_rate::float8,
	b.doc_total_cents`

func scanBill(row pgx.Row) (Bill, error) {
	var b Bill
	err := row.Scan(&b.ID, &b.BillNo, &b.VendorID, &b.BranchID, &b.POID, &b.PONo, &b.Status, &b.MatchState,
		&b.BillDate, &b.DueDate, &b.SubtotalCents, &b.TaxCents, &b.TotalCents,
		&b.GLJournalID, &b.PostedAt, &b.VarianceNote, &b.VarianceByUser, &b.CreatedAt,
		&b.Currency, &b.FXRate, &b.DocTotalCents)
	return b, err
}

type insertBillParams struct {
	BillNo    string
	VendorID  string
	BranchID  string
	POID      string
	BillDate  string
	DueDate   string
	Subtotal  int64
	Tax       int64
	Total     int64
	DocTotal  int64
	Match     string
	CreatedBy string
	// DIB-421. Subtotal/Tax/Total above are ALREADY CAD, the bill is converted at entry, so the
	// three-way match, the journal legs and the AP balance never see a foreign amount. These two
	// record WHAT THE VENDOR WROTE and at what rate we read it, which is what an AP clerk holding
	// the paper invoice needs in order to agree with the screen.
	Currency string
	FXRate   float64
	// HouseDay is the dealer's business day the service decided before the transaction opened: the
	// bill date when none was named. Blank is the Ontario dealer's day.
	HouseDay string
}

func (r *Repository) InsertBill(ctx context.Context, tx pgx.Tx, in insertBillParams) (string, error) {
	if r.pool == nil {
		return "", errNoDB
	}
	houseDay := in.HouseDay
	if houseDay == "" {
		houseDay = houseday.Today(nil)
	}
	docTotal := in.DocTotal
	if docTotal <= 0 {
		docTotal = in.Total
	}
	var id string
	err := r.q(tx).QueryRow(ctx, `
		INSERT INTO purchasing_bills (bill_no, vendor_id, branch_id, po_id, status, match_state, bill_date,
		                              due_date, subtotal_cents, tax_cents, total_cents, doc_total_cents,
		                              created_by, currency, fx_rate)
		VALUES ($1,$2,$3,$4,'DRAFT',$5,COALESCE($6::date, $14::date),$7::date,$8,$9,$10,$15,$11,$12,$13)
		RETURNING id::text`,
		in.BillNo, in.VendorID, in.BranchID, nullStr(in.POID), in.Match, nullStr(in.BillDate),
		nullStr(in.DueDate), in.Subtotal, in.Tax, in.Total, nullStr(in.CreatedBy),
		in.Currency, in.FXRate, houseDay, docTotal).Scan(&id)
	if isUniqueViolation(err) {
		return "", conflict("purchasing: bill %q has already been entered for this vendor", in.BillNo)
	}
	if err != nil {
		return "", fmt.Errorf("purchasing: insert bill: %w", err)
	}
	return id, nil
}

type insertBillLineParams struct {
	BillID     string
	LineNo     int
	POLineID   string
	Desc       string
	Qty        float64
	UnitCost   int64
	LineTotal  int64
	MatchState string
	QtyVar     float64
	PriceVar   int64
}

func (r *Repository) InsertBillLine(ctx context.Context, tx pgx.Tx, in insertBillLineParams) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		INSERT INTO purchasing_bill_lines (bill_id, line_no, po_line_id, description, qty_billed_base,
		                                   unit_cost_cents, line_total_cents, match_state,
		                                   qty_variance_base, price_variance_cents)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		in.BillID, in.LineNo, nullStr(in.POLineID), in.Desc, in.Qty, in.UnitCost, in.LineTotal,
		in.MatchState, in.QtyVar, in.PriceVar)
	if err != nil {
		return fmt.Errorf("purchasing: insert bill line: %w", err)
	}
	return nil
}

func (r *Repository) InsertInstalment(ctx context.Context, tx pgx.Tx, billID string, seq int, dueDate string, amount int64, note string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		INSERT INTO purchasing_bill_instalments (bill_id, seq, due_date, amount_cents, note)
		VALUES ($1,$2,$3::date,$4,$5)`, billID, seq, dueDate, amount, note)
	if err != nil {
		return fmt.Errorf("purchasing: insert instalment: %w", err)
	}
	return nil
}

func (r *Repository) BillForUpdate(ctx context.Context, tx pgx.Tx, id string) (Bill, error) {
	if r.pool == nil {
		return Bill{}, errNoDB
	}
	// The PO join is only for the display number, so the lock is taken on the bill row alone.
	b, err := scanBill(r.q(tx).QueryRow(ctx, `SELECT `+billCols+`
		  FROM purchasing_bills b LEFT JOIN purchasing_pos p ON p.id = b.po_id
		 WHERE b.id = $1 FOR UPDATE OF b`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, errNotFound
	}
	if err != nil {
		return Bill{}, fmt.Errorf("purchasing: bill for update: %w", err)
	}
	return b, nil
}

func (r *Repository) GetBill(ctx context.Context, id string) (BillDetail, error) {
	if r.pool == nil {
		return BillDetail{}, errNoDB
	}
	b, err := scanBill(r.pool.QueryRow(ctx, `SELECT `+billCols+`
		  FROM purchasing_bills b LEFT JOIN purchasing_pos p ON p.id = b.po_id WHERE b.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return BillDetail{}, errNotFound
	}
	if err != nil {
		return BillDetail{}, fmt.Errorf("purchasing: get bill: %w", err)
	}
	detail := BillDetail{Bill: b}
	lines, err := r.BillLines(ctx, nil, id)
	if err != nil {
		return BillDetail{}, err
	}
	detail.Lines = lines

	rows, err := r.pool.Query(ctx, `
		SELECT id::text, seq, to_char(due_date,'YYYY-MM-DD'), amount_cents, status, note, doc_amount_cents
		  FROM purchasing_bill_instalments WHERE bill_id = $1 ORDER BY seq`, id)
	if err != nil {
		return BillDetail{}, fmt.Errorf("purchasing: instalments: %w", err)
	}
	defer rows.Close()
	var paidDoc, clearedCad int64
	for rows.Next() {
		var in Instalment
		var docAmount int64
		if err := rows.Scan(&in.ID, &in.Seq, &in.DueDate, &in.AmountCents, &in.Status, &in.Note, &docAmount); err != nil {
			return BillDetail{}, fmt.Errorf("purchasing: scan instalment: %w", err)
		}
		if in.Status == "PAID" {
			paidDoc += docAmount
			clearedCad += in.AmountCents
		}
		detail.Instalments = append(detail.Instalments, in)
	}
	if err := rows.Err(); err != nil {
		return BillDetail{}, err
	}
	// The open amount in the bill's own currency, net of the vendor credits applied to it, the same
	// figure PayBill takes to settle the bill (billDocOpen).
	credited, err := r.CreditedBillCentsTx(ctx, nil, id)
	if err != nil {
		return BillDetail{}, err
	}
	openCad := detail.TotalCents - clearedCad - credited
	if strings.EqualFold(strings.TrimSpace(detail.Currency), "USD") {
		detail.DocOpenCents = billDocOpen(detail.TotalCents, detail.DocTotalCents, paidDoc, credited, openCad)
	} else {
		detail.DocOpenCents = openCad
	}
	return detail, nil
}

// BillLines returns a bill's lines joined to the PO line they claim to bill, so the match numbers a caller
// reads are the same numbers the posting path recomputes.
func (r *Repository) BillLines(ctx context.Context, tx pgx.Tx, billID string) ([]BillLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.q(tx).Query(ctx, `
		SELECT bl.id::text, bl.line_no, COALESCE(bl.po_line_id::text,''),
		       COALESCE(l.product_id::text,''), COALESCE(pr.sku::text,''), bl.description,
		       bl.qty_billed_base, COALESCE(l.qty_ordered_base,0), COALESCE(l.qty_received_base,0),
		       bl.unit_cost_cents, COALESCE(l.unit_cost_cents,0), bl.line_total_cents,
		       bl.match_state, bl.qty_variance_base, bl.price_variance_cents
		  FROM purchasing_bill_lines bl
		  LEFT JOIN purchasing_po_lines l ON l.id = bl.po_line_id
		  LEFT JOIN products pr ON pr.id = l.product_id
		 WHERE bl.bill_id = $1 ORDER BY bl.line_no`, billID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: bill lines: %w", err)
	}
	defer rows.Close()
	var out []BillLine
	for rows.Next() {
		var l BillLine
		if err := rows.Scan(&l.ID, &l.LineNo, &l.POLineID, &l.ProductID, &l.SKU, &l.Description,
			&l.QtyBilledBase, &l.QtyOrderedBase, &l.QtyReceivedBase, &l.UnitCostCents, &l.POUnitCostCents,
			&l.LineTotalCents, &l.MatchState, &l.QtyVarianceBase, &l.PriceVarianceCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan bill line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repository) ListBills(ctx context.Context, branchID, status, matchState string) ([]Bill, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `SELECT `+billCols+`
		  FROM purchasing_bills b LEFT JOIN purchasing_pos p ON p.id = b.po_id
		 WHERE ($1 = '' OR b.branch_id = $1::uuid)
		   AND ($2 = '' OR b.status = $2)
		   AND ($3 = '' OR b.match_state = $3)
		 ORDER BY b.created_at DESC LIMIT 500`, branchID, status, matchState)
	if err != nil {
		return nil, fmt.Errorf("purchasing: list bills: %w", err)
	}
	defer rows.Close()
	var out []Bill
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			return nil, fmt.Errorf("purchasing: scan bill: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PriorBilledQty is how much of a PO line other POSTED bills have already claimed. Excluding the bill being
// evaluated is what makes a re-evaluation idempotent; counting only POSTED bills is what stops a stack of
// unposted drafts from making every one of them look like a duplicate.
func (r *Repository) PriorBilledQty(ctx context.Context, tx pgx.Tx, poLineID, excludeBillID string) (float64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var q float64
	err := r.q(tx).QueryRow(ctx, `
		SELECT COALESCE(sum(bl.qty_billed_base),0)
		  FROM purchasing_bill_lines bl JOIN purchasing_bills b ON b.id = bl.bill_id
		 WHERE bl.po_line_id = $1 AND b.status = 'POSTED' AND ($2 = '' OR b.id <> $2::uuid)`,
		poLineID, excludeBillID).Scan(&q)
	if err != nil {
		return 0, fmt.Errorf("purchasing: prior billed qty: %w", err)
	}
	return q, nil
}

func (r *Repository) UpdateBillLineMatch(ctx context.Context, tx pgx.Tx, lineID, state string, qtyVar float64, priceVar int64) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_bill_lines SET match_state = $2, qty_variance_base = $3, price_variance_cents = $4
		 WHERE id = $1`, lineID, state, qtyVar, priceVar)
	if err != nil {
		return fmt.Errorf("purchasing: update bill line match: %w", err)
	}
	return nil
}

func (r *Repository) SetBillMatchState(ctx context.Context, tx pgx.Tx, billID, state string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx,
		`UPDATE purchasing_bills SET match_state = $2, updated_at = now() WHERE id = $1`, billID, state)
	if err != nil {
		return fmt.Errorf("purchasing: set bill match state: %w", err)
	}
	return nil
}

// SetBillPosted stamps the POSTED status, the journal it wrote, and, only when the bill needed one, the
// recorded variance acceptance. acceptedBy is empty for a bill that matched, so a POSTED+VARIANCE bill
// without an acceptance is impossible to write through this path.
func (r *Repository) SetBillPosted(ctx context.Context, tx pgx.Tx, billID, journalID, acceptedBy, note string) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_bills
		   SET status = 'POSTED', posted_at = now(), gl_journal_id = $2,
		       variance_accepted_by = $3,
		       variance_accepted_at = CASE WHEN $3::uuid IS NULL THEN NULL ELSE now() END,
		       variance_accepted_note = $4, updated_at = now()
		 WHERE id = $1`, billID, nullStr(journalID), nullStr(acceptedBy), note)
	if err != nil {
		return fmt.Errorf("purchasing: set bill posted: %w", err)
	}
	return nil
}

func (r *Repository) BumpBilled(ctx context.Context, tx pgx.Tx, poLineID string, qty float64, cents int64) error {
	if r.pool == nil {
		return errNoDB
	}
	_, err := r.q(tx).Exec(ctx, `
		UPDATE purchasing_po_lines
		   SET qty_billed_base = qty_billed_base + $2, billed_cents = billed_cents + $3, updated_at = now()
		 WHERE id = $1`, poLineID, qty, cents)
	if err != nil {
		return fmt.Errorf("purchasing: bump billed: %w", err)
	}
	return nil
}

// POLineBrief is the narrow PO-line read the bill paths need (no products join, no display fields).
type POLineBrief struct {
	ID              string
	POID            string
	LineNo          int
	ProductID       string
	QtyOrderedBase  float64
	QtyReceivedBase float64
	UnitCostCents   int64
}

func (r *Repository) POLineBriefs(ctx context.Context, tx pgx.Tx, poID string, lock bool) (map[string]POLineBrief, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	sql := `SELECT id::text, po_id::text, line_no, product_id::text, qty_ordered_base, qty_received_base, unit_cost_cents
		  FROM purchasing_po_lines WHERE po_id = $1 ORDER BY line_no`
	if lock {
		sql += ` FOR UPDATE`
	}
	rows, err := r.q(tx).Query(ctx, sql, poID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: po line briefs: %w", err)
	}
	defer rows.Close()
	out := map[string]POLineBrief{}
	for rows.Next() {
		var b POLineBrief
		if err := rows.Scan(&b.ID, &b.POID, &b.LineNo, &b.ProductID, &b.QtyOrderedBase, &b.QtyReceivedBase, &b.UnitCostCents); err != nil {
			return nil, fmt.Errorf("purchasing: scan po line brief: %w", err)
		}
		out[b.ID] = b
	}
	return out, rows.Err()
}

// - the vendor master writes (vendor_master.go's repository half; they sit here with the module's
// other table access so the tablegate's file scoped census reads them beside the PO writes that
// stamp the same 0213 seq) -

// InsertVendorMaster mints the number, draws the seq and inserts, in ONE statement: the number and
// the seq are both minted by the database, so a rolled back create burns neither.
func (r *Repository) InsertVendorMaster(ctx context.Context, orgID, name string, terms int, email string) (VendorMaster, error) {
	if r.pool == nil {
		return VendorMaster{}, errNoDB
	}
	remit := "{}"
	if email != "" {
		blob, err := json.Marshal(map[string]string{"email": email})
		if err != nil {
			return VendorMaster{}, fmt.Errorf("purchasing: encode remit address: %w", err)
		}
		remit = string(blob)
	}
	var v VendorMaster
	err := r.pool.QueryRow(ctx, vendorSeqCTE+`
		INSERT INTO vendors (vendor_no, name, payment_terms_days, remit_address, is_active, org_id, seq)
		VALUES (next_vendor_no(), $1, $2, $3::jsonb, TRUE, $4::uuid, (SELECT next_value FROM vseq))
		RETURNING id::text, vendor_no::text, name, payment_terms_days, COALESCE(remit_address->>'email',''), is_active,
		          is_t5018_subcontractor, COALESCE(business_number, '')`,
		name, terms, remit, orgID).Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.OrderEmail, &v.IsActive,
		&v.IsT5018Subcontractor, &v.BusinessNumber)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The minted number is already a row (planted by a hand or a load): the sequence has moved
		// on, so a retry draws the next one. Said in words, never the raw constraint as a 500.
		return VendorMaster{}, conflict("purchasing: the vendor number the mint drew is already taken, try again")
	}
	if err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: insert vendor: %w", err)
	}
	return v, nil
}

// UpdateVendorMaster applies the named corrections in one statement that also draws a fresh seq, so
// the edit reaches a synced device. The email arm merges (or removes) only the email key: the remit
// blob is free form and other hands may have planted keys in it, so no edit here replaces the lot.
//
// THE ORG TEST LIVES IN THE WHERE, not in a read before it: a row stamped with another org matches
// nothing and answers ErrUnknownVendor, the same 404 an unknown id gets, and a carried row (org_id
// NULL, 0213) is claimed for the caller's org in the same statement, so two orgs editing one carried
// row cannot both win.
func (r *Repository) UpdateVendorMaster(ctx context.Context, orgID, vendorID string, name *string, terms *int, email *string, isActive *bool) (VendorMaster, error) {
	if r.pool == nil {
		return VendorMaster{}, errNoDB
	}
	// The email rides as a bound parameter on every arm, never as statement text: $5 says whether
	// the body named the email at all, $6 is the address ('' clears it).
	setEmail, address := email != nil, ""
	if setEmail {
		address = *email
	}
	var v VendorMaster
	// The vendors counter first, without advancing it, then the row (a load batch holds the counter before its
	// item rows, LD-5; PR 556 round 3e).
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: update vendor: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := LockVendorsSeqTx(ctx, tx); err != nil {
		return VendorMaster{}, err
	}
	err = tx.QueryRow(ctx, `
		UPDATE vendors SET
			name = COALESCE($2, name),
			payment_terms_days = COALESCE($3, payment_terms_days),
			remit_address = CASE
				WHEN NOT $5::boolean THEN remit_address
				WHEN $6::text = '' THEN remit_address - 'email'
				ELSE remit_address || jsonb_build_object('email', $6::text)
			END,
			is_active = COALESCE($4, is_active),
			org_id = COALESCE(org_id, $7::uuid),
			updated_at = now()
		WHERE id = $1::uuid AND (org_id IS NULL OR org_id = $7::uuid)
		RETURNING id::text, vendor_no::text, name, payment_terms_days, COALESCE(remit_address->>'email',''), is_active,
		          is_t5018_subcontractor, COALESCE(business_number, '')`,
		vendorID, name, terms, isActive, setEmail, address, orgID).Scan(&v.ID, &v.VendorNo, &v.Name, &v.PaymentTermsDays, &v.OrderEmail, &v.IsActive,
		&v.IsT5018Subcontractor, &v.BusinessNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return VendorMaster{}, ErrUnknownVendor
	}
	if err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: update vendor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VendorMaster{}, fmt.Errorf("purchasing: update vendor: %w", err)
	}
	return v, nil
}
