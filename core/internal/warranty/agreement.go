package warranty

// agreement.go, the walk change 6 vendor credit agreements surface: how each vendor credits
// warranty. Dealer Admin's Warranty page sets it (the board's second card), Manager Home's Warranty
// Vendors and credits board reads it. The audit, the validation-in-words and the stamped caller all
// follow the 0396 threshold's road (policy.go); the default kind lives in the read alone, so a
// vendor the dealer never set answers claim_by_claim without a row.

import (
	"context"
	"strconv"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// actWarrantyAgreementChanged files one moved knob under its own verb, a warranty-owned string for
// the same reason the threshold keeps WARRANTY_POLICY_CHANGED local (policy.go): a settings change
// only this module writes has no business widening a package every module imports.
const actWarrantyAgreementChanged = "WARRANTY_VENDOR_AGREEMENT_CHANGED"

// agreementEntity is the audit entity_type these rows file under; the entity id is the vendor's own
// uuid, a real row id, so no scope sentinel is needed (the threshold's org-scope problem does not
// arise here).
const agreementEntity = "warranty_vendor_agreement"

// The boards' closed set of kinds, and the audit rows' before/after words.
const (
	AgreementKindClaimByClaim       = "claim_by_claim"
	AgreementKindPeriodicCreditNote = "periodic_credit_note"
	AgreementKindFixedAllowance     = "fixed_allowance"
)

// The standard periods (WARRANTY-DEFAULTS ruling A, rule 5: periodic credit notes run on standard
// months and quarters).
const (
	AgreementPeriodMonthly   = "monthly"
	AgreementPeriodQuarterly = "quarterly"
)

// agreementKinds is the closed vocabulary the write validates against, the CHECK constraint's set.
var agreementKinds = map[string]bool{
	AgreementKindClaimByClaim:       true,
	AgreementKindPeriodicCreditNote: true,
	AgreementKindFixedAllowance:     true,
}

// agreementPeriods is the closed period vocabulary; a claim_by_claim agreement carries none.
var agreementPeriods = map[string]bool{
	AgreementPeriodMonthly:   true,
	AgreementPeriodQuarterly: true,
}

// allowanceMaxBP is the ceiling one fixed allowance may carry: 10000 basis points is 100 percent of
// the vendor's purchases, past which the number is a typo, not a deal. The 0404 CHECK is the
// backstop; this constant is the one place the rule's edge is named in Go.
const allowanceMaxBP = 10000

// VendorAgreement is one vendor's EFFECTIVE agreement: the stored row when the dealer set one, the
// default kind when not. VendorNo and VendorName ride joined from the vendor master for display.
// UpdatedBy stays OFF the wire (json:"-"), the threshold's ruling: a person's id never rides it, the
// audit spine carries who changed what.
type VendorAgreement struct {
	VendorID      string `json:"vendor_id"`
	VendorNo      string `json:"vendor_no"`
	VendorName    string `json:"vendor_name"`
	AgreementKind string `json:"agreement_kind"`
	Period        string `json:"period,omitempty"`
	AllowanceBP   int    `json:"allowance_bp"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	UpdatedBy     string `json:"-"`
}

// AgreementInput is the write payload for one vendor.
type AgreementInput struct {
	AgreementKind string `json:"agreement_kind"`
	Period        string `json:"period,omitempty"`
	AllowanceBP   int    `json:"allowance_bp"`
}

// Agreements returns every active vendor with its effective agreement, in vendor name order, the
// page's second card in one read.
func (s *Service) Agreements(ctx context.Context) ([]VendorAgreement, error) {
	return s.store.VendorAgreements(ctx)
}

// UpdateAgreement validates and stores one vendor's agreement, then records each moved knob on the
// audit spine with its old and new value, the threshold's pattern: one row per moved knob, and a
// save that moved nothing writes none. The route is RequireAdmin; the service still requires the
// stamped caller so updated_by is a real attribution.
//
// The validation answers in words first (a 422 naming the knob and the rule, the 0404 CHECKs are the
// backstop). claim_by_claim is the rule the kind carries, so its period and allowance are cleared on
// the write rather than refused: a stale client still riding them lands in a sane row, and the
// read-back shows the truth. A failed pre-image read does not block the save, the threshold's
// ruling: losing the before-image costs a history row, refusing the dealer's change would cost the
// change itself.
func (s *Service) UpdateAgreement(ctx context.Context, vendorID string, in AgreementInput) (VendorAgreement, error) {
	if !agreementKinds[in.AgreementKind] {
		return VendorAgreement{}, unprocessable("warranty: agreement_kind must be one of claim_by_claim, periodic_credit_note, fixed_allowance")
	}
	period, bp := "", 0
	if in.AgreementKind != AgreementKindClaimByClaim {
		if !agreementPeriods[in.Period] {
			return VendorAgreement{}, unprocessable("warranty: period must be monthly or quarterly for a %s agreement", in.AgreementKind)
		}
		period = in.Period
		if in.AllowanceBP < 0 || in.AllowanceBP > allowanceMaxBP {
			return VendorAgreement{}, unprocessable("warranty: allowance_bp must be between 0 and %d (basis points of the vendor's purchases)", allowanceMaxBP)
		}
		bp = in.AllowanceBP
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return VendorAgreement{}, err
	}
	before, beforeErr := s.store.VendorAgreement(ctx, vendorID)
	saved, err := s.store.UpsertVendorAgreement(ctx, vendorID, AgreementInput{AgreementKind: in.AgreementKind, Period: period, AllowanceBP: bp}, caller.UserID)
	if err != nil {
		return VendorAgreement{}, err
	}
	if beforeErr == nil && s.audit != nil {
		s.recordAgreementChange(ctx, caller, vendorID, before, saved)
	}
	return saved, nil
}

// recordAgreementChange files ONE audit row per moved knob, in the {field, before, after, actor_role}
// shape the audit allowlist already admits (field/before/after/actor_role, the pos policy history's
// shape the threshold reuses). Best-effort, like every other Recorder call here: an audit outage must
// never cost a dealer the setting they just saved.
func (s *Service) recordAgreementChange(ctx context.Context, caller identity.StaffContext, vendorID string, before, after VendorAgreement) {
	knobs := []struct {
		field         string
		before, after string
		moved         bool
	}{
		{"agreement_kind", before.AgreementKind, after.AgreementKind, before.AgreementKind != after.AgreementKind},
		{"period", before.Period, after.Period, before.Period != after.Period},
		{"allowance_bp", strconv.Itoa(before.AllowanceBP), strconv.Itoa(after.AllowanceBP), before.AllowanceBP != after.AllowanceBP},
	}
	for _, k := range knobs {
		if !k.moved {
			continue
		}
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actWarrantyAgreementChanged,
			EntityType: agreementEntity,
			EntityID:   vendorID,
			ActorID:    caller.UserID,
			Extra: map[string]any{
				"field":      k.field,
				"before":     k.before,
				"after":      k.after,
				"actor_role": caller.RoleCode,
			},
		})
	}
}
