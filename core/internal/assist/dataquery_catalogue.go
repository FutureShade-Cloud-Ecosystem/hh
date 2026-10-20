package assist

// dataquery_catalogue.go, the allow listed view catalogue behind data.query (plan r3 sections 2 and 3.4).
//
// The catalogue is the single source of what a question can name: a view, its columns, each column's type
// and class. It generates the tool description the model sees, per asker, and the checks the compiler
// runs. A view or column that is not in the asker's catalogue is absent from the tool description and
// refused as unknown, never as "not allowed", so a refusal never confirms that a gated column exists.
//
// The classes are the plan's: plain, cost, margin, payment, pii_contact, pii_location, secret. In this
// slice the two views carry plain columns only (no cost, margin, payment detail or contested contact
// and location text); the registry that decides who holds the other classes arrives with slice 6, and
// until then no asker holds any class but plain. A secret class column is in no view at all.

import "sort"

// Column classes (plan 3.4).
const (
	ClassPlain      = "plain"
	ClassCost       = "cost"
	ClassMargin     = "margin"
	ClassPayment    = "payment"
	ClassPIIContact = "pii_contact"
	ClassPIILoc     = "pii_location"
	ClassSecret     = "secret"
)

// colType is how a column's values are read from a question and shown in an answer.
type colType int

const (
	typeText colType = iota
	typeInt
	typeCents   // integer cents, shown as money
	typeDate    // a calendar date
	typeTime    // a moment (timestamptz)
	typeUUID    // a record id, filter only
	typeNumeric // a quantity
)

// ColumnDef is one column of a view.
type ColumnDef struct {
	Name   string
	Label  string
	Type   colType
	Class  string
	Filter bool // may appear in where
	Group  bool // may appear in group_by and as a plain select
	Search bool // may use the contains operator
	Agg    bool // may be summed, averaged, minimised or maximised
	Hidden bool // never selected by a question (record id columns used for citations)
	// Personal is the sensitivity tag of the registry (not a class, it gates nothing): the column holds personal
	// data. Set from the registry, never in a view definition.
	Personal bool
	// Gate names which of the two cost vocabularies decides a cost column: "" or GateRoleList for the dealer's
	// cost and margin role list (stock, quote margin), GatePurchasePolicy for the per branch purchasing policy
	// (what the dealer pays on a purchase order). Set from the registry.
	Gate string
}

// ViewDef is one allow listed view.
type ViewDef struct {
	Name    string // schema qualified, as it is written in a spec: "ai.orders"
	Schema  string
	Table   string
	Title   string
	About   string
	MinTier int // numerically highest tier that may query it; the registry floor
	Columns []ColumnDef
	// Default is the select list of a question that names none, and DefaultOrder its order.
	Default      []string
	DefaultOrder []OrderBy
	// Period is the date or time column the answer's scope period is read from.
	Period string
	// Citation: the record each row is about. IDCol names the id column, LabelCol the column the label is
	// built from, LabelPrefix the word before it ("Order "), Kind the Ref kind.
	CiteKind, CiteIDCol, CiteLabelCol, CiteLabelPrefix string
	// Large marks a view over a big table: a sequential scan of it is refused by the plan shape check.
	Large bool
}

// Column reports a column of the view by name (for tests and the registry's callers).
func (v ViewDef) Column(name string) (ColumnDef, bool) { return v.col(name) }

func (v ViewDef) col(name string) (ColumnDef, bool) {
	for _, c := range v.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return ColumnDef{}, false
}

// classHeld reports whether the caller holds a column class. Plain is held by everyone with a role. Cost is
// held by a caller with at least one cost branch and margin by one the dealer's cost rule gives margin (both
// resolved in Go from the dealer's cost and margin role list, see Handler.costFor); the database then masks
// cost per row by branch. The payment trail is the role gate's answer. The personal data classes wait for 6b.
// The two cost vocabularies.
const (
	GateRoleList       = "role_list"
	GatePurchasePolicy = "purchase_policy"
	// GatePaymentTrail is the role gate's admission to the two payment trail routes (ruling 87), carried in the claim.
	GatePaymentTrail = "payment_trail"
)

// columnHeld is classHeld for one column: a cost column is held according to the vocabulary that governs it.
func columnHeld(c Caller, col ColumnDef) bool {
	if col.Class == ClassCost && col.Gate == GatePurchasePolicy {
		return c.HasRole() && len(c.POCostBranchIDs) > 0
	}
	return classHeld(c, col.Class)
}

