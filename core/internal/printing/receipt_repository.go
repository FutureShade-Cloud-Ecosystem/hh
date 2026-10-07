package printing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/jackc/pgx/v5"
)

// receiptSale is the committed sale's header as the receipt needs it. It is read from pos_sale +
// orders (the sale is the counter fact; the order carries the money and the #SO number). The two
// cash facts ride beside the money:
//
//   - CashTenderedCents is the order's own cash-tendered fact (orders.metadata), the same column
//     pos's replay read serves, so the receipt's change due is the ring's own figure and never a
//     recomputation from the tender rows.
//   - DepositCents is the money the ring itself captured on an unpaid-at-ring sale: the payments
//     allocated to this invoice whose received_at equals the invoice's created_at. Both stamps are
//     now() of the ring's ONE transaction (a deposit sale writes invoice + payment + allocation in
//     a single tx), so the equality names exactly the ring's own money; a later AR settlement is a
//     different, later transaction and never rides this figure.
type receiptSale struct {
	OrderID           string
	OrderNo           string
	BranchID          string
	InvoiceID         string
	InvoiceNo         string
	PaymentID         string
	Tender            string
	SaleType          string
	Status            string
	SplitTender       bool
	SubtotalCents     int64
	TaxCents          int64
	TotalCents        int64
	BalanceCents      int64
	CashTenderedCents int64
	DepositCents      int64
	SoldAt            time.Time
	CustomerName      string
	CustomerAcct      string
	CashierName       string
	// The whole-ticket manual discount (pos_sale 0365) and the role label of its approver.
	SaleDiscountCents int64
	SaleDiscountLabel string
}

// branchHeader is the letterhead: the branch row from locations plus the org fields from
// system_settings. Nothing here is hardcoded in a template, a branch that moves changes one row.
type branchHeader struct {
	OrgName    string
	Name       string
	Code       string
	Address    string
	HSTRateBps int
	// QuantityNotice is quote_policy.quantity_notice for this branch (branch row over the dealer
	// default), or the house wording when no policy row exists.
	QuantityNotice string
}

// defaultQuantityNotice is the house wording, the same sentence as quote.DefaultQuantityNotice and the
// 0366 column default (a test pins the two equal; printing may not import quote outside tests).
const defaultQuantityNotice = "All quantities are to be confirmed by the customer. Please review them before you accept it."

