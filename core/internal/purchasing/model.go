package purchasing

// model.go, the purchasing domain vocabulary: the PO lifecycle states, the three-way-match states, the
// wire shapes, and the error taxonomy (mirroring the landedcost/till house style). Money is integer cents
// and CAD everywhere past the boundary; quantities are DECIMAL(19,4) carried as float64 with an explicit
// epsilon, exactly as inventory carries them.

import (
	"fmt"
	"net/http"
	"time"
)

// qtyEpsilon absorbs DECIMAL(19,4) float round-trip noise when comparing quantities. Same constant, same
// reason, as inventory.qtyEpsilon, a receipt of 20.0000 must not read as 19.999999 short.
const qtyEpsilon = 1e-6

// The purchase-order lifecycle (purchasing_pos.status, migration 0120).
//
//	DRAFT      a buyer is still building it. Lines may be added and removed; nothing has been promised.
//	ORDERED    sent to the vendor. The line set is frozen, a PO is a contract, and editing one after it has
//	           been placed is how a three-way match is quietly defeated.
//	PARTIAL    some, but not all, of the ordered quantity has landed.
//	RECEIVED   every line is fully received.
//	CLOSED     deliberately finished. A PO short-shipped by the vendor is CLOSED, not left PARTIAL forever.
//	CANCELLED  abandoned before anything was received.
const (
	StatusDraft     = "DRAFT"
	StatusOrdered   = "ORDERED"
	StatusPartial   = "PARTIAL"
	StatusReceived  = "RECEIVED"
	StatusClosed    = "CLOSED"
	StatusCancelled = "CANCELLED"
)

// The three-way-match verdicts (purchasing_bills.match_state / purchasing_bill_lines.match_state).
// See migration 0121 for what each one means and why SHORT_BILLED is not folded into VARIANCE.
const (
	MatchUnmatched   = "UNMATCHED"
	MatchMatched     = "MATCHED"
	MatchVariance    = "VARIANCE"
	MatchShortBilled = "SHORT_BILLED"
)

// Bill document status.
const (
	BillDraft  = "DRAFT"
	BillPosted = "POSTED"
	BillVoid   = "VOID"
)

// The GL event type + line roles the bill journal resolves through (posting_rules, migration 0122). Never a
// literal account code, a branch may point its variance somewhere else with one row.
const (
	eventBillPosted = "vendor.bill.posted"
	roleAP          = "ap"
	roleAPAccrual   = "ap_accrual"
	rolePriceVar    = "price_variance"
	roleTax         = "tax"
)

// glSourcePOReceipt is the 0007 gl_journals.source value a purchasing document posts under. A vendor bill is
// the invoice leg of a PO receipt, so it shares the receipt's source, the CHECK set is closed and adding a
// value to it would be an edit to gl's schema.
const glSourcePOReceipt = "PO_RECEIPT"

// - wire shapes -

// Vendor is the projection purchasing needs of a vendor. It is supplied by the VendorDirectory seam;
// purchasing owns no vendor SQL and never JOINs the table.
type Vendor struct {
	ID               string `json:"id"`
	VendorNo         string `json:"vendor_no"`
	Name             string `json:"name"`
	PaymentTermsDays int    `json:"payment_terms_days"`
	IsActive         bool   `json:"is_active"`
	// IsT5018Subcontractor is the Financial Controller's mark on the vendor: the flag the year's
	// T5018 report filters on. False is the default for every vendor that has never been marked.
	IsT5018Subcontractor bool `json:"is_t5018_subcontractor"`
	// BusinessNumber is the contractor's CRA business number, the other half of the T5018 form.
	// Empty is a real answer: the contractor has not given one.
	BusinessNumber string `json:"business_number"`
}

