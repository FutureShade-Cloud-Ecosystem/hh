// Package order is the orders read service (P2.3) plus the one narrow write the portal
// owns: delivery instructions on a future-scheduled delivery (CP-06). Module boundary:
// this module is the sole reader of orders + order_lines (migration 0008) and reads
// manifests (0012) for delivery status; it does NOT create or fulfill orders (that is
// the ERP order-to-cash write path) and it does NOT touch invoices/payments. The
// delivery_instructions column it writes is additive (migration 0027).
package order

import "time"

// Order is the list/summary view of an orders row.
type Order struct {
	ID            string    `json:"id"`
	OrderNo       string    `json:"order_no"`
	CustomerID    string    `json:"customer_id"`
	QuoteID       string    `json:"quote_id"`    // "" when not quote-originated
	Status        string    `json:"status"`      // DRAFT..CLOSED|CANCELLED (0008 CHECK)
	Fulfillment   string    `json:"fulfillment"` // PICKUP|DELIVERY|WILL_CALL
	RequestedDate time.Time `json:"requested_date"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	TotalCents    int64     `json:"total_cents"`
	CreatedAt     time.Time `json:"created_at"`
	// HasBackorder is true while any line is short and still owed (migration 0366). Absent means
	// false, so a recorded capture without it stays valid.
	HasBackorder bool `json:"has_backorder,omitempty"`
}

// Line is an order_lines row, including allocation/fulfillment progress for the status
// tracker.
type Line struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"` // DECIMAL as string
	UOMEntered     string `json:"uom_entered"`
	QtyBase        string `json:"qty_base"`
	AllocatedBase  string `json:"allocated_base"`
	FulfilledBase  string `json:"fulfilled_base"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	// BackorderEntered and BackorderBase are what the yard picked short and the order still owes
	// the customer; qty_entered is what is to be fulfilled now (ordered = quantity + backorder).
	BackorderEntered string `json:"backorder_entered"`
	BackorderBase    string `json:"backorder_base"`
}

// Detail is one order with its lines and the delivery instructions the contractor has
// attached for a scheduled delivery.
type Detail struct {
	Order
	Lines                []Line `json:"lines"`
	DeliveryInstructions string `json:"delivery_instructions"`
}

// Filter is the order list query. Empty Status lists every status for the tenant.
type Filter struct {
	Status string
	Limit  int
	// Cursor pagination (HH-3.2 fold), the tenant list half.
	// Stable-ordering basis: created_at DESC, order_no (unique); the
	// cursor is that pair.
	AfterCursor   bool
	CursorCreated string
	CursorOrderNo string
}
