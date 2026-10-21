package assist

import "sort"

// dataquery_registry.go, the classification registry (plan r3 section 3.4): the single source of the class of
// every column of every view the assistant can read. The per asker catalogue and the compiler's class check
// read it, and two tests hold it to the database: every column of every view in schema ai is registered, and
// no column of class secret is in any view. A column that is not registered is treated as secret, which is in
// no catalogue, so a forgotten entry hides a column and never shows one.
//
// The classes are plain, cost, margin, payment, pii_contact, pii_location and secret. Cost and margin are the
// money classes of this slice (6a); payment and the two personal data classes arrive with 6b and are held by no
// asker until then (classHeld).

// columnRegistry maps "view.column" to its class.
var columnRegistry = map[string]string{
	// ai.orders: what every manager sees on the order screen.
	"ai.orders.id":             ClassPlain,
	"ai.orders.order_no":       ClassPlain,
	"ai.orders.status":         ClassPlain,
	"ai.orders.fulfillment":    ClassPlain,
	"ai.orders.branch_id":      ClassPlain,
	"ai.orders.branch_label":   ClassPlain,
	"ai.orders.customer_id":    ClassPlain,
	"ai.orders.customer_name":  ClassPlain,
	"ai.orders.requested_date": ClassPlain,
	"ai.orders.created_at":     ClassPlain,
	"ai.orders.subtotal_cents": ClassPlain,
	"ai.orders.tax_cents":      ClassPlain,
	"ai.orders.total_cents":    ClassPlain,
	// ai.order_lines.
	"ai.order_lines.id":               ClassPlain,
	"ai.order_lines.order_id":         ClassPlain,
	"ai.order_lines.order_no":         ClassPlain,
	"ai.order_lines.branch_id":        ClassPlain,
	"ai.order_lines.line_no":          ClassPlain,
	"ai.order_lines.description":      ClassPlain,
	"ai.order_lines.qty_entered":      ClassPlain,
	"ai.order_lines.uom":              ClassPlain,
	"ai.order_lines.unit_price_cents": ClassPlain,
	"ai.order_lines.line_total_cents": ClassPlain,
	// ai.stock_balances: quantities are plain; the branch average cost is the one cost class column.
	"ai.stock_balances.product_id":     ClassPlain,
	"ai.stock_balances.sku":            ClassPlain,
	"ai.stock_balances.product_name":   ClassPlain,
	"ai.stock_balances.branch_id":      ClassPlain,
	"ai.stock_balances.branch_label":   ClassPlain,
	"ai.stock_balances.on_hand_base":   ClassPlain,
	"ai.stock_balances.reserved_base":  ClassPlain,
	"ai.stock_balances.available_base": ClassPlain,
	"ai.stock_balances.uom":            ClassPlain,
	"ai.stock_balances.avg_cost_cents": ClassCost,
	// ai.quotes: what the quote screens show; the personal text is tagged, not gated.
	"ai.quotes.id":                 ClassPlain,
	"ai.quotes.quote_no":           ClassPlain,
	"ai.quotes.status":             ClassPlain,
	"ai.quotes.valid_until":        ClassPlain,
	"ai.quotes.branch_id":          ClassPlain,
	"ai.quotes.branch_label":       ClassPlain,
	"ai.quotes.customer_id":        ClassPlain,
	"ai.quotes.customer_name":      ClassPlain,
	"ai.quotes.contact_name":       ClassPlain,
	"ai.quotes.delivery_address":   ClassPlain,
	"ai.quotes.customer_notes":     ClassPlain,
	"ai.quotes.fulfillment":        ClassPlain,
	"ai.quotes.requested_date":     ClassPlain,
	"ai.quotes.deposit_pct":        ClassPlain,
	"ai.quotes.subtotal_cents":     ClassPlain,
	"ai.quotes.tax_cents":          ClassPlain,
	"ai.quotes.delivery_fee_cents": ClassPlain,
	"ai.quotes.total_cents":        ClassPlain,
	"ai.quotes.created_at":         ClassPlain,
	// ai.quote_lines: the margin cells are class margin, gated by the cost and margin role list as the quote
	// screen's margin glance is.
	"ai.quote_lines.id":               ClassPlain,
	"ai.quote_lines.quote_id":         ClassPlain,
	"ai.quote_lines.quote_no":         ClassPlain,
	"ai.quote_lines.branch_id":        ClassPlain,
	"ai.quote_lines.line_no":          ClassPlain,
	"ai.quote_lines.description":      ClassPlain,
	"ai.quote_lines.qty_entered":      ClassPlain,
	"ai.quote_lines.uom":              ClassPlain,
	"ai.quote_lines.unit_price_cents": ClassPlain,
	"ai.quote_lines.disc_pct":         ClassPlain,
	"ai.quote_lines.line_total_cents": ClassPlain,
	"ai.quote_lines.line_cost_cents":  ClassMargin,
	"ai.quote_lines.margin_bps":       ClassMargin,
	"ai.quote_lines.est_profit_cents": ClassMargin,
	// ai.purchase_orders: the header subtotal is class cost, gated by the per branch purchasing policy, not the role
	// list; the notes are tagged personal.
	"ai.purchase_orders.id":                ClassPlain,
	"ai.purchase_orders.po_no":             ClassPlain,
	"ai.purchase_orders.status":            ClassPlain,
	"ai.purchase_orders.branch_id":         ClassPlain,
	"ai.purchase_orders.branch_label":      ClassPlain,
	"ai.purchase_orders.vendor_id":         ClassPlain,
	"ai.purchase_orders.vendor_name":       ClassPlain,
	"ai.purchase_orders.expected_at":       ClassPlain,
	"ai.purchase_orders.ordered_at":        ClassPlain,
	"ai.purchase_orders.document_currency": ClassPlain,
	"ai.purchase_orders.delivery_method":   ClassPlain,
	"ai.purchase_orders.notes":             ClassPlain,
	"ai.purchase_orders.line_count":        ClassPlain,
	"ai.purchase_orders.subtotal_cents":    ClassCost,
	// ai.purchase_order_lines: unit cost, line total and billed amount are class cost under the same policy.
	"ai.purchase_order_lines.id":                   ClassPlain,
	"ai.purchase_order_lines.po_id":                ClassPlain,
	"ai.purchase_order_lines.po_no":                ClassPlain,
	"ai.purchase_order_lines.branch_id":            ClassPlain,
	"ai.purchase_order_lines.line_no":              ClassPlain,
	"ai.purchase_order_lines.sku":                  ClassPlain,
	"ai.purchase_order_lines.description":          ClassPlain,
	"ai.purchase_order_lines.qty_ordered_base":     ClassPlain,
	"ai.purchase_order_lines.qty_received_base":    ClassPlain,
	"ai.purchase_order_lines.qty_billed_base":      ClassPlain,
	"ai.purchase_order_lines.qty_outstanding_base": ClassPlain,
	"ai.purchase_order_lines.uom":                  ClassPlain,
	"ai.purchase_order_lines.unit_cost_cents":      ClassCost,
	"ai.purchase_order_lines.line_total_cents":     ClassCost,
	"ai.purchase_order_lines.billed_cents":         ClassCost,
	// ai.invoices: the invoice's own figures are not sensitive (the register shows them to any staff with reach).
	"ai.invoices.id":             ClassPlain,
	"ai.invoices.invoice_no":     ClassPlain,
	"ai.invoices.doc_type":       ClassPlain,
	"ai.invoices.status":         ClassPlain,
	"ai.invoices.issue_date":     ClassPlain,
	"ai.invoices.due_date":       ClassPlain,
	"ai.invoices.branch_id":      ClassPlain,
	"ai.invoices.branch_label":   ClassPlain,
	"ai.invoices.customer_id":    ClassPlain,
	"ai.invoices.customer_name":  ClassPlain,
	"ai.invoices.account_no":     ClassPlain,
	"ai.invoices.order_id":       ClassPlain,
	"ai.invoices.order_no":       ClassPlain,
	"ai.invoices.subtotal_cents": ClassPlain,
	"ai.invoices.tax_cents":      ClassPlain,
	"ai.invoices.holdback_cents": ClassPlain,
	"ai.invoices.total_cents":    ClassPlain,
	"ai.invoices.balance_cents":  ClassPlain,
	// ai.ar_aging: the aging screen's per account buckets.
	"ai.ar_aging.customer_id":        ClassPlain,
	"ai.ar_aging.customer_name":      ClassPlain,
	"ai.ar_aging.account_no":         ClassPlain,
	"ai.ar_aging.as_of":              ClassPlain,
	"ai.ar_aging.current_cents":      ClassPlain,
	"ai.ar_aging.days_1_30_cents":    ClassPlain,
	"ai.ar_aging.days_31_60_cents":   ClassPlain,
	"ai.ar_aging.days_61_90_cents":   ClassPlain,
	"ai.ar_aging.days_90_plus_cents": ClassPlain,
	"ai.ar_aging.open_cents":         ClassPlain,
	// ai.payments: what identifies a payment is plain; what the payment trail screens show (method, status, the
	// amount and the unapplied figure) is class payment, held by the claim's payment trail (ruling 87).
	"ai.payments.id":              ClassPlain,
	"ai.payments.payment_no":      ClassPlain,
	"ai.payments.received_at":     ClassPlain,
	"ai.payments.branch_id":       ClassPlain,
	"ai.payments.branch_label":    ClassPlain,
	"ai.payments.customer_id":     ClassPlain,
	"ai.payments.customer_name":   ClassPlain,
	"ai.payments.method":          ClassPayment,
	"ai.payments.status":          ClassPayment,
	"ai.payments.amount_cents":    ClassPayment,
	"ai.payments.unapplied_cents": ClassPayment,
	// ai.payment_allocations: rows exist only for an asker who holds the trail; the amount and date stay class payment.
	"ai.payment_allocations.payment_id":   ClassPlain,
	"ai.payment_allocations.payment_no":   ClassPlain,
	"ai.payment_allocations.invoice_id":   ClassPlain,
	"ai.payment_allocations.invoice_no":   ClassPlain,
	"ai.payment_allocations.amount_cents": ClassPayment,
	"ai.payment_allocations.allocated_at": ClassPayment,
}

