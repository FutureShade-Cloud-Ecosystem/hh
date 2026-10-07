// M2 DEBT: every cross-table write below is one the future M2 order/invoice/AR/stock modules will own.
// They are isolated in this one file behind the posStore interface so they can be re-homed without
// touching the service. No `payments` INSERT lives here, that is A-03's (till.TakeTender).
package pos

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements posStore over pgx. The constructor is the stable surface.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository wraps a pool (may be nil in scaffold/offline mode). The Sale opens a tx from this pool.
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Pool exposes the pool so callers can compose the sale tx if they need to.
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

var errNoPool = errors.New("pos: no database pool configured")

// RunInTx opens ONE transaction from the pool and runs fn; it commits on success, rolls back on error.
func (r *Repository) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if r.pool == nil {
		return errNoPool
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// -- idempotency precheck --------------------------------------------------

func (r *Repository) FindSaleByKey(ctx context.Context, key string) (Sale, bool, error) {
	if r.pool == nil {
		return Sale{}, false, errNoPool
	}
	// gateway_ref is read back from the authoritative payments row (A-03 is the sole writer) so a replayed
	// card sale surfaces the same gateway_ref as the original; cash/cheque/account carry none → "".
	// DIB-488 rev 6: the replay also reads back the 0168 order facts (po_job_ref / salesperson, with
	// the display name joined for the sold panel) and the cash-tendered amount parked in the order
	// metadata, so a replayed ring answers VERBATIM, change due included.
	const q = `
SELECT ps.order_id::text, o.order_no, COALESCE(ps.invoice_id::text,''), COALESCE(i.invoice_no,''),
       COALESCE(ps.payment_id::text,''), COALESCE(pay.gateway_ref,''), ps.tender, ps.sale_type, ps.tax_reason_code,
       o.subtotal_cents, o.tax_cents, o.total_cents,
       COALESCE(o.po_job_ref,''), COALESCE(o.salesperson_id::text,''), COALESCE(u.display_name,''),
       COALESCE((o.metadata->>'cash_tendered_cents')::bigint, 0),
       COALESCE(o.job_address,''), COALESCE(o.job_reference,'')
  FROM pos_sale ps
  JOIN orders o ON o.id = ps.order_id
  LEFT JOIN invoices i ON i.id = ps.invoice_id
  LEFT JOIN payments pay ON pay.id = ps.payment_id
  LEFT JOIN users u ON u.id = o.salesperson_id
 WHERE ps.idempotency_key = $1`
	var s Sale
	err := r.pool.QueryRow(ctx, q, key).Scan(
		&s.OrderID, &s.OrderNo, &s.InvoiceID, &s.InvoiceNo, &s.PaymentID, &s.GatewayRef,
		&s.Tender, &s.SaleType, &s.TaxReasonCode, &s.SubtotalCents, &s.TaxCents, &s.TotalCents,
		&s.POJobRef, &s.SalespersonID, &s.SalespersonName, &s.CashTenderedCents,
		&s.JobAddress, &s.JobReference, // DIB-488 rev 8 (0170): job facts replay verbatim too
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sale{}, false, nil
	}
	if err != nil {
		return Sale{}, false, err
	}
	if s.CashTenderedCents > 0 {
		s.ChangeDueCents = s.CashTenderedCents - s.TotalCents
	}
	s.Status = "COMMITTED"
	return s, true, nil
}

// -- credit-gate + pricing reads -------------------------------------------

// readCustomerQuery is one customer's credit facts: the terms, the limit and
// the running balance (the latest customer_transactions balance_after). The
// pool read and the open-transaction twin (ReadCustomerTx) share it, the
// findReturnByKeyQuery pattern.
const readCustomerQuery = `
SELECT c.id::text, c.customer_type, c.payment_terms_days, c.credit_limit_cents,
       COALESCE((SELECT t.balance_after FROM customer_transactions t
                  WHERE t.customer_id = c.id ORDER BY t.occurred_at DESC LIMIT 1), 0)
  FROM customers c WHERE c.id = $1`

func (r *Repository) ReadCustomer(ctx context.Context, customerID string) (customerRec, error) {
	if r.pool == nil {
		return customerRec{}, errNoPool
	}
	var c customerRec
	err := r.pool.QueryRow(ctx, readCustomerQuery, customerID).Scan(
		&c.ID, &c.CustomerType, &c.PaymentTermsDays, &c.CreditLimitCents, &c.OpenBalanceCents,
	)
	return c, err
}

func (r *Repository) ResolveWalkIn(ctx context.Context) (string, error) {
	if r.pool == nil {
		return "", errNoPool
	}
	var id string
	err := r.pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE account_no = 'WALK-IN'`).Scan(&id)
	return id, err
}

func (r *Repository) ReadStockOnHand(ctx context.Context, productID string) (map[string]float64, error) {
	out := map[string]float64{}
	if r.pool == nil {
		return out, errNoPool
	}
	rows, err := r.pool.Query(ctx,
		`SELECT location_id::text, on_hand_base - qty_reserved FROM stock_balances WHERE product_id = $1`, productID) // UNION-W4 (B E3): the POS glance shows AVAILABLE, a fully hard-reserved product must not read as sellable
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var loc string
		var qty float64
		if err := rows.Scan(&loc, &qty); err != nil {
			return out, err
		}
		out[loc] = qty
	}
	return out, rows.Err()
}

// ReadStockUnitMeta (DIB-488) reads the unit facts the stock-status line needs to speak the glance's
// base-unit availability in the item's SELL unit, plus the dealer's authored low thresholds. Same
// M2-debt posture as ReadStockOnHand directly above (and the products read in repository_w7.go:
// parkedProductName): an isolated cross-module READ, display-only, never a decrement.
func (r *Repository) ReadStockUnitMeta(ctx context.Context, productID string) (baseUOM, sellUOM string, sellFactor float64, lowBase map[string]float64, err error) {
	lowBase = map[string]float64{}
	if r.pool == nil {
		return "", "", 0, lowBase, errNoPool
	}
	// The sell unit is the product's declared sold_by, else its stocking base unit, the SAME
	// fallback the terminal's add-line path uses, so the status line and the qty box agree.
	err = r.pool.QueryRow(ctx,
		`SELECT base_uom, COALESCE(NULLIF(sold_by,''), base_uom) FROM products WHERE id::text = $1`,
		productID).Scan(&baseUOM, &sellUOM)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", 0, lowBase, clientErr(http.StatusNotFound, "pos: no such product")
	}
	if err != nil {
		return "", "", 0, lowBase, err
	}
	if sellUOM == baseUOM {
		sellFactor = 1
	} else {
		// product_uoms: how many BASE units is one sell unit. No row → factor stays 0 ("no
		// conversion on file") and the client shows the base unit rather than dividing by a guess.
		var f float64
		e := r.pool.QueryRow(ctx,
			`SELECT qty_in_base::float8 FROM product_uoms WHERE product_id::text = $1 AND uom_code = $2`,
			productID, sellUOM).Scan(&f)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return "", "", 0, lowBase, e
		}
		sellFactor = f
	}
	rows, err := r.pool.Query(ctx,
		`SELECT branch_id::text, reorder_point_base::float8
		   FROM product_reorder_points WHERE product_id::text = $1 AND is_active`, productID)
	if err != nil {
		return "", "", 0, lowBase, err
	}
	defer rows.Close()
	for rows.Next() {
		var branch string
		var point float64
		if err := rows.Scan(&branch, &point); err != nil {
			return "", "", 0, lowBase, err
		}
		lowBase[branch] = point
	}
	return baseUOM, sellUOM, sellFactor, lowBase, rows.Err()
}

func (r *Repository) ReadEligibilityProfileMasked(ctx context.Context, profileID string) (eligibilityMasked, error) {
	if r.pool == nil {
		return eligibilityMasked{}, errNoPool
	}
	// NEVER selects proof_identifier_enc, the cashier only ever sees the mask.
	const q = `
SELECT id::text, purchaser_type, proof_identifier_mask, COALESCE(ontario_eligibility,'UNKNOWN'),
       eligibility_type, COALESCE(tcrd_expiry < $2::date, FALSE)
  FROM purchaser_eligibility_profile WHERE id = $1`
	// The certificate lapses with the dealer's day, so today is decided in Go in the house zone
	// and bound, not read from CURRENT_DATE, which is the database session's zone.
	houseDay := houseday.Today(nil)
	var p eligibilityMasked
	err := r.pool.QueryRow(ctx, q, profileID, houseDay).Scan(
		&p.ProfileID, &p.PurchaserType, &p.MaskedIdentifier, &p.OntarioEligibility, &p.EligibilityType, &p.Expired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return eligibilityMasked{}, nil
	}
	return p, err
}

// -- operator reopen -------------------------------------------------------

func (r *Repository) GetSaleView(ctx context.Context, id string) (SaleView, bool, error) {
	if r.pool == nil {
		return SaleView{}, false, errNoPool
	}
	const q = `
SELECT ps.id::text, o.id::text, o.order_no, o.customer_id::text, o.branch_id::text, ps.sale_type, ps.tender,
       o.status, o.subtotal_cents, o.tax_cents, o.total_cents, ps.tax_reason_code,
       COALESCE(ps.invoice_id::text,''), COALESCE(i.invoice_no,''), COALESCE(ps.payment_id::text,''),
       ps.split_tender
  FROM pos_sale ps
  JOIN orders o ON o.id = ps.order_id
  LEFT JOIN invoices i ON i.id = ps.invoice_id
 WHERE ps.id = $1 OR ps.order_id = $1`
	var v SaleView
	var posSaleID string
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&posSaleID, &v.OrderID, &v.OrderNo, &v.CustomerID, &v.BranchID, &v.SaleType, &v.Tender,
		&v.Status, &v.SubtotalCents, &v.TaxCents, &v.TotalCents, &v.TaxReasonCode,
		&v.InvoiceID, &v.InvoiceNo, &v.PaymentID, &v.SplitTender,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SaleView{}, false, nil
	}
	if err != nil {
		return SaleView{}, false, err
	}
	// DIB-165: the settlement legs. Only a split carries rows here, so a single-tender reopen does
	// one extra indexed lookup that returns nothing and the view is byte-identical to before.
	if v.SplitTender {
		legs, lerr := r.readSaleTenders(ctx, posSaleID)
		if lerr != nil {
			return SaleView{}, false, lerr
		}
		v.Tenders = legs
	}
	rows, err := r.pool.Query(ctx,
		`SELECT line_no, COALESCE(product_id::text,''), description, qty_entered, uom_entered,
		        unit_price_cents, price_uom, line_total_cents, discount_cents, sale_discount_cents
		   FROM order_lines WHERE order_id = $1 ORDER BY line_no`, v.OrderID)
	if err != nil {
		return SaleView{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var ln SaleViewLine
		if err := rows.Scan(&ln.LineNo, &ln.ProductID, &ln.Description, &ln.QtyEntered, &ln.UOMEntered,
			&ln.UnitPriceCents, &ln.PriceUOM, &ln.LineTotalCents, &ln.DiscountCents, &ln.SaleDiscountCents); err != nil {
			return SaleView{}, false, err
		}
		v.Lines = append(v.Lines, ln)
	}
	if err := rows.Err(); err != nil {
		return SaleView{}, false, err
	}
	rows.Close()
	// The manual discounts and who approved them, so a reopen and a reprint show the same receipt.
	ds, err := r.ReadSaleDiscounts(ctx, v.OrderID)
	if err != nil {
		return SaleView{}, false, err
	}
	for _, d := range ds {
		v.DiscountCents += d.AmountCents
	}
	if len(ds) > 0 {
		v.Discounts = ds
	}
	return v, true, nil
}

// -- the M2-debt writes (inside the Sale tx) -------------------------------

func (r *Repository) InsertOrder(ctx context.Context, tx pgx.Tx, o orderRow) (string, string, error) {
	meta, err := json.Marshal(o.Metadata)
	if err != nil {
		return "", "", err
	}
	// DIB-388: the ring draws its number from the branch-coded mint (migration 0087's next_order_no,
	// $2 = branch_id) rather than the global '#SO-' || nextval sequence, one mint for all three order
	// writers (ring, reorder, quote conversion), so the three-way format drift cannot recur.
	// DIB-488 rev 6 (0168): po_job_ref + salesperson_id ride the same insert, validated in the
	// service (422 in words) before the CHECK/FK could 500.
	// DIB-488 rev 8 (0170): job_address + job_reference join the same insert, same posture.
	const q = `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents, created_by, metadata,
                    po_job_ref, salesperson_id, job_address, job_reference)
VALUES (next_order_no($2), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING id::text, order_no`
	var id, no string
	err = tx.QueryRow(ctx, q, o.CustomerID, o.BranchID, o.Status, o.Fulfillment, nullStr(o.RequestedDate),
		o.SubtotalCents, o.TaxCents, o.TotalCents, nullStr(o.CreatedBy), meta,
		o.POJobRef, nullStr(o.SalespersonID), o.JobAddress, o.JobReference).Scan(&id, &no)
	return id, no, err
}

func (r *Repository) InsertOrderLines(ctx context.Context, tx pgx.Tx, orderID string, lines []orderLineRow) ([]string, error) {
	ids := make([]string, 0, len(lines))
	for _, l := range lines {
		const q = `
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered, qty_base,
                         unit_price_cents, price_uom, line_total_cents, tax_cents,
                         discount_cents, discount_percent_bps, discount_approved_by, discount_approved_role,
                         sale_discount_cents, discount_approved_qty, discount_approved_base_cents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING id::text`
		var id string
		if err := tx.QueryRow(ctx, q, orderID, l.LineNo, nullStr(l.ProductID), l.Description, l.QtyEntered,
			l.UOMEntered, l.QtyBase, l.UnitPriceCents, l.PriceUOM, l.LineTotalCents, l.TaxCents,
			l.DiscountCents, nullInt(l.DiscountPercentBps), nullStr(l.DiscountApprovedBy),
			nullStr(l.DiscountRoleID), l.SaleDiscountCents, nullFloat(l.DiscountApprovedQty),
			nullInt64(l.DiscountApprovedBase)).Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *Repository) InsertInvoice(ctx context.Context, tx pgx.Tx, inv invoiceRow) (string, string, error) {
	// The 0213 sync legs stamp in the same INSERT (the invoices feed
	// seq, the org off the branch's location): the ring's document is
	// the payer's document, and a sale no pull can see is a feed that
	// lies by omission, the posture the billing writer carries on the
	// same legs.
	const q = `
WITH iseq AS (INSERT INTO scope_sequences (scope) VALUES ('invoices')
              ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1
              RETURNING next_value)
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, created_by,
                      org_id, seq)
VALUES ('#INV-' || nextval('invoice_no_seq'), $1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10,
        (SELECT org_id FROM locations WHERE id = $3::uuid),
        (SELECT next_value FROM iseq))
RETURNING id::text, invoice_no`
	var id, no string
	err := tx.QueryRow(ctx, q, inv.CustomerID, nullStr(inv.OrderID), inv.BranchID, inv.Status, inv.DueDate,
		inv.SubtotalCents, inv.TaxCents, inv.TotalCents, inv.BalanceCents, nullStr(inv.CreatedBy)).Scan(&id, &no)
	return id, no, err
}

func (r *Repository) InsertInvoiceLines(ctx context.Context, tx pgx.Tx, invoiceID string, orderLineIDs []string, lines []orderLineRow) error {
	for i, l := range lines {
		var orderLineID any
		if i < len(orderLineIDs) {
			orderLineID = orderLineIDs[i]
		}
		const q = `
INSERT INTO invoice_lines (invoice_id, line_no, order_line_id, product_id, description, qty_entered,
                           uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		if _, err := tx.Exec(ctx, q, invoiceID, l.LineNo, orderLineID, nullStr(l.ProductID), l.Description,
			l.QtyEntered, l.UOMEntered, l.QtyBase, l.UnitPriceCents, l.PriceUOM, l.LineTotalCents, l.TaxCents); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) InsertPaymentAllocation(ctx context.Context, tx pgx.Tx, paymentID, invoiceID string, amountCents int64, allocatedBy string) error {
	// allocated_by (0043, Q-044): WHO matched this payment to this invoice, stamped on the allocation.
	_, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, allocated_by) VALUES ($1, $2, $3, $4)`,
		paymentID, invoiceID, amountCents, nullStr(allocatedBy))
	return err
}

func (r *Repository) InsertCustomerTxn(ctx context.Context, tx pgx.Tx, t customerTxnRow) error {
	// occurred_at = clock_timestamp(), NOT the default now(): a deposit sale writes INVOICE + PAYMENT
	// in ONE tx, and now() is transaction-fixed, two rows with identical occurred_at made the
	// latest-balance read (ReadCustomer) tie-break arbitrarily and report a stale running balance.
	// clock_timestamp() advances within the tx, so insert order IS the ledger order.
	const q = `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, clock_timestamp())`
	_, err := tx.Exec(ctx, q, t.CustomerID, t.TxnType, t.AmountCents, t.BalanceAfter, t.SourceType, t.SourceRefID, nullStr(t.BranchID))
	return err
}

func (r *Repository) InsertPosSale(ctx context.Context, tx pgx.Tx, s posSaleRow) (string, error) {
	const q = `