func classHeld(c Caller, class string) bool {
	switch class {
	case ClassPlain:
		return c.HasRole()
	case ClassCost:
		return c.HasRole() && len(c.CostBranchIDs) > 0
	case ClassMargin:
		return c.HasRole() && c.Margin
	case ClassPayment:
		return c.PaymentTrail
	}
	return false
}

// builtinViews is the shipped catalogue, before any asker's filtering.
func builtinViews() []ViewDef {
	return []ViewDef{ordersView(), orderLinesView(), stockBalancesView(), quotesView(), quoteLinesView(), purchaseOrdersView(), purchaseOrderLinesView(), invoicesView(), arAgingView(), paymentsView(), paymentAllocationsView(), deliveryRunsView(), deliveryStopsView(), customersView(), customerContactsView(), messageChannelsView()}
}

func ordersView() ViewDef { return classify(rawOrdersView()) }

func rawOrdersView() ViewDef {
	return ViewDef{
		Name: "ai.orders", Schema: "ai", Table: "orders", Title: "Orders", MinTier: 2, Large: true,
		About: "One row per order: number, status, branch, customer, dates and totals in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "order_no", Label: "Order", Type: typeText, Filter: true, Group: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "fulfillment", Label: "Fulfilment", Type: typeText, Filter: true, Group: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true},
			{Name: "requested_date", Label: "Requested date", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "created_at", Label: "Placed", Type: typeTime, Filter: true, Agg: true},
			{Name: "subtotal_cents", Label: "Subtotal", Type: typeCents, Filter: true, Agg: true},
			{Name: "tax_cents", Label: "Tax", Type: typeCents, Filter: true, Agg: true},
			{Name: "total_cents", Label: "Total", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"order_no", "customer_name", "branch_label", "status", "created_at", "total_cents"},
		DefaultOrder: []OrderBy{{Col: "created_at", Dir: "desc"}, {Col: "order_no", Dir: "asc"}},
		Period:       "created_at",
		CiteKind:     RefOrder, CiteIDCol: "id", CiteLabelCol: "order_no", CiteLabelPrefix: "Order ",
	}
}

func orderLinesView() ViewDef { return classify(rawOrderLinesView()) }

func rawOrderLinesView() ViewDef {
	return ViewDef{
		Name: "ai.order_lines", Schema: "ai", Table: "order_lines", Title: "Order lines", MinTier: 2, Large: true,
		About: "One row per order line: the order, the product description, quantity with its unit, price and total in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Line id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "order_id", Label: "Order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "order_no", Label: "Order", Type: typeText, Filter: true, Group: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "line_no", Label: "Line", Type: typeInt, Filter: true, Group: true},
			{Name: "description", Label: "Description", Type: typeText, Filter: true, Group: true},
			{Name: "qty_entered", Label: "Quantity", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "uom", Label: "Unit", Type: typeText, Filter: true, Group: true},
			{Name: "unit_price_cents", Label: "Unit price", Type: typeCents, Filter: true, Agg: true},
			{Name: "line_total_cents", Label: "Line total", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"order_no", "line_no", "description", "qty_entered", "uom", "line_total_cents"},
		DefaultOrder: []OrderBy{{Col: "order_no", Dir: "asc"}, {Col: "line_no", Dir: "asc"}},
		CiteKind:     RefOrder, CiteIDCol: "order_id", CiteLabelCol: "order_no", CiteLabelPrefix: "Order ",
	}
}

// CatalogueFor is the catalogue THIS asker may name: views at or below their tier floor, and within each
// only the columns whose class they hold. It is the only enumeration of the data a question can reach,
// so it is rebuilt for every asker and never cached across askers.
func CatalogueFor(c Caller) []ViewDef { return filterCatalogue(builtinViews(), c) }

