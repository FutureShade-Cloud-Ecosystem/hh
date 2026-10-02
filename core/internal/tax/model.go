package tax

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// -- reason codes + rate constants (mirror pos/model.go + tax_decision_snapshot CHECK) ---------------------
const (
	ReasonStdON13    = "STD_ON_HST_13"           // 13% standard-rated Ontario HST
	ReasonONFNPos8   = "ON_FN_POS_8"             // Ontario First Nations point-of-sale: 8% credited → collect 5%
	ReasonFedVendDel = "FED_S87_VENDOR_DELIVERY" // federal 0% provisional, Dibbits delivery to a reserve (evidence-gated)
	ReasonFNGST5     = "FNGST_5"                 // First Nations GST 5%
	ReasonManual     = "MANUAL_TAX_REVIEW"       // routed to a person

	jurisdictionON = "ON"

	stdRateBps      = 1300 // 13% default fallback (bit-identical to pos.flatHSTRateBps)
	ontarioCredBps  = 800  // the 8% Ontario credit on the ON_FN_POS_8 path
	ontarioNetBps   = 500  // 13% − 8% = the 5% net collected on the Ontario path
	roundHalfUpBase = 10000

	// ruleVersionW4 is the W4 flat-13 snapshot marker; ruleVersionM5 marks a recompute output. The recompute
	// is INSERT-only: it never rewrites a W4 row, it inserts a parallel M5 audit-truth row.
	ruleVersionW4 = "W4-flat-13"
	ruleVersionM5 = "M5-recompute"

	// GL account codes for the HST → GL reconciliation (0007/0042).
	acctHSTPayable = "2200"
	acctHSTFederal = "2201"
	acctHSTOntario = "2202"
)

// evidenceGatedReasons are the relief reasons whose 0%/relief rate BINDS only once fulfilment evidence
// (POD/waybill for delivery, or a validated status-card record for the FN point-of-sale reliefs) is attached -
// the HARD GATE. Without evidence the binding rate falls back to the standard rate (preserving the W4/pos
// behavior) and any point-of-sale credit is withheld; the reason code is retained for the audit trail. EVERY
// reason that resolves below the 13% standard must appear here, an ungated relief reason would let a caller
// claim the reduced rate with no proof (S3 W6 B3). ReasonManual routes to a person and ReasonStdON13 is the
// standard rate itself, so neither is a self-serve relief and neither is gated.
var evidenceGatedReasons = map[string]bool{
	ReasonFedVendDel: true, // federal 0% on-reserve vendor delivery, POD/waybill
	ReasonONFNPos8:   true, // Ontario FN point-of-sale 8% credit (collect 5%), status-card evidence
	ReasonFNGST5:     true, // First Nations GST 5%, status-card evidence
}

// errNoDB marks a repository call made with no pool (scaffold/offline).
var errNoDB = errors.New("tax: database not configured")

// ErrNoStandardRate is the sentinel the engine refuses with when no standard
// STD_ON_HST_13 row is in effect for the as-of date. A sale, a quote, a relief
// assessment and the counter's tax-rate read must never bind a silent 0% (row
// 680 item 2): with the standard row deleted, never seeded or fully expired, a
// zero-rate Quote with a nil error rings every ticket untaxed while the ledger
// believes a rate is configured. Match with errors.Is; the concrete error
// carries the jurisdiction and the as-of date in its message (noStandardRate).
var ErrNoStandardRate = errors.New("tax: no standard rate row in effect")

// noStandardRate builds the refusal the engine returns when a fallback read
// finds the standard row absent too. The jurisdiction and the as-of date are
// the two facts an operator needs to seed or re-open the row, and neither can
// carry a secret.
func noStandardRate(jurisdiction, asOf string) error {
	return fmt.Errorf("%w (jurisdiction %s, as of %s)", ErrNoStandardRate, jurisdiction, asOf)
}

// -- DTOs (JSON is the tax-admin surface; money is integer cents; card numbers are MASKED-only) ------------

