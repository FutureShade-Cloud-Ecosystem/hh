package warranty

// model.go, the warranty DTOs (JSON-serialized) + input structs. Money is integer cents (BIGINT); dates and
// timestamps are ISO-8601 strings ("" when null). These are the shapes the handler emits + the store
// round-trips. Spec: PRODUCT_SPEC.md WTY-01 (G-LOCK-077), DIB-153.

// - FR-1 vendor registry -

// VendorProfile is one vendor's warranty terms (FR-1). Status carries the explicit UNDOCUMENTED state (the
// Oaks case) so a claim against an undocumented vendor prompts "capture the terms you negotiate".
type VendorProfile struct {
	ID         string            `json:"id"`
	VendorID   string            `json:"vendor_id"`
	VendorName string            `json:"vendor_name,omitempty"` // joined from the vendor master for display
	VendorNo   string            `json:"vendor_no,omitempty"`
	Status     string            `json:"status"` // DOCUMENTED | UNDOCUMENTED
	Coverage   string            `json:"coverage"`
	TermMonths *int              `json:"term_months,omitempty"` // nil = unknown/none
	Channel    string            `json:"channel"`               // EMAIL | PORTAL | REP | PHONE | OTHER
	ChannelRef string            `json:"channel_ref"`
	Docs       []DocRef          `json:"docs"`
	Notes      string            `json:"notes"`
	Overrides  []ProductOverride `json:"overrides,omitempty"`
	CreatedAt  string            `json:"created_at,omitempty"`
	UpdatedAt  string            `json:"updated_at,omitempty"`
}

// DocRef is one policy-document reference on a profile (the FR-10 AI-triage corpus seam, P2).
type DocRef struct {
	Title string `json:"title"`
	Ref   string `json:"ref"`
}