func filterCatalogue(all []ViewDef, c Caller) []ViewDef {
	var out []ViewDef
	for _, v := range all {
		if !c.HasRole() || c.Tier > v.MinTier {
			continue
		}
		fv := v
		fv.Columns = nil
		for _, col := range v.Columns {
			if columnHeld(c, col) {
				fv.Columns = append(fv.Columns, col)
			}
		}
		out = append(out, fv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func stockBalancesView() ViewDef { return classify(rawStockBalancesView()) }

func rawStockBalancesView() ViewDef {
	return ViewDef{
		Name: "ai.stock_balances", Schema: "ai", Table: "stock_balances", Title: "Stock", MinTier: 2, Large: true,
		About: "One row per product and branch: on hand, reserved (hard holds) and available quantities in the product's unit, and the branch average cost where you may see it.",
		Columns: []ColumnDef{
			{Name: "product_id", Label: "Product id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "sku", Label: "SKU", Type: typeText, Filter: true, Group: true},
			{Name: "product_name", Label: "Product", Type: typeText, Filter: true, Group: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "on_hand_base", Label: "On hand", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "reserved_base", Label: "Reserved", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "available_base", Label: "Available", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "uom", Label: "Unit", Type: typeText, Filter: true, Group: true},
			{Name: "avg_cost_cents", Label: "Average cost", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"sku", "product_name", "branch_label", "on_hand_base", "reserved_base", "available_base", "uom"},
		DefaultOrder: []OrderBy{{Col: "sku", Dir: "asc"}, {Col: "branch_label", Dir: "asc"}},
		CiteKind:     RefProduct, CiteIDCol: "product_id", CiteLabelCol: "sku", CiteLabelPrefix: "Stock ",
	}
}

func quotesView() ViewDef { return classify(rawQuotesView()) }

func rawQuotesView() ViewDef {
	return ViewDef{
		Name: "ai.quotes", Schema: "ai", Table: "quotes", Title: "Quotes", MinTier: 2, Large: true,
		About: "One row per quote: number, status, branch, customer, contact, delivery, dates and totals in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Quote id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "quote_no", Label: "Quote", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "valid_until", Label: "Valid until", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "contact_name", Label: "Contact", Type: typeText, Filter: true, Group: true},
			{Name: "delivery_address", Label: "Delivery address", Type: typeText, Filter: true},
			{Name: "customer_notes", Label: "Customer notes", Type: typeText, Filter: true},
			{Name: "fulfillment", Label: "Fulfilment", Type: typeText, Filter: true, Group: true},
			{Name: "requested_date", Label: "Requested date", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "deposit_pct", Label: "Deposit percent", Type: typeInt, Filter: true, Agg: true},
			{Name: "subtotal_cents", Label: "Subtotal", Type: typeCents, Filter: true, Agg: true},
			{Name: "tax_cents", Label: "Tax", Type: typeCents, Filter: true, Agg: true},
			{Name: "delivery_fee_cents", Label: "Delivery fee", Type: typeCents, Filter: true, Agg: true},
			{Name: "total_cents", Label: "Total", Type: typeCents, Filter: true, Agg: true},
			{Name: "created_at", Label: "Created", Type: typeTime, Filter: true, Agg: true},
		},
		Default:      []string{"quote_no", "customer_name", "branch_label", "status", "created_at", "total_cents"},
		DefaultOrder: []OrderBy{{Col: "created_at", Dir: "desc"}, {Col: "quote_no", Dir: "asc"}},
		Period:       "created_at",
		CiteKind:     RefQuote, CiteIDCol: "id", CiteLabelCol: "quote_no", CiteLabelPrefix: "Quote ",
	}
}

func quoteLinesView() ViewDef { return classify(rawQuoteLinesView()) }

func rawQuoteLinesView() ViewDef {
	return ViewDef{
		Name: "ai.quote_lines", Schema: "ai", Table: "quote_lines", Title: "Quote lines", MinTier: 2, Large: true,
		About: "One row per quote line: the quote, description, quantity with its unit, price, discount and total in cents, and where you may see it the line's cost, margin in basis points and estimated profit.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Line id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "quote_id", Label: "Quote id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "quote_no", Label: "Quote", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "line_no", Label: "Line", Type: typeInt, Filter: true, Group: true},
			{Name: "description", Label: "Description", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "qty_entered", Label: "Quantity", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "uom", Label: "Unit", Type: typeText, Filter: true, Group: true},
			{Name: "unit_price_cents", Label: "Unit price", Type: typeCents, Filter: true, Agg: true},
			{Name: "disc_pct", Label: "Discount percent", Type: typeInt, Filter: true, Agg: true},
			{Name: "line_total_cents", Label: "Line total", Type: typeCents, Filter: true, Agg: true},
			{Name: "line_cost_cents", Label: "Line cost", Type: typeCents, Filter: true, Agg: true},
			{Name: "margin_bps", Label: "Margin (basis points)", Type: typeInt, Filter: true, Agg: true},
			{Name: "est_profit_cents", Label: "Estimated profit", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"quote_no", "line_no", "description", "qty_entered", "uom", "line_total_cents"},
		DefaultOrder: []OrderBy{{Col: "quote_no", Dir: "asc"}, {Col: "line_no", Dir: "asc"}},
		CiteKind:     RefQuote, CiteIDCol: "quote_id", CiteLabelCol: "quote_no", CiteLabelPrefix: "Quote ",
	}
}

func purchaseOrdersView() ViewDef { return classify(rawPurchaseOrdersView()) }

// The purchase order views carry no citation: purchasing orders have no routable reference kind in the app, and
// a link to nothing is worse than none.
func rawPurchaseOrdersView() ViewDef {
	return ViewDef{
		Name: "ai.purchase_orders", Schema: "ai", Table: "purchase_orders", Title: "Purchase orders", MinTier: 2, Large: true,
		About: "One row per purchase order: number, status, branch, vendor, dates, currency, line count and, where you may see what the dealer pays, the subtotal in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Purchase order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "po_no", Label: "Purchase order", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "vendor_id", Label: "Vendor id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "vendor_name", Label: "Vendor", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "expected_at", Label: "Expected", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "ordered_at", Label: "Ordered", Type: typeTime, Filter: true, Agg: true},
			{Name: "document_currency", Label: "Document currency", Type: typeText, Filter: true, Group: true},
			{Name: "delivery_method", Label: "Delivery method", Type: typeText, Filter: true, Group: true},
			{Name: "notes", Label: "Notes", Type: typeText, Filter: true},
			{Name: "line_count", Label: "Lines", Type: typeInt, Filter: true, Agg: true},
			{Name: "subtotal_cents", Label: "Subtotal", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"po_no", "vendor_name", "branch_label", "status", "expected_at", "line_count"},
		DefaultOrder: []OrderBy{{Col: "po_no", Dir: "asc"}},
		Period:       "ordered_at",
	}
}

func purchaseOrderLinesView() ViewDef { return classify(rawPurchaseOrderLinesView()) }

func rawPurchaseOrderLinesView() ViewDef {
	return ViewDef{
		Name: "ai.purchase_order_lines", Schema: "ai", Table: "purchase_order_lines", Title: "Purchase order lines", MinTier: 2, Large: true,
		About: "One row per purchase order line: the order, product, quantities ordered, received, billed and outstanding in the base unit and, where you may see what the dealer pays, the unit cost, line total and billed amount in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Line id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "po_id", Label: "Purchase order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "po_no", Label: "Purchase order", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "line_no", Label: "Line", Type: typeInt, Filter: true, Group: true},
			{Name: "sku", Label: "SKU", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "description", Label: "Description", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "qty_ordered_base", Label: "Ordered", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "qty_received_base", Label: "Received", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "qty_billed_base", Label: "Billed quantity", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "qty_outstanding_base", Label: "Outstanding", Type: typeNumeric, Filter: true, Agg: true},
			{Name: "uom", Label: "Unit", Type: typeText, Filter: true, Group: true},
			{Name: "unit_cost_cents", Label: "Unit cost", Type: typeCents, Filter: true, Agg: true},
			{Name: "line_total_cents", Label: "Line total", Type: typeCents, Filter: true, Agg: true},
			{Name: "billed_cents", Label: "Billed amount", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"po_no", "line_no", "sku", "description", "qty_ordered_base", "qty_received_base", "uom"},
		DefaultOrder: []OrderBy{{Col: "po_no", Dir: "asc"}, {Col: "line_no", Dir: "asc"}},
	}
}

func invoicesView() ViewDef { return classify(rawInvoicesView()) }

func rawInvoicesView() ViewDef {
	return ViewDef{
		Name: "ai.invoices", Schema: "ai", Table: "invoices", Title: "Invoices", MinTier: 2, Large: true,
		About: "One row per invoice or credit memo: number, type, status, issue and due date, branch, customer, order, totals, holdback and balance due in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Invoice id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "invoice_no", Label: "Invoice", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "doc_type", Label: "Type", Type: typeText, Filter: true, Group: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "issue_date", Label: "Issued", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "due_date", Label: "Due", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "account_no", Label: "Account", Type: typeText, Filter: true, Group: true},
			{Name: "order_id", Label: "Order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "order_no", Label: "Order", Type: typeText, Filter: true, Group: true},
			{Name: "subtotal_cents", Label: "Subtotal", Type: typeCents, Filter: true, Agg: true},
			{Name: "tax_cents", Label: "Tax", Type: typeCents, Filter: true, Agg: true},
			{Name: "holdback_cents", Label: "Holdback", Type: typeCents, Filter: true, Agg: true},
			{Name: "total_cents", Label: "Total", Type: typeCents, Filter: true, Agg: true},
			{Name: "balance_cents", Label: "Balance due", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"invoice_no", "customer_name", "branch_label", "status", "due_date", "balance_cents"},
		DefaultOrder: []OrderBy{{Col: "due_date", Dir: "asc"}, {Col: "invoice_no", Dir: "asc"}},
		Period:       "issue_date",
		CiteKind:     RefInvoice, CiteIDCol: "id", CiteLabelCol: "invoice_no", CiteLabelPrefix: "Invoice ",
	}
}

func arAgingView() ViewDef { return classify(rawARAgingView()) }

func rawARAgingView() ViewDef {
	return ViewDef{
		Name: "ai.ar_aging", Schema: "ai", Table: "ar_aging", Title: "Receivables aging", MinTier: 2, Large: true,
		About: "One row per account with an open balance: the balance in cents, as of the dealer's day, in the aging screen's bands (current, 1 to 30, 31 to 60, 61 to 90 and over 90 days past due).",
		Columns: []ColumnDef{
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "account_no", Label: "Account", Type: typeText, Filter: true, Group: true},
			{Name: "as_of", Label: "As of", Type: typeDate, Filter: true},
			{Name: "current_cents", Label: "Current", Type: typeCents, Filter: true, Agg: true},
			{Name: "days_1_30_cents", Label: "1 to 30 days", Type: typeCents, Filter: true, Agg: true},
			{Name: "days_31_60_cents", Label: "31 to 60 days", Type: typeCents, Filter: true, Agg: true},
			{Name: "days_61_90_cents", Label: "61 to 90 days", Type: typeCents, Filter: true, Agg: true},
			{Name: "days_90_plus_cents", Label: "Over 90 days", Type: typeCents, Filter: true, Agg: true},
			{Name: "open_cents", Label: "Open balance", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"customer_name", "account_no", "current_cents", "days_1_30_cents", "days_31_60_cents", "days_61_90_cents", "days_90_plus_cents", "open_cents"},
		DefaultOrder: []OrderBy{{Col: "open_cents", Dir: "desc"}, {Col: "account_no", Dir: "asc"}},
		CiteKind:     RefCustomer, CiteIDCol: "customer_id", CiteLabelCol: "account_no", CiteLabelPrefix: "Account ",
	}
}

func paymentsView() ViewDef { return classify(rawPaymentsView()) }

func rawPaymentsView() ViewDef {
	return ViewDef{
		Name: "ai.payments", Schema: "ai", Table: "payments", Title: "Payments", MinTier: 2, Large: true,
		About: "One row per payment: number, date, branch and customer and, where you hold the payment trail, the method, status, amount and unapplied amount in cents.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Payment id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "payment_no", Label: "Payment", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "received_at", Label: "Received", Type: typeTime, Filter: true, Agg: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "method", Label: "Method", Type: typeText, Filter: true, Group: true},
			{Name: "status", Label: "Payment status", Type: typeText, Filter: true, Group: true},
			{Name: "amount_cents", Label: "Amount", Type: typeCents, Filter: true, Agg: true},
			{Name: "unapplied_cents", Label: "Unapplied", Type: typeCents, Filter: true, Agg: true},
		},
		Default:      []string{"payment_no", "customer_name", "branch_label", "received_at"},
		DefaultOrder: []OrderBy{{Col: "received_at", Dir: "desc"}, {Col: "payment_no", Dir: "asc"}},
		Period:       "received_at",
		CiteKind:     RefPayment, CiteIDCol: "id", CiteLabelCol: "payment_no", CiteLabelPrefix: "Payment ",
	}
}

func paymentAllocationsView() ViewDef { return classify(rawPaymentAllocationsView()) }

func rawPaymentAllocationsView() ViewDef {
	return ViewDef{
		Name: "ai.payment_allocations", Schema: "ai", Table: "payment_allocations", Title: "Payment allocations", MinTier: 2, Large: true,
		About: "One row per payment applied to an invoice: the payment, the invoice, the amount applied in cents and when. Only askers who hold the payment trail see any.",
		Columns: []ColumnDef{
			{Name: "payment_id", Label: "Payment id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "payment_no", Label: "Payment", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "invoice_id", Label: "Invoice id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "invoice_no", Label: "Invoice", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "amount_cents", Label: "Amount applied", Type: typeCents, Filter: true, Agg: true},
			{Name: "allocated_at", Label: "Applied", Type: typeTime, Filter: true, Agg: true},
		},
		Default:      []string{"payment_no", "invoice_no"},
		DefaultOrder: []OrderBy{{Col: "payment_no", Dir: "asc"}, {Col: "invoice_no", Dir: "asc"}},
		Period:       "allocated_at",
		CiteKind:     RefPayment, CiteIDCol: "payment_id", CiteLabelCol: "payment_no", CiteLabelPrefix: "Payment ",
	}
}

func deliveryRunsView() ViewDef { return classify(rawDeliveryRunsView()) }

func rawDeliveryRunsView() ViewDef {
	return ViewDef{
		Name: "ai.delivery_runs", Schema: "ai", Table: "delivery_runs", Title: "Delivery runs", MinTier: 2, Large: true,
		About: "One row per delivery run: run number, date, branch, truck, driver, status, how many stops, and when it left and finished.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Run id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "run_no", Label: "Run", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "scheduled_date", Label: "Date", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "truck_code", Label: "Truck", Type: typeText, Filter: true, Group: true},
			{Name: "driver_label", Label: "Driver", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "stop_count", Label: "Stops", Type: typeInt, Filter: true, Agg: true},
			{Name: "dispatched_at", Label: "Left", Type: typeTime, Filter: true, Agg: true},
			{Name: "completed_at", Label: "Finished", Type: typeTime, Filter: true, Agg: true},
		},
		Default:      []string{"run_no", "scheduled_date", "branch_label", "truck_code", "driver_label", "status", "stop_count"},
		DefaultOrder: []OrderBy{{Col: "scheduled_date", Dir: "desc"}, {Col: "run_no", Dir: "asc"}},
		Period:       "scheduled_date",
	}
}

