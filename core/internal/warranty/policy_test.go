package warranty

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// policy_test.go, the walk change 6 warranty policy over the DB-free fakeStore: the effective read
// (branch override wins over the org row), the threshold validation, and the audit spine entry that
// records the old and new value on a change (the pos counter-settings pattern).

// - fake store policy rows -

// policyRows mirrors the 0396 shape: keyed by branch_id with "" the org default row (branch_id NULL).
type policyRows map[string]WarrantyPolicy

// policyRead is the fake's read half: the branch row when present, else the org row, the resolution
// the real SQL carries (repository_policy.go).
func (f *fakeStore) WarrantyPolicy(_ context.Context, branchID string) (WarrantyPolicy, error) {
	if err := f.fail("WarrantyPolicy"); err != nil {
		return WarrantyPolicy{}, err
	}
	if p, ok := f.policy[branchID]; ok {
		return p, nil
	}
	if p, ok := f.policy[""]; ok && branchID != "" {
		return p, nil
	}
	return WarrantyPolicy{}, notFound("warranty: no warranty policy row for %q", branchID)
}

func (f *fakeStore) UpsertWarrantyPolicy(_ context.Context, in PolicyInput, updatedBy string) (WarrantyPolicy, error) {
	if err := f.fail("UpsertWarrantyPolicy"); err != nil {
		return WarrantyPolicy{}, err
	}
	f.policy[in.BranchID] = WarrantyPolicy{
		BranchID:            in.BranchID,
		ClaimThresholdCents: in.ClaimThresholdCents,
		UpdatedBy:           updatedBy,
	}
	return f.policy[in.BranchID], nil
}

// recordingAudit is the fake Recorder the unit tests read: it collects what the service filed.
type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.SensitiveAccess
}

func (r *recordingAudit) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, a)
	return nil
}

func (r *recordingAudit) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

func (r *recordingAudit) last() audit.SensitiveAccess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows[len(r.rows)-1]
}

// adminUserID is the acting administrator the policy tests file their writes under.
const adminUserID = "d7000000-0000-4000-9000-000000000010"

// identityAdmin builds the administrator caller the Dealer Admin write runs as (the route is
// RequireAdmin; the service reads the stamped caller for updated_by and the audit row).
func identityAdmin() identity.StaffContext {
	return identity.StaffContext{
		UserID: adminUserID, RoleCode: "SUPER_ADMIN",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}
}

// newPolicyService wires the fake store with the org default row 0396 seeds and an admin caller.
func newPolicyService(storeRows policyRows, rec *recordingAudit) *Service {
	f := newFakeStore()
	f.policy = storeRows
	return NewService(nil).withStore(f).withCaller(testCallerFrom).WithAudit(rec)
}

func adminCtx() context.Context {
	return ctxWith(identityAdmin())
}

func TestPolicyReadsTheOrgDefault(t *testing.T) {
	svc := newPolicyService(policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}, &recordingAudit{})
	p, err := svc.Policy(context.Background(), "")
	if err != nil {
		t.Fatalf("read the org policy: %v", err)
	}
	if p.ClaimThresholdCents != 5000 {
		t.Fatalf("org claim_threshold_cents = %d, want the 0396 seed 5000", p.ClaimThresholdCents)
	}
}

func TestPolicyBranchOverrideWins(t *testing.T) {
	svc := newPolicyService(policyRows{
		"":         {BranchID: "", ClaimThresholdCents: 5000},
		testBranch: {BranchID: testBranch, ClaimThresholdCents: 2500},
	}, &recordingAudit{})
	p, err := svc.Policy(context.Background(), testBranch)
	if err != nil {
		t.Fatalf("read the branch policy: %v", err)
	}
	if p.ClaimThresholdCents != 2500 {
		t.Fatalf("branch claim_threshold_cents = %d, want the branch override 2500", p.ClaimThresholdCents)
	}
	// The org scope still answers the org row.
	org, err := svc.Policy(context.Background(), "")
	if err != nil {
		t.Fatalf("read the org policy beside a branch override: %v", err)
	}
	if org.ClaimThresholdCents != 5000 {
		t.Fatalf("org claim_threshold_cents = %d, want 5000 untouched by the branch override", org.ClaimThresholdCents)
	}
}

