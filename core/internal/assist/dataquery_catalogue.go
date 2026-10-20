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

func (v ViewDef) col(name string) (ColumnDef, bool) {
	for _, c := range v.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return ColumnDef{}, false
}

// classHeld reports whether the caller holds a column class. Plain is held by everyone with a role;
// every other class waits for slice 6's registry, so it is held by no one yet. Written as a function
// of the Caller so the later slice changes one place.
func classHeld(c Caller, class string) bool {
	switch class {
	case ClassPlain:
		return c.HasRole()
	case ClassPayment:
		return c.PaymentTrail
	}
	return false
}

// builtinViews is the shipped catalogue, before any asker's filtering.
func builtinViews() []ViewDef {
	return []ViewDef{ordersView(), orderLinesView()}
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
			if classHeld(c, col.Class) {
				fv.Columns = append(fv.Columns, col)
			}
		}
		out = append(out, fv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
