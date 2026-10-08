package pos

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SaleType mirrors the counter sale fulfilment mode (D-A19-1). COD is the W7 exception: it is a PAYMENT
// POSTURE, not a fulfilment mode, the client's "cash on DEMAND" pay-as-you-go account (2026-06-30 call),
// NOT cash-on-delivery. A COD ring works for any fulfilment (SaleInput.Fulfilment, default PICKUP), gets a
// FINALIZED invoice number immediately (customers need it to pay), and is LOCKED from fulfilment until
// paid: no stock decrement, no order.fulfilled at ring.
const (
	SaleCollectNow   = "COLLECT_NOW"   // counter pickup  → orders.fulfillment PICKUP
	SaleCollectLater = "COLLECT_LATER" // will-call       → orders.fulfillment WILL_CALL
	SaleDelivered    = "DELIVERED"     // delivery        → orders.fulfillment DELIVERY
	SaleCOD          = "COD"           // cash on DEMAND  → orders.fulfillment from SaleInput.Fulfilment (default PICKUP)
)

// Tender is the POS tender choice. ACCOUNT means NO payment row + an AR invoice.
const (
	TenderAccount = "ACCOUNT"
	TenderCash    = "CASH"
	TenderCard    = "CARD"
	TenderCheque  = "CHEQUE"
)

// SaleInput is the idempotent POST /api/v1/pos/sales body (offline-first: the client mints IdempotencyKey).
type SaleInput struct {
	IdempotencyKey string          `json:"idempotency_key"` // client UUID v4, REQUIRED (replay-safe)
	BranchID       string          `json:"branch_id"`
	TillSessionID  string          `json:"till_session_id"`
	CustomerID     string          `json:"customer_id"`          // "" → the house WALK-IN (resolved server-side)
	SaleType       string          `json:"sale_type"`            // COLLECT_NOW default
	Fulfilment     string          `json:"fulfilment,omitempty"` // COD only, PICKUP|WILL_CALL|DELIVERY ("" → PICKUP); other sale types carry fulfilment in sale_type
	CashSale       bool            `json:"cash_sale"`
	Tender         string          `json:"tender"`        // ACCOUNT|CASH|CARD|CHEQUE (for COD: the settlement method later collected)
	PaymentToken   string          `json:"payment_token"` // CARD only, client-tokenized; PAN NEVER sent
	CardType       string          `json:"card_type,omitempty"`
	SignatureRef   string          `json:"signature_ref"`
	Relief         *ReliefDecision `json:"relief,omitempty"`
	RequestedDate  string          `json:"requested_date"`
	Notes          string          `json:"notes"`
	// -- DIB-488 rev 6 (item D, 0168): optional counter-sale facts under "More options" --
	// POJobRef is the customer's PO / job reference ("PO 4471", "SMITH DECK"), free text, ≤64
	// chars (422-in-words before the 0168 CHECK could 500). SalespersonID is the staff member
	// credited with the sale; the terminal defaults it to the signed-in operator. Both optional.
	POJobRef      string `json:"po_job_ref,omitempty"`
	SalespersonID string `json:"salesperson_id,omitempty"`
	// -- DIB-488 rev 8 (item 11, 0170): the JOB fields joining the 0168 pair under More options -
	// same persistence pattern (on the order, length-capped, 422-in-words, replay-verbatim, audit
	// Extra, sold-panel facts). Both optional free text.
	JobAddress   string `json:"job_address,omitempty"`   // ≤200 chars
	JobReference string `json:"job_reference,omitempty"` // ≤64 chars
	// DIB-488 rev 6 (item F): the optional cash-tendered quick entry. Only meaningful on a plain
	// CASH-tendered, fully-paid ring; must cover the total (change due = tendered − total). 0 =
	// not entered, one-click cash is unchanged.
	CashTenderedCents int64  `json:"cash_tendered_cents,omitempty"`
	Offline           bool   `json:"offline"`
	DepositCents      int64  `json:"deposit_cents,omitempty"`  // unpaid-at-ring only (ACCOUNT/COD); 0 < deposit < total
	DepositMethod     string `json:"deposit_method,omitempty"` // CASH|CARD|CHEQUE, taken through the till at ring
	// SupplierDepositCents is what the SUPPLIER demanded on a special order
	// (sub item 9.5): billed to a credit account at once as its own invoice,
	// never taken at the counter. Allowed only on a credit shape (an ACCOUNT
	// tender, terms above zero, a credit line, no counter deposit, no split).
	SupplierDepositCents int64       `json:"supplier_deposit_cents,omitempty"`
	Lines                []LineInput `json:"lines"`
	// Discount is the optional whole-ticket discount, struck from the subtotal after every line
	// discount. It names the manager who approved it (discount.go).
	Discount *DiscountInput `json:"discount,omitempty"`
	// DIB-165 split tender: 2..6 legs that must sum EXACTLY to the sale total. When set, `tender`
	// above is ignored (the primary leg is derived) and `deposit_cents` is refused, a split settles
	// the sale itself. See split_tender.go.
	Tenders []TenderLegInput `json:"tenders,omitempty"`

	// swapCreditCents is the credit note a swap spends on this sale (10.5): unexported, so no JSON
	// body can set it. The sale applies min(credit, total) to the new order, the tender takes the rest.
	swapCreditCents int64
}

// LineInput is one cart line. The PRICE is NOT in the body, it is resolved server-side, and no
// cost rides the ring either: the till rings the customer's rule and tier price and the cost is
// the core's own reading (D48 row 481, D49).
type LineInput struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
	// Discount is the optional manual discount on this line (discount.go): a percent or an
	// amount, with the manager who said yes at the counter.
	Discount *DiscountInput `json:"discount,omitempty"`
}

// Sale is the committed result (also returned verbatim on an idempotent replay).
type Sale struct {
	SwapID        string `json:"swap_id,omitempty"` // set when the sale is the new order of a swap
	OrderID       string `json:"order_id"`
	OrderNo       string `json:"order_no"`
	InvoiceID     string `json:"invoice_id"`
	InvoiceNo     string `json:"invoice_no"`
	PaymentID     string `json:"payment_id,omitempty"`
	GatewayRef    string `json:"gateway_ref,omitempty"`
	Tender        string `json:"tender"`
	SaleType      string `json:"sale_type"`
	SubtotalCents int64  `json:"subtotal_cents"`
	TaxCents      int64  `json:"tax_cents"` // PROVISIONAL flat-13% (D-A19-9); M5 owns the real value
	TotalCents    int64  `json:"total_cents"`
	DepositCents  int64  `json:"deposit_cents,omitempty"` // taken at ring on an unpaid sale; balance = total − deposit
	// SupplierDepositCents is the supplier's demanded deposit billed to the
	// account (its own FINALIZED invoice, posted to Customer Deposits); the
	// sale's own invoice carries the rest. SupplierDepositInvoiceID/No name
	// the deposit document for the receipt and the Summary.
	SupplierDepositCents     int64  `json:"supplier_deposit_cents,omitempty"`
	SupplierDepositInvoiceID string `json:"supplier_deposit_invoice_id,omitempty"`
	SupplierDepositInvoiceNo string `json:"supplier_deposit_invoice_no,omitempty"`
	TaxReasonCode            string `json:"tax_reason_code"`
	Status                   string `json:"status"`
	Replayed                 bool   `json:"replayed"`
	// -- DIB-488 rev 6 -- the item-D facts echoed back (and on a replay, read back off the order),
	// plus the item-F change-due arithmetic the sold panel shows. All absent when not used.
	POJobRef          string `json:"po_job_ref,omitempty"`
	SalespersonID     string `json:"salesperson_id,omitempty"`
	SalespersonName   string `json:"salesperson_name,omitempty"`
	CashTenderedCents int64  `json:"cash_tendered_cents,omitempty"`
	ChangeDueCents    int64  `json:"change_due_cents,omitempty"`
	// -- DIB-488 rev 8 (item 11, 0170): the JOB fields echoed back (and read back on a replay).
	JobAddress   string `json:"job_address,omitempty"`
	JobReference string `json:"job_reference,omitempty"`
	// DIB-165: SplitTender flags a sale settled across several tenders; Tenders carries the legs in
	// submission order (the receipt prints them). Both are absent on a single-tender sale.
	SplitTender bool              `json:"split_tender,omitempty"`
	Tenders     []TenderLegResult `json:"tenders,omitempty"`
	// DiscountCents is every manual discount on the sale together; Discounts lists them, each
	// with the role of the manager who approved it (the receipt's "Discount approved by").
	DiscountCents int64          `json:"discount_cents,omitempty"`
	Discounts     []SaleDiscount `json:"discounts,omitempty"`
}

