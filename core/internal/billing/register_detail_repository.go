package billing

// register_detail_repository.go, the invoice DETAIL read (DIB-510): one document, everything the
// right-hand panel shows, in one round trip.
//
// ONE READ, NOT SIX. The panel is one thing an operator opens; six requests is six chances to paint
// half a document, and the half that arrives last is always the one with the money on it. The cost is
// a handful of small indexed queries inside one method instead of one enormous JOIN, deliberately,
// because a five-way LEFT JOIN with two one-to-many legs (lines and photos) multiplies rows and the
// de-duplication that follows is where this sort of code goes wrong.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// InvoiceHeaderFacts is the small header read the detail, the document renderer and every write path
// share, so "which invoice, whose, how much is left" has one spelling.
type InvoiceHeaderFacts struct {
	ID            string
	InvoiceNo     string
	DocType       string
	CustomerID    string
	BranchID      string
	StoredStatus  string
	IssueDate     time.Time
	DueDate       time.Time
	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	BalanceCents  int64
	HoldbackCents int64
	OrderID       string
}

// InvoiceHeader reads one document's header. ErrNotFound when it does not exist, never a different
// answer for "exists but you may not see it", because a distinguishable 403 is an existence oracle.
func (r *Repository) InvoiceHeader(ctx context.Context, invoiceID string) (InvoiceHeaderFacts, error) {
	if r.pool == nil {
		return InvoiceHeaderFacts{}, errNoDB
	}
	var h InvoiceHeaderFacts
	var orderID *string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, invoice_no::text, doc_type, customer_id::text, branch_id::text, status,
		        issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents,
		        holdback_cents, order_id::text
		   FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&h.ID, &h.InvoiceNo, &h.DocType, &h.CustomerID, &h.BranchID, &h.StoredStatus,
			&h.IssueDate, &h.DueDate, &h.SubtotalCents, &h.TaxCents, &h.TotalCents,
			&h.BalanceCents, &h.HoldbackCents, &orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvoiceHeaderFacts{}, ErrNotFound
		}
		return InvoiceHeaderFacts{}, fmt.Errorf("billing: read invoice header: %w", err)
	}
	if orderID != nil {
		h.OrderID = *orderID
	}
	return h, nil
}