// ProductOverride is a per-product deviation from a vendor's default terms (FR-1).
type ProductOverride struct {
	ID          string `json:"id"`
	ProfileID   string `json:"profile_id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name,omitempty"`
	Coverage    string `json:"coverage"`
	TermMonths  *int   `json:"term_months,omitempty"`
	Notes       string `json:"notes"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// VendorProfileInput is the create/update-profile payload.
type VendorProfileInput struct {
	VendorID   string   `json:"vendor_id"`
	Status     string   `json:"status"`
	Coverage   string   `json:"coverage"`
	TermMonths *int     `json:"term_months"`
	Channel    string   `json:"channel"`
	ChannelRef string   `json:"channel_ref"`
	Docs       []DocRef `json:"docs"`
	Notes      string   `json:"notes"`
}

// ProductOverrideInput is the add-override payload.
type ProductOverrideInput struct {
	ProfileID  string `json:"profile_id"`
	ProductID  string `json:"product_id"`
	Coverage   string `json:"coverage"`
	TermMonths *int   `json:"term_months"`
	Notes      string `json:"notes"`
}

// - FR-2 claim object -

// Claim is a tracked warranty-claim object (FR-2). claim_no is #WTY-n. vendor_id "" = UNKNOWN pending triage.
type Claim struct {
	ID                   string      `json:"id"`
	ClaimNo              string      `json:"claim_no"`
	CustomerID           string      `json:"customer_id,omitempty"`
	CustomerName         string      `json:"customer_name,omitempty"`
	JobAddress           Address     `json:"job_address"`
	VendorID             string      `json:"vendor_id,omitempty"`
	VendorName           string      `json:"vendor_name,omitempty"`
	OwnerID              string      `json:"owner_id,omitempty"`
	OwnerName            string      `json:"owner_name,omitempty"`
	BranchID             string      `json:"branch_id"`
	Phase                string      `json:"phase"`
	Source               string      `json:"source"`
	Description          string      `json:"description"`
	InvoiceID            string      `json:"invoice_id,omitempty"`
	OrderID              string      `json:"order_id,omitempty"`
	POID                 string      `json:"po_id,omitempty"`
	SupplierPurchaseDate string      `json:"supplier_purchase_date,omitempty"`
	SubmittedAt          string      `json:"submitted_at,omitempty"`
	SLADue               string      `json:"sla_due,omitempty"`
	Outcome              string      `json:"outcome,omitempty"`
	ExpectedCents        int64       `json:"expected_cents"`
	RealizedCents        int64       `json:"realized_cents"`
	GLJournalID          string      `json:"gl_journal_id,omitempty"`
	OpenedAt             string      `json:"opened_at,omitempty"`
	ClosedAt             string      `json:"closed_at,omitempty"`
	Lines                []ClaimLine `json:"lines,omitempty"`
	CreatedAt            string      `json:"created_at,omitempty"`
}

// Address is the job-site (failure-location) address JSONB shape.
type Address struct {
	Line1    string `json:"line1,omitempty"`
	City     string `json:"city,omitempty"`
	Province string `json:"province,omitempty"`
	Postal   string `json:"postal,omitempty"`
}

// ClaimLine is one product+qty on a claim, carrying both sheet purchase dates (FR-2).
type ClaimLine struct {
	ID                     string `json:"id"`
	ClaimID                string `json:"claim_id"`
	ProductID              string `json:"product_id,omitempty"`
	ProductText            string `json:"product_text"`
	Qty                    string `json:"qty"` // DECIMAL(19,4) as string, never float
	UOMCode                string `json:"uom_code,omitempty"`
	PurchaseDateSupplier   string `json:"purchase_date_supplier,omitempty"`
	PurchaseDateContractor string `json:"purchase_date_contractor,omitempty"`
	Notes                  string `json:"notes"`
}

// ClaimLineInput is one line on the capture form.
type ClaimLineInput struct {
	ProductID              string `json:"product_id"`
	ProductText            string `json:"product_text"`
	Qty                    string `json:"qty"`
	UOMCode                string `json:"uom_code"`
	PurchaseDateSupplier   string `json:"purchase_date_supplier"`
	PurchaseDateContractor string `json:"purchase_date_contractor"`
	Notes                  string `json:"notes"`
}

// ClaimInput is the capture payload (office/counter form OR from an invoice/order line). OpenedAt supports
// BACK-DATING (FR-9): when set, the claim enters with its TRUE opened date, not now().
type ClaimInput struct {
	CustomerID           string           `json:"customer_id"`
	JobAddress           Address          `json:"job_address"`
	VendorID             string           `json:"vendor_id"` // "" = UNKNOWN pending triage
	OwnerID              string           `json:"owner_id"`
	BranchID             string           `json:"branch_id"`
	Source               string           `json:"source"`
	Description          string           `json:"description"`
	InvoiceID            string           `json:"invoice_id"`
	OrderID              string           `json:"order_id"`
	POID                 string           `json:"po_id"`
	SupplierPurchaseDate string           `json:"supplier_purchase_date"`
	ExpectedCents        int64            `json:"expected_cents"`
	OpenedAt             string           `json:"opened_at"` // FR-9 back-date (ISO-8601); "" = now()
	Lines                []ClaimLineInput `json:"lines"`
}

// - FR-3 lifecycle events + checklist -

// ClaimEvent is one audit-spine row: a phase transition, a checklist tick, a settlement, a note, or evidence.
type ClaimEvent struct {
	ID              string         `json:"id"`
	ClaimID         string         `json:"claim_id"`
	EventType       string         `json:"event_type"`
	FromPhase       string         `json:"from_phase,omitempty"`
	ToPhase         string         `json:"to_phase,omitempty"`
	ChecklistItemID string         `json:"checklist_item_id,omitempty"`
	ChecklistKey    string         `json:"checklist_key,omitempty"`
	Checked         *bool          `json:"checked,omitempty"`
	Note            string         `json:"note"`
	EvidenceRef     string         `json:"evidence_ref,omitempty"`
	ActorID         string         `json:"actor_id,omitempty"`
	Detail          map[string]any `json:"detail,omitempty"`
	OccurredAt      string         `json:"occurred_at"`
}

// ChecklistItem is one checklist-template row (FR-3). Required items gate advancement out of the phase.
type ChecklistItem struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	VendorID  string `json:"vendor_id,omitempty"` // "" = default (all vendors)
	ItemKey   string `json:"item_key"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	SortOrder int    `json:"sort_order"`
}

// ChecklistState is a checklist item resolved against a claim's ticks, the card shows this.
type ChecklistState struct {
	Item    ChecklistItem `json:"item"`
	Checked bool          `json:"checked"`
}

