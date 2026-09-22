// Package audit records an append-only trail of mutating API requests (who changed what, when, where,
// why). It hooks in as middleware over the whole ERP mux, so every module is covered without per-handler
// instrumentation, and exposes a Recorder seam siblings call for sensitive-access (FN privacy + override
// accountability) rows. A-21 owns audit_log (0013), it is the table's first and only Go writer.
package audit

// Entry is one audit_log row, projected for the viewers (0013 columns + the 0078 seq).
type Entry struct {
	ID         string         `json:"id"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id,omitempty"`
	ActorID    string         `json:"actor_id,omitempty"`
	ActorRole  string         `json:"actor_role,omitempty"` // from changes.actor_role for the coarse trail
	BranchID   string         `json:"branch_id,omitempty"`
	Changes    map[string]any `json:"changes"`
	RequestID  string         `json:"request_id,omitempty"`
	CreatedAt  string         `json:"created_at"`
	// Seq is the 0078 monotonic insertion key. It is the CAUSAL sort key for a per-entity trail
	// (created_at is transaction-time and ties within a tx / inverts across concurrent txs). DIB-175's
	// order-edit history sorts by it. 0 for a row read before 0078 or from a fake store.
	Seq int64 `json:"seq,omitempty"`
}

// SensitiveAccess is the explicit-record input siblings pass to Recorder.Record. The service enforces
// the masked-only contract (D-A21-6) before persisting, cleartext identifiers/PANs never reach the log.
//
// SCAFFOLD-OWNED SHARED SEAM (consumed by RUN-PAY / A-03 / A-19 / FN / SignatureProvider).
type SensitiveAccess struct {
	Action     string         // one of the sensitive action constants below
	EntityType string         // "fn.eligibility_profile" | "fn.evidence_doc" | "signature" | "pos.sale" | "till.session" | "pricing.override"
	EntityID   string         // precise uuid the row is keyed to (reopen path)
	ActorID    string         // resolved caller (identity.StaffUserIDFromContext)
	BranchID   string         // caller branch when known
	Purpose    string         // e.g. "TAX_RELIEF_ATTESTATION", "REOPEN_VIEW"
	MaskedRef  string         // "•••• 4321", NEVER the cleartext identifier/PAN
	Reason     string         // override/decline reason code where applicable
	Extra      map[string]any // additional ALLOWLISTED, already-masked fields
}

// Action verbs.
const (
	// coarse (middleware), derived from HTTP method
	ActHTTPPost   = "HTTP.POST"
	ActHTTPPut    = "HTTP.PUT"
	ActHTTPPatch  = "HTTP.PATCH"
	ActHTTPDelete = "HTTP.DELETE"
	// sensitive (explicit Recorder), FN privacy + override accountability
	ActEvidenceView     = "EVIDENCE_VIEW"
	ActEvidenceChange   = "EVIDENCE_CHANGE"
	ActEvidenceDownload = "EVIDENCE_DOWNLOAD"
	ActEvidenceOverride = "EVIDENCE_OVERRIDE"
	ActSignatureCapture = "SIGNATURE_CAPTURE"
	ActSignatureView    = "SIGNATURE_VIEW"
	ActPriceOverride    = "PRICE_OVERRIDE"
	ActTillOverride     = "TILL_OVERRIDE"
	ActSaleCompleted    = "SALE_COMPLETED"
	ActTaxReliefAssess  = "TAX_RELIEF_ASSESS"
	// order-change history (Q-044): entity-grain rows beyond the coarse HTTP middleware record
	ActReturnCompleted    = "RETURN_COMPLETED"     // one committed POS return (method + return_no)
	ActOrderStatusChanged = "ORDER_STATUS_CHANGED" // any orders.status transition (detail old→new)
	ActOrderChanged       = "ORDER_CHANGED"        // a non-status order field change (detail old→new)
	// payment attribution (Q-044): explicit business-semantic payment records, WHO took a payment
	// and WHO applied it to WHICH invoice, beyond the coarse HTTP middleware row.
	ActPaymentReceived  = "PAYMENT_RECEIVED"  // one tender/payment row inserted (method, amount_cents, payment_no)
	ActPaymentAllocated = "PAYMENT_ALLOCATED" // one payment→invoice application (invoice_id, amount_cents)
	// quote-change history (DIB-56, bundle W10-A): the staff quote builder's per-column trail. Shares
	// ORDER_CHANGED's {field, line_no, before, after} Extra shape exactly, so it needs no new allowlist
	// keys, see allowedExtraKeys in service.go. Kept a DISTINCT action (rather than reusing
	// ORDER_CHANGED with entity_type "quote") so the admin viewer and the per-entity history reads can
	// filter on action alone, as the order history already does.
	ActQuoteChanged  = "QUOTE_CHANGED"  // a quote field/line change (detail old→new)
	ActQuoteAccepted = "QUOTE_ACCEPTED" // a customer acceptance via share link (DIB-57)
	// messaging consent (DIB-482): a member of staff recording, or withdrawing, a customer's
	// permission to be sent text messages. Shares ORDER_CHANGED's {field, before, after} Extra shape
	// exactly, so it needs no new allowlist keys, see allowedExtraKeys in service.go. A DISTINCT
	// action rather than a reused one because this is the row a CASL question is answered from: "who
	// recorded that this customer agreed, and when", filtered on action alone.
	ActConsentChanged = "CONSENT_CHANGED" // customer_sms_prefs consent granted or withdrawn by staff
)

// sensitiveActions are badged in the viewer and follow the masked-only contract.
var sensitiveActions = map[string]bool{
	ActEvidenceView:     true,
	ActEvidenceChange:   true,
	ActEvidenceDownload: true,
	ActEvidenceOverride: true,
	ActSignatureCapture: true,
	ActSignatureView:    true,
	ActPriceOverride:    true,
	ActTillOverride:     true,
	ActTaxReliefAssess:  true,
}

// Filter is the admin viewer query.
type Filter struct {
	EntityType string
	Action     string
	ActorID    string
	Since      string // RFC3339; optional
	Limit      int    // clamped 1..500, default 200
}