// ReliefDecision is the per-transaction FN relief assessment recorded with the sale (US-A19-06).
type ReliefDecision struct {
	PurchaserType string `json:"purchaser_type"`
	DeclaredUse   string `json:"declared_use"`
	Fulfilment    string `json:"fulfilment"`
	ProfileID     string `json:"profile_id"`
	ReasonCode    string `json:"reason_code"`
	EvidenceDocID string `json:"evidence_doc_id,omitempty"`
	SignatureRef  string `json:"signature_ref,omitempty"`
}

// StockGlance is the read-only multi-branch on-hand display (US-A19-08).
//
// DIB-488: the glance now also carries the UNIT META the counter needs to speak the stock number in
// the item's SELL unit ("In stock · 250 SF available"), and the branch low thresholds where the
// dealer has authored reorder points (0117 product_reorder_points). All of it is EXISTING
// availability data surfaced in one read, the glance still never decrements or allocates, and a
// product with no reorder point simply has no low threshold (the client renders in/out only; the
// absence of a threshold is not an error).
type StockGlance struct {
	ProductID string             `json:"product_id"`
	OnHand    map[string]float64 `json:"on_hand"` // branch_id → AVAILABLE qty (base UOM; on_hand − reserved)
	// Unit meta (DIB-488). SellFactor is base units per ONE sell unit (product_uoms.qty_in_base for
	// the sell UOM); 0 means "no conversion on file" and the client must not divide by it.
	BaseUOM    string  `json:"base_uom,omitempty"`
	SellUOM    string  `json:"sell_uom,omitempty"`
	SellFactor float64 `json:"sell_factor,omitempty"`
	// LowBase is branch_id → reorder_point_base for ACTIVE reorder-point rows only. Absent branch =
	// no authored threshold there.
	LowBase map[string]float64 `json:"low_base,omitempty"`
	// DIB-488 rev 8 (item 9): the product's unit weight (kg per BASE unit), DISCOVERED existing
	// catalogue data (products.weight_per_unit_kg), surfaced on the same display-only read so the
	// ticket can speak a per-line Total Weight honestly. 0/absent = no weight on file (the line
	// shows an em-dash and is excluded from the footer total, never a guessed number).
	WeightKgPerBase float64 `json:"weight_kg_per_base,omitempty"`
}

// -- tax reason codes (W4 records the decision; the deterministic engine is M5) --
const (
	TaxStdON13      = "STD_ON_HST_13"           // 13% provisional default
	TaxONFNPos8     = "ON_FN_POS_8"             // Ontario First Nations point-of-sale 8% credit → collect 5%
	TaxFedVendorDel = "FED_S87_VENDOR_DELIVERY" // federal 0% provisional, Dibbits delivery to a reserve
	TaxManualReview = "MANUAL_TAX_REVIEW"       // routed to a person
	TaxReliefDecld  = "RELIEF_DECLINED_EVIDENCE"

	ruleVersionW4    = "W4-flat-13"
	flatHSTRateBps   = 1300 // 13% binding fallback at W4 (M5 owns the real rate)
	ontarioCreditBps = 800  // the 8% Ontario credit when ON_FN_POS_8
	ontarioNetBps    = 500  // 13% - 8% = the 5% net collected on the Ontario path
)

// SaleView is the operator-reopen read (US-A19-09). It deliberately carries NO FN-evidence identifiers
// and NO granular flow log, that deep view is the A-21 admin surface (RequireAdmin).
type SaleView struct {
	OrderID       string         `json:"order_id"`
	OrderNo       string         `json:"order_no"`
	CustomerID    string         `json:"customer_id"`
	BranchID      string         `json:"branch_id"`
	SaleType      string         `json:"sale_type"`
	Tender        string         `json:"tender"`
	Status        string         `json:"status"`
	SubtotalCents int64          `json:"subtotal_cents"`
	TaxCents      int64          `json:"tax_cents"`
	TotalCents    int64          `json:"total_cents"`
	TaxReasonCode string         `json:"tax_reason_code"`
	InvoiceID     string         `json:"invoice_id"`
	InvoiceNo     string         `json:"invoice_no"`
	PaymentID     string         `json:"payment_id,omitempty"`
	Lines         []SaleViewLine `json:"lines"`
	// DIB-165: the settlement legs, so a reopened/reprinted sale shows how it was actually paid
	// rather than only the primary tender.
	SplitTender bool              `json:"split_tender,omitempty"`
	Tenders     []TenderLegResult `json:"tenders,omitempty"`
	// The manual discounts the sale carries (discount.go), absent when there were none.
	DiscountCents int64          `json:"discount_cents,omitempty"`
	Discounts     []SaleDiscount `json:"discounts,omitempty"`
}