// DetailLines reads the line table, with the SKU and the has-a-primary-image flag the thumbnail
// component wants. The flag rather than the bytes: the image route is product's and stays product's.
func (r *Repository) DetailLines(ctx context.Context, invoiceID string) ([]DetailLine, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx,
		`SELECT il.line_no, COALESCE(il.product_id::text,''), COALESCE(p.sku::text,''),
		        EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = il.product_id AND pi.sort_order = 0),
		        il.description, il.qty_entered::text, il.uom_entered::text,
		        il.unit_price_cents, il.price_uom::text, il.line_total_cents
		   FROM invoice_lines il
		   LEFT JOIN products p ON p.id = il.product_id
		  WHERE il.invoice_id = $1::uuid
		  ORDER BY il.line_no`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("billing: read invoice lines: %w", err)
	}
	defer rows.Close()
	out := []DetailLine{}
	for rows.Next() {
		var l DetailLine
		var hasImage bool
		if err := rows.Scan(&l.LineNo, &l.ProductID, &l.SKU, &hasImage, &l.Description,
			&l.QtyEntered, &l.UOMEntered, &l.UnitPriceCents, &l.PriceUOM, &l.LineTotalCents); err != nil {
			return nil, fmt.Errorf("billing: scan invoice line: %w", err)
		}
		if hasImage {
			l.ImageRef = l.ProductID
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AppliedCents is the sum of what has actually been APPLIED to this document, the stored
// payment_allocations aggregate. See the Totals doc comment for why this is not total − balance.
func (r *Repository) AppliedCents(ctx context.Context, invoiceID string) (int64, error) {
	if r.pool == nil {
		return 0, errNoDB
	}
	var sum int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_cents),0) FROM payment_allocations WHERE invoice_id = $1::uuid`,
		invoiceID).Scan(&sum); err != nil {
		return 0, fmt.Errorf("billing: applied cents: %w", err)
	}
	return sum, nil
}

// CustomerBlockFor reads the identity card: the account facts plus the PRIMARY contact. A customer
// with no contact row renders name-and-account only, which is the truth rather than a blank field
// labelled "Contact".
func (r *Repository) CustomerBlockFor(ctx context.Context, customerID string) (CustomerBlock, bool, bool, error) {
	if r.pool == nil {
		return CustomerBlock{}, false, false, errNoDB
	}
	var b CustomerBlock
	var onHold, disputed bool
	var email, phone, contact *string
	err := r.pool.QueryRow(ctx,
		`SELECT c.id::text, c.name, c.account_no::text, c.payment_terms_days,
		        cc.name, cc.email::text, cc.phone,
		        EXISTS (SELECT 1 FROM credit_holds h WHERE h.customer_id = c.id AND h.status = 'ACTIVE'),
		        EXISTS (SELECT 1 FROM ar_invoice_disputes d WHERE d.customer_id = c.id AND d.status = 'OPEN')
		   FROM customers c
		   LEFT JOIN LATERAL (
		         SELECT name, email, phone FROM customer_contacts
		          WHERE customer_id = c.id AND is_active
		          ORDER BY is_primary DESC, created_at ASC LIMIT 1
		   ) cc ON TRUE
		  WHERE c.id = $1::uuid`, customerID).
		Scan(&b.CustomerID, &b.Name, &b.AccountNo, &b.TermsDays, &contact, &email, &phone, &onHold, &disputed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CustomerBlock{}, false, false, ErrNotFound
		}
		return CustomerBlock{}, false, false, fmt.Errorf("billing: read customer block: %w", err)
	}
	if contact != nil {
		b.ContactName = *contact
	}
	if email != nil {
		b.ContactEmail = *email
	}
	if phone != nil {
		b.ContactPhone = *phone
	}
	return b, onHold, disputed, nil
}

// AccountPositionFor reads the ACCOUNT-level figures. It is a separate method from everything else in
// this file BECAUSE it is the gated one: a caller the policy refuses never reaches it, and the
// separation is what makes that reviewable at a glance rather than by reading a big SELECT list.
//
// Open balance and overdue come from the same invoices rows the aging screen reads and are scoped to
// the WHOLE account, never a branch (customer_ar_read.go's rule: a customer owes what a customer
// owes). Available credit is limit − open, floored at zero, the AvailableCreditCents contract this
// package already ships, kept identical so two screens cannot quote two headrooms.
func (r *Repository) AccountPositionFor(ctx context.Context, asOf time.Time, customerID string) (AccountPosition, error) {
	if r.pool == nil {
		return AccountPosition{}, errNoDB
	}
	var a AccountPosition
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(i.balance_cents),0),
		        COALESCE(SUM(i.balance_cents) FILTER (WHERE i.due_date < $2::date), 0),
		        MAX(c.credit_limit_cents)
		   FROM customers c
		   LEFT JOIN invoices i
		          ON i.customer_id = c.id AND i.status IN ('FINALIZED','PARTIALLY_PAID')
		  WHERE c.id = $1::uuid`, customerID, asOf).
		Scan(&a.OpenBalanceCents, &a.OverdueCents, &a.CreditLimitCents); err != nil {
		return AccountPosition{}, fmt.Errorf("billing: account position: %w", err)
	}
	a.AvailableCreditCents = a.CreditLimitCents - a.OpenBalanceCents
	if a.AvailableCreditCents < 0 {
		a.AvailableCreditCents = 0
	}
	return a, nil
}