func deliveryStopsView() ViewDef { return classify(rawDeliveryStopsView()) }

func rawDeliveryStopsView() ViewDef {
	return ViewDef{
		Name: "ai.delivery_stops", Schema: "ai", Table: "delivery_stops", Title: "Delivery stops", MinTier: 2, Large: true,
		About: "One row per delivery stop: the run and stop number, date, branch, order and customer, status, time window, the contact, phone and address, why it failed, and when it was delivered.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Stop id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "run_id", Label: "Run id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "run_no", Label: "Run", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "stop_no", Label: "Stop", Type: typeInt, Filter: true, Group: true},
			{Name: "scheduled_date", Label: "Date", Type: typeDate, Filter: true, Group: true, Agg: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "order_id", Label: "Order id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "order_no", Label: "Order", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "status", Label: "Status", Type: typeText, Filter: true, Group: true},
			{Name: "time_window", Label: "Window", Type: typeText, Filter: true, Group: true},
			{Name: "contact_name", Label: "Contact", Type: typeText, Filter: true, Group: true},
			{Name: "contact_phone", Label: "Phone", Type: typeText, Filter: true},
			{Name: "address_line", Label: "Address", Type: typeText, Filter: true},
			{Name: "city", Label: "City", Type: typeText, Filter: true, Group: true},
			{Name: "postal_code", Label: "Postal code", Type: typeText, Filter: true, Group: true},
			{Name: "failure_reason", Label: "Failure reason", Type: typeText, Filter: true},
			{Name: "delivered_at", Label: "Delivered", Type: typeTime, Filter: true, Agg: true},
		},
		Default:      []string{"scheduled_date", "run_no", "stop_no", "order_no", "customer_name", "status", "time_window"},
		DefaultOrder: []OrderBy{{Col: "scheduled_date", Dir: "desc"}, {Col: "run_no", Dir: "asc"}, {Col: "stop_no", Dir: "asc"}},
		Period:       "scheduled_date",
		CiteKind:     RefOrder, CiteIDCol: "order_id", CiteLabelCol: "order_no", CiteLabelPrefix: "Order ",
	}
}