// SaleViewLine is one reopened line with its resolved (engine) price, no evidence.
type SaleViewLine struct {
	LineNo         int     `json:"line_no"`
	ProductID      string  `json:"product_id"`
	Description    string  `json:"description"`
	QtyEntered     float64 `json:"qty_entered"`
	UOMEntered     string  `json:"uom_entered"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	PriceUOM       string  `json:"price_uom"`
	LineTotalCents int64   `json:"line_total_cents"` // what was charged: net of every discount
	// DiscountCents is this line's own discount, SaleDiscountCents its share of the whole-ticket
	// discount; both are already out of LineTotalCents and absent when there was none.
	DiscountCents     int64 `json:"discount_cents,omitempty"`
	SaleDiscountCents int64 `json:"sale_discount_cents,omitempty"`
}

// ReliefAssessInput drives the per-transaction "Assess tax relief" entry point (US-A19-06). It is a
// per-transaction assessment, it NEVER references a permanent customer exemption flag, and it has NO
// card-image / scan upload field (data-minimization, FN spec §15; pinned by U26).
type ReliefAssessInput struct {
	ProfileID        string `json:"profile_id"`        // pre-populates from purchaser_eligibility_profile (masked at the edge)
	OrderID          string `json:"order_id"`          // optional, when set, the assessment snapshot is recorded
	PurchaserType    string `json:"purchaser_type"`    // fallback when no profile
	DeclaredUse      string `json:"declared_use"`      // personal|commercial|band|mixed|unknown
	Fulfilment       string `json:"fulfilment"`        // pickup|dibbits_truck|dibbits_carrier|purchaser_carrier
	SubtotalCents    int64  `json:"subtotal_cents"`    // pre-tax basis for the displayed outcome
	EvidenceAttached bool   `json:"evidence_attached"` // POD/waybill present (the 0% hard gate)
	PurposeAck       bool   `json:"purpose_ack"`       // PIPEDA purpose notice acknowledged before collection
}

// ReliefOutcome is the assess-relief result, masked-only, with the displayed 0%/5%/13%/review outcome
// and the snapshot-shaped fields. At W4 the BINDING collected tax stays flat-13% (M5 owns the engine);
// the outcome is recorded as the auditable "why".
type ReliefOutcome struct {
	PurchaserType     string `json:"purchaser_type"`
	MaskedIdentifier  string `json:"masked_identifier"` // the ONLY identifier a cashier ever sees
	Outcome           string `json:"outcome"`           // "0%"|"5%"|"13%"|"REVIEW"
	ReasonCode        string `json:"reason_code"`
	RateBps           int    `json:"rate_bps"` // binding rate at W4 (13% unless the Ontario 5% path)
	FullHSTCents      int64  `json:"full_hst_cents"`
	OnCreditCents     int64  `json:"on_credit_cents"`
	NetCollectedCents int64  `json:"net_collected_cents"`
	Automated         bool   `json:"automated"`          // ALWAYS false at W4 (M5 flips it true)
	RequiresEvidence  bool   `json:"requires_evidence"`  // the 0% fulfilment hard gate
	EvidenceSatisfied bool   `json:"evidence_satisfied"` // POD/waybill attached
}

// -- internal row types for the M2-debt writes (consumed by Repository + the test fake) --

type orderRow struct {
	CustomerID    string
	BranchID      string
	Status        string
	Fulfillment   string
	RequestedDate string
	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	CreatedBy     string
	Metadata      map[string]any
	// DIB-488 rev 6 (0168): the optional counter-sale facts, carried onto the committed order.
	POJobRef      string
	SalespersonID string
	// DIB-488 rev 8 (0170): the JOB fields, same pattern, same insert.
	JobAddress   string
	JobReference string
}

type orderLineRow struct {
	LineNo         int
	ProductID      string
	Description    string
	QtyEntered     float64
	UOMEntered     string
	QtyBase        float64
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64 // net of every discount: what the customer pays and what a return refunds against
	TaxCents       int64 // per-line tax (0042 order_lines.tax_cents / invoice_lines.tax_cents); largest-remainder split of the header tax
	// The manual discount facts (0365, discount.go). DiscountCents is the line's own discount,
	// SaleDiscountCents its share of the ticket discount, both already out of LineTotalCents.
	DiscountCents      int64
	DiscountPercentBps int    // the percent entered, 0 when the discount was an amount
	DiscountApprovedBy string // staff id of the approving manager, "" with no discount
	DiscountRoleID     string // that manager's role (staff_role id) when approving
	SaleDiscountCents  int64
	// What the manager's yes was given for, stored beside the discount (0365): the quantity and
	// the line total it was struck from.
	DiscountApprovedQty  float64
	DiscountApprovedBase int64
}

type invoiceRow struct {
	CustomerID string
	OrderID    string
	BranchID   string
	Status     string
	// IssueDate and DueDate are YYYY-MM-DD bound by the service on the
	// ring's clock in the house zone: the ring never leaves either to the
	// invoices table's CURRENT_DATE default, which names the database
	// session's day (the dealer's day rule, see platform/houseday).
	IssueDate     string // YYYY-MM-DD
	DueDate       string // YYYY-MM-DD
	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	BalanceCents  int64
	CreatedBy     string
	// SourceKind is the document's provenance (invoices.source_kind, 0146):
	// "" for the ring's ordinary invoice, SUPPLIER_DEPOSIT for the deposit
	// document a supplier demanded (its GL posting is the liability, not
	// revenue; sub item 9.5).
	SourceKind string
}

type customerTxnRow struct {
	CustomerID   string
	TxnType      string // INVOICE | PAYMENT
	AmountCents  int64
	BalanceAfter int64
	SourceType   string
	SourceRefID  string
	BranchID     string
}

type posSaleRow struct {
	IdempotencyKey string
	OrderID        string
	InvoiceID      string
	PaymentID      string
	BranchID       string
	TillSessionID  string
	SaleType       string
	CashSale       bool
	Tender         string
	TaxReasonCode  string
	OfflineOrigin  bool
	CreatedBy      string
	SplitTender    bool // DIB-165, TRUE when pos_sale_tenders carries the legs
	// The whole-ticket discount (0365), zero value when none.
	Discount saleDiscountRow
}

type taxSnapshotRow struct {
	OrderID           string
	OrderLineID       string
	TaxReasonCode     string
	RuleVersion       string
	RateBps           int
	FullHSTCents      int64
	OnCreditCents     int64
	NetCollectedCents int64
	Automated         bool
	EvidenceDocID     string
	SignatureID       string
}

type signatureRow struct {
	Purpose      string
	RefType      string
	RefID        string
	SignatureEnc []byte
	KeyVersion   int
	Provider     string
	CapturedBy   string
}

// customerRec is the credit-gate + pricing read of a customer.
type customerRec struct {
	ID               string
	CustomerType     string
	PaymentTermsDays int
	CreditLimitCents int64
	OpenBalanceCents int64
}

// eligibilityMasked is the masked-only read of a purchaser_eligibility_profile (never the cleartext id).
type eligibilityMasked struct {
	ProfileID          string
	PurchaserType      string
	MaskedIdentifier   string
	OntarioEligibility string
	EligibilityType    string
	Expired            bool
}

// -- error taxonomy (mirrors internal/pricing) ------------------------------
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }
func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

// ErrCreditBlocked aborts an ACCOUNT sale that fails the credit gate.
type ErrCreditBlocked struct{ Policy string }

func (e ErrCreditBlocked) Error() string { return fmt.Sprintf("pos: credit blocked (%s)", e.Policy) }

// ErrNoOpenTill aborts a tendered sale (CASH/CARD/CHEQUE) with no OPEN till session to attach to.
type ErrNoOpenTill struct{}

func (ErrNoOpenTill) Error() string { return "pos: open a till session first" }

// -- the card charge ring's typed answers (the card charge design 3.3 to 3.7) --
//
// A ring that took a card capture never answers a bare 500: the money moved, so the answer names
// what became of it. The handler maps each of these to the design's word (503
// sale_failed_card_reversed, 409 card_charge_needs_manager, card_charge_in_flight,
// card_charge_unresolved_on_till, 402 card_charge_card_declined).

// ErrSaleFailedCardReversed answers a ring whose sale transaction failed after the card was
// captured and whose every captured leg was then reversed: no money remains and no sale exists;
// the client clears its key and rings again.
var ErrSaleFailedCardReversed = errors.New("pos: the sale failed and every card charge it took was reversed")

// ErrCardChargeNeedsManager answers a ring holding a card charge whose outcome, or whose
// reversal's outcome, could not be told: the money may be held with no sale. A manager has to
// close the attempt before the card is charged again; Refs names the attempts' short references.
type ErrCardChargeNeedsManager struct{ Refs []string }

func (e ErrCardChargeNeedsManager) Error() string {
	if len(e.Refs) == 0 {
		return "pos: a card charge on this sale has no answer from the card processor, a manager has to check it"
	}
	return fmt.Sprintf("pos: a card charge on this sale has no answer from the card processor, a manager has to check it (attempts %s)",
		strings.Join(e.Refs, ", "))
}

// ErrCardChargeCardDeclined answers a ring a card leg was declined on: no money moved for that
// leg, and any card already captured for the sale was reversed (the design's split decline).
var ErrCardChargeCardDeclined = errors.New("pos: a card leg was declined, any card already charged for this sale was reversed")

// ErrCardChargeUnresolvedOnTill answers the till guard's refusal: an earlier card charge for this
// amount at this till has no answer yet, under another sale key.
var ErrCardChargeUnresolvedOnTill = errors.New("pos: an earlier card charge for this amount at this till has no answer yet")

// ErrCardChargeInFlight answers a same-key double submit: the leg's charge is already claimed or
// captured by another request. The database's unique leg key settled the race; the loser charges
// nothing and answers this.
var ErrCardChargeInFlight = errors.New("pos: the card charge for this sale is still in flight")

// ErrUnknownTaxReason aborts a relief ring that named a tax reason the engine refuses to bind (outside
// the lawful reason universe, the mirror of the snapshot CHECK). It is the exported sentinel the
// composition's tax adapter translates the engine's refusal into (tops_adapters.go), and the writeErr
// arm maps it to the counter's 400. Wrapping keeps the engine's own words, the offending reason, in
// the message the counter reads.
var ErrUnknownTaxReason = errors.New("pos: that tax reason is not one the counter can ring; choose a listed relief reason or ring the sale standard")

// ErrTaxRailUnavailable refuses the tax-bound doors (ringing a sale, pricing an
// order, assessing relief, reading the tax rate) when the wired tax engine
// reports no standard rate row in effect: the rate table is not configured for
// the date, tax cannot be assessed, and answering an assessed shape anyway is
// not an answer (a sale rung untaxed, a quote priced untaxed, a read answering
// rate 0). pos never imports the tax package: the TaxAssessor
// seam in sale_hooks.go is the boundary, and the composition root's adapter
// (cmd/core posTaxAdapter) translates the tax engine's typed refusal into this
// sentinel, which writeErr maps to the counter-readable 503. The flat default
// assessor never produces it, so unwired scaffold compositions are unchanged.
var ErrTaxRailUnavailable = errors.New("pos: the tax rate table is not configured, tax cannot be assessed")

var (
	errBadRequest    = clientError{status: http.StatusBadRequest, msg: "pos: invalid request"}
	errMissingKey    = clientError{status: http.StatusBadRequest, msg: "idempotency_key (client UUID) is required"}
	errNoLines       = clientError{status: http.StatusBadRequest, msg: "pos: a sale needs at least one line"}
	errBadTender     = clientError{status: http.StatusBadRequest, msg: "pos: tender must be ACCOUNT, CASH, CARD or CHEQUE"}
	errCardDeclined  = clientError{status: http.StatusPaymentRequired, msg: "card declined, try another tender"}
	errCardOffline   = clientError{status: http.StatusConflict, msg: "card needs connectivity, take cash/cheque or retry online"}
	errSaleNotFound  = clientError{status: http.StatusNotFound, msg: "pos: sale not found"}
	errNoSigProvider = clientError{status: http.StatusServiceUnavailable, msg: "pos: signature provider unavailable"}

	// -- W7 counter-v2 errors --
	errCODDisabled      = clientError{status: http.StatusUnprocessableEntity, msg: "cash-on-demand sales are disabled by policy for this branch"}
	errBadFulfilment    = clientError{status: http.StatusUnprocessableEntity, msg: "pos: fulfilment must be PICKUP, WILL_CALL or DELIVERY"}
	errDepositOnPaid    = clientError{status: http.StatusUnprocessableEntity, msg: "pos: a deposit is only valid on an unpaid-at-ring sale (ACCOUNT tender or COD)"}
	errBadDepositMethod = clientError{status: http.StatusUnprocessableEntity, msg: "pos: deposit_method must be CASH, CARD or CHEQUE"}
	errBadRefundMethod  = clientError{status: http.StatusUnprocessableEntity, msg: "pos: refund_method must be CASH, CARD or ACCOUNT_CREDIT"}
	errReturnNotFound   = clientError{status: http.StatusNotFound, msg: "pos: no committed POS sale found for that order"}
	errCardRefundNoRef  = clientError{status: http.StatusUnprocessableEntity, msg: "pos: card refunds need the original card sale (no gateway reference on this order)"}
	errNoCardRefunder   = clientError{status: http.StatusServiceUnavailable, msg: "pos: card refund rail not configured"}
	errNoReturnLines    = clientError{status: http.StatusBadRequest, msg: "pos: a return needs at least one line"}
	errParkedNotFound   = clientError{status: http.StatusNotFound, msg: "pos: parked ticket not found"}
	errPricingNotFound  = clientError{status: http.StatusNotFound, msg: "pos: pricing order not found"}

	// -- DIB-488 rev 6 (items D + F) --
	errPORefTooLong        = clientError{status: http.StatusUnprocessableEntity, msg: "pos: po_job_ref must be 64 characters or fewer"}
	errSalespersonNotFound = clientError{status: http.StatusUnprocessableEntity, msg: "pos: salesperson not found (must be an active staff member)"}
	errCashTenderedNotCash = clientError{status: http.StatusUnprocessableEntity, msg: "pos: cash_tendered_cents rides only a plain CASH-tendered, fully-paid sale"}
	errCashTenderedShort   = clientError{status: http.StatusUnprocessableEntity, msg: "pos: cash_tendered_cents must cover the sale total"}

	// -- DIB-488 rev 8 (item 11, 0170) --
	errJobAddressTooLong   = clientError{status: http.StatusUnprocessableEntity, msg: "pos: job_address must be 200 characters or fewer"}
	errJobReferenceTooLong = clientError{status: http.StatusUnprocessableEntity, msg: "pos: job_reference must be 64 characters or fewer"}

	// -- A-06 portal return requests --
	errBadFeeBps       = clientError{status: http.StatusUnprocessableEntity, msg: "pos: fee must be between 0% and 100%"}
	errReservedKey     = clientError{status: http.StatusUnprocessableEntity, msg: "pos: idempotency keys with the reserved \"rreq:\" prefix are internal to the return-request approval rail"}
	errFeeNeedsCredit  = clientError{status: http.StatusUnprocessableEntity, msg: "pos: a restocking fee is only supported on ACCOUNT_CREDIT refunds"}
	errBadReasonCode   = clientError{status: http.StatusUnprocessableEntity, msg: "pos: reason_code must be DAMAGED, DEFECT, WRONG_ITEM, NO_LONGER_NEEDED or OTHER"}
	errOtherNeedsNote  = clientError{status: http.StatusUnprocessableEntity, msg: "pos: reason OTHER needs a note explaining the return"}
	errRequestNotFound = clientError{status: http.StatusNotFound, msg: "pos: return request not found"}
	errRejectNeedsWhy  = clientError{status: http.StatusBadRequest, msg: "pos: a rejection needs a reason"}
)

// flatHST13 is the W4 PROVISIONAL 13% HST fallback (D-A19-9). M5 owns the real tax_cents. Round half-up.
func flatHST13(subtotalCents int64) int64 { return (subtotalCents*flatHSTRateBps + 5000) / 10000 }

// -- W7 counter-v2 domain types (0043: pos_policy / pos_returns / pos_parked_tickets / pricing_orders) --

// Refund rails for a POS return.
const (
	RefundCash          = "CASH"           // drawer pay-out, needs an OPEN till session
	RefundCard          = "CARD"           // processor reversal against the original capture
	RefundAccountCredit = "ACCOUNT_CREDIT" // credit the customer's account (invoice offset + credit memo)
)

// Return pickup statuses (pos_returns.pickup_status, the logistics hand-off surface).
const (
	PickupNone      = "NONE"
	PickupRequested = "REQUESTED"
	PickupScheduled = "SCHEDULED"
	PickupDone      = "DONE"
)

// The two DIB-488/D-096 line-presentation values (pos_policy.line_presentation; 0165).
const (
	LinePresentationLines = "LINES" // compact thumbnail line table, the D-096 default
	LinePresentationCards = "CARDS" // tap-tile product grid as the primary items surface
)

// DIB-488 rev 2 (0166): the two POS workspace arrangements and the split bounds. split_pct is the
// INVENTORY-LOOKUP panel's share of the workspace width, side-independent, swapping the sides
// does not renumber the split.
const (
	ArrangementLookupLeft = "LOOKUP_LEFT" // inventory-lookup panel on the left (the default)
	ArrangementSaleLeft   = "SALE_LEFT"   // sale panel on the left
	WorkspaceSplitMin     = 20
	WorkspaceSplitMax     = 80
	WorkspaceSplitDefault = 55
)

// DIB-488 rev 3 (0167): the two presentations of the INVENTORY-LOOKUP panel's results. Same data,
// same behaviors, only the shape changes. CARDS (the shipped grid) stays the default.
const (
	LookupPresentationCards = "CARDS" // the existing product-card grid, the default
	LookupPresentationRows  = "ROWS"  // dense listed rows: name · SKU · category · price · stock
)

// DIB-488 rev 8 (0170): who may RECEIVE margin/est-profit fields in POS payloads. The gate runs at
// the marshal layer, a non-allowed caller's payload carries the fields BYTE-ABSENT.
const (
	MarginVisibilityManagerUp = "MANAGER_UP" // Tier-1/Tier-2 staff (the default)
	MarginVisibilityAllStaff  = "ALL_STAFF"
	MarginVisibilityHidden    = "HIDDEN" // nobody, the dealer switched the surface off
)

// 0181: who may RECEIVE a customer's past-due balance on the Customer step. The same three words as
// margin visibility, gated the same way at the same layer, but ALL_STAFF by default, because a
// past-due balance is the CUSTOMER's fact and the counter person is the one being asked to raise it.
const (
	BalanceVisibilityAllStaff  = "ALL_STAFF" // the default
	BalanceVisibilityManagerUp = "MANAGER_UP"
	BalanceVisibilityHidden    = "HIDDEN"
)

// PosPolicy is the branch-overridable POS behavior policy (pos_policy; branch NULL = org default).
type PosPolicy struct {
	BranchID         string `json:"branch_id"` // "" = the org-wide default row
	CODEnabled       bool   `json:"cod_enabled"`
	RestockingFeeBps int    `json:"restocking_fee_bps"` // A-06: fee on an approved portal return (0 = none; commander-ruled default)
	Notes            string `json:"notes"`
	// DIB-488 / D-096 (0165): dealer-level counter-workspace presentation.
	LinePresentation  string `json:"line_presentation"`   // LINES | CARDS
	QuickChipsEnabled bool   `json:"quick_chips_enabled"` // the quick category chip row renders at all
	// DIB-488 rev 2 (0166): dealer-level workspace DEFAULTS ("configure once at dealer level").
	// What a till opens on before the person has moved anything; per-user ergonomics live in
	// pos_user_workspace and override these whole-or-absent (see workspace.go).
	WorkspaceSplitPct    int    `json:"workspace_split_pct"`   // 20-80; the lookup panel's default share
	WorkspaceArrangement string `json:"workspace_arrangement"` // LOOKUP_LEFT | SALE_LEFT
	// DIB-488 rev 3 (0167): the dealer-default lookup-results presentation.
	LookupPresentation string `json:"lookup_presentation"` // CARDS | ROWS
	// DIB-488 rev 8 (0170): who may RECEIVE margin/est-profit fields in POS payloads, the
	// marshal-layer gate's dealer switch. MANAGER_UP (default) | ALL_STAFF | HIDDEN.
	MarginVisibility string `json:"margin_visibility"`
	// 0181: who may RECEIVE a customer's past-due balance on the Customer step. Same vocabulary and
	// the same marshal-layer posture as MarginVisibility, deliberately, a dealer configuring the
	// counter meets ONE idea about visibility, not two. The default differs (ALL_STAFF) because the
	// two protect opposite things: margin is the dealer's own position, a past-due balance is the
	// customer's own fact and the counter person is the one being asked to raise it.
	// ALL_STAFF (default) | MANAGER_UP | HIDDEN.
	BalanceVisibility string `json:"balance_visibility"`
	// DIB-488 rev 8 (0170, item 4): the curated single-row quick-chip categories. Stored as a
	// comma-separated TEXT column; served as a list. Empty on the wire = the service DERIVED
	// nothing (no curated set and no sales history), the client falls back to the full chip row.
	QuickChipSet []string `json:"quick_chip_set,omitempty"`
}

// WorkspacePrefs is the RESOLVED POS workspace for the calling user: their pos_user_workspace row
// when one exists (source "user"), else the pos_policy dealer defaults (source "dealer"). The
// override is whole-or-absent, the dashboard_user_tiles ruling, so there is never a field merge.
type WorkspacePrefs struct {
	SplitPct        int    `json:"split_pct"`
	Arrangement     string `json:"arrangement"`
	LookupCollapsed bool   `json:"lookup_collapsed"`
	SaleCollapsed   bool   `json:"sale_collapsed"`
	// DIB-488 rev 3 (0167): CARDS | ROWS, rides the same whole-or-absent resolution.
	LookupPresentation string `json:"lookup_presentation"`
	// DIB-488 rev 8 (0170): the margin quick-hide (eye) toggle, rides the same row.
	MarginHidden bool   `json:"margin_hidden"`
	Source       string `json:"source"` // "user" | "dealer"
	Replayed     bool   `json:"replayed,omitempty"`
}

// WorkspaceSaveInput is the PUT /api/v1/pos/workspace body, one person saving their own workspace.
// The idempotency key is the settings-write idiom: minted once per save, retried verbatim on a
// network error; a replay of the LAST accepted key returns the stored row without a second write.
type WorkspaceSaveInput struct {
	IdempotencyKey  string `json:"idempotency_key"`
	SplitPct        int    `json:"split_pct"`
	Arrangement     string `json:"arrangement"`
	LookupCollapsed bool   `json:"lookup_collapsed"`
	SaleCollapsed   bool   `json:"sale_collapsed"`
	// DIB-488 rev 3 (0167): rides the same whole-or-absent save ("" normalizes to CARDS).
	LookupPresentation string `json:"lookup_presentation"`
	// DIB-488 rev 8 (0170): the margin quick-hide toggle rides the same whole-or-absent save.
	MarginHidden bool `json:"margin_hidden"`
}

// Salesperson is one row of the counter's salesperson selector (DIB-488 rev 6, item D): the
// active staff members, id + display name only, no email, no role, nothing a counter surface
// does not need. Served by GET /api/v1/pos/salespeople (requireStaff).
type Salesperson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// userWorkspaceRow is the stored pos_user_workspace row (0166; lookup_presentation added by 0167).
type userWorkspaceRow struct {
	UserID             string
	SplitPct           int
	Arrangement        string
	LookupCollapsed    bool
	SaleCollapsed      bool
	LookupPresentation string
	// DIB-488 rev 8 (0170): the margin quick-hide toggle.
	MarginHidden bool
	WriteKey     string
}

// ReturnLineInput is one returned line (a fraction or all of one original order line).
type ReturnLineInput struct {
	OrderLineID string  `json:"order_line_id"`
	QtyBase     float64 `json:"qty_base"`
	ReasonCode  string  `json:"reason_code"`
	// Disposition says what became of the goods (sub item 10.3): RESTOCK, DAMAGED or NO_RETURN.
	// Empty falls back to the whole return's restock flag (true is RESTOCK, false NO_RETURN), the
	// single order path's meaning from before dispositions existed.
	Disposition string `json:"disposition,omitempty"`
}

// The three dispositions of a returned line (pos_return_lines.disposition, 0378).
const (
	DispositionRestock  = "RESTOCK"
	DispositionDamaged  = "DAMAGED"
	DispositionNoReturn = "NO_RETURN"
)

// PickupInput is the optional logistics pickup block on a return.
type PickupInput struct {
	Required      bool   `json:"required"`
	Site          string `json:"site"`
	RequestedDate string `json:"requested_date"` // YYYY-MM-DD
}

// ReturnInput is the idempotent POST /api/v1/pos/returns body.
type ReturnInput struct {
	IdempotencyKey string            `json:"idempotency_key"`
	OrderID        string            `json:"order_id"` // one of order_id /
	OrderNo        string            `json:"order_no"` // order_no identifies the original sale
	Lines          []ReturnLineInput `json:"lines"`
	RefundMethod   string            `json:"refund_method"` // CASH | CARD | ACCOUNT_CREDIT
	Reason         string            `json:"reason"`
	Restock        bool              `json:"restock"`
	TillSessionID  string            `json:"till_session_id"` // CASH refunds, the OPEN drawer
	Pickup         *PickupInput      `json:"pickup,omitempty"`
	// FeeBps (A-06) is the optional restocking fee, honored ONLY on the ACCOUNT_CREDIT rail: the fee is
	// derived from the ACTUAL committed subtotal inside the tx (round half-up), reduces the credited
	// amount, and books to the fee_revenue line through pos.return.committed.fee_cents. 0 = no fee.
	FeeBps int `json:"fee_bps,omitempty"`

	// ApproverCredential is the credential carried in the contract's
	// approver header beside the clerk's bearer on a return above the
	// amount (row 482): the same Bearer scheme prefix as Authorization, the
	// manager's own credential entered at the clerk's till. The handler
	// reads it from the header once and carries it verbatim; it never
	// arrives in a body (the json tag refuses) and nothing on the request
	// path logs it.
	ApproverCredential string `json:"-"`

	// internalRail marks the ONE internal caller (ApproveReturnRequest) allowed to use the reserved
	// "rreq:" idempotency-key namespace. Unexported, so it can never be set from a JSON body, a public
	// POST /api/v1/pos/returns with an "rreq:" key is refused (422) before it can hijack a request's
	// replay slot.
	internalRail bool

	// railOptional marks a child of a swap (10.5) whose leftover is zero, in
	// which case no refund rail runs and the rail's own preconditions (a drawer, a card capture, a
	// spendable account) are not asked for.
	railOptional bool

	// noteChild marks a child of a returns-across note (returns_across.go), whose idempotency key
	// is DERIVED under the reserved "note:" namespace. Unexported for the same reason: a public
	// body can never claim it, only ReturnAcross's own children carry it.
	noteChild bool
}

// Return is the committed result (returned verbatim on an idempotent replay).
type Return struct {
	ReturnID        string `json:"return_id"`
	ReturnNo        string `json:"return_no"`
	OrderID         string `json:"order_id"`
	InvoiceID       string `json:"invoice_id"`
	RefundMethod    string `json:"refund_method"`
	SubtotalCents   int64  `json:"subtotal_cents"`
	TaxCents        int64  `json:"tax_cents"`
	TotalCents      int64  `json:"total_cents"`
	FeeCents        int64  `json:"fee_cents,omitempty"`         // A-06 restocking fee, the credited amount is total − fee
	AppliedCents    int64  `json:"applied_cents,omitempty"`     // a swap child: the part of the total spent on the new order, the rail moved total less this
	RefundPaymentID string `json:"refund_payment_id,omitempty"` // the REFUNDED payments row (CASH/CARD)
	PickupStatus    string `json:"pickup_status"`
	Status          string `json:"status"`
	Replayed        bool   `json:"replayed"`
}

// ReturnLookup is the pre-return read the counter uses to build a return: the original sale's lines
// with what is still returnable. Read-only, the commit re-guards everything under FOR UPDATE.
type ReturnLookup struct {
	OrderID        string               `json:"order_id"`
	OrderNo        string               `json:"order_no"`
	BranchID       string               `json:"branch_id"`
	CustomerID     string               `json:"customer_id"`
	InvoiceID      string               `json:"invoice_id"`
	SaleTender     string               `json:"sale_tender"`
	CardRefundable bool                 `json:"card_refundable"` // original CARD capture with a gateway ref
	Lines          []ReturnableLineView `json:"lines"`
}

// ReturnableLineView is one original line with its prior-return sums netted for display.
type ReturnableLineView struct {
	OrderLineID    string  `json:"order_line_id"`
	ProductID      string  `json:"product_id"`
	Description    string  `json:"description"`
	QtySold        float64 `json:"qty_sold"`
	QtyReturned    float64 `json:"qty_returned"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	LineTotalCents int64   `json:"line_total_cents"`
	TaxCents       int64   `json:"tax_cents"`
}

