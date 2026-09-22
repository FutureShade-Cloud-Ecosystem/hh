// Package reporting is the DIB-95 native report substrate (the ~20 core BisTrack replacements, M4's
// gate criterion) and DIB-96 commission tracking.
//
// -- THE ONE THING TO UNDERSTAND ABOUT THIS PACKAGE ---------------------------------------------------
// A report is a DEFINITION in the database and a QUERY in this Go. The two are joined only by a code.
//
//	report_templates row   name, category, the ordered parameters it accepts   (0143, data)
//	catalog.go entry       ONE fixed SQL string + an explicit column allowlist (compiled, reviewed)
//
// No SQL text is ever stored in a row, read from a row, or built from user input. Parameters BIND; they
// never interpolate. An optional filter is written SQL-side as ($n::uuid IS NULL OR col = $n::uuid),
// never by appending a WHERE clause. An unknown parameter is refused rather than ignored, and the
// columns the database actually returned are checked against the allowlist before a single value is
// read, so a template whose SQL drifts fails loudly instead of leaking a column nobody declared.
//
// The reason is not paranoia about a hypothetical attacker. It is that a report substrate whose query
// text lives in a row is one bad import away from being a way to run arbitrary SQL as the application
// role, and the people who would do that by accident are ordinary admins doing ordinary admin things.
//
// -- THIS PACKAGE IMPORTS NO PEER MODULE --------------------------------------------------------------
// internal/reporting names no sibling domain package: not order, not billing, not inventory, not
// purchasing, not messaging. It reads their TABLES read-only through its own repository (that is what a
// reporting module IS), and it reaches sibling BEHAVIOUR only through consumer-declared seams that the
// composition root binds, the dashboard-provider discipline. There is exactly one such seam this wave:
// RepAssignmentReader, over the W5-D rep-assignment substrate. See seams.go.
//
// -- MONEY IS INTEGER CENTS. QUANTITIES ARE NOT MONEY -------------------------------------------------
// Every money column is BIGINT cents end to end, SQL, Go, JSON, CSV, and never becomes a float at any
// point. Quantities (yards, pieces, pallets) cross as float64 by an explicit ::float8 cast, because
// they are NUMERIC(18,4) display values, not money, and carrying them as pgtype.Numeric through a
// generic row reader buys nothing a report reader can see. The distinction is enforced by KindCents vs
// KindQty below and is not a matter of taste: a rounding error in a quantity is a wrong number on a
// screen, and a rounding error in money is a wrong number in someone's pay.
package reporting

import (
	"encoding/json"
	"time"
)

// -- column kinds -------------------------------------------------------------

// Column kinds. The kind decides how a value is read out of Postgres, how it is totalled, and how the
// surface renders it. It is declared per column in the catalog, never inferred from the data.
const (
	KindText  = "text"  // TEXT, rendered as-is, never totalled
	KindInt   = "int"   // BIGINT counts, totalled as int64
	KindCents = "cents" // BIGINT integer cents, totalled as int64, rendered as money
	KindQty   = "qty"   // FLOAT8 (from NUMERIC), totalled as float64, rendered to 4 decimals
	KindDate  = "date"  // TEXT already cast ::text in SQL, so no timezone can be introduced on the way out
	KindBool  = "bool"  // BOOLEAN
)

// Column is one entry in a template's EXPLICIT allowlist. Name must equal the column name the SQL
// projects, byte for byte: the runner compares the database's own field descriptions against this list
// and refuses the run if they differ.
type Column struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	// Total marks a column that rolls into the report's totals row. Only int/cents/qty may.
	Total bool `json:"total"`
}

// -- parameters ---------------------------------------------------------------

// Parameter kinds. Each maps to exactly one validation and one Go bind type; there is no "string"
// kind, on purpose, a free-text parameter is the one shape that would have to be trusted.
const (
	ParamDate = "date" // YYYY-MM-DD → time.Time
	ParamUUID = "uuid" // canonical UUID → string, or NULL when omitted
	ParamEnum = "enum" // one of ParamSpec.Values → string, or "" when omitted
	ParamInt  = "int"  // decimal integer → int64
)

