package warranty

// policy.go, the walk change 6 warranty policy surface: the claim threshold, the dealer wide knob
// that decides whether a defect's cost becomes a warranty claim or is logged as shrinkage. Manager
// Home's Warranty board reads it, the counter till reads it for the return sentence, and Dealer
// Admin's Warranty page sets it. The shape is the pos_policy precedent (internal/pos/policy.go): the
// branch row overrides the org NULL row, the read resolves that way, and a change is recorded on the
// audit spine with the old and new value.

import (
	"context"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// actWarrantyPolicyChanged files one moved threshold under its own verb. A warranty-owned string
// rather than a constant added to internal/audit, for the same reason pos keeps POS_POLICY_CHANGED
// local: the audit package's exported verbs are the SHARED ones several modules emit, and a settings
// change only this module writes has no business widening a package every module imports.
const actWarrantyPolicyChanged = "WARRANTY_POLICY_CHANGED"

const (
	// policyEntity is the audit entity_type these rows file under.
	policyEntity = "warranty_policy"
	// policyOrgScopeID is the entity id a DEALER-DEFAULT (branch_id NULL) change files under. An empty
	// entity id cannot be read back (audit writes it as NULLIF('')::uuid and reads entity_id = $2::uuid,
	// so a "" row lands as SQL NULL and no predicate matches it again); the nil uuid is the sentinel
	// because it can never collide with a real branches.id and carries no foreign key. The pos
	// dealerScopeID lesson, kept.
	policyOrgScopeID = "00000000-0000-0000-0000-000000000000"
)

// policyScopeID maps a policy's branch onto the entity id its audit rows file under. See
// policyOrgScopeID for why "" cannot be used as itself.
func policyScopeID(branchID string) string {
	if strings.TrimSpace(branchID) == "" {
		return policyOrgScopeID
	}
	return branchID
}

// WarrantyPolicy is the EFFECTIVE policy for a scope (the branch row when present, else the org
// default). Money is integer cents. UpdatedBy stays OFF the wire (json:"-"): a person's id never
// rides it, the audit spine carries who changed what and the client reads that through its own
// surfaces.
type WarrantyPolicy struct {
	BranchID            string `json:"branch_id,omitempty"`
	ClaimThresholdCents int64  `json:"claim_threshold_cents"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	UpdatedBy           string `json:"-"`
}

// PolicyInput is the write payload. claim_threshold_cents arrives in integer cents on the wire, the
// only money shape this program puts on a wire.
type PolicyInput struct {
	BranchID            string `json:"branch_id,omitempty"`
	ClaimThresholdCents int64  `json:"claim_threshold_cents"`
}

// CostOverClaimThreshold answers the one question the walk change 6 defect classification asks: does
// this defect's cost reach the claim threshold? The cost is the LINE TOTAL (qty x unit cost of the
// damaged line, integer cents, captured once and never recomputed), never a per unit figure.
//
// THE RULE IS OVER OR EQUAL: a cost at exactly the threshold is a warranty claim, only a cost
// strictly under it is logged as shrinkage. This function is the one place that rule lives; the
// defect classification calls it, and nothing else in the program compares a cost to the threshold,
// so the tie cannot come to mean two things. Money is integer cents end to end (BIGINT in the
// database, int64 here, cents on the wire), so the edges are exact where a float would round.
func CostOverClaimThreshold(costCents, thresholdCents int64) bool {
	return costCents >= thresholdCents
}

// Policy returns the effective policy for a branch (the branch row when present, else the org
// default, seeded by 0396).
func (s *Service) Policy(ctx context.Context, branchID string) (WarrantyPolicy, error) {
	return s.store.WarrantyPolicy(ctx, branchID)
}

// UpdatePolicy validates and stores the policy, then records a CHANGE on the audit spine with the old
// and new value, the pos counter-settings pattern: one row per moved knob, and a save that moved
// nothing writes none (a history of every save is a list of times somebody pressed a button).
//
// The route is RequireAdmin; the service still requires the stamped caller so updated_by is a real
// attribution and never an empty stamp. The threshold is validated HERE, before the row is written,
// so a bad value is a 422 with words rather than a CHECK-constraint 500. A failed pre-image read does
// not block the save: losing the before-image costs a history row, refusing the dealer's change would
// cost the change itself.
func (s *Service) UpdatePolicy(ctx context.Context, in PolicyInput) (WarrantyPolicy, error) {
	if in.ClaimThresholdCents < 0 {
		return WarrantyPolicy{}, unprocessable("warranty: claim_threshold_cents must be >= 0")
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return WarrantyPolicy{}, err
	}
	before, beforeErr := s.store.WarrantyPolicy(ctx, in.BranchID)
	saved, err := s.store.UpsertWarrantyPolicy(ctx, in, caller.UserID)
	if err != nil {
		return WarrantyPolicy{}, err
	}
	if beforeErr == nil && before.ClaimThresholdCents != saved.ClaimThresholdCents && s.audit != nil {
		// ONE row for the one knob this table carries, in the {field, before, after, actor_role} shape
		// the audit allowlist already admits. Best-effort, like every other Recorder call here: an
		// audit outage must never cost a dealer the setting they just saved.
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action:     actWarrantyPolicyChanged,
			EntityType: policyEntity,
			EntityID:   policyScopeID(saved.BranchID),
			ActorID:    caller.UserID,
			Extra: map[string]any{
				"field":      "claim_threshold_cents",
				"before":     strconv.FormatInt(before.ClaimThresholdCents, 10),
				"after":      strconv.FormatInt(saved.ClaimThresholdCents, 10),
				"actor_role": caller.RoleCode,
			},
		})
	}
	return saved, nil
}