// CustomerLinesInput is the customer lines read's parameters (sub item 10.1): one account customer,
// optionally narrowed to one branch. The read is the pre-return read of the returns-across flow:
// every sales order the customer holds in HH, till rings and quote conversions alike, newest first,
// each line with the quantity the commit guard would accept.
type CustomerLinesInput struct {
	CustomerID string
	BranchID   string
	Limit      int
}

// CustomerReturnableLineView is one line of one order in the customer lines read: the guard's own
// netting (qty open is qty sold less every returned qty, the same SQL the commit reads) plus the
// money and tax already back, the product facts for the tile, and the shelf state the disposition
// work reads later.
type CustomerReturnableLineView struct {
	OrderLineID        string  `json:"order_line_id"`
	ProductID          string  `json:"product_id"`
	SKU                string  `json:"sku"`
	Description        string  `json:"description"`
	QtySold            float64 `json:"qty_sold"`
	QtyReturned        float64 `json:"qty_returned"`
	QtyOpen            float64 `json:"qty_open"`
	UnitPriceCents     int64   `json:"unit_price_cents"`
	LineTotalCents     int64   `json:"line_total_cents"`
	TaxCents           int64   `json:"tax_cents"`
	MoneyReturnedCents int64   `json:"money_returned_cents"`
	TaxReturnedCents   int64   `json:"tax_returned_cents"`
	ShelfState         string  `json:"shelf_state"`
}

