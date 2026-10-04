package order

// invoice_source.go, DIB-422: the ORDER half of the order→invoice writer.
//
// billing declares OrderInvoiceSource (billing/invoice_writer.go) and never imports this package.
// This file provides the two methods that seam names, and the main.go adapter joins them by value.
// The split of responsibility is deliberate and worth stating, because getting it backwards is how
// two modules end up disagreeing about whether something was billed:
//
//   ORDER   decides WHETHER, and WHICH LINES. Readiness, the 0126 policy, the caller's authority,
//           the already-billed guard, all of it lives here, upstream of billing.
//   BILLING decides WHAT THE DOCUMENT LOOKS LIKE. Tax, rounding, numbering, the GL shape, the AR
//           subledger. It raises a correct document for a decision already taken.
//
// So InvoiceableOrder returns FACTS, never a permission, and it deliberately has no opinion about
// whether the caller should be allowed to bill: by the time billing calls it, order has said yes.
//
// - THE MONEY THIS SEAM HANDS OVER -
// For a GROUP invoice the line money is ShareCents, the order line's total PRO-RATED by the group's
// quantity, computed by the same prorateShares the plan surface uses. Not unit_price × qty: a line
// total may carry rounding the unit price cannot reproduce (a discounted line, a landed-cost
// adjustment), and recomputing it would put a cent of drift into every progress invoice and leave
// the sum of the progress invoices ≠ the order. Pro-rating guarantees Σ shares = the line, exactly.
//
// For a WHOLE-ORDER invoice the line money is line_total_cents unchanged, and the billed quantity is
// the FULFILLED quantity, not the ordered one, an order delivered short is billed short. Lines with
// nothing fulfilled are omitted entirely rather than billed at zero, because a zero line on an
// invoice is a question the customer will phone about.

import (
	"context"
	"fmt"
	"time"
)

// - the shape order OWNS and main.go converts -
//
// These mirror billing.OrderInvoiceFacts / billing.OrderInvoiceLine FIELD FOR FIELD, and are NOT an
// import of them. billing declares the seam it consumes; order declares the shape it produces; the
// composition root converts one to the other in six lines. That is more typing than a shared package
// and it is the point: neither module can change the other's build by editing its own types, and the
// day billing wants a field order does not have, the conversion in main.go is where that argument
// happens, visibly, in one place, instead of as a silent coupling.

// InvoiceFacts is everything billing needs to raise a document for an order.
type InvoiceFacts struct {
	OrderID    string
	OrderNo    string
	CustomerID string
	BranchID   string
	// TermsDays is the customer's payment terms; 0 lets billing apply its own default.
	TermsDays int
	// TaxReasonCode is the order's tax treatment; "" means billing's standard.
	TaxReasonCode string
	// ClosesOrder is FALSE for a 0126 ON_DEMAND_SUBSET progress invoice.
	ClosesOrder bool
	GroupIDs    []string
	Lines       []InvoiceLine
	// FreightCents is the delivery fee the order's agreed price carries (the frozen quoted fee),
	// stated as its own document line. Zero for every caller but the counter's agreed invoice.
	FreightCents int64
	// AgreedTaxCents, when set, is the tax the customer was shown, billed as stated instead of
	// assessed again. Nil for every caller but the counter's agreed invoice.
	AgreedTaxCents *int64
}

// InvoiceLine is one billable line. Quantities are DECIMAL strings, never floats.
type InvoiceLine struct {
	LineNo         int
	OrderLineID    string
	ProductID      string
	Description    string
	QtyEntered     string
	UOMEntered     string
	QtyBase        string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
}

// invoiceSourceStore is the store capability this seam needs, asserted rather than assumed, the
// shipStore precedent. A store that cannot answer these (the unit fake) makes the seam report itself
// unconfigured instead of panicking.
type invoiceSourceStore interface {
	GroupInvoiceFacts(ctx context.Context, orderID string, groupIDs []string) (InvoiceFacts, error)
	WholeOrderInvoiceFacts(ctx context.Context, orderID string) (InvoiceFacts, error)
	MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error
}

func (s *Service) invoiceSource() (invoiceSourceStore, error) {
	is, ok := s.store.(invoiceSourceStore)
	if !ok {
		return nil, ErrShipNotConfigured
	}
	return is, nil
}

// InvoiceableOrder resolves what may be billed on an order right now.
//
// groupIDs empty ⇒ the WHOLE delivered order. groupIDs non-empty ⇒ exactly those shipment groups,
// which the caller (InvoiceShipments) has already filtered to COMPLETE-and-unbilled.
func (s *Service) InvoiceableOrder(ctx context.Context, orderID string, groupIDs []string) (InvoiceFacts, error) {
	is, err := s.invoiceSource()
	if err != nil {
		return InvoiceFacts{}, err
	}
	if len(groupIDs) > 0 {
		return is.GroupInvoiceFacts(ctx, orderID, groupIDs)
	}
	return is.WholeOrderInvoiceFacts(ctx, orderID)
}