INSERT INTO pos_sale (idempotency_key, order_id, invoice_id, payment_id, branch_id, till_session_id,
                      sale_type, cash_sale, tender, tax_reason_code, offline_origin, created_by,
                      split_tender, discount_cents, discount_percent_bps, discount_approved_by,
                      discount_approved_role, discount_approved_base_cents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING id::text`
	var id string
	err := tx.QueryRow(ctx, q, s.IdempotencyKey, s.OrderID, nullStr(s.InvoiceID), nullStr(s.PaymentID),
		s.BranchID, nullStr(s.TillSessionID), s.SaleType, s.CashSale, s.Tender, s.TaxReasonCode,
		s.OfflineOrigin, nullStr(s.CreatedBy), s.SplitTender, s.Discount.AmountCents,
		nullInt(s.Discount.PercentBps), nullStr(s.Discount.ApprovedBy), nullStr(s.Discount.RoleID), nullInt64(s.Discount.ApprovedBase)).Scan(&id)
	return id, err
}

// InsertSaleTenders writes the split-tender legs (0100). It runs inside the sale tx: the deferred
// constraint trigger on pos_sale_tenders evaluates the sum at COMMIT, so a leg list that does not
// settle the sale exactly aborts the whole sale, no half-written split can reach the drawer.
func (r *Repository) InsertSaleTenders(ctx context.Context, tx pgx.Tx, posSaleID string, legs []saleTenderRow) error {
	const q = `
INSERT INTO pos_sale_tenders (pos_sale_id, seq, method, amount_cents, payment_id, till_session_id, gateway_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7)`
	for _, l := range legs {
		if _, err := tx.Exec(ctx, q, posSaleID, l.Seq, l.Method, l.AmountCents,
			nullStr(l.PaymentID), nullStr(l.TillSessionID), l.GatewayRef); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) InsertTaxSnapshot(ctx context.Context, tx pgx.Tx, snap taxSnapshotRow) (string, error) {
	const q = `
INSERT INTO tax_decision_snapshot (order_id, order_line_id, tax_reason_code, rule_version, rate_bps,
                                   full_hst_cents, on_credit_cents, net_collected_cents, automated,
                                   evidence_doc_id, signature_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id::text`
	var id string
	err := tx.QueryRow(ctx, q, snap.OrderID, nullStr(snap.OrderLineID), snap.TaxReasonCode, snap.RuleVersion,
		snap.RateBps, snap.FullHSTCents, snap.OnCreditCents, snap.NetCollectedCents, snap.Automated,
		nullStr(snap.EvidenceDocID), nullStr(snap.SignatureID)).Scan(&id)
	return id, err
}

func (r *Repository) EmitEvent(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, b)
	return err
}

// InsertSignature writes the signature row on the SHARED POOL: the standalone capture's insert.
func (r *Repository) InsertSignature(ctx context.Context, sig signatureRow) (string, error) {
	return r.InsertSignatureTx(ctx, nil, sig)
}

// InsertSignatureTx is InsertSignature joined to the caller's transaction (nil tx means the pool,
// the shape of the tree's other store seams). This is the module's ONE pos_signature insert: the
// standalone capture and the tx capture (the dispatch pickups confirm, through the provider) both
// persist through it, so the two copies the tx path used to carry inline (an edit to one silently
// diverging from the other) cannot come back, and a signature row reads the same however it was
// captured.
func (r *Repository) InsertSignatureTx(ctx context.Context, tx pgx.Tx, sig signatureRow) (string, error) {
	if r.pool == nil {
		return "", errNoPool
	}
	const q = `
INSERT INTO pos_signature (purpose, ref_type, ref_id, signature_enc, provider, captured_by)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`
	var id string
	var err error
	if tx != nil {
		err = tx.QueryRow(ctx, q, sig.Purpose, sig.RefType, sig.RefID, sig.SignatureEnc, sig.Provider,
			nullStr(sig.CapturedBy)).Scan(&id)
	} else {
		err = r.pool.QueryRow(ctx, q, sig.Purpose, sig.RefType, sig.RefID, sig.SignatureEnc, sig.Provider,
			nullStr(sig.CapturedBy)).Scan(&id)
	}
	return id, err
}

// -- DIB-488 rev 6 (item D): the salesperson reads ---------------------------
//
// Same M2-debt posture as ReadStockOnHand / parkedProductName: isolated cross-module READS of the
// identity-owned users table, display-only, never a write. identity stays the users owner.

// ReadStaffName resolves an ACTIVE staff member's display name, the salesperson validation read
// (a departed or unknown id is 422-in-words in the service, never an FK 500 mid-tx).
func (r *Repository) ReadStaffName(ctx context.Context, userID string) (string, bool, error) {
	if r.pool == nil {
		return "", false, errNoPool
	}
	var name string
	err := r.pool.QueryRow(ctx,
		`SELECT display_name FROM users WHERE id::text = $1 AND status = 'ACTIVE'`, userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// ListSalespeople lists the ACTIVE, ROLE-HOLDING staff for the counter's salesperson selector -
// id + display name only. The user_roles join is what makes this a STAFF list rather than "every
// active users row" (fixture/service accounts carry no role and must not clutter a counter
// selector); the signed-in operator is guaranteed client-side regardless.
func (r *Repository) ListSalespeople(ctx context.Context) ([]Salesperson, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT u.id::text, u.display_name
		   FROM users u JOIN user_roles ur ON ur.user_id = u.id
		  WHERE u.status = 'ACTIVE' ORDER BY u.display_name, u.id::text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Salesperson
	for rows.Next() {
		var s Salesperson
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListDiscountStaff reads the logins the counter discount's approver rule may judge: ACTIVE staff
// with one live role grant that reaches the branch (a global role, or a grant on that branch), one
// row per grant in tier order so a login's highest role leads. userID narrows to one login. The
// platform grant tables are the authority every guard resolves from (D3); the dealer's list of
// who may approve is NOT here, it is the service's read of the role configuration. The org
// predicate is the request org the guard resolved (the scope every grant read carries), and
// falls back to the branch's own org only when the call carries none.
func (r *Repository) ListDiscountStaff(ctx context.Context, orgID, branchID, userID string) ([]discountStaffRow, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
SELECT u.id::text, u.display_name, sr.id, sr.label, sr.tier::int
  FROM users u
  JOIN staff_role_grant g ON g.platform_user_id = u.platform_user_id AND g.deleted_at IS NULL
  JOIN staff_role sr ON sr.id = g.role_id AND sr.deleted_at IS NULL
 WHERE u.status = 'ACTIVE' AND u.deleted_at IS NULL AND u.platform_user_id IS NOT NULL
   AND g.org_id = COALESCE(NULLIF($3, '')::uuid, (SELECT l.org_id FROM locations l WHERE l.id = $1::uuid))
   AND (sr.scope = 'global' OR g.branch_id = $1::uuid)
   AND ($2 = '' OR u.id::text = $2)
 ORDER BY u.display_name, u.id::text, sr.tier, sr.id`, branchID, userID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []discountStaffRow
	for rows.Next() {
		var d discountStaffRow
		if err := rows.Scan(&d.UserID, &d.Name, &d.RoleID, &d.RoleLabel, &d.Tier); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReadSaleDiscounts reads the manual discounts one committed sale carries, lines first in line
// order then the ticket discount, the role labels read from the role catalogue by the role the
// approver held at the time. A sale with none answers an empty list.
func (r *Repository) ReadSaleDiscounts(ctx context.Context, orderID string) ([]SaleDiscount, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
SELECT 'LINE', ol.line_no, COALESCE(ol.discount_percent_bps, 0), ol.discount_cents,
       ol.discount_approved_by::text, ol.discount_approved_role, COALESCE(sr.label, ol.discount_approved_role)
  FROM order_lines ol
  LEFT JOIN staff_role sr ON sr.id = ol.discount_approved_role
 WHERE ol.order_id = $1 AND ol.discount_cents > 0
UNION ALL
SELECT 'SALE', 0, COALESCE(ps.discount_percent_bps, 0), ps.discount_cents,
       ps.discount_approved_by::text, ps.discount_approved_role, COALESCE(sr.label, ps.discount_approved_role)
  FROM pos_sale ps
  LEFT JOIN staff_role sr ON sr.id = ps.discount_approved_role
 WHERE ps.order_id = $1 AND ps.discount_cents > 0
 ORDER BY 1, 2`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SaleDiscount
	for rows.Next() {
		var d SaleDiscount
		if err := rows.Scan(&d.Scope, &d.LineNo, &d.PercentBps, &d.AmountCents,
			&d.ApprovedBy, &d.ApprovedByRole, &d.ApprovedByLabel); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// nullFloat and nullInt64 map 0 → SQL NULL for the optional approval columns.
func nullFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func nullInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// nullInt maps 0 → SQL NULL for the optional percent column.
func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// nullStr maps "" → SQL NULL so optional UUID/text columns are not fed an empty string.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// compile-time assertion: *Repository satisfies the service's store seam.
var _ posStore = (*Repository)(nil)
