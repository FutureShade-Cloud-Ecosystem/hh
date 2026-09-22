// Package procurement owns the contractor portal's Planning board (P4.2, M3 centerpiece).
// It is the sole reader/writer of procurement_projects + procurement_project_lines
// (migration 0028) -- a NEW aggregate for the private Planning phase. Committed artifacts
// (quotes/orders/invoices) are reused downstream and are NOT modeled here (hybrid model,
// plan §4.4). SubmitRFQ composes the pricing seam in-process to price each line
// INDICATIVELY (provisional cost basis -- never authoritative).
package procurement

import "time"

// Status is a procurement project's lane on the board. The five values are also the
// procurement_projects CHECK set (migration 0028).
const (
	StatusPlanning  = "PLANNING"
	StatusQuoting   = "QUOTING"
	StatusOrdered   = "ORDERED"
	StatusFulfilled = "FULFILLED"
	StatusCancelled = "CANCELLED"
)

// Project is the list/summary view of a procurement_projects row. line_count and
// subtotal_cents are derived (aggregated over the project's lines), not stored columns.
type Project struct {
	ID            string    `json:"id"`
	CustomerID    string    `json:"customer_id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	LineCount     int       `json:"line_count"`
	SubtotalCents int64     `json:"subtotal_cents"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Line is a procurement_project_lines row. qty_entered is a DECIMAL serialized as a
// string to preserve scale. indicative_*_cents are populated by SubmitRFQ and are
// flagged priced=true; the FE renders an "indicative" badge (prices are never authoritative).
type Line struct {
	ID                       string `json:"id"`
	LineNo                   int    `json:"line_no"`
	ProductID                string `json:"product_id"`
	SKU                      string `json:"sku"`
	Description              string `json:"description"`
	QtyEntered               string `json:"qty_entered"`
	UOM                      string `json:"uom"`
	Notes                    string `json:"notes"`
	IndicativeUnitPriceCents int64  `json:"indicative_unit_price_cents"`
	IndicativeLineTotalCents int64  `json:"indicative_line_total_cents"`
	Priced                   bool   `json:"priced"`
}

// ProjectDetail is one project with its lines (Project flattened + lines[]).
type ProjectDetail struct {
	Project
	Lines []Line `json:"lines"`
}

// CreateProjectRequest is the POST /procurement/projects body.
type CreateProjectRequest struct {
	Name string `json:"name"`
}

// UpdateProjectRequest is the PUT /procurement/projects/{id} body: rename and/or lane move.
// Both fields are optional pointers so the BFF can tell "omitted" from "set to empty".
type UpdateProjectRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

// AddLineRequest is the POST /procurement/projects/{id}/lines body. Notes is optional.
type AddLineRequest struct {
	ProductID  string `json:"product_id"`
	QtyEntered string `json:"qty_entered"`
	UOM        string `json:"uom"`
	Notes      string `json:"notes"`
}

// UpdateLineRequest is the PUT /procurement/projects/{id}/lines/{lineId} body. All fields
// are optional pointers (partial update).
type UpdateLineRequest struct {
	QtyEntered *string `json:"qty_entered"`
	UOM        *string `json:"uom"`
	Notes      *string `json:"notes"`
}

// PricedLine is one line's INDICATIVE price, computed by the service via the pricing seam
// and handed to the store to persist + transition the project PLANNING -> QUOTING in one
// place. Not a wire type (internal to the SubmitRFQ seam boundary).
type PricedLine struct {
	LineID                   string
	IndicativeUnitPriceCents int64
	IndicativeLineTotalCents int64
	Priced                   bool
}
