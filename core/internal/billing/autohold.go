// autohold.go, DIB-483 / M3-G7B-NO-HOLD-SWITCH: the master switch for automatic credit holds gets a
// surface.
//
// THE GAP. Migration 0104 ships ar_credit_policy.auto_hold_enabled FALSE, deliberately and on the
// record: "merging this migration changes nobody's ability to transact until an operator turns it on."
// The problem the M3 gate found is that there was no way for an operator to turn it on. The column had
// exactly two readers, the nightly sweep, which reports DryRun when it is false, and the evaluator
// behind it, and no writer anywhere in the codebase. The safety flag was a one-way door in the wrong
// direction: shipped off, unturnable-on without a psql session.
//
// WHAT THIS ADDS, AND WHAT IT POINTEDLY DOES NOT.
//
//	ADDS:      a read of the effective policy, and a write of the ONE boolean. Tech-admin gated.
//	DOES NOT:  let anyone edit a THRESHOLD. aging_bucket_days, aging_threshold_cents,
//	           limit_tolerance_cents and the per-trigger enables are returned READ-ONLY.
//
// That split is a decision, not an oversight. Those numbers are the client's, the commander takes
// them to Dibbits and they come back as a policy UPDATE, and a screen that lets an admin type
// "aging_threshold_cents: 0" at 4pm on a Friday is a screen that will eventually hold the whole book.
// The switch is reversible in one click and holds nobody by itself; a threshold is neither.
//
// THE DIB-434 CAVEAT applies: admins own visibility and enablement knobs. This is that convention
// (assist/admin.go, messaging/event_class.go, dashboard/quickaction.go), applied to the one knob in AR
// that decides whether a machine may stop a customer transacting.
//
// NO MIGRATION. The column, its default and its org-default row all exist (0104). This is a surface
// over data that shipped nine migrations ago.
package billing

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// ActAutoHoldToggled is the audit action for a flip of the master switch. A constant, so a typo cannot
// split one action into two in the trail, the same rule the three actions above it follow.
const ActAutoHoldToggled = "AR_AUTO_HOLD_TOGGLED"

// AutoHoldSetting is what the admin surface renders: the switch, plus the thresholds it would apply,
// read-only, so the person flipping it can see what they are turning on rather than trusting a label.
//
// Scope is "ORG" or "BRANCH", which ROW answered, not which row was asked for. A branch with no row
// of its own inherits the org default, and an admin who cannot tell those apart will eventually flip
// the org switch believing they changed one branch.
type AutoHoldSetting struct {
	Enabled  bool   `json:"auto_hold_enabled"`
	Scope    string `json:"scope"`               // ORG | BRANCH
	BranchID string `json:"branch_id,omitempty"` // "" when the org default answered

	// -- read-only thresholds (client-set; see the file header) -----------------------------------
	AgingBucketDays     int   `json:"aging_bucket_days"`
	AgingThresholdCents int64 `json:"aging_threshold_cents"`
	AgingHoldEnabled    bool  `json:"aging_hold_enabled"`
	LimitHoldEnabled    bool  `json:"limit_hold_enabled"`
	LimitToleranceCents int64 `json:"limit_tolerance_cents"`
	CODEnforced         bool  `json:"cod_enforced"`

	// ThresholdsEditable is always false. It is on the wire rather than assumed, so the client renders
	// the numbers as facts instead of as inputs, and so a future decision to open them up is a visible
	// change in this struct rather than a quiet one in a template.
	ThresholdsEditable bool `json:"thresholds_editable"`
}

// autoHoldStore is the write half. It is a separate interface from creditStore so a deployment (or a
// test) can carry the evaluator without the admin switch, and so "who may write this column" stays a
// one-line answer.
type autoHoldStore interface {
	SetAutoHoldEnabled(ctx context.Context, branchID string, enabled bool) error
}

// ErrAutoHoldNotConfigured, the switch's write seam is not wired. A deliberate 503-shaped refusal
// rather than a generic 500: an operator should learn this is a deploy problem, not a policy one.
var ErrAutoHoldNotConfigured = errors.New("billing: the auto-hold switch is not configured on this server")

// WithAutoHoldStore injects the write seam. The billing Repository satisfies it in production.
func (s *CreditService) WithAutoHoldStore(st autoHoldStore) *CreditService {
	s.autoHold = st
	return s
}

// AutoHoldSwitchWired reports whether the switch can be written. The surface renders itself read-only
// rather than offering a control that will fail, the "a nil seam turns the feature off rather than
// failing" rule this codebase follows everywhere else.
func (s *CreditService) AutoHoldSwitchWired() bool { return s.autoHold != nil }

// AutoHold reads the effective setting for a branch ("" = the org default).
//
// It is a projection of CreditPolicy, not a second read: the same resolver the nightly sweep consults,
// so the switch an admin looks at and the flag the machine obeys can never disagree. That is the same
// reason the aging screen and the auto-hold rule share one query (see AgingRow's comment).
func (s *CreditService) AutoHold(ctx context.Context, branchID string) (AutoHoldSetting, error) {
	pol, err := s.store.CreditPolicy(ctx, branchID)
	if err != nil {
		return AutoHoldSetting{}, err
	}
	scope := "ORG"
	if pol.BranchID != "" {
		scope = "BRANCH"
	}
	return AutoHoldSetting{
		Enabled:             pol.AutoHoldEnabled,
		Scope:               scope,
		BranchID:            pol.BranchID,
		AgingBucketDays:     pol.AgingBucketDays,
		AgingThresholdCents: pol.AgingThresholdCents,
		AgingHoldEnabled:    pol.AgingHoldEnabled,
		LimitHoldEnabled:    pol.LimitHoldEnabled,
		LimitToleranceCents: pol.LimitToleranceCents,
		CODEnforced:         pol.CODEnforced,
		ThresholdsEditable:  false,
	}, nil
}