// ProofFor reads the delivery behind an invoice: the stop, who signed, and the photograph metadata.
// found=false means this invoice has no order or the order has no stop, a counter pickup, which is
// an ordinary answer and renders as nothing.
func (r *Repository) ProofFor(ctx context.Context, orderID string) (Proof, bool, error) {
	if r.pool == nil {
		return Proof{}, false, errNoDB
	}
	if orderID == "" {
		return Proof{}, false, nil
	}
	var p Proof
	var signedAt *time.Time
	var status, line, city string
	err := r.pool.QueryRow(ctx,
		`SELECT s.id::text, COALESCE(o.order_no::text,''), s.status,
		        s.pod_received_by, s.pod_at, s.pod_signature_ref, s.address_line, s.city
		   FROM delivery_stops s
		   JOIN orders o ON o.id = s.order_id
		  WHERE s.order_id = $1::uuid
		  ORDER BY s.scheduled_date DESC, s.stop_no DESC
		  LIMIT 1`, orderID).
		Scan(&p.StopID, &p.OrderNo, &status, &p.SignedBy, &signedAt, &p.SignatureRef, &line, &city)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Proof{}, false, nil
		}
		return Proof{}, false, fmt.Errorf("billing: read proof: %w", err)
	}
	p.OrderID = orderID
	p.Delivered = status == "DELIVERED"
	p.SignedAt = signedAt
	p.Address = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line+", "+city), ","))
	p.Photos = []ProofPhoto{}

	rows, err := r.pool.Query(ctx,
		`SELECT id::text, caption, captured_at FROM delivery_pod_photos
		  WHERE stop_id = $1::uuid ORDER BY captured_at`, p.StopID)
	if err != nil {
		return Proof{}, false, fmt.Errorf("billing: read proof photos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ph ProofPhoto
		if err := rows.Scan(&ph.ID, &ph.Caption, &ph.CapturedAt); err != nil {
			return Proof{}, false, fmt.Errorf("billing: scan proof photo: %w", err)
		}
		p.Photos = append(p.Photos, ph)
	}
	return p, true, rows.Err()
}

// OrderFactsFor reads the order number, customer PO and salesperson for the detail's facts row.
func (r *Repository) OrderFactsFor(ctx context.Context, orderID string) (orderNo, customerPO, salesperson string, err error) {
	if r.pool == nil {
		return "", "", "", errNoDB
	}
	if orderID == "" {
		return "", "", "", nil
	}
	err = r.pool.QueryRow(ctx,
		`SELECT o.order_no::text, COALESCE(o.po_job_ref,''), COALESCE(u.display_name,'')
		   FROM orders o LEFT JOIN users u ON u.id = o.salesperson_id
		  WHERE o.id = $1::uuid`, orderID).Scan(&orderNo, &customerPO, &salesperson)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", fmt.Errorf("billing: read order facts: %w", err)
	}
	return orderNo, customerPO, salesperson, nil
}