// personalColumns is the sensitivity tag, separate from the class (PII-CLASSES-r2): a column that holds personal
// data carries it whatever its visibility class. The tag does not gate reading (most personal data is shown to
// every asker who reaches the row, so the class stays plain). It governs what TanyAI does with the value beyond
// the answer: never embedded off the box, never in a citation label, never written to the query log beyond what
// the asker typed, and counted for the retention purge. Free text columns that may hold anything typed carry it
// too and stay plain; they are never indexed unless the scrubber has run on them.
var personalColumns = map[string]bool{
	"ai.orders.customer_name":    true, // customers.name: may be a person for a sole trader
	"ai.quotes.customer_name":    true, // as above
	"ai.quotes.contact_name":     true, // the quote screen shows it to staff with reach
	"ai.quotes.delivery_address": true, // as above
	"ai.quotes.customer_notes":   true, // free text shown on the quote; may hold anything typed
	"ai.purchase_orders.notes":   true, // free text on the order
	"ai.invoices.customer_name":  true, // as ai.orders.customer_name
	"ai.ar_aging.customer_name":  true, // as above
	"ai.payments.customer_name":  true, // as above
}

// columnGates names the vocabulary that gates a cost column when it is not the role list.
var columnGates = map[string]string{
	"ai.purchase_orders.subtotal_cents":        GatePurchasePolicy,
	"ai.purchase_order_lines.unit_cost_cents":  GatePurchasePolicy,
	"ai.purchase_order_lines.line_total_cents": GatePurchasePolicy,
	"ai.purchase_order_lines.billed_cents":     GatePurchasePolicy,
	"ai.payments.method":                       GatePaymentTrail,
	"ai.payments.status":                       GatePaymentTrail,
	"ai.payments.amount_cents":                 GatePaymentTrail,
	"ai.payments.unapplied_cents":              GatePaymentTrail,
	"ai.payment_allocations.amount_cents":      GatePaymentTrail,
	"ai.payment_allocations.allocated_at":      GatePaymentTrail,
}