func TestUpdatePolicyRefusesANegativeThreshold(t *testing.T) {
	rec := &recordingAudit{}
	svc := newPolicyService(policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}, rec)
	_, err := svc.UpdatePolicy(adminCtx(), PolicyInput{ClaimThresholdCents: -1})
	if err == nil {
		t.Fatal("a negative claim_threshold_cents was accepted, the CHECK constraint must never be the line that refuses it")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("a negative threshold answered %v, want a 422 in the core's own words", err)
	}
	if !strings.Contains(err.Error(), "claim_threshold_cents must be >= 0") {
		t.Fatalf("the refusal %q does not name the knob and the rule", err.Error())
	}
	if rec.count() != 0 {
		t.Fatalf("a refused write filed %d audit rows, want none", rec.count())
	}
}

func TestUpdatePolicyRecordsOldAndNewOnTheAuditSpine(t *testing.T) {
	rec := &recordingAudit{}
	svc := newPolicyService(policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}, rec)
	saved, err := svc.UpdatePolicy(adminCtx(), PolicyInput{ClaimThresholdCents: 2500})
	if err != nil {
		t.Fatalf("save the threshold: %v", err)
	}
	if saved.ClaimThresholdCents != 2500 {
		t.Fatalf("saved claim_threshold_cents = %d, want 2500", saved.ClaimThresholdCents)
	}
	if rec.count() != 1 {
		t.Fatalf("one moved knob filed %d audit rows, want exactly 1", rec.count())
	}
	row := rec.last()
	if row.Action != "WARRANTY_POLICY_CHANGED" {
		t.Fatalf("audit action = %q, want WARRANTY_POLICY_CHANGED", row.Action)
	}
	if row.EntityType != "warranty_policy" {
		t.Fatalf("audit entity_type = %q, want warranty_policy", row.EntityType)
	}
	if row.Extra["field"] != "claim_threshold_cents" {
		t.Fatalf("audit field = %v, want claim_threshold_cents", row.Extra["field"])
	}
	if row.Extra["before"] != "5000" || row.Extra["after"] != "2500" {
		t.Fatalf("audit before/after = %v/%v, want the old and new value 5000/2500", row.Extra["before"], row.Extra["after"])
	}
	if row.EntityID == "" {
		t.Fatal("the org-default change filed an empty entity id, which lands as SQL NULL and is never readable again (the pos dealerScopeID lesson)")
	}
}

func TestUpdatePolicyNoChangeWritesNoAuditRow(t *testing.T) {
	rec := &recordingAudit{}
	svc := newPolicyService(policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}, rec)
	if _, err := svc.UpdatePolicy(adminCtx(), PolicyInput{ClaimThresholdCents: 5000}); err != nil {
		t.Fatalf("save an unchanged threshold: %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("a save that moved nothing filed %d audit rows, want none: a history of every save is a list of button presses", rec.count())
	}
}

func TestUpdatePolicyStampsUpdatedByFromTheCaller(t *testing.T) {
	f := newFakeStore()
	f.policy = policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}
	svc := NewService(nil).withStore(f).withCaller(testCallerFrom).WithAudit(&recordingAudit{})
	if _, err := svc.UpdatePolicy(adminCtx(), PolicyInput{ClaimThresholdCents: 2500}); err != nil {
		t.Fatalf("save the threshold: %v", err)
	}
	if got := f.policy[""].UpdatedBy; got != adminUserID {
		t.Fatalf("updated_by = %q, want the acting caller %q", got, adminUserID)
	}
}

func TestUpdatePolicyRefusedWithoutACaller(t *testing.T) {
	f := newFakeStore()
	f.policy = policyRows{"": {BranchID: "", ClaimThresholdCents: 5000}}
	svc := NewService(nil).withStore(f).withCaller(testCallerFrom)
	if _, err := svc.UpdatePolicy(context.Background(), PolicyInput{ClaimThresholdCents: 2500}); err == nil {
		t.Fatal("an unauthenticated write was accepted; updated_by must never be an empty stamp")
	}
}
