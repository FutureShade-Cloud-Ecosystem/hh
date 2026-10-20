package assist

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
		cols[i] = c
	}
	v.Columns = cols
	return v
}

// RawViewColumns lists the columns each built-in view declares, before classification, as "view.column".
func RawViewColumns() []string {
	var out []string
	for _, v := range []ViewDef{rawOrdersView(), rawOrderLinesView()} {
		for _, c := range v.Columns {
			out = append(out, v.Name+"."+c.Name)
		}
	}
	return out
}