// MarkGroupsInvoiced writes the 0147 link back. Idempotent BY CONSTRUCTION: the UPDATE only touches
// groups whose invoice_id IS NULL, so a replay after billing's separate-transaction retry (see
// InvoiceOrderGroups' comment on why it is separate) converges instead of clobbering. A group already
// linked to a DIFFERENT document is left exactly as it is, the first document wins, because it is
// the one the customer already has.
func (s *Service) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	if len(groupIDs) == 0 || invoiceID == "" {
		return nil
	}
	is, err := s.invoiceSource()
	if err != nil {
		return err
	}
	return is.MarkGroupsInvoiced(ctx, groupIDs, invoiceID, at)
}

// - the repository side -

// MarkGroupsInvoiced writes the 0147 link. See the Service method for the idempotence ruling.
func (r *Repository) MarkGroupsInvoiced(ctx context.Context, groupIDs []string, invoiceID string, at time.Time) error {
	if r.pool == nil {
		return errNoDB
	}
	if len(groupIDs) == 0 || invoiceID == "" {
		return nil
	}
	if _, err := r.pool.Exec(ctx, `
UPDATE shipment_groups
   SET invoice_id = $2::uuid, invoiced_at = $3
 WHERE id = ANY($1::uuid[]) AND invoice_id IS NULL`, groupIDs, invoiceID, at); err != nil {
		return fmt.Errorf("order: mark shipment groups invoiced: %w", err)
	}
	return nil
}

// - the reads -

// orderBillingHeader is the customer/branch/terms facts every invoice needs.
func (r *Repository) orderBillingHeader(ctx context.Context, orderID string) (InvoiceFacts, error) {
	var f InvoiceFacts
	f.OrderID = orderID
	err := r.pool.QueryRow(ctx, `
SELECT o.order_no::text, o.customer_id::text, o.branch_id::text,
       COALESCE(c.payment_terms_days, 0)
  FROM orders o
  LEFT JOIN customers c ON c.id = o.customer_id
 WHERE o.id = $1`, orderID).Scan(&f.OrderNo, &f.CustomerID, &f.BranchID, &f.TermsDays)
	if err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: invoice header for %s: %w", orderID, err)
	}
	return f, nil
}

// groupInvoiceFacts collects the pro-rated money of the named groups.
func (r *Repository) GroupInvoiceFacts(ctx context.Context, orderID string, groupIDs []string) (InvoiceFacts, error) {
	if r.pool == nil {
		return InvoiceFacts{}, errNoDB
	}
	f, err := r.orderBillingHeader(ctx, orderID)
	if err != nil {
		return InvoiceFacts{}, err
	}
	f.GroupIDs = groupIDs

	// ClosesOrder is the 0146 progress-invoice flag, and it is decided by ARITHMETIC rather than by
	// the policy name: this document closes the order's billing exactly when no live group is left
	// unbilled after it. That is the honest test, ON_ALL_COMPLETE happens to satisfy it always, and
	// a final ON_DEMAND_SUBSET slice satisfies it too, which is correct and would be missed by
	// switching on the policy string.
	var remaining int
	if err := r.pool.QueryRow(ctx, `
SELECT count(*) FROM shipment_groups
 WHERE order_id = $1 AND status <> 'CANCELLED'
   AND invoice_id IS NULL AND NOT (id = ANY($2::uuid[]))`, orderID, groupIDs).Scan(&remaining); err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: remaining unbilled groups: %w", err)
	}
	f.ClosesOrder = remaining == 0

	// The group's share of each line, pro-rated by quantity across ALL the line's groups, not just
	// the ones being billed now. The denominator is the whole line's planned quantity, so slice 1 and
	// slice 2 of a two-group line sum back to the line exactly.
	rows, err := r.pool.Query(ctx, `
SELECT ol.id::text, COALESCE(ol.product_id::text,''), ol.line_no, ol.description,
       ol.uom_entered, ol.unit_price_cents, COALESCE(ol.price_uom, ol.uom_entered), ol.line_total_cents,
       sgl.qty_base,
       (SELECT COALESCE(SUM(x.qty_base),0) FROM shipment_group_lines x
          JOIN shipment_groups xg ON xg.id = x.shipment_group_id
         WHERE x.order_line_id = ol.id AND xg.status <> 'CANCELLED') AS line_planned_base
  FROM shipment_group_lines sgl
  JOIN order_lines ol ON ol.id = sgl.order_line_id
 WHERE sgl.shipment_group_id = ANY($1::uuid[])
 ORDER BY ol.line_no`, groupIDs)
	if err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: group invoice lines: %w", err)
	}
	defer rows.Close()

	// Several groups may carry slices of ONE order line (that is the point of a phased job). They are
	// merged into a single invoice line: the customer bought one thing and should be billed for one
	// thing, with the quantity being the amount that actually went out on this document.
	type acc struct {
		line       InvoiceLine
		qty        float64
		lineTotal  int64
		plannedAll float64
	}
	byLine := map[string]*acc{}
	orderKeys := []string{}
	for rows.Next() {
		var (
			olID, prodID, desc, uom, priceUOM string
			lineNo                            int
			unitPrice, lineTotal              int64
			qtyBase, plannedAll               float64
		)
		if err := rows.Scan(&olID, &prodID, &lineNo, &desc, &uom, &unitPrice, &priceUOM,
			&lineTotal, &qtyBase, &plannedAll); err != nil {
			return InvoiceFacts{}, fmt.Errorf("order: scan group invoice line: %w", err)
		}
		a, ok := byLine[olID]
		if !ok {
			a = &acc{line: InvoiceLine{
				LineNo: lineNo, OrderLineID: olID, ProductID: prodID, Description: desc,
				UOMEntered: uom, UnitPriceCents: unitPrice, PriceUOM: priceUOM,
			}, lineTotal: lineTotal, plannedAll: plannedAll}
			byLine[olID] = a
			orderKeys = append(orderKeys, olID)
		}
		a.qty += qtyBase
	}
	if err := rows.Err(); err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: group invoice lines: %w", err)
	}

	for i, k := range orderKeys {
		a := byLine[k]
		share := a.lineTotal
		if a.plannedAll > 0 && a.qty < a.plannedAll {
			// prorateShares over [this document's qty, the rest], the SAME largest-remainder split the
			// plan surface shows, so the number on the invoice equals the number the dispatcher saw.
			share = prorateShares(a.lineTotal, []float64{a.qty, a.plannedAll - a.qty})[0]
		}
		a.line.LineNo = i + 1
		a.line.QtyEntered = formatQty(a.qty)
		a.line.QtyBase = formatQty(a.qty)
		a.line.LineTotalCents = share
		f.Lines = append(f.Lines, a.line)
	}
	return f, nil
}