// CustomerOrderView is one order of the customer lines read. Source names where the order came from:
// TILL (a committed ring, the facts of which ride beside it) or QUOTE (an order a quote became,
// an order like any other that carries no till facts). FulfilmentState is the order's own ladder word
// (CONFIRMED through FULFILLED); ShelfState on each line says whether the goods ever left the shelf.
type CustomerOrderView struct {
	OrderID         string `json:"order_id"`
	OrderNo         string `json:"order_no"`
	BranchID        string `json:"branch_id"`
	BranchName      string `json:"branch_name"`
	CreatedAt       string `json:"created_at"`
	JobLabel        string `json:"job_label"`
	Source          string `json:"source"`
	SaleType        string `json:"sale_type"`
	Tender          string `json:"tender"`
	FulfilmentState string `json:"fulfilment_state"`
	InvoiceID       string `json:"invoice_id"`
	CardRefundable  bool   `json:"card_refundable"`
	// Returnable says whether the order can be returned now; when it cannot, NotReturnableReason says
	// why in words (a converted order with no live invoice has nothing to refund against yet).
	Returnable          bool                         `json:"returnable"`
	NotReturnableReason string                       `json:"not_returnable_reason"`
	Lines               []CustomerReturnableLineView `json:"lines"`
}

// CustomerLinesView is the customer lines read's answer: the customer echoed, the orders newest
// first, bounded by the limit.
type CustomerLinesView struct {
	CustomerID string              `json:"customer_id"`
	Items      []CustomerOrderView `json:"items"`
}