// PhaseTransitionInput advances a claim to a new phase. OccurredAt supports BACK-DATING the transition (FR-9):
// when set (ISO-8601), the transition, and the closed_at/submitted_at stamps it drives, are recorded at the
// TRUE historical time so a live claim already resolved before go-live enters with honest time-to-settlement and
// age. "" = now().
type PhaseTransitionInput struct {
	ToPhase    string `json:"to_phase"`
	Note       string `json:"note"`
	OccurredAt string `json:"occurred_at"`
}

// ChecklistTickInput toggles a checklist item on a claim.
type ChecklistTickInput struct {
	ItemKey string `json:"item_key"`
	Checked bool   `json:"checked"`
}

// EvidenceInput attaches a masked evidence reference to a claim (FR-4 seam, no file infra in P1).
type EvidenceInput struct {
	EvidenceRef string `json:"evidence_ref"`
	Note        string `json:"note"`
}

// - FR-5 settlement -

// SettlementInput records a settlement outcome + amounts (FR-5, manual recording for P1). ExpectedCents is a
// pointer so an omitted value (nil) keeps the captured exposure while an explicit 0 (a decision that zeroed a
// previously-estimated recovery) is honoured, a sentinel 0 would be wrong for a money field.
type SettlementInput struct {
	Outcome       string `json:"outcome"` // VENDOR_CREDIT | REPLACEMENT_STOCK | CUSTOMER_ACCOMMODATION | WRITE_OFF
	ExpectedCents *int64 `json:"expected_cents"`
	RealizedCents int64  `json:"realized_cents"`
	Note          string `json:"note"`
	// ManualOverride keeps P1's MANUAL settlement available as an explicit override (ERP:warranty-p2). When
	// true the per-outcome money path is deliberately NOT executed, the manager is recording a settlement whose
	// money was booked by hand outside the module, and the posting row carries status MANUAL with the reason.
	// It is opt-in and reason-bearing precisely because the default is now automatic: an override you can take
	// by accident is not an override, it is a hole.
	ManualOverride bool   `json:"manual_override"`
	OverrideReason string `json:"override_reason"`
}

// - ERP:warranty-p2 per-outcome money paths (0129) -

// MoneyPathConfig is one row of warranty_outcome_paths: which named money path an outcome takes, which seam
// owns it, and (for the paths warranty posts itself, plus the customer-credit fallback) the account pair.
type MoneyPathConfig struct {
	Outcome         string `json:"outcome"`
	MoneyPath       string `json:"money_path"`
	Seam            string `json:"seam"`
	DebitAccount    string `json:"debit_account_code,omitempty"`
	CreditAccount   string `json:"credit_account_code,omitempty"`
	AccruesRecovery bool   `json:"accrues_recovery"`
	Notes           string `json:"notes,omitempty"`
}

// SettlementPosting is one executed money path (warranty_settlement_postings). Status is the honest report:
// POSTED / NO_EFFECT / SEAM_UNWIRED / FALLBACK_GL / FAILED / MANUAL.
type SettlementPosting struct {
	ID              string         `json:"id,omitempty"`
	ClaimID         string         `json:"claim_id"`
	Outcome         string         `json:"outcome"`
	MoneyPath       string         `json:"money_path"`
	Seam            string         `json:"seam"`
	Status          string         `json:"status"`
	AmountCents     int64          `json:"amount_cents"`
	AccruesRecovery bool           `json:"accrues_recovery"`
	GLJournalID     string         `json:"gl_journal_id,omitempty"`
	ExternalRef     string         `json:"external_ref,omitempty"`
	Request         map[string]any `json:"request,omitempty"`
	ErrorText       string         `json:"error_text,omitempty"`
	Note            string         `json:"note,omitempty"`
	PostedAt        string         `json:"posted_at,omitempty"`
}