func customersView() ViewDef { return classify(rawCustomersView()) }

func rawCustomersView() ViewDef {
	return ViewDef{
		Name: "ai.customers", Schema: "ai", Table: "customers", Title: "Customers", MinTier: 2, Large: true,
		About: "One row per customer account: account number, name, type, home branch, payment terms, whether it is on hold, tax exempt and active.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "account_no", Label: "Account", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "customer_type", Label: "Type", Type: typeText, Filter: true, Group: true},
			{Name: "branch_id", Label: "Home branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Home branch", Type: typeText, Filter: true, Group: true},
			{Name: "payment_terms_days", Label: "Payment terms (days)", Type: typeInt, Filter: true, Group: true, Agg: true},
			{Name: "on_hold", Label: "On hold", Type: typeText, Filter: true, Group: true},
			{Name: "tax_exempt", Label: "Tax exempt", Type: typeText, Filter: true, Group: true},
			{Name: "is_active", Label: "Active", Type: typeText, Filter: true, Group: true},
		},
		Default:      []string{"account_no", "name", "customer_type", "branch_label", "on_hold", "is_active"},
		DefaultOrder: []OrderBy{{Col: "name", Dir: "asc"}, {Col: "account_no", Dir: "asc"}},
		CiteKind:     RefCustomer, CiteIDCol: "id", CiteLabelCol: "account_no", CiteLabelPrefix: "Account ",
	}
}