// ParamSpec declares ONE parameter. The order of a template's ParamSpecs is the $1..$n bind order; that
// is why the schema is an array in 0143 and a slice here.
type ParamSpec struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Label    string   `json:"label"`
	Values   []string `json:"values,omitempty"` // ParamEnum only, the closed set
}

// -- templates ----------------------------------------------------------------

// Template is a report DEFINITION joined to its catalog entry. SQL and Columns come from the compiled
// catalog and are never serialized to a client: the surface needs to know what a report RETURNS, not
// how it is computed, and shipping the query text to a browser is how query text ends up in a bug
// report, a screenshot, and eventually a support ticket that quotes it back as a suggestion.
type Template struct {
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	Category    string      `json:"category"`
	Description string      `json:"description"`
	LegacyName  string      `json:"legacy_name"`
	SortOrder   int         `json:"sort_order"`
	IsActive    bool        `json:"is_active"`
	Params      []ParamSpec `json:"params"`
	Columns     []Column    `json:"columns"`

	// sql is unexported and has no JSON tag. It is populated from the catalog on read and is the only
	// query this template can ever run.
	sql string
}

// SQL exposes the bound query to this package's tests only through the export_test seam; production
// callers outside the package have no way to reach it.
func (t Template) sqlText() string { return t.sql }

// Category groups templates for the surface's left-hand list.
type Category struct {
	Code      string     `json:"code"`
	Label     string     `json:"label"`
	Templates []Template `json:"templates"`
}

// CategoryLabels is the display name per category code (the 0143 CHECK list).
var CategoryLabels = map[string]string{
	"SALES":      "Sales",
	"MARGIN":     "Margin",
	"AR":         "Accounts Receivable",
	"INVENTORY":  "Inventory",
	"PURCHASING": "Purchasing",
	"TAX":        "Tax",
	"PAYMENTS":   "Payments",
	"CREDIT":     "Credit",
	"DELIVERY":   "Delivery",
	"WARRANTY":   "Warranty",
	"COMMISSION": "Commissions",
}

// -- results ------------------------------------------------------------------

// Result is one executed report. Rows are positional and align with Columns by index.
type Result struct {
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	Category string         `json:"category"`
	Columns  []Column       `json:"columns"`
	Rows     [][]any        `json:"rows"`
	RowCount int            `json:"row_count"`
	Totals   map[string]any `json:"totals"`
	// Params echoes back exactly what was bound, so a screenshot of a report says what it is a report of.
	Params map[string]string `json:"params"`
	RunAt  time.Time         `json:"run_at"`
}

// Run is a persisted report artefact (report_runs). Written only on the print path.
type Run struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	TemplateCode string            `json:"template_code"`
	Name         string            `json:"name,omitempty"`
	Params       map[string]string `json:"params"`
	BranchID     string            `json:"branch_id,omitempty"`
	RowCount     int               `json:"row_count"`
	Totals       map[string]any    `json:"totals"`
	HTML         string            `json:"html,omitempty"`
	PrintJobID   string            `json:"print_job_id,omitempty"`
	RunBy        string            `json:"run_by,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Run kinds (0143 CHECK).
const (
	RunKindTemplate   = "TEMPLATE"
	RunKindCommission = "COMMISSION"
)

// PrintDocType is the print_queue doc_type this module owns (widened in 0143, 0105 precedent).
const PrintDocType = "REPORT"

// PrintRenderTarget, a report is a browser document, like a statement.
const PrintRenderTarget = "BROWSER"

// SavedView is a stored parameter set (0145). Params are re-validated on every run, never on save.
type SavedView struct {
	ID           string            `json:"id"`
	TemplateCode string            `json:"template_code"`
	OwnerUserID  string            `json:"owner_user_id"`
	Name         string            `json:"name"`
	Params       map[string]string `json:"params"`
	IsShared     bool              `json:"is_shared"`
	CreatedAt    time.Time         `json:"created_at"`
}

// jsonMap decodes a JSONB column into a string map, tolerating NULL/empty.
func jsonMap(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// jsonAnyMap decodes a JSONB totals column.
func jsonAnyMap(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