// ActivityFor builds the timeline by INTERLEAVING four sources at read time, the sends and opens
// (0175), the payments applied, the delivery, and the promise/dispute rows. One UNION rather than
// four round trips, ordered newest-first by the database so the screen never sorts money-adjacent
// facts in JavaScript.
func (r *Repository) ActivityFor(ctx context.Context, invoiceID, orderID string) ([]ActivityEntry, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT kind, at, detail, amount_cents, actor FROM (
		    -- the document was raised
		    SELECT 'ISSUED'::text AS kind, i.finalized_at AS at, i.invoice_no::text AS detail,
		           i.total_cents AS amount_cents, ''::text AS actor
		      FROM invoices i WHERE i.id = $1::uuid AND i.finalized_at IS NOT NULL
		    UNION ALL
		    -- what we sent, and what it was
		    SELECT CASE WHEN d.kind = 'REMINDER' THEN 'REMINDER' ELSE 'EMAILED' END,
		           d.sent_at,
		           NULLIF(TRIM(BOTH ' ' FROM COALESCE(d.tone,'') || ' ' || d.to_address), ''),
		           d.balance_cents, COALESCE(u.display_name,'')
		      FROM ar_invoice_deliveries d LEFT JOIN users u ON u.id = d.sent_by
		     WHERE d.invoice_id = $1::uuid
		    UNION ALL
		    -- and whether they opened it
		    SELECT 'VIEWED', d.opened_at, d.to_address, 0, ''
		      FROM ar_invoice_deliveries d
		     WHERE d.invoice_id = $1::uuid AND d.opened_at IS NOT NULL
		    UNION ALL
		    -- money applied
		    SELECT 'PAYMENT', pa.created_at, p.payment_no::text || ' · ' || p.method,
		           pa.amount_cents, COALESCE(u.display_name,'')
		      FROM payment_allocations pa
		      JOIN payments p ON p.id = pa.payment_id
		      LEFT JOIN users u ON u.id = COALESCE(pa.allocated_by, p.received_by)
		     WHERE pa.invoice_id = $1::uuid
		    UNION ALL
		    -- the delivery that earned the invoice
		    SELECT 'DELIVERED', s.pod_at, s.pod_received_by, 0, COALESCE(u.display_name,'')
		      FROM delivery_stops s LEFT JOIN users u ON u.id = s.pod_by
		     WHERE $2 <> '' AND s.order_id = $2::uuid AND s.pod_at IS NOT NULL
		    UNION ALL
		    -- commitments and arguments
		    SELECT 'PROMISE', pr.taken_at, pr.promised_by, pr.promised_cents, COALESCE(u.display_name,'')
		      FROM ar_promises_to_pay pr LEFT JOIN users u ON u.id = pr.taken_by
		     WHERE pr.invoice_id = $1::uuid
		    UNION ALL
		    SELECT 'DISPUTE', di.raised_at, di.reason, di.amount_cents, COALESCE(u.display_name,'')
		      FROM ar_invoice_disputes di LEFT JOIN users u ON u.id = di.raised_by
		     WHERE di.invoice_id = $1::uuid
		) t
		WHERE at IS NOT NULL
		ORDER BY at DESC
		LIMIT 40`, invoiceID, orderID)
	if err != nil {
		return nil, fmt.Errorf("billing: read activity: %w", err)
	}
	defer rows.Close()
	out := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		var detail *string
		if err := rows.Scan(&e.Kind, &e.At, &detail, &e.AmountCents, &e.Actor); err != nil {
			return nil, fmt.Errorf("billing: scan activity: %w", err)
		}
		if detail != nil {
			e.Detail = *detail
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PromiseFor reads the live promise against a document (0176), if any.
func (r *Repository) PromiseFor(ctx context.Context, invoiceID string) (PromiseView, bool, error) {
	if r.pool == nil {
		return PromiseView{}, false, errNoDB
	}
	var p PromiseView
	var takenBy *string
	err := r.pool.QueryRow(ctx,
		`SELECT pr.id::text, pr.promised_cents, pr.promised_date, pr.status, pr.promised_by, pr.note,
		        u.display_name, pr.taken_at, pr.settled_at
		   FROM ar_promises_to_pay pr LEFT JOIN users u ON u.id = pr.taken_by
		  WHERE pr.invoice_id = $1::uuid
		  ORDER BY (pr.status = 'OPEN') DESC, pr.taken_at DESC
		  LIMIT 1`, invoiceID).
		Scan(&p.ID, &p.PromisedCents, &p.PromisedDate, &p.Status, &p.PromisedBy, &p.Note,
			&takenBy, &p.TakenAt, &p.SettledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PromiseView{}, false, nil
	}
	if err != nil {
		return PromiseView{}, false, fmt.Errorf("billing: read promise: %w", err)
	}
	if takenBy != nil {
		p.TakenBy = *takenBy
	}
	return p, true, nil
}

// DisputeFor reads the dispute against a document (0177), open one first.
func (r *Repository) DisputeFor(ctx context.Context, invoiceID string) (DisputeView, bool, error) {
	if r.pool == nil {
		return DisputeView{}, false, errNoDB
	}
	var d DisputeView
	var raisedBy *string
	err := r.pool.QueryRow(ctx,
		`SELECT di.id::text, di.status, di.reason, di.note, di.amount_cents,
		        u.display_name, di.raised_at, di.outcome, di.resolved_at
		   FROM ar_invoice_disputes di LEFT JOIN users u ON u.id = di.raised_by
		  WHERE di.invoice_id = $1::uuid
		  ORDER BY (di.status = 'OPEN') DESC, di.raised_at DESC
		  LIMIT 1`, invoiceID).
		Scan(&d.ID, &d.Status, &d.Reason, &d.Note, &d.AmountCents, &raisedBy, &d.RaisedAt,
			&d.Outcome, &d.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DisputeView{}, false, nil
	}
	if err != nil {
		return DisputeView{}, false, fmt.Errorf("billing: read dispute: %w", err)
	}
	if raisedBy != nil {
		d.RaisedBy = *raisedBy
	}
	return d, true, nil
}

// LastContactFor is the most recent send to an ACCOUNT, the cooldown fact. Per customer, never per
// invoice: a contractor with eleven overdue documents should get one conversation, not eleven (0110's
// rule, consumed rather than re-invented).
func (r *Repository) LastContactFor(ctx context.Context, customerID string) (*time.Time, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var at *time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT MAX(sent_at) FROM ar_invoice_deliveries WHERE customer_id = $1::uuid`,
		customerID).Scan(&at); err != nil {
		return nil, fmt.Errorf("billing: last contact: %w", err)
	}
	return at, nil
}