// ReturnAcrossOrderInput is one order's share of a returns-across note: the order id and the lines
// of that one order being returned (each a fraction or all of one original order line).
type ReturnAcrossOrderInput struct {
	OrderID string            `json:"order_id"`
	Lines   []ReturnLineInput `json:"lines"`
}

// ReturnAcrossInput is the idempotent POST /api/v1/pos/returns-across body (sub item 10.2): one
// credit note across several orders of ONE customer at ONE branch, on ONE refund rail, in ONE
// transaction. There is no fee field: the flow carries no restocking fee (the Q10 ruling). The
// per-line dispositions are sub item 10.3's; the restock boolean here is the whole note's, the
// single-order path's own flag carried across its children until 10.3 makes it per line.
type ReturnAcrossInput struct {
	IdempotencyKey string                   `json:"idempotency_key"`
	CustomerID     string                   `json:"customer_id"`
	Orders         []ReturnAcrossOrderInput `json:"orders"`
	RefundMethod   string                   `json:"refund_method"` // CASH | CARD | ACCOUNT_CREDIT
	Reason         string                   `json:"reason"`
	Restock        bool                     `json:"restock"`
	TillSessionID  string                   `json:"till_session_id"` // CASH refunds, the OPEN drawer

	// ApproverCredential rides X-Approver-Authorization beside the clerk's bearer, the single
	// return's own second-credential machinery (row 482); the handler reads it from the header.
	ApproverCredential string `json:"-"`

	// The swap's facts (10.5), unexported: the credit the swap was priced against (the note must
	// total exactly it), the part of it spent on the new order, and whether the leftover is zero
	// (then no rail runs). All zero on an ordinary note.
	swap         bool
	swapCredit   int64
	swapApplied  int64
	railOptional bool
}