// ReadReceiptSale reads one COMMITTED sale by pos_sale id, order id, or the VISIBLE "#SO-" number.
// An uncommitted/void sale is NOT a receipt, the read filters on status so no paper is produced for
// one.
//
// THE #SO PATH IS BRANCH-SCOPED AT RESOLUTION (order/repository.go ResolveOrderID, verbatim shape).
// A uuid is unguessable, so a cross-branch uuid is answered 403 by the caller's CanAccessBranch
// check. An "#SO-4" is sequential and trivially guessable, so resolving it unscoped would turn the
// receipt route into an oracle for "does branch X have a sale numbered N". Scoping the RESOLUTION
// makes a cross-branch number a plain not-found instead.
func (r *Repository) ReadReceiptSale(ctx context.Context, id string, global bool, branchIDs []string) (receiptSale, bool, error) {
	if r.pool == nil {
		return receiptSale{}, false, errNoPool
	}
	match, args := `(ps.id = $1::uuid OR ps.order_id = $1::uuid)`, []any{id}
	if !looksLikeUUID(id) {
		no := strings.TrimSpace(id)
		if !strings.HasPrefix(no, "#") {
			no = "#" + no
		}
		// A scoped caller with NO branch grants reaches nothing by number, short-circuit rather
		// than issue a `= ANY('{}')` that can never match.
		if !global && len(branchIDs) == 0 {
			return receiptSale{}, false, nil
		}
		match, args = `o.order_no = $1 AND ($2 OR o.branch_id = ANY($3::uuid[]))`, []any{no, global, branchIDs}
	}
	q := `
SELECT o.id::text, o.order_no, o.branch_id::text,
       COALESCE(ps.invoice_id::text,''), COALESCE(i.invoice_no,''), COALESCE(ps.payment_id::text,''),
       ps.tender, ps.sale_type, ps.status,
       EXISTS(SELECT 1 FROM pos_sale_tenders st WHERE st.pos_sale_id = ps.id),
       o.subtotal_cents, o.tax_cents, o.total_cents, COALESCE(i.balance_cents, 0),
       COALESCE((o.metadata->>'cash_tendered_cents')::bigint, 0),
       COALESCE((SELECT SUM(p.amount_cents) FROM payments p
                  JOIN payment_allocations pa ON pa.payment_id = p.id
                 WHERE pa.invoice_id = i.id AND p.status <> 'VOID'
                   AND p.received_at = i.created_at), 0),
       ps.created_at, COALESCE(c.name,''), COALESCE(c.account_no,''), COALESCE(u.display_name,''),
       ps.discount_cents, COALESCE(dsr.label, ps.discount_approved_role, '')
  FROM pos_sale ps
  LEFT JOIN staff_role dsr ON dsr.id = ps.discount_approved_role
  JOIN orders o ON o.id = ps.order_id
  LEFT JOIN invoices i ON i.id = ps.invoice_id
  LEFT JOIN customers c ON c.id = o.customer_id
  LEFT JOIN users u ON u.id = ps.created_by
 WHERE ` + match + ` AND ps.status = 'COMMITTED'
 ORDER BY ps.created_at DESC
 LIMIT 1`
	var s receiptSale
	err := r.pool.QueryRow(ctx, q, args...).Scan(&s.OrderID, &s.OrderNo, &s.BranchID,
		&s.InvoiceID, &s.InvoiceNo, &s.PaymentID, &s.Tender, &s.SaleType, &s.Status, &s.SplitTender,
		&s.SubtotalCents, &s.TaxCents, &s.TotalCents, &s.BalanceCents,
		&s.CashTenderedCents, &s.DepositCents,
		&s.SoldAt, &s.CustomerName, &s.CustomerAcct, &s.CashierName,
		&s.SaleDiscountCents, &s.SaleDiscountLabel)
	if errors.Is(err, pgx.ErrNoRows) {
		return receiptSale{}, false, nil
	}
	if err != nil {
		return receiptSale{}, false, err
	}
	return s, true, nil
}