// PersonalColumns is the tag list, exported for the text index and the egress slices: every "view.column" that
// holds personal data, sorted.
func PersonalColumns() []string {
	out := make([]string, 0, len(personalColumns))
	for k := range personalColumns {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RegisteredColumns is a copy of the registry, for the tests that hold it to the database.
func RegisteredColumns() map[string]string {
	out := make(map[string]string, len(columnRegistry))
	for k, v := range columnRegistry {
		out[k] = v
	}
	return out
}

// classOf is the registered class of a view column, or secret when it is not registered.
func classOf(view, column string) string {
	if c, ok := columnRegistry[view+"."+column]; ok {
		return c
	}
	return ClassSecret
}

// classify stamps every column of v with its registered class.
func classify(v ViewDef) ViewDef {
	cols := make([]ColumnDef, len(v.Columns))
	for i, c := range v.Columns {
		c.Class = classOf(v.Name, c.Name)
		c.Personal = personalColumns[v.Name+"."+c.Name]
		c.Gate = columnGates[v.Name+"."+c.Name]
		cols[i] = c
	}
	v.Columns = cols
	return v
}

// RawViewColumns lists the columns each built-in view declares, before classification, as "view.column".
func RawViewColumns() []string {
	var out []string
	for _, v := range []ViewDef{rawOrdersView(), rawOrderLinesView(), rawStockBalancesView(), rawQuotesView(), rawQuoteLinesView(), rawPurchaseOrdersView(), rawPurchaseOrderLinesView(), rawInvoicesView(), rawARAgingView(), rawPaymentsView(), rawPaymentAllocationsView()} {
		for _, c := range v.Columns {
			out = append(out, v.Name+"."+c.Name)
		}
	}
	return out
}