// ReturnPreviewInput is the POST /api/v1/pos/returns/preview body: the draft of a returns-across
// note (the customer and the orders' lines), without a key, a rail or a drawer.
type ReturnPreviewInput struct {
	CustomerID string                   `json:"customer_id"`
	Orders     []ReturnAcrossOrderInput `json:"orders"`
}

// ReturnPreviewLine is one draft line's credit, priced by the commit's own function.
type ReturnPreviewLine struct {
	OrderLineID   string  `json:"order_line_id"`
	QtyBase       float64 `json:"qty_base"`
	SubtotalCents int64   `json:"subtotal_cents"`
	TaxCents      int64   `json:"tax_cents"`
	TotalCents    int64   `json:"total_cents"`
}

// ReturnPreviewOrder is one order's share of the draft credit.
type ReturnPreviewOrder struct {
	OrderID       string              `json:"order_id"`
	OrderNo       string              `json:"order_no"`
	SubtotalCents int64               `json:"subtotal_cents"`
	TaxCents      int64               `json:"tax_cents"`
	TotalCents    int64               `json:"total_cents"`
	Lines         []ReturnPreviewLine `json:"lines"`
}

// ReturnPreview is the credit so far: what the note would credit, money and tax, were it
// committed now. The figures are the commit's own (priceReturnLine); nothing is written.
type ReturnPreview struct {
	CustomerID    string               `json:"customer_id"`
	BranchID      string               `json:"branch_id"`
	SubtotalCents int64                `json:"subtotal_cents"`
	TaxCents      int64                `json:"tax_cents"`
	TotalCents    int64                `json:"total_cents"`
	Orders        []ReturnPreviewOrder `json:"orders"`
}

// ReturnNote is the committed credit note across orders (answered verbatim on an idempotent
// replay): its own number, its customer, its one branch, its totals summed over the children, and
// every child return with its own return number kept.
type ReturnNote struct {
	NoteID        string   `json:"note_id"`
	NoteNo        string   `json:"note_no"`
	CustomerID    string   `json:"customer_id"`
	BranchID      string   `json:"branch_id"`
	RefundMethod  string   `json:"refund_method"`
	SubtotalCents int64    `json:"subtotal_cents"`
	TaxCents      int64    `json:"tax_cents"`
	TotalCents    int64    `json:"total_cents"`
	Returns       []Return `json:"returns"`
	Replayed      bool     `json:"replayed"`
	SwapID        string   `json:"swap_id,omitempty"` // set when the note is the credit of a swap
}

// ReturnPickup is one row on the logistics pickup queue (REQUESTED/SCHEDULED returns).
type ReturnPickup struct {
	ReturnID      string `json:"return_id"`
	ReturnNo      string `json:"return_no"`
	OrderID       string `json:"order_id"`
	CustomerID    string `json:"customer_id"`
	BranchID      string `json:"branch_id"`
	Site          string `json:"site"`
	RequestedDate string `json:"requested_date,omitempty"`
	PickupStatus  string `json:"pickup_status"`
	TotalCents    int64  `json:"total_cents"`
	CreatedAt     string `json:"created_at"`
}

// ParkedLineInput is one saved-cart line, NO price (prices re-derive from the engine on resume so a
// parked ticket never rings at a stale price).
type ParkedLineInput struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
}

// ParkInput is the POST /api/v1/pos/parked body.
type ParkInput struct {
	BranchID   string            `json:"branch_id"`
	CustomerID string            `json:"customer_id"` // "" = walk-in
	SaleType   string            `json:"sale_type"`   // COLLECT_NOW default
	Notes      string            `json:"notes"`
	Lines      []ParkedLineInput `json:"lines"`
}

// ParkedTicket is one saved ticket (status PARKED until resumed/discarded). No stock reservation in V2.
type ParkedTicket struct {
	ID         string            `json:"id"`
	TicketNo   string            `json:"ticket_no"`
	BranchID   string            `json:"branch_id"`
	CustomerID string            `json:"customer_id,omitempty"`
	SaleType   string            `json:"sale_type"`
	Notes      string            `json:"notes"`
	Status     string            `json:"status"`
	Lines      []ParkedLineInput `json:"lines"`
	CreatedAt  string            `json:"created_at"`
}

// PricingOrderLineInput is one line to price on the printable pricing doc.
type PricingOrderLineInput struct {
	ProductID  string  `json:"product_id"`
	QtyEntered float64 `json:"qty_entered"`
	UOMEntered string  `json:"uom_entered"`
}

// PricingOrderInput is the POST /api/v1/pos/pricing-orders body, the client's printable price/compare
// document: NOT a quote (no acceptance lifecycle), NOT an order (no stock/AR).
type PricingOrderInput struct {
	BranchID     string                  `json:"branch_id"`
	CustomerID   string                  `json:"customer_id"`   // "" = walk-in / prospect
	CustomerName string                  `json:"customer_name"` // printable name even for walk-ins
	Notes        string                  `json:"notes"`
	ValidUntil   string                  `json:"valid_until"` // YYYY-MM-DD; "" → today+14d
	Lines        []PricingOrderLineInput `json:"lines"`
}

// PricingOrderLine snapshots the engine price AND the list price so the printed doc carries the comparison.
type PricingOrderLine struct {
	LineNo         int     `json:"line_no"`
	ProductID      string  `json:"product_id"`
	SKU            string  `json:"sku"`
	Name           string  `json:"name"`
	QtyEntered     float64 `json:"qty_entered"`
	UOMEntered     string  `json:"uom_entered"`
	UnitPriceCents int64   `json:"unit_price_cents"` // the engine price offered
	ListPriceCents int64   `json:"list_price_cents"` // the list comparison
	LineTotalCents int64   `json:"line_total_cents"`
}

// PricingOrder is the committed printable document.
type PricingOrder struct {
	ID            string             `json:"id"`
	DocNo         string             `json:"doc_no"`
	BranchID      string             `json:"branch_id"`
	CustomerID    string             `json:"customer_id,omitempty"`
	CustomerName  string             `json:"customer_name"`
	Notes         string             `json:"notes"`
	ValidUntil    string             `json:"valid_until,omitempty"`
	SubtotalCents int64              `json:"subtotal_cents"`
	TaxCents      int64              `json:"tax_cents"`
	TotalCents    int64              `json:"total_cents"`
	CreatedAt     string             `json:"created_at"`
	Lines         []PricingOrderLine `json:"lines"`
}