func customerContactsView() ViewDef { return classify(rawCustomerContactsView()) }

func rawCustomerContactsView() ViewDef {
	return ViewDef{
		Name: "ai.customer_contacts", Schema: "ai", Table: "customer_contacts", Title: "Customer contacts", MinTier: 2, Large: true,
		About: "One row per account that has an invoice you can see: the account's contact (the primary one first) with name, email and phone.",
		Columns: []ColumnDef{
			{Name: "customer_id", Label: "Customer id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "account_no", Label: "Account", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "customer_name", Label: "Customer", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "contact_name", Label: "Contact", Type: typeText, Filter: true, Group: true},
			{Name: "contact_email", Label: "Email", Type: typeText, Filter: true},
			{Name: "contact_phone", Label: "Phone", Type: typeText, Filter: true},
		},
		Default:      []string{"account_no", "customer_name", "contact_name", "contact_phone"},
		DefaultOrder: []OrderBy{{Col: "customer_name", Dir: "asc"}, {Col: "account_no", Dir: "asc"}},
		CiteKind:     RefCustomer, CiteIDCol: "customer_id", CiteLabelCol: "account_no", CiteLabelPrefix: "Account ",
	}
}

func messageChannelsView() ViewDef { return classify(rawMessageChannelsView()) }

// The message channel view has no citation: a channel has no routable reference kind in the app, and the answer
// names the channel, never a message.
func rawMessageChannelsView() ViewDef {
	return ViewDef{
		Name: "ai.message_channels", Schema: "ai", Table: "message_channels", Title: "Message channels", MinTier: 2,
		About: "One row per House Messages channel you are in: name, kind, branch, how many members, your notification mode, how many messages are unread for you (blank when the mode is mentions only) and when the latest unread one arrived. Never a message or a member.",
		Columns: []ColumnDef{
			{Name: "id", Label: "Channel id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "kind", Label: "Kind", Type: typeText, Filter: true, Group: true},
			{Name: "name", Label: "Channel", Type: typeText, Filter: true, Group: true, Search: true},
			{Name: "branch_id", Label: "Branch id", Type: typeUUID, Filter: true, Hidden: true},
			{Name: "branch_label", Label: "Branch", Type: typeText, Filter: true, Group: true},
			{Name: "member_count", Label: "Members", Type: typeInt, Filter: true, Agg: true},
			{Name: "notify_mode", Label: "Notifications", Type: typeText, Filter: true, Group: true},
			{Name: "unread_count", Label: "Unread", Type: typeInt, Filter: true, Agg: true},
			{Name: "latest_unread_at", Label: "Latest unread", Type: typeTime, Filter: true, Agg: true},
		},
		Default:      []string{"name", "kind", "branch_label", "member_count", "notify_mode", "unread_count", "latest_unread_at"},
		DefaultOrder: []OrderBy{{Col: "name", Dir: "asc"}},
	}
}
