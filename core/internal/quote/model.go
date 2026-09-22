// Package quote is the quotes read service plus the contractor-facing state machine
// (P2.5 read; P4.4/P4.5 accept/reject/convert). Module boundary: this module is the
// sole reader of quotes + quote_lines (migration 0008). Accept/Reject transition the
// quote in place; Convert hands off to the ERP order write path (it returns the new
// order's id rather than importing the order module, conversion is a coordinated
// write).
//
// DIB-387: the credit/hold gate runs HERE, not "upstream". The claim that conversion was
// gated upstream was false, Convert INSERTed an order behind a status check alone, so a
// customer on an ACTIVE credit hold converted straight past the counter. The gate now runs
// inside the convert transaction through the quote.CreditGate seam (credit.go), which
// till.HoldAwareCreditGate satisfies structurally, the SAME gate instance the POS ring uses.
package quote

import "time"

// Quote is the list/summary view of a quotes row.
type Quote struct {
	ID            string    `json:"id"`
	QuoteNo       string    `json:"quote_no"`
	CustomerID    string    `json:"customer_id"`
	Status        string    `json:"status"` // DRAFT|SENT|ACCEPTED|CONVERTED|EXPIRED|LOST
	ValidUntil    time.Time `json:"valid_until"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	TotalCents    int64     `json:"total_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

// Line is a quote_lines row.
type Line struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"` // DECIMAL as string
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// Detail is one quote with its priced lines.
type Detail struct {
	Quote
	Lines []Line `json:"lines"`
}

// Filter is the quote list query. Empty Status lists every status for the tenant.
type Filter struct {
	Status string
	Limit  int
	// Cursor pagination (HH-3.2 B4 and the fold): both halves of the quote
	// list family convert. Stable-ordering basis: created_at DESC, quote_no
	// (quote_no is unique), the cursor is the created_at/quote_no pair of
	// the last kept row.
	AfterCursor   bool
	CursorCreated string
	CursorQuoteNo string
}

// ConvertResult is what Convert returns: the new order's id/number, so the BFF can
// redirect the contractor to the freshly-created order without this module importing
// the order module.
type ConvertResult struct {
	OrderID string `json:"order_id"`
	OrderNo string `json:"order_no"`
}
