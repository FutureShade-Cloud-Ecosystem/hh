package landedcost

// model.go, the landed-cost DTOs, status/account constants, and the error taxonomy. Money is integer
// cents (BIGINT); quantities are float64 projections of the DECIMAL(19,4) columns. All *_cad_cents amounts
// are CAD, the USD→CAD conversion (the DX-19 scoped exception) happens in-module at Create time so the GL
// and the inventory valuation only ever see CAD.

import (
	"fmt"
	"net/http"
	"time"
)

// Landed-cost status lifecycle (mirrors the 0041 landed_costs.status CHECK set).
const (
	StatusDraft         = "DRAFT"
	StatusAllocated     = "ALLOCATED"
	StatusPosted        = "POSTED"
	StatusVoided        = "VOIDED"
	StatusRetroAdjusted = "RETRO_ADJUSTED"
)

// GL account codes the capitalization / retro-split / void journals post to. landedcost was frozen WITHOUT a
// RuleResolver seam (unlike invcount), and main.go wires no rule adapter for it, so these are module constants
// keyed to the frozen 0007 chart (1200 Inventory · 5000 COGS · 2100 Accounts Payable). Recorded as a deviation.
const (
	acctInventory      = "1200" // capitalized landed cost raises inventory value
	acctCOGS           = "5000" // the retro sold-through portion expenses straight to COGS
	acctFreightPayable = "2100" // the freight/duty/handling owed (Accounts Payable)
)

// GL journal source (the 0007 source CHECK set). A landed cost attaches to a PO receipt.
const (
	glSourcePOReceipt  = "PO_RECEIPT"
	glSourceAdjustment = "ADJUSTMENT"
)

// LandedCost is the landed_costs header projection.
type LandedCost struct {
	ID                 string    `json:"id"`
	POID               string    `json:"po_id"`
	PORef              string    `json:"po_ref"`
	BranchID           string    `json:"branch_id"`
	Currency           string    `json:"currency"`
	FXRate             float64   `json:"fx_rate"`
	FreightCents       int64     `json:"freight_cents"`
	DutyCents          int64     `json:"duty_cents"`
	HandlingCents      int64     `json:"handling_cents"`
	FreightCadCents    int64     `json:"freight_cad_cents"`
	DutyCadCents       int64     `json:"duty_cad_cents"`
	HandlingCadCents   int64     `json:"handling_cad_cents"`
	AllocationBasis    string    `json:"allocation_basis"`
	ExcludeSpecialBuys bool      `json:"exclude_special_buys"`
	Status             string    `json:"status"`
	RetroOfID          string    `json:"retro_of_id,omitempty"`
	GLJournalID        string    `json:"gl_journal_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// totalCadCents is the CAD landed cost to apportion (freight + duty + handling, already CAD).
func (lc LandedCost) totalCadCents() int64 {
	return lc.FreightCadCents + lc.DutyCadCents + lc.HandlingCadCents
}

// CreateInput is the POST /landed-costs body. currency+fx_rate drive the USD→CAD conversion (DX-19). When
// retro_of_id is set the doc is a late-freight retro-adjustment; on_hand carries the per-product still-on-hand
// base used to split capitalize-vs-sold-through at RetroAdjust time (the frozen Capitalizer seam surfaces no
// on-hand reader, so the split basis is captured here, see the deviation note).
type CreateInput struct {
	POID               string       `json:"po_id"`
	PORef              string       `json:"po_ref"`
	BranchID           string       `json:"branch_id"`
	Currency           string       `json:"currency"`
	FXRate             float64      `json:"fx_rate"`
	FreightCents       int64        `json:"freight_cents"`
	DutyCents          int64        `json:"duty_cents"`
	HandlingCents      int64        `json:"handling_cents"`
	AllocationBasis    string       `json:"allocation_basis"`
	ExcludeSpecialBuys *bool        `json:"exclude_special_buys"`
	RetroOfID          string       `json:"retro_of_id"`
	OnHand             []OnHandLine `json:"on_hand"`
}

// OnHandLine is the still-on-hand base for one received product at retro time.
type OnHandLine struct {
	ProductID  string  `json:"product_id"`
	POLineID   string  `json:"po_line_id"`
	OnHandBase float64 `json:"on_hand_base"`
}

// Allocation is one apportioned share of a landed cost (one row of landed_cost_allocations).
type Allocation struct {
	POLineID            string  `json:"po_line_id"`
	ProductID           string  `json:"product_id"`
	BasisWeight         float64 `json:"basis_weight"`
	AllocatedCadCents   int64   `json:"allocated_cad_cents"`
	SoldThroughCadCents int64   `json:"sold_through_cad_cents"`
	// PreApplyAvgUnitCost is the exact per-unit weighted-average cost captured at Apply, BEFORE this allocation
	// capitalized into it (0058 landed_cost_allocations.pre_apply_avg_unit_cost). Void restores it verbatim so an
	// Apply/Void round-trip reproduces the WAC to the cent even for sub-unit fractional on-hand (L8 MAJOR). A NULL
	// column (rows written before 0058, or seed/retro rows) scans as HasPreApplyAvg=false → Void falls back to the
	// legacy reverse-via-negate path, so old applied docs stay voidable.
	PreApplyAvgUnitCost int64 `json:"pre_apply_avg_unit_cost,omitempty"`
	HasPreApplyAvg      bool  `json:"-"`
}

// AllocationPreview is the live per-basis preview the allocate screen renders (no persistence).
type AllocationPreview struct {
	Basis         string        `json:"basis"`
	TotalCadCents int64         `json:"total_cad_cents"`
	Lines         []PreviewLine `json:"lines"`
}

// PreviewLine is one product's previewed share under a basis.
type PreviewLine struct {
	ProductID         string  `json:"product_id"`
	POLineID          string  `json:"po_line_id"`
	BasisWeight       float64 `json:"basis_weight"`
	AllocatedCadCents int64   `json:"allocated_cad_cents"`
	IsSpecialBuy      bool    `json:"is_special_buy"`
}

// LandedCostDetail is the header + its allocations (the GET /{id} projection).
type LandedCostDetail struct {
	LandedCost
	Allocations []Allocation `json:"allocations"`
}

// - error taxonomy (mirrors the till/pricing house style) -

type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

var (
	errNotFound     = clientError{status: http.StatusNotFound, msg: "landedcost: not found"}
	errNoDB         = clientError{status: http.StatusServiceUnavailable, msg: "landedcost: database not configured"}
	errUnauthorized = clientError{status: http.StatusUnauthorized, msg: "landedcost: authentication required"}
	// errForbidden is the horizontal-access boundary: a staff-gated caller who may not reach THIS branch. A
	// MISSING landed cost still returns errNotFound (never a leak of existence via the authz path), mirroring the
	// order-history / till payment-history not-found-vs-forbidden split.
	errForbidden = clientError{status: http.StatusForbidden, msg: "landedcost: caller cannot access this branch"}
)