// SettlementResult reports a recorded settlement, including whether the GL journal was posted (P1 seam) or left
// as a recorded-only entry, and (P2) WHICH named money path ran and what it produced.
type SettlementResult struct {
	ClaimID       string `json:"claim_id"`
	Outcome       string `json:"outcome"`
	ExpectedCents int64  `json:"expected_cents"`
	RealizedCents int64  `json:"realized_cents"`
	GLJournalID   string `json:"gl_journal_id,omitempty"`
	GLPosted      bool   `json:"gl_posted"` // false in P1 unless the GL seam is wired (documented seam)
	GLNote        string `json:"gl_note"`
	// - P2 money path (ERP:warranty-p2) -
	MoneyPath   string `json:"money_path,omitempty"`   // VENDOR_RECOVERY | CUSTOMER_CREDIT | INVENTORY_REPLACEMENT | EXPENSE_WRITE_OFF
	PathSeam    string `json:"path_seam,omitempty"`    // GL | BILLING | INVENTORY
	PathStatus  string `json:"path_status,omitempty"`  // POSTED | NO_EFFECT | SEAM_UNWIRED | FALLBACK_GL | FAILED | MANUAL
	ExternalRef string `json:"external_ref,omitempty"` // billing credit-memo no / inventory movement ref
	// AccruedCents is what this settlement put ON the WTY-RECOVERY board as outstanding vendor money
	// (DIB-221). Non-zero only for the vendor-recovery path, and only when it actually posted.
	AccruedCents int64 `json:"accrued_cents"`
}

// - FR-7 reporting -

// ManufacturerFrequency is one vendor's claim volume + win/loss (FR-7 "by manufacturer" + win/loss ratio).
type ManufacturerFrequency struct {
	VendorID      string  `json:"vendor_id"`
	VendorName    string  `json:"vendor_name"`
	TotalClaims   int     `json:"total_claims"`
	OpenClaims    int     `json:"open_claims"`
	WonClaims     int     `json:"won_claims"`  // settled with a credit/replacement/accommodation
	LostClaims    int     `json:"lost_claims"` // rejected / abandoned / written off
	WinRate       float64 `json:"win_rate"`    // won / (won+lost); 0 when no decided claims
	ExpectedCents int64   `json:"expected_cents"`
	RealizedCents int64   `json:"realized_cents"`
}

// FunnelCount is the claim count currently in one phase (FR-7 funnel counts per phase).
type FunnelCount struct {
	Phase string `json:"phase"`
	Count int    `json:"count"`
}

// TimeToSettlement is the settlement-cycle summary (FR-7 time-to-settlement).
type TimeToSettlement struct {
	SettledCount int     `json:"settled_count"`
	AvgDays      float64 `json:"avg_days"`
	MedianDays   float64 `json:"median_days"`
	MinDays      int     `json:"min_days"`
	MaxDays      int     `json:"max_days"`
}

// ExposureByVendor is the open-recovery exposure aggregated per vendor (FR-7 $ exposure by vendor).
type ExposureByVendor struct {
	VendorID      string `json:"vendor_id"`
	VendorName    string `json:"vendor_name"`
	OpenClaims    int    `json:"open_claims"`
	ExposureCents int64  `json:"exposure_cents"` // Σ expected_cents on OPEN claims
}

// OpenClaimAge is one open claim's age classification (FR-7 open claims by phase/vendor/age).
type OpenClaimAge struct {
	ClaimID       string `json:"claim_id"`
	ClaimNo       string `json:"claim_no"`
	Phase         string `json:"phase"`
	VendorID      string `json:"vendor_id,omitempty"`
	VendorName    string `json:"vendor_name,omitempty"`
	AgeDays       int    `json:"age_days"`
	Bucket        string `json:"bucket"` // 0-30 | 31-60 | 61-90 | 90+
	ExpectedCents int64  `json:"expected_cents"`
}

// ReportBundle is the whole FR-7 metrics payload the reporting screen reads in one call.
type ReportBundle struct {
	Funnel             []FunnelCount           `json:"funnel"`
	ByManufacturer     []ManufacturerFrequency `json:"by_manufacturer"`
	TimeToSettlement   TimeToSettlement        `json:"time_to_settlement"`
	Exposure           []ExposureByVendor      `json:"exposure_by_vendor"`
	OpenClaimsByAge    []OpenClaimAge          `json:"open_claims_by_age"`
	TotalOpen          int                     `json:"total_open"`
	TotalExposureCents int64                   `json:"total_exposure_cents"`
}