// ReadReceiptLines reads the sold lines with the SKU the yard and the customer both point at.
//
// The description FALLS BACK to the product name. A POS ring writes order_lines.description empty
// (the counter sends a product id, not prose), so reading the column alone produces a receipt whose
// items are blank, a document that cannot answer "what did I buy". The catalogue name is the right
// answer, and the stored description still wins when a human wrote one.
func (r *Repository) ReadReceiptLines(ctx context.Context, orderID string) ([]ReceiptLine, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ol.line_no, COALESCE(p.sku,''),
		       COALESCE(NULLIF(btrim(ol.description),''), NULLIF(btrim(p.name),''), 'Item'),
		       ol.qty_entered, ol.uom_entered::text,
		       ol.unit_price_cents, ol.price_uom::text, ol.line_total_cents,
		       ol.discount_cents, COALESCE(dsr.label, ol.discount_approved_role, ''), ol.sale_discount_cents
		  FROM order_lines ol
		  LEFT JOIN products p ON p.id = ol.product_id
		  LEFT JOIN staff_role dsr ON dsr.id = ol.discount_approved_role
		 WHERE ol.order_id = $1::uuid
		 ORDER BY ol.line_no`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReceiptLine{}
	for rows.Next() {
		var l ReceiptLine
		if err := rows.Scan(&l.LineNo, &l.SKU, &l.Description, &l.Qty, &l.UOM,
			&l.UnitPriceCents, &l.PriceUOM, &l.LineTotalCents,
			&l.DiscountCents, &l.DiscountApprovedByLabel, &l.SaleDiscountShareCents); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReadReceiptTenders reads how the sale was paid: the sale's own payment row plus anything else
// allocated to its invoice (a deposit at ring, a later settlement on a COD account). A-03 owns
// payments; this is a read.
func (r *Repository) ReadReceiptTenders(ctx context.Context, paymentID, invoiceID string) ([]ReceiptTender, error) {
	if r.pool == nil {
		return nil, errNoPool
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.method, p.amount_cents, COALESCE(p.card_type,''), p.payment_no, p.received_at,
		       COALESCE(p.gateway_ref, '')
		  FROM payments p
		 WHERE p.status <> 'VOID'
		   AND (p.id = NULLIF($1,'')::uuid
		        OR p.id IN (SELECT pa.payment_id FROM payment_allocations pa
		                     WHERE pa.invoice_id = NULLIF($2,'')::uuid))
		 ORDER BY p.received_at, p.payment_no`, paymentID, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReceiptTender{}
	for rows.Next() {
		var t ReceiptTender
		var cardType, paymentNo, gatewayRef string
		var receivedAt time.Time
		if err := rows.Scan(&t.Method, &t.AmountCents, &cardType, &paymentNo, &receivedAt, &gatewayRef); err != nil {
			return nil, err
		}
		t.Label = t.Method
		if t.Method == "CARD" && cardType != "" {
			t.Label = "CARD · " + cardType
		}
		t.Ref = paymentNo
		// The word follows the ROW's own ref: a CARD capture whose gateway ref the receipt can
		// name as a simulated rail's mint never reached a processor, and the paper says so
		// beside the tender. TWO rails' stubs mint such refs, each stamped by its own
		// construction: the TILL's capture stub by its exported prefix
		// (payment.StubCaptureRefPrefix), and the PORTAL rail's own stub (payments.StubGateway,
		// the default gateway) by its exact mint shape (payments.IsStubChargeRef: the prefix
		// plus exactly 16 lowercase hex chars, a construction whose only minter in the
		// repository is that stub's own mint). The exact shape is the discriminator, not a bare
		// prefix: the live gateways write their own refs (the Run adapter persists the
		// processor's TransID verbatim), a shape not ours to bound, so a live charge stamps
		// nothing and a reprint stays honest even after a box switches rails.
		t.Simulated = (t.Method == "CARD" && strings.HasPrefix(gatewayRef, payment.StubCaptureRefPrefix)) ||
			(t.Method == "CARD" && payments.IsStubChargeRef(gatewayRef))
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReadBranchHeader reads the letterhead for one branch: the locations row + the org-level
// system_settings fields (org name, HST rate). A missing org setting degrades to a sane default
// rather than failing the print, a receipt without a letterhead still beats no receipt.
func (r *Repository) ReadBranchHeader(ctx context.Context, branchID string) (branchHeader, error) {
	if r.pool == nil {
		return branchHeader{}, errNoPool
	}
	h := branchHeader{HSTRateBps: 1300, QuantityNotice: defaultQuantityNotice}
	var addr []byte
	err := r.pool.QueryRow(ctx,
		`SELECT name, code::text, address FROM locations WHERE id = $1::uuid`, branchID).
		Scan(&h.Name, &h.Code, &addr)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return branchHeader{}, err
	}
	h.Address = formatAddress(addr)

	// The dealer's quantity notice: the branch's own row over the dealer default (the 0043 idiom
	// quote_policy follows), read only. No row at all keeps the house wording.
	if err := r.pool.QueryRow(ctx, `
		SELECT quantity_notice FROM quote_policy
		 WHERE branch_id = $1::uuid OR branch_id IS NULL
		 ORDER BY branch_id NULLS LAST LIMIT 1`, branchID).Scan(&h.QuantityNotice); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return branchHeader{}, err
	}

	rows, err := r.pool.Query(ctx,
		`SELECT key::text, value FROM system_settings WHERE key IN ('org_name','hst_rate_bps')`)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return h, err
		}
		switch k {
		case "org_name":
			var s string
			if json.Unmarshal(v, &s) == nil {
				h.OrgName = s
			}
		case "hst_rate_bps":
			var n int
			if json.Unmarshal(v, &n) == nil && n > 0 {
				h.HSTRateBps = n
			}
		}
	}
	return h, rows.Err()
}

// formatAddress renders locations.address (a jsonb blob) as one human line. Unknown/absent keys are
// simply skipped, the header degrades, it never renders "<nil>" at a customer.
func formatAddress(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var a struct {
		Line1    string `json:"line1"`
		Line2    string `json:"line2"`
		City     string `json:"city"`
		Province string `json:"province"`
		Postal   string `json:"postal"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return ""
	}
	parts := []string{}
	for _, p := range []string{a.Line1, a.Line2, a.City, a.Province, a.Postal} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	return strings.Join(parts, ", ")
}

// looksLikeUUID is the cheap shape check the id-vs-order_no fork keys off (the order module's
// looksLikeUUID, same purpose: decide which column to match without a failed uuid cast).
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