// -- internal row types for the W7 writes (consumed by Repository + the test fake) --

// returnOrderCtx is the pre-tx read of the original sale a return runs against.
type returnOrderCtx struct {
	OrderID       string
	OrderNo       string
	BranchID      string
	CustomerID    string
	InvoiceID     string
	InvoiceStatus string
	SaleTender    string // pos_sale.tender of the original ring
	GatewayRef    string // the original CARD capture's processor ref ("" for non-card)
	// CapturedCents is what the original capture holds (payments.amount_cents of the sale's payment,
	// 0 when it has none): the ceiling every card refund of the order is judged against (D5).
	CapturedCents int64
	// Converted marks an order a quote became (no pos_sale facet): its invoice comes from the billing
	// seam, it has no tender and no card capture.
	Converted bool
}

// cardRefundFact is one processor refund made inside a return transaction: what the processor
// holds, kept so a rolled back transaction leaves a record of it (D4) and a committed one closes it.
type cardRefundFact struct {
	RefundKey   string
	OrderID     string
	BranchID    string
	CustomerID  string
	GatewayRef  string
	RefundRef   string
	AmountCents int64
}

// returnableLine is the FOR UPDATE-locked read of one original order line plus its prior-return sums.
type returnableLine struct {
	OrderLineID    string
	ProductID      string
	QtySold        float64
	UnitPriceCents int64
	LineTotalCents int64
	TaxCents       int64
	PriorQty       float64
	PriorMoney     int64
	PriorTax       int64
	// PriorCreditOnly is the part of the earlier returns that was credit only (no goods came back).
	PriorCreditOnly float64
	// LeftShelfBase is the quantity of the line that has left the shelf: all of it on a collect now
	// ring (stock is decremented at the ring), else what fulfilment has taken out (fulfilled_base).
	LeftShelfBase float64
}

// returnRow is the pos_returns header insert.
type returnRow struct {
	OrderID         string
	InvoiceID       string
	CustomerID      string
	BranchID        string
	RefundMethod    string
	SubtotalCents   int64
	TaxCents        int64
	TotalCents      int64
	FeeCents        int64 // A-06 restocking fee (ACCOUNT_CREDIT only; 0 elsewhere)
	Reason          string
	Restock         bool
	RefundPaymentID string
	TillSessionID   string
	PickupRequired  bool
	PickupSite      string
	PickupDate      string
	PickupStatus    string
	IdempotencyKey  string
	CreatedBy       string
	// NoteID links the return to its across-orders note (0377); empty on every single-order return.
	NoteID string
	// AppliedCents is the part of the total applied to a swap's new order instead of going out on the
	// rail (0380); zero on every other return.
	AppliedCents int64
}

// returnNoteRow is the return_notes header insert (sub item 10.2).
type returnNoteRow struct {
	CustomerID     string
	BranchID       string
	RefundMethod   string
	SubtotalCents  int64
	TaxCents       int64
	TotalCents     int64
	Reason         string
	IdempotencyKey string
	CreatedBy      string
}

// returnLineRow is one pos_return_lines insert.
type returnLineRow struct {
	OrderLineID    string
	ProductID      string
	QtyBase        float64
	UnitPriceCents int64
	LineTotalCents int64
	TaxCents       int64
	ReasonCode     string
	// Disposition and the two quantities it produced (0378): what became of the goods, how much
	// went back on the shelf, how much was written off. The last two are zero for goods that never
	// left the shelf, whatever the disposition asked for.
	Disposition    string
	RestockedBase  float64
	WrittenOffBase float64
	// CreditOnlyBase is the part of the returned quantity that was credit only: the goods never left
	// the shelf, so none came back (0381).
	CreditOnlyBase float64
}

// -- DIB-488 rev 8 (item 10): the role-gated margin glance ---------------------------------------

// MarginGlanceInput is the POST /api/v1/pos/margin-glance body: the CURRENT ticket's lines, sent
// exactly as the ring body sends them (product/qty/uom, never a client price). The server derives
// each line's revenue through the SAME counter resolve the terminal prices with
// (pricing.DeriveCounterLinePrice) and marries it to the engine's cost source, the per-branch WAC
// row (product_branch_costs.avg_unit_cost_cents, per product BASE uom; the same row the pricing
// low-margin scan and inventory.BranchCostCents read).
type MarginGlanceInput struct {
	BranchID     string      `json:"branch_id"`
	CustomerID   string      `json:"customer_id"`
	CustomerType string      `json:"customer_type"`
	Lines        []LineInput `json:"lines"`
}

// MarginGlanceLine is one ticket line's margin answer, index-aligned with the input lines. Both
// fields are POINTERS + omitempty on purpose: a line with no cost on file (or an unpriceable line)
// answers `{}`, absent, never a fake zero.
type MarginGlanceLine struct {
	MarginBps      *int64 `json:"margin_bps,omitempty"`
	EstProfitCents *int64 `json:"est_profit_cents,omitempty"`
}

// MarginGlance is the endpoint's answer. THE MARSHAL-LAYER GATE (the commander's critical rev-8
// ruling): every field is omitempty, and for a caller the dealer's margin_visibility does not
// allow, the service returns the zero value, the payload is `{}` with margin/profit BYTE-ABSENT.
// The footer pair is computed over the lines where BOTH revenue and cost are known; `partial`
// says some lines were excluded, so the footer never silently poses as the whole ticket.
type MarginGlance struct {
	Lines          []MarginGlanceLine `json:"lines,omitempty"`
	MarginBps      *int64             `json:"margin_bps,omitempty"`       // footer: blended margin over the known lines
	EstProfitCents *int64             `json:"est_profit_cents,omitempty"` // footer: summed est. profit over the known lines
	Partial        bool               `json:"partial,omitempty"`          // some lines lacked cost/price and are excluded
}

// -- DIB-502 (rev-8 item 8): suggested add-ons ---------------------------------------------------

// AddonSuggestion is one suggestion tile: enough to render (thumb via the existing byte route,
// name, pack/size line, list price) and to ADD through the existing engine-priced path (the
// client sends product_id/qty/uom to the same resolve+ring machinery, never a client price).
type AddonSuggestion struct {
	ProductID      string  `json:"product_id"`
	SKU            string  `json:"sku"`
	Name           string  `json:"name"`
	SizeLine       string  `json:"size_line,omitempty"`        // pack/size wording (product_size, else dimensions)
	UOM            string  `json:"uom"`                        // the sell unit an add would use (sold_by || base_uom)
	DefaultQty     float64 `json:"default_qty"`                // sensible qty-box default (minimum_order_qty || 1)
	ListPriceCents *int64  `json:"list_price_cents,omitempty"` // display only, the add path re-prices
	PriceUOM       string  `json:"price_uom,omitempty"`
	HasImage       bool    `json:"has_image"`
	Application    string  `json:"application,omitempty"`
}

// AddonSuggestions is the GET /api/v1/pos/addon-suggestions answer. An empty ticket, an unknown
// category, or a pairing map with nothing to say all answer the same way: an empty list, never an
// error, the strip hides entirely (empty-never-error).
type AddonSuggestions struct {
	Suggestions []AddonSuggestion `json:"suggestions"`
}

// addonCandidate is the store's raw candidate row (repository_rev8.go).
type addonCandidate struct {
	ProductID      string
	SKU            string
	Name           string
	ProductSize    string
	DimensionsLWH  string
	BaseUOM        string
	SoldBy         string
	MinimumOrder   float64
	ListPriceCents *int64
	PriceUOM       string
	HasImage       bool
	Application    string
}