// PO is the purchase-order header projection (list + detail).
type PO struct {
	ID         string     `json:"id"`
	PONo       string     `json:"po_no"`
	VendorID   string     `json:"vendor_id"`
	VendorName string     `json:"vendor_name"`
	BranchID   string     `json:"branch_id"`
	Status     string     `json:"status"`
	ExpectedAt string     `json:"expected_at,omitempty"`
	OrderedAt  *time.Time `json:"ordered_at,omitempty"`
	Notes      string     `json:"notes"`
	LineCount  int        `json:"line_count"`

	// - DIB-421 document currency -
	// Currency/FXRate are the DOCUMENT's, frozen when the PO was agreed (0134). Every unit cost
	// on this PO's LINES is expressed IN THIS CURRENCY. SubtotalCents likewise: it is the
	// document total a buyer reconciles against the vendor's paperwork, so converting it would
	// make the number on screen disagree with the number on the vendor's order confirmation.
	// Everything downstream of the boundary, receipts, stock, the match, the GL, is CAD.
	Currency string  `json:"currency"`
	FXRate   float64 `json:"fx_rate"`
	// DeliveryMethod is PICKUP, DELIVERY or UNKNOWN: how the goods are expected to arrive at the
	// yard, or the fact that nobody has recorded it (the 0217 and 0219 ports of the ERP's 0214 and
	// 0216). Correctable through PUT /purchasing/pos/{id}/delivery-method (DIB-572).
	DeliveryMethod string `json:"delivery_method"`
	// SubtotalCadCents is the same total AFTER toCAD, carried alongside rather than instead of,
	// because both numbers are real and a reader who cannot see both cannot check the rate.
	//
	// BOTH TOTALS ARE `json:"-"` AND REACH THE WIRE THROUGH POCosts (cost_gate.go). The int64 stays
	// because the arithmetic that builds it must not be able to dereference a nil; the KEY a client
	// reads is contributed by the embedded gate, so a caller the dealer's cost_visibility refuses
	// gets a purchase order with no money key on it at all.
	SubtotalCadCents int64 `json:"-"`

	SubtotalCents int64 `json:"-"`

	// Warranty credit applied up to subtotal, ruling R3.
	WarrantyCreditAppliedCents int64   `json:"-"`
	WarrantyClaimID            *string `json:"warranty_claim_id,omitempty"`
	WarrantyClaimNo            *string `json:"warranty_claim_no,omitempty"`
	NetTotalCents              int64   `json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// POCosts contributes `subtotal_cents` + `subtotal_cad_cents` for an allowed caller and nothing
	// whatsoever for a refused one. Stamped by exactly one producer (costLens.costs).
	POCosts
}

// OpenWarrantyCredit describes available or applied vendor warranty credit.
type OpenWarrantyCredit struct {
	ClaimID     string  `json:"claim_id"`
	ClaimNo     string  `json:"claim_no"`
	AmountCents int64   `json:"amount_cents"`
	Description string  `json:"description"`
	Summary     *string `json:"summary,omitempty"`
	VendorID    *string `json:"vendor_id,omitempty"`
	VendorName  *string `json:"vendor_name,omitempty"`
}

// ApplyWarrantyCreditRequest carries the parameters for applying warranty credit.
type ApplyWarrantyCreditRequest struct {
	ClaimID     *string `json:"claim_id,omitempty"`
	AmountCents *int64  `json:"amount_cents,omitempty"`
}

// POLine is one purchasing_po_lines row plus the derived outstanding quantity the receiving worklist reads.
type POLine struct {
	ID              string  `json:"id"`
	LineNo          int     `json:"line_no"`
	ProductID       string  `json:"product_id"`
	SKU             string  `json:"sku"`
	Description     string  `json:"description"`
	QtyOrderedBase  float64 `json:"qty_ordered_base"`
	QtyReceivedBase float64 `json:"qty_received_base"`
	QtyBilledBase   float64 `json:"qty_billed_base"`
	QtyOutstanding  float64 `json:"qty_outstanding_base"`
	UOM             string  `json:"uom"`
	// The three money figures are `json:"-"` and reach the wire through the embedded POCosts -
	// see cost_gate.go for why the gate is at the marshal layer and not on the route.
	UnitCostCents  int64 `json:"-"`
	LineTotalCents int64 `json:"-"`
	BilledCents    int64 `json:"-"`
	POCosts
}

// PODetail is a PO with its lines and its receipts.
type PODetail struct {
	PO
	Lines              []POLine            `json:"lines"`
	Receipts           []Receipt           `json:"receipts"`
	OpenWarrantyCredit *OpenWarrantyCredit `json:"open_warranty_credit,omitempty"`
}

// Receipt is one receiving event.
type Receipt struct {
	ID            string        `json:"id"`
	POID          string        `json:"po_id"`
	PONo          string        `json:"po_no"`
	Seq           int           `json:"seq"`
	GRNNo         string        `json:"grn_no"`
	StockDocID    string        `json:"stock_doc_id"`
	LandedCostID  string        `json:"landed_cost_id,omitempty"`
	ValueCadCents int64         `json:"-"` // → POCosts.ValueCad
	ReceivedBy    string        `json:"received_by,omitempty"`
	ReceivedAt    time.Time     `json:"received_at"`
	Notes         string        `json:"notes"`
	Lines         []ReceiptLine `json:"lines,omitempty"`
	POCosts
}

// ReceiptLine is one line of a receiving event.
type ReceiptLine struct {
	ID            string  `json:"id"`
	POLineID      string  `json:"po_line_id"`
	ProductID     string  `json:"product_id"`
	SKU           string  `json:"sku"`
	QtyBase       float64 `json:"qty_base"`
	UnitCostCents int64   `json:"-"` // → POCosts.UnitCost
	ValueCents    int64   `json:"-"` // → POCosts.Value
	POCosts
}

// WorklistItem is one row of the receiving worklist: a placed PO with something still outstanding.
type WorklistItem struct {
	POID           string  `json:"po_id"`
	PONo           string  `json:"po_no"`
	VendorID       string  `json:"vendor_id"`
	VendorName     string  `json:"vendor_name"`
	BranchID       string  `json:"branch_id"`
	Status         string  `json:"status"`
	ExpectedAt     string  `json:"expected_at,omitempty"`
	OpenLines      int     `json:"open_lines"`
	QtyOutstanding float64 `json:"qty_outstanding_base"`
	ValueOpenCents int64   `json:"-"` // → POCosts.ValueOpen
	POCosts
}

// Bill is a vendor bill header projection.
type Bill struct {
	ID         string `json:"id"`
	BillNo     string `json:"bill_no"`
	VendorID   string `json:"vendor_id"`
	VendorName string `json:"vendor_name"`
	BranchID   string `json:"branch_id"`
	POID       string `json:"po_id"`
	PONo       string `json:"po_no"`
	Status     string `json:"status"`
	MatchState string `json:"match_state"`
	BillDate   string `json:"bill_date"`
	DueDate    string `json:"due_date,omitempty"`
	// SubtotalCents/TaxCents/TotalCents are CAD, ALWAYS. The bill is converted at ENTRY (DIB-421),
	// so the three-way match, the four journal legs and the AP balance never handle a foreign
	// amount, the match's own doc comment has said so since 0121 and now it is true of USD too.
	SubtotalCents int64 `json:"subtotal_cents"`
	TaxCents      int64 `json:"tax_cents"`
	TotalCents    int64 `json:"total_cents"`
	// Currency/FXRate record what the VENDOR wrote and the rate we read it at. An AP clerk holding
	// the paper invoice needs both to agree with the screen; without them a converted total looks
	// like a transcription error.
	Currency       string     `json:"currency"`
	FXRate         float64    `json:"fx_rate"`
	GLJournalID    string     `json:"gl_journal_id,omitempty"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
	VarianceNote   string     `json:"variance_accepted_note,omitempty"`
	VarianceByUser string     `json:"variance_accepted_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// BillLine is one bill line with its match verdict + the two signed variances.
type BillLine struct {
	ID                 string  `json:"id"`
	LineNo             int     `json:"line_no"`
	POLineID           string  `json:"po_line_id,omitempty"`
	ProductID          string  `json:"product_id,omitempty"`
	SKU                string  `json:"sku,omitempty"`
	Description        string  `json:"description"`
	QtyBilledBase      float64 `json:"qty_billed_base"`
	QtyOrderedBase     float64 `json:"qty_ordered_base"`
	QtyReceivedBase    float64 `json:"qty_received_base"`
	UnitCostCents      int64   `json:"unit_cost_cents"`
	POUnitCostCents    int64   `json:"po_unit_cost_cents"`
	LineTotalCents     int64   `json:"line_total_cents"`
	MatchState         string  `json:"match_state"`
	QtyVarianceBase    float64 `json:"qty_variance_base"`
	PriceVarianceCents int64   `json:"price_variance_cents"`
}

// Instalment is one scheduled payment against a bill.
type Instalment struct {
	ID          string `json:"id"`
	Seq         int    `json:"seq"`
	DueDate     string `json:"due_date"`
	AmountCents int64  `json:"amount_cents"`
	Status      string `json:"status"`
	Note        string `json:"note"`
}

// BillDetail is a bill with its lines and instalment schedule.
type BillDetail struct {
	Bill
	Lines       []BillLine   `json:"lines"`
	Instalments []Instalment `json:"instalments"`
}

// Tolerance is the resolved three-way-match tolerance for a branch (the branch row, else the org default).
type Tolerance struct {
	BranchID            string  `json:"branch_id,omitempty"`
	QtyPct              float64 `json:"qty_tolerance_pct"`
	PricePct            float64 `json:"price_tolerance_pct"`
	PriceAbsCents       int64   `json:"price_tolerance_abs_cents"`
	IsBranchOverride    bool    `json:"is_branch_override"`
	ResolvedFromDefault bool    `json:"resolved_from_default"`
}

// - requests -

// CreatePORequest is the POST /purchasing/pos body. The PO number is minted server-side (next_po_no) and is
// never accepted from a caller.
type CreatePORequest struct {
	VendorID   string          `json:"vendor_id"`
	BranchID   string          `json:"branch_id"`
	ExpectedAt string          `json:"expected_at"`
	Notes      string          `json:"notes"`
	Lines      []AddLineParams `json:"lines"`

	// DIB-421. Omitted → CAD at fx 1.0, which is what every pre-W5 caller sends and where the
	// conversion is the identity. "USD" needs a positive fx_rate or the create is refused: a USD
	// order with no rate is not a cheap order, it is an unpriced one.
	Currency string  `json:"currency"`
	FXRate   float64 `json:"fx_rate"`
}

// AddLineParams is one PO line on create or on POST .../lines.
type AddLineParams struct {
	ProductID      string  `json:"product_id"`
	Description    string  `json:"description"`
	QtyOrderedBase float64 `json:"qty_ordered_base"`
	UOM            string  `json:"uom"`
	UnitCostCents  int64   `json:"unit_cost_cents"`
}

// UpdateLineParams is the PUT .../lines/{lineID} body: the quantity or the cost of a line already
// on the draft, each optional, absent meaning unchanged. The product is not editable through it.
type UpdateLineParams struct {
	QtyOrderedBase *float64 `json:"qty_ordered_base"`
	UnitCostCents  *int64   `json:"unit_cost_cents"`
}

// SetExpectedDateRequest is the PUT /purchasing/pos/{id}/expected-date body, DIB-460's edit path.
// An EMPTY expected_at clears the date: a withdrawn vendor promise leaves us with no date, and a
// stale one left on the record is a screen that lies to the person planning around it.
type SetExpectedDateRequest struct {
	ExpectedAt string `json:"expected_at"`
}

// SetDeliveryMethodRequest is the PUT /purchasing/pos/{id}/delivery-method body, DIB-572's
// correction path (ported from the ERP). An EMPTY value is REFUSED rather than folded to a
// default: on a correction it is a request that would silently erase what a buyer recorded.
// UNKNOWN is set by NAMING it.
type SetDeliveryMethodRequest struct {
	DeliveryMethod string `json:"delivery_method"`
}

// TransitionRequest is the POST /purchasing/pos/{id}/transition body: an OPERATOR-driven lifecycle move.
// The receipt-driven moves (→ PARTIAL, → RECEIVED) are never requested; they are computed from what landed.
type TransitionRequest struct {
	To string `json:"to"`
}

// ReceiveRequest is the POST /purchasing/pos/{id}/receive body: the quantities landing right now.
type ReceiveRequest struct {
	Lines []ReceiveLineParams `json:"lines"`
	Notes string              `json:"notes"`
}

// ReceiveLineParams receives an explicit quantity against one PO line.
type ReceiveLineParams struct {
	POLineID string  `json:"po_line_id"`
	QtyBase  float64 `json:"qty_base"`
}

// LandedCostRequest is the POST /purchasing/receipts/{id}/landed-cost body, the freight/duty/handling
// invoice for one truckload, apportioned over exactly the lines of that receipt.
type LandedCostRequest struct {
	Currency      string  `json:"currency"`
	FXRate        float64 `json:"fx_rate"`
	FreightCents  int64   `json:"freight_cents"`
	DutyCents     int64   `json:"duty_cents"`
	HandlingCents int64   `json:"handling_cents"`
	Basis         string  `json:"allocation_basis"`
}

// CreateBillRequest is the POST /purchasing/bills body.
type CreateBillRequest struct {
	BillNo   string `json:"bill_no"`
	POID     string `json:"po_id"`
	BillDate string `json:"bill_date"`
	DueDate  string `json:"due_date"`
	// TaxCents is IN THE BILL'S CURRENCY, like every other amount the vendor wrote on the invoice.
	// It is converted with the rest at the boundary.
	TaxCents    int64              `json:"tax_cents"`
	Lines       []BillLineParams   `json:"lines"`
	Instalments []InstalmentParams `json:"instalments"`

	// DIB-421. The BILL's currency, which need NOT be the PO's, a vendor may invoice a USD order
	// in CAD, and the rate on the invoice is the rate they used. Inheriting the PO's would compare
	// two differently-converted numbers in the match while calling them the same currency.
	Currency string  `json:"currency"`
	FXRate   float64 `json:"fx_rate"`
}

// BillLineParams is one billed line. po_line_id may be empty, a charge with no PO behind it (see 0121).
type BillLineParams struct {
	POLineID      string  `json:"po_line_id"`
	Description   string  `json:"description"`
	QtyBilledBase float64 `json:"qty_billed_base"`
	UnitCostCents int64   `json:"unit_cost_cents"`
}

// InstalmentParams is one scheduled payment. Σ amounts must equal the bill total.
type InstalmentParams struct {
	DueDate     string `json:"due_date"`
	AmountCents int64  `json:"amount_cents"`
	Note        string `json:"note"`
}

// PostBillRequest is the POST /purchasing/bills/{id}/post body. AcceptVariance is the recorded, deliberate
// acceptance a VARIANCE bill needs, without it the post is refused with the variance spelled out.
type PostBillRequest struct {
	AcceptVariance bool   `json:"accept_variance"`
	Note           string `json:"note"`
}

// SetToleranceRequest is the PUT /purchasing/tolerance body. An empty branch_id sets the ORG DEFAULT.
type SetToleranceRequest struct {
	BranchID      string  `json:"branch_id"`
	QtyPct        float64 `json:"qty_tolerance_pct"`
	PricePct      float64 `json:"price_tolerance_pct"`
	PriceAbsCents int64   `json:"price_tolerance_abs_cents"`
}

// - error taxonomy (mirrors internal/landedcost) -

type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

func badReq(format string, a ...any) error { return clientErr(http.StatusBadRequest, format, a...) }

func unprocessable(format string, a ...any) error {
	return clientErr(http.StatusUnprocessableEntity, format, a...)
}

func conflict(format string, a ...any) error { return clientErr(http.StatusConflict, format, a...) }

var (
	// errNotFound is also the answer for a PO outside the caller's branch reach on every id door
	// (read, mutate, send, document, costs, lines, receiving): an out of reach order and a missing
	// one are the same answer, so the status cannot be used to probe which ids exist. errForbidden
	// is kept for a branch the caller names outright (branch_id) and for the role wall.
	errNotFound     = clientError{status: http.StatusNotFound, msg: "purchasing: not found"}
	errNoDB         = clientError{status: http.StatusServiceUnavailable, msg: "purchasing: database not configured"}
	errUnauthorized = clientError{status: http.StatusUnauthorized, msg: "purchasing: authentication required"}
	errForbidden    = clientError{status: http.StatusForbidden, msg: "purchasing: caller cannot access this branch"}

	// ErrStockSeamUnwired is the honest refusal. Receiving means MOVING STOCK, and purchasing does not move
	// stock, inventory does, through the StockReceiver seam. With that seam unwired, writing a receipt row
	// would record that goods arrived while the ledger, the weighted-average cost and GRNI all stayed
	// untouched: the yard would believe the stock is there, the balance sheet would not, and the divergence
	// would be silent and permanent. Refusing is the only safe answer, and it is a 503 (a wiring fault, not
	// the caller's mistake) so an operator reads "this deployment is misconfigured" rather than "your input
	// was wrong". Proven by TestReceive_StockSeamUnwired_RefusesAndWritesNothing.
	ErrStockSeamUnwired = clientError{
		status: http.StatusServiceUnavailable,
		msg:    "purchasing: stock seam not wired, refusing to record a receipt that would move no stock",
	}

	// ErrVendorSeamUnwired mirrors it for the vendor directory: a PO must name a real, active vendor, and
	// with no directory there is nothing to check that against.
	ErrVendorSeamUnwired = clientError{
		status: http.StatusServiceUnavailable,
		msg:    "purchasing: vendor directory not wired",
	}

	// ErrUnknownVendor is the directory's "no such vendor". It is exported so the composition-root adapter
	// can return it without purchasing having to interpret a pgx error, the module never learns what
	// database the directory is backed by.
	ErrUnknownVendor = clientError{status: http.StatusNotFound, msg: "purchasing: unknown vendor"}

	// ErrLandedSeamUnwired mirrors it for the landed-cost tie-in.
	ErrLandedSeamUnwired = clientError{
		status: http.StatusServiceUnavailable,
		msg:    "purchasing: landed-cost seam not wired",
	}

	// ErrGLSeamUnwired mirrors it for bill posting: a payable that books no journal is an invisible liability.
	ErrGLSeamUnwired = clientError{
		status: http.StatusServiceUnavailable,
		msg:    "purchasing: GL seam not wired, refusing to post a bill that would book no journal",
	}

	// ErrAPPaymentsReaderUnwired mirrors it for the AP subledger read: an open-AP balance that silently
	// ignores settlements is a wrong number wearing a tie-out's clothes.
	ErrAPPaymentsReaderUnwired = clientError{
		status: http.StatusServiceUnavailable,
		msg:    "purchasing: AP payments reader not wired, refusing an open AP balance that would ignore settlements",
	}
)