// Rate is one effective-dated ON-HST rate row (tax_rates).
type Rate struct {
	ID            string `json:"id"`
	Jurisdiction  string `json:"jurisdiction"`
	ReasonCode    string `json:"reason_code"`
	RateBps       int    `json:"rate_bps"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// StatusCardProfile is one admin-editable exemption profile (tax_status_card_profiles). Each FORCES
// photo + number + attestation evidence via the requires_* flags.
type StatusCardProfile struct {
	ID                  string `json:"id"`
	Code                string `json:"code"`
	Name                string `json:"name"`
	RateBps             int    `json:"rate_bps"`
	RequiresPhoto       bool   `json:"requires_photo"`
	RequiresNumber      bool   `json:"requires_number"`
	RequiresAttestation bool   `json:"requires_attestation"`
	Status              string `json:"status"` // ACTIVE | PENDING | RETIRED
	Notes               string `json:"notes,omitempty"`
}

// Evidence is a status-card evidence record (tax_evidence). The raw card number is NEVER stored or
// serialized here, only NumberMask (e.g. "•••• 4821") ever leaves the service.
type Evidence struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profile_id,omitempty"`
	CardProfileID string `json:"card_profile_id,omitempty"`
	OrderID       string `json:"order_id,omitempty"`
	PhotoRef      string `json:"photo_ref,omitempty"` // pointer to the encrypted image, never inline bytes
	NumberMask    string `json:"number_mask"`         // the ONLY identifier serialized
	Attestation   bool   `json:"attestation"`
	TCRDExpiry    string `json:"tcrd_expiry,omitempty"`
	CapturedBy    string `json:"captured_by,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`

	// AttestationStatus is a fixed banner: attestation ships PENDING_LEGAL_REVIEW at W6 (not a stored column).
	AttestationStatus string `json:"attestation_status,omitempty"`
}

// EvidenceCapture is the capture request. RawNumber is accepted, masked, and DISCARDED, it is never stored
// or echoed. The forced-evidence gate (photo + number + attestation, per the profile) is enforced in the
// service before insert.
type EvidenceCapture struct {
	ProfileID       string `json:"profile_id"`
	CardProfileCode string `json:"card_profile_code"`
	OrderID         string `json:"order_id"`
	PhotoRef        string `json:"photo_ref"`
	RawNumber       string `json:"raw_number"` // masked then discarded, NEVER persisted or serialized back
	Attestation     bool   `json:"attestation"`
	TCRDExpiry      string `json:"tcrd_expiry"`
}

// EvidenceFilter narrows the masked evidence list.
type EvidenceFilter struct {
	OrderID   string
	ProfileID string
	Limit     int
}

// Config is the engine-wide tax config (tax_config); the org-default row has an empty BranchID.
type Config struct {
	BranchID             string `json:"branch_id,omitempty"`
	HSTRegistrationNo    string `json:"hst_registration_no"`
	RoundMode            string `json:"round_mode"` // INVOICE_TOTAL | PER_LINE
	AvalaraEnabled       bool   `json:"avalara_enabled"`
	PostalRoutingEnabled bool   `json:"postal_routing_enabled"`
	Notes                string `json:"notes,omitempty"`
}

// Snapshot is a projection of tax_decision_snapshot (0029) used by the recompute + recon paths.
type Snapshot struct {
	ID                string `json:"id,omitempty"`
	OrderID           string `json:"order_id"`
	OrderLineID       string `json:"order_line_id,omitempty"`
	ReasonCode        string `json:"tax_reason_code"`
	RuleVersion       string `json:"rule_version"`
	RateBps           int    `json:"rate_bps"`
	FullHSTCents      int64  `json:"full_hst_cents"`
	OnCreditCents     int64  `json:"on_credit_cents"`
	NetCollectedCents int64  `json:"net_collected_cents"`
	Automated         bool   `json:"automated"`
}

// RecomputeResult reports a recompute run (insert-only, audit-truth only, no retro GL correction).
type RecomputeResult struct {
	FromDate      string `json:"from_date"`
	ToDate        string `json:"to_date"`
	Scanned       int    `json:"scanned"`
	Inserted      int    `json:"inserted"`
	SkippedExists int    `json:"skipped_exists"`
}

