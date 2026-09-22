// Package billing is the invoices + AR read service (P2.1). It backs the portal's
// invoice list/detail and the dashboard's open-balance / available-credit figures,
// and is the AR side of the multi-invoice pay flow. Module boundary: this module is
// the sole reader of invoices + invoice_lines + customer_transactions (migration
// 0009); the payments module owns the write that settles a balance. It assumes the
// ERP posting path maintains invoices.balance_cents (a coordinated upstream concern).
package billing

import "time"

// Invoice is the AR view of an invoices row. BalanceCents is the open amount;
// HoldbackCents is the Bill 60 legally-retained portion that must be EXCLUDED from
// what a contractor can pay (resolve exclusion before any pay slice, §4.2 #7).
type Invoice struct {
	ID            string    `json:"id"`
	InvoiceNo     string    `json:"invoice_no"`
	CustomerID    string    `json:"customer_id"`
	OrderID       string    `json:"order_id"` // "" when not order-linked
	Status        string    `json:"status"`   // DRAFT|FINALIZED|PARTIALLY_PAID|PAID|VOID
	IssueDate     time.Time `json:"issue_date"`
	DueDate       time.Time `json:"due_date"`
	SubtotalCents int64     `json:"subtotal_cents"`
	TaxCents      int64     `json:"tax_cents"`
	HoldbackCents int64     `json:"holdback_cents"`
	TotalCents    int64     `json:"total_cents"`
	BalanceCents  int64     `json:"balance_cents"`
	Lines         []Line    `json:"lines,omitempty"` // populated by GetInvoice, omitted in lists
}

// Line is an invoice_lines row.
type Line struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"` // DECIMAL as string (preserve scale)
	UOMEntered     string `json:"uom_entered"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
}

// Filter is the invoice list query. Status is optional (one of the CHECK values);
// the cursor pair carries the clamped window. Empty Status lists every status for
// the tenant. Cursor pagination (HH-3.2 fold): stable-ordering basis issue_date
// DESC, invoice_no (unique); the cursor is that pair.
type Filter struct {
	Status          string
	Limit           int
	AfterCursor     bool
	CursorIssueDate string
	CursorInvoiceNo string
}
