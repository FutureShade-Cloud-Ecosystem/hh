package gl

// approvers.go, DIB-360/361: per-branch JE threshold configuration + approver assignment.
//
// - WHAT ALREADY EXISTED, SO THE NEW PART IS VISIBLE -
// The THRESHOLD half shipped long ago: gl_policy.je_approval_threshold_cents (0032, $5,000, per-branch
// via gl_policy.branch_id), read by thresholdFor(), applied by CreateJournal (park as DRAFT at or
// above) and rendered by derivedStatus() as PENDING_APPROVAL. Migration 0075 blessed it as-is and
// recorded that raising it at go-live is an UPDATE, not a deploy. None of that is changed here; what
// is added is the WRITE path, so "configurable" stops meaning "configurable by someone with psql".
//
// The half that never existed is WHO may sign. Today the rule is "a manager who is not the maker",
// which means a $400,000 reclass can be approved by whichever manager happens to be logged in. DIB-361
// is the named-approver config that narrows that.
//
// - NARROWING, NEVER WIDENING. THIS IS THE LOAD-BEARING PROPERTY -
// An assignment can only REMOVE people from an already-authorized set. identity still decides who is
// a manager; four-eyes (approver ≠ maker) is still enforced in the store's single guarded UPDATE. A
// config row can never GRANT authority, if it could, this table would be a privilege-escalation
// surface, and a finance config screen is a poor place to keep one.
//
// - AN EMPTY ASSIGNMENT SET FAILS OPEN, DELIBERATELY -
// No named approvers for a branch → the shipped rule applies (any eligible manager). That is the
// OPPOSITE of the choice made for payment credentials in DIB-416, and the asymmetry is the point: an
// unconfigured branch that cannot post ANY journal is an outage on day one for every branch that has
// not been set up yet, while an unconfigured branch that posts under the old rule is exactly where it
// was yesterday. Fail-closed is right when the failure is silently moving money; fail-open is right
// when the failure is a config nobody has filled in yet.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ApproverAssignment is one gl_approver_assignments row (0106).
type ApproverAssignment struct {
	ID         string    `json:"id"`
	BranchID   string    `json:"branch_id"` // "" = org-wide
	PolicyType string    `json:"policy_type"`
	Role       string    `json:"role"`
	UserID     string    `json:"user_id"`
	UserName   string    `json:"user_name,omitempty"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// PolicyJEOverThreshold is the approval policy this module files (0075).
const PolicyJEOverThreshold = "JE_OVER_THRESHOLD"

// ErrNotAnAssignedApprover, the caller is a manager, but not one of the named approvers configured
// for this branch. A distinct error so the UI can say something useful instead of a bare 403.
var ErrNotAnAssignedApprover = errors.New("gl: you are not a configured approver for this branch")

// approverStore is the persistence the approver rules need.
type approverStore interface {
	ListApprovers(ctx context.Context, policyType, branchID string) ([]ApproverAssignment, error)
	UpsertApprover(ctx context.Context, a ApproverAssignment) (string, error)
	RemoveApprover(ctx context.Context, id string) error
	SetJEThreshold(ctx context.Context, branchID string, cents int64) error
}

// - threshold configuration (DIB-360) -

// maxJEThreshold is a sanity ceiling on the configurable threshold ($10,000,000).
//
// It exists because a threshold is the one number in this system where a typo is SILENT and
// UNBOUNDED in the dangerous direction: type an extra zero and every journal below the new figure
// posts unreviewed, with nothing on any screen to say the control was switched off. A ceiling turns
// that fat finger into an error message. It is deliberately generous, it is a guard against a slip,
// not an opinion about anyone's business.
const maxJEThreshold int64 = 10_000_000_00

// SetJEThreshold configures the maker-checker threshold for a branch ("" = the org default).
//
// A threshold of 0 is REFUSED. In derivedStatus() a zero threshold means "no approval rail at all"
// (the `threshold > 0` guard), so saving zero here would disable maker-checker for the branch while
// looking on screen exactly like a very strict setting. If somebody genuinely wants every journal to
// need a second signer, that is threshold 1, and they should have to type it.
func (s *Service) SetJEThreshold(ctx context.Context, branchID string, cents int64) error {
	if _, err := s.caller(ctx); err != nil {
		return err
	}
	store, ok := s.store.(approverStore)
	if !ok {
		return errors.New("gl: this store does not support approval configuration")
	}
	if cents <= 0 {
		return badReq("gl: a JE approval threshold must be positive, 0 would silently DISABLE the " +
			"maker-checker rail while reading on screen like the strictest possible setting. Use 1 to " +
			"require approval for every journal")
	}
	if cents > maxJEThreshold {
		return badReq("gl: a JE approval threshold above %s is refused as a typo guard, an extra zero "+
			"here silently posts every journal below it unreviewed", moneyCents(maxJEThreshold))
	}
	return store.SetJEThreshold(ctx, branchID, cents)
}

// - approver assignment (DIB-361) -

// ListApprovers returns the configured approvers for a policy at a branch. Org-wide rows are included.
func (s *Service) ListApprovers(ctx context.Context, policyType, branchID string) ([]ApproverAssignment, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	store, ok := s.store.(approverStore)
	if !ok {
		return nil, errors.New("gl: this store does not support approval configuration")
	}
	if policyType == "" {
		policyType = PolicyJEOverThreshold
	}
	return store.ListApprovers(ctx, policyType, branchID)
}

// AssignApprover names a user as an approver for a policy at a branch ("" = org-wide).
func (s *Service) AssignApprover(ctx context.Context, a ApproverAssignment) (string, error) {
	if _, err := s.caller(ctx); err != nil {
		return "", err
	}
	store, ok := s.store.(approverStore)
	if !ok {
		return "", errors.New("gl: this store does not support approval configuration")
	}
	if a.UserID == "" {
		return "", badReq("gl: an approver assignment needs a user")
	}
	if a.PolicyType == "" {
		a.PolicyType = PolicyJEOverThreshold
	}
	return store.UpsertApprover(ctx, a)
}

// RemoveApprover deletes an assignment.
func (s *Service) RemoveApprover(ctx context.Context, id string) error {
	if _, err := s.caller(ctx); err != nil {
		return err
	}
	store, ok := s.store.(approverStore)
	if !ok {
		return errors.New("gl: this store does not support approval configuration")
	}
	if id == "" {
		return badReq("gl: an assignment id is required")
	}
	return store.RemoveApprover(ctx, id)
}

// MayApprove reports whether userID is permitted to decide this policy at this branch, given the
// configuration. See the header: an EMPTY assignment set means "not configured" and returns true.
//
// It is a pure decision over a fetched list, so the rule can be tested without a database, the same
// reason billing's credit rule is a pure function.
func MayApprove(assignments []ApproverAssignment, userID string) bool {
	var anyEnabled bool
	for _, a := range assignments {
		if !a.Enabled {
			continue
		}
		anyEnabled = true
		if a.UserID == userID {
			return true
		}
	}
	return !anyEnabled
}

// CheckApprover is MayApprove against the live configuration, for the decide path.
func (s *Service) CheckApprover(ctx context.Context, policyType, branchID, userID string) error {
	store, ok := s.store.(approverStore)
	if !ok {
		return nil // a store without the config table cannot narrow anything
	}
	if policyType == "" {
		policyType = PolicyJEOverThreshold
	}
	assignments, err := store.ListApprovers(ctx, policyType, branchID)
	if err != nil {
		// A configuration read failure must not block an approval: the shipped rule (any eligible
		// manager, four-eyes enforced in the store) is still in force underneath, so failing open here
		// degrades to yesterday's behaviour rather than stopping the month-end close.
		return nil
	}
	if !MayApprove(assignments, userID) {
		return ErrNotAnAssignedApprover
	}
	return nil
}

// moneyCents renders cents for an error message.
func moneyCents(cents int64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