// ReconReport ties the collected HST (the tax-decision side) to the GL liability accounts for a period.
type ReconReport struct {
	Year           int    `json:"year"`
	Month          int    `json:"month"`
	BranchID       string `json:"branch_id,omitempty"`
	CollectedCents int64  `json:"collected_cents"`  // Σ net_collected from tax decisions
	GLPayableCents int64  `json:"gl_payable_cents"` // 2200 balance
	GLFederalCents int64  `json:"gl_federal_cents"` // 2201 balance
	GLOntarioCents int64  `json:"gl_ontario_cents"` // 2202 balance
	VarianceCents  int64  `json:"variance_cents"`   // GL 2200 − collected (0 = tied out)
	TiedOut        bool   `json:"tied_out"`
}

// -- evidence-relief eligibility (the tcrd_expiry gate) -----------------------------------------------------

// ReliefEligibility is the result of validating a status-card evidence record for relief. Blocked=true with a
// BlockReason when the tcrd is expired or a forced field is missing.
type ReliefEligibility struct {
	Eligible    bool   `json:"eligible"`
	BlockReason string `json:"block_reason,omitempty"`
	NumberMask  string `json:"number_mask,omitempty"`
}

// -- store seam (the service depends on this; *Repository satisfies it; unit tests inject a fake) ----------
type taxStore interface {
	ResolveRate(ctx context.Context, jurisdiction, reasonCode, asOf string) (rateBps int, found bool, err error)
	ListRates(ctx context.Context) ([]Rate, error)
	InsertRate(ctx context.Context, r Rate) (Rate, error)

	ListProfiles(ctx context.Context) ([]StatusCardProfile, error)
	GetProfileByCode(ctx context.Context, code string) (StatusCardProfile, bool, error)
	UpsertProfile(ctx context.Context, p StatusCardProfile) (StatusCardProfile, error)

	InsertEvidence(ctx context.Context, e Evidence) (Evidence, error)
	ListEvidence(ctx context.Context, f EvidenceFilter) ([]Evidence, error)
	GetEvidence(ctx context.Context, id string) (Evidence, bool, error)

	GetConfig(ctx context.Context, branchID string) (Config, error)
	UpsertConfig(ctx context.Context, c Config) (Config, error)

	ListW4Snapshots(ctx context.Context, fromDate, toDate string) ([]Snapshot, error)
	SnapshotExists(ctx context.Context, orderID, orderLineID, ruleVersion string) (bool, error)
	InsertSnapshot(ctx context.Context, s Snapshot) (string, error)

	CollectedHSTCents(ctx context.Context, year, month int) (int64, error)
}

// -- pure helpers --------------------------------------------------------------

// hstOn rounds subtotal × rateBps ÷ 10000 HALF-UP, once, at the total. Bit-identical to pos.flatHST13 at
// rateBps 1300 (parity is asserted in the tests). This is the single rounding authority for the header tax;
// the per-line split is the caller's (moneyutil.AllocateLargestRemainder).
func hstOn(subtotalCents int64, rateBps int) int64 {
	return (subtotalCents*int64(rateBps) + roundHalfUpBase/2) / roundHalfUpBase
}

// maskCardNumber renders the masked identifier ("•••• 4821") from a raw card number. The raw value is used
// ONLY to derive the mask and is never returned or stored. Short/empty inputs mask fully.
func maskCardNumber(raw string) string {
	digits := make([]rune, 0, len(raw))
	for _, c := range raw {
		if c >= '0' && c <= '9' {
			digits = append(digits, c)
		}
	}
	if len(digits) < 4 {
		return "•••• ••••"
	}
	return "•••• " + string(digits[len(digits)-4:])
}

// today returns YYYY-MM-DD for the given clock (or time.Now when nil).
func today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().Format("2006-01-02")
}

// normDate coerces "" → today and validates YYYY-MM-DD; returns a BadRequestError on a malformed date.
func normDate(s string, now func() time.Time) (string, error) {
	if s == "" {
		return today(now), nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", httpx.BadRequestf("date must be YYYY-MM-DD")
	}
	return s, nil
}