// SetAutoHold flips the master switch and returns the setting as it now stands.
//
// Every flip is audited, in BOTH directions and with the direction recorded. Turning the machine ON is
// the moment it may start stopping customers; turning it OFF is the moment it stops, and the second
// is the one somebody will need to explain later, when an account that should have been held wasn't.
// A trail that only records the scary direction is a trail with a hole in exactly the shape of the
// question it will be asked.
//
// Re-asserting the current value is not an error. A UI that renders a switch will re-post its state,
// and refusing a no-op would turn a double-click into a red banner.
func (s *CreditService) SetAutoHold(ctx context.Context, branchID string, enabled bool, actorID, reason string) (AutoHoldSetting, error) {
	if s.autoHold == nil {
		return AutoHoldSetting{}, ErrAutoHoldNotConfigured
	}
	before, err := s.AutoHold(ctx, branchID)
	if err != nil {
		return AutoHoldSetting{}, err
	}
	if err := s.autoHold.SetAutoHoldEnabled(ctx, branchID, enabled); err != nil {
		return AutoHoldSetting{}, err
	}
	after, err := s.AutoHold(ctx, branchID)
	if err != nil {
		return AutoHoldSetting{}, err
	}
	if reason == "" {
		reason = "auto-hold switch"
	}
	// UNION TRAIN 9, THE TRAIL IS WRITTEN IN THE HOUSE SHAPE, AND THIS IS A FIX, NOT A TIDY-UP.
	//
	// As first written this row NEVER REACHED audit_log at the organisation scope, and at any scope it
	// recorded no direction. Both causes are invisible to a fake recorder and were found by flipping
	// the real switch in the union walk and then looking in the table:
	//
	//   1. EntityID was policyEntityID(after), which is the literal "ORG" for the org default. The
	//      column is written `NULLIF($3,'')::uuid` (audit/repository.go:41), so "ORG" raises SQLSTATE
	//      22P02 and the INSERT fails. CreditService.record is best-effort (`_ = s.audit.Record(…)`),
	//      so the error is swallowed, the screen reports success and NO ROW EXISTS. The org default is
	//      the scope the shipped admin panel actually flips.
	//   2. from/to/scope/at/no_op/branch_id are NOT on audit's deny-by-default allowlist
	//      (allowedExtraKeys, audit/service.go:303), so every one of them was dropped before
	//      persistence. Even where the row did land, the DIRECTION, the whole point of auditing the
	//      OFF flip, was not on it.
	//
	// The shape below is the one the order-edit trail, the consent trail and the order-status trail
	// already use: {field, before, after}, plus `status` for the scope. Those keys are allowlisted, so
	// this needs no allowlist change, which is the argument for using the house shape in the first
	// place. The scope rides as `status` rather than the entity id, and the branch id rides in the
	// row's own BranchID column, where it is a uuid and searchable.
	//
	// EntityID is left EMPTY at org scope on purpose: NULLIF makes it NULL, and a row with a NULL
	// subject that EXISTS is worth incomparably more than a searchable subject on a row that does not.
	// The row is still found by action + entity_type, and by branch_id when there is one.
	s.record(ctx, audit.SensitiveAccess{
		Action: ActAutoHoldToggled, EntityType: "ar_credit_policy", EntityID: policyEntityID(after),
		ActorID: actorID, BranchID: branchID, Purpose: "CREDIT_POLICY_CHANGE", Reason: reason,
		Extra: map[string]any{
			"field":  "auto_hold_enabled",
			"before": autoHoldWord(before.Enabled),
			"after":  autoHoldWord(after.Enabled),
			"status": after.Scope,
		},
	})
	return after, nil
}

// policyEntityID names the row the audit entry is about: the branch's uuid, or NOTHING for the
// organisation default.
//
// UNION TRAIN 9, it used to return the literal "ORG" for the org default, on the reasoning that an
// audit row whose subject is "" is a row nobody can search for. The reasoning is sound and the value
// was still wrong: audit_log.entity_id is a uuid column written through `NULLIF($3,”)::uuid`, so
// "ORG" is not an unsearchable subject, it is a FAILED INSERT, and CreditService.record swallows the
// error. The org default is the scope the admin panel flips, so the master credit switch went
// completely unaudited in production while both its tests passed against a fake recorder.
//
// The scope is not lost: it rides on the row as changes.status ("organisation default" / the branch's
// name), and a branch flip also carries its uuid in the row's own branch_id column.
func policyEntityID(s AutoHoldSetting) string {
	if s.BranchID != "" {
		return s.BranchID
	}
	return ""
}

// autoHoldWord renders the switch's state for the trail. Words rather than booleans because the
// {field, before, after} shape this row now uses carries strings everywhere else in this program, and
// a trail that reads "before: false" makes a reader work out what "false" was the state OF.
func autoHoldWord(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