// wholeOrderInvoiceFacts bills a delivered order that has no shipment plan.
func (r *Repository) WholeOrderInvoiceFacts(ctx context.Context, orderID string) (InvoiceFacts, error) {
	if r.pool == nil {
		return InvoiceFacts{}, errNoDB
	}
	f, err := r.orderBillingHeader(ctx, orderID)
	if err != nil {
		return InvoiceFacts{}, err
	}
	f.ClosesOrder = true

	// fulfilled_base > 0 only. An order delivered short is billed short, and a line with nothing on
	// the truck is omitted rather than billed at zero, see the header.
	rows, err := r.pool.Query(ctx, `
SELECT ol.id::text, COALESCE(ol.product_id::text,''), ol.line_no, ol.description,
       ol.uom_entered, ol.unit_price_cents, COALESCE(ol.price_uom, ol.uom_entered),
       ol.line_total_cents, ol.qty_base, ol.fulfilled_base
  FROM order_lines ol
 WHERE ol.order_id = $1 AND ol.fulfilled_base > 0
 ORDER BY ol.line_no`, orderID)
	if err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: whole-order invoice lines: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var (
			l                       InvoiceLine
			qtyBase, fulfilled      float64
			lineTotal               int64
			olID, prodID, desc, uom string
			priceUOM                string
			unitPrice               int64
			lineNo                  int
		)
		if err := rows.Scan(&olID, &prodID, &lineNo, &desc, &uom, &unitPrice, &priceUOM,
			&lineTotal, &qtyBase, &fulfilled); err != nil {
			return InvoiceFacts{}, fmt.Errorf("order: scan whole-order line: %w", err)
		}
		n++
		l = InvoiceLine{
			LineNo: n, OrderLineID: olID, ProductID: prodID, Description: desc,
			UOMEntered: uom, UnitPriceCents: unitPrice, PriceUOM: priceUOM,
			QtyEntered: formatQty(fulfilled), QtyBase: formatQty(fulfilled),
			LineTotalCents: lineTotal,
		}
		// A short delivery bills its share of the line, pro-rated so a partial and its remainder sum
		// back to the ordered line exactly.
		if qtyBase > 0 && fulfilled < qtyBase {
			l.LineTotalCents = prorateShares(lineTotal, []float64{fulfilled, qtyBase - fulfilled})[0]
		}
		f.Lines = append(f.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return InvoiceFacts{}, fmt.Errorf("order: whole-order invoice lines: %w", err)
	}
	return f, nil
}

// Quantities go over this seam as DECIMAL strings via edit.go's formatQty, which renders at the
// column's own DECIMAL(19,4) precision. Never a float on the wire: billing casts these straight into
// numeric columns, and a quantity that has been through a float64's text form is a quantity somebody
// will argue about.

// AgreedBillingRefusal is the order module's refusal to answer the counter's agreed invoice: the
// order is in a state that cannot be billed, or its own header disagrees with its lines.
type AgreedBillingRefusal struct{ Msg string }

func (e *AgreedBillingRefusal) Error() string { return e.Msg }

// AgreedInvoiceableOrder is the counter's agreed invoice facts (stub, red first).
func (s *Service) AgreedInvoiceableOrder(ctx context.Context, orderID string) (InvoiceFacts, error) {
	return InvoiceFacts{}, fmt.Errorf("order: agreed invoice facts not built")
}
