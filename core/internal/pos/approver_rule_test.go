package pos

// approver_rule_test.go, the rule over the manager's credential at the
// clerk's till (row 482): a qualifying credential commits the return as
// the manager's decision, its record and audit row naming the approver who
// decided and the clerk who rang it and citing the refusal; every
// non-qualifying credential leaves the standing refusal in place and
// commits nothing; a credential that cannot be JUDGED refuses the request
// on its own family instead of passing for a refusal.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// fakeApproverSource records the credential it was handed and answers with
// what the test set.
type fakeApproverSource struct {
	resolved guards.Approver
	err      error

	lastCredential string
	lastOrgSlug    string
	calls          int
}

func (f *fakeApproverSource) ResolveApprover(_ context.Context, credential, orgSlug string) (guards.Approver, error) {
	f.calls++
	f.lastCredential = credential
	f.lastOrgSlug = orgSlug
	return f.resolved, f.err
}

// qualifyingApprover is the demo Sales manager's credential as the guards
// would resolve it: a person other than the clerk, the listed role, the
// sale's branch, issued inside the window of the harness clock.
func qualifyingApprover() guards.Approver {
	return guards.Approver{
		StaffScope: authz.StaffScope{
			PlatformUserID: "plat-manager", OrgSlug: "hh-demo", OrgID: "org-1",
			IssuedAt:  time.Date(2026, 6, 29, 11, 58, 0, 0, time.UTC),
			ExpiresAt: time.Date(2026, 6, 29, 12, 8, 0, 0, time.UTC),
		},
		StaffAuthority: identity.StaffAuthority{
			RoleID: "sales_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch,
			Branches: []string{"branch-trt"},
		},
	}
}

// clerkRequestCtx stamps exactly what the handler adapter stamps in
// production: the bridged caller (whose UserID the linker filled, the
// audit actor) and the request org facts the rule reads.
func clerkRequestCtx() context.Context {
	ctx := context.WithValue(context.Background(), requestOrgKey{}, requestOrg{
		OrgSlug: "hh-demo", OrgID: "org-1", PlatformUserID: "plat-clerk",
	})
	return staffcontext.ContextWithStaffUserID(ctx, "u-harness")
}

// harnessWithRule wires the default configuration, a fake resolver and the
// seeded 300.00 sale: above the amount, so every test here sits on the
// approval path.
func harnessWithRule(t *testing.T) (*harness, *fakeApproverSource) {
	t.Helper()
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t))
	src := &fakeApproverSource{}
	h.svc = h.svc.WithApproverSource(src)
	return h, src
}

func aboveAmountReturn() ReturnInput {
	in := wholeReturnInput()
	in.ApproverCredential = "Bearer approver-credential-value"
	return in
}

func TestApproverRule_QualifyingCredential_CommitsAsTheManagersDecision(t *testing.T) {
	h, src := harnessWithRule(t)
	src.resolved = qualifyingApprover()
	h.caller = salesCaller()

	out, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	if err != nil {
		t.Fatalf("the manager's credential must commit the return: %v", err)
	}
	if out.TotalCents != 30000 {
		t.Fatalf("return money wrong: %+v", out)
	}
	if src.lastOrgSlug != "hh-demo" || src.lastCredential != "Bearer approver-credential-value" {
		t.Fatalf("the resolver must be handed the credential in the request org: %q %q", src.lastOrgSlug, src.lastCredential)
	}
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok {
		t.Fatal("the committed record must exist")
	}
	if ev["decided_by"] != "plat-manager" || ev["decided_by_role"] != "sales_manager" || ev["rang_by"] != "u-harness" {
		t.Fatalf("the record must name the approver who decided and the clerk who rang it: %v", ev)
	}
	approval, isMap := ev["approval"].(map[string]any)
	if !isMap || approval["refusal"] != refundApprovalRequiredError {
		t.Fatalf("the record must cite the refusal: %v", ev["approval"])
	}
	if len(h.rec.rows) != 1 || h.rec.rows[0].Action != "RETURN_COMPLETED" {
		t.Fatalf("want the completion row, got %v", h.rec.rows)
	}
	row := h.rec.rows[0]
	if row.Extra["approver_id"] != "plat-manager" || row.Extra["amount_cents"] != int64(30000) {
		t.Fatalf("the audit row must name the approver and the amount: %+v", row.Extra)
	}
	if row.ActorID != "u-harness" || row.Reason != refundApprovalRequiredError {
		t.Fatalf("the audit row must keep the clerk as the ringer and cite the refusal: %+v", row)
	}
}

func TestApproverRule_NoCredential_StaysRefused(t *testing.T) {
	h, src := harnessWithRule(t)
	src.resolved = qualifyingApprover()
	h.caller = salesCaller()

	in := aboveAmountReturn()
	in.ApproverCredential = ""
	_, err := h.svc.Return(clerkRequestCtx(), in)
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("want the standing refusal, got %v", err)
	}
	if src.calls != 0 {
		t.Fatal("an absent credential must not reach the resolver")
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a refused return must persist nothing")
	}
}

func TestApproverRule_TheClerksOwnCredential_StaysRefused(t *testing.T) {
	h, src := harnessWithRule(t)
	approved := qualifyingApprover()
	approved.PlatformUserID = "plat-clerk" // the same person as the request's bearer
	src.resolved = approved
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("the clerk cannot approve their own refund: %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a refused return must persist nothing")
	}
}

func TestApproverRule_CredentialOutsideTheWindow_StaysRefused(t *testing.T) {
	h, src := harnessWithRule(t)
	stale := qualifyingApprover()
	stale.IssuedAt = time.Date(2026, 6, 29, 11, 50, 0, 0, time.UTC) // past the window, inside the ceiling
	src.resolved = stale
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("a credential older than the window cannot stand in for a manager: %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a refused return must persist nothing")
	}
}

func TestApproverRole_NotOnTheApproversList_StaysRefused(t *testing.T) {
	h, src := harnessWithRule(t)
	unlisted := qualifyingApprover()
	unlisted.RoleID = "inside_sales" // a Sales credential can never approve above the amount
	src.resolved = unlisted
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("a role off the approvers list cannot approve: %v", err)
	}
}

func TestApproverRule_GrantShortOfTheSalesBranch_StaysRefused(t *testing.T) {
	h, src := harnessWithRule(t)
	otherBranch := qualifyingApprover()
	otherBranch.Branches = []string{"branch-kgt"} // a Kingston manager cannot approve Trenton's till
	src.resolved = otherBranch
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("the approval belongs to the sale's branch: %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a refused return must persist nothing")
	}
}

func TestApproverRule_CredentialThatDidNotVerify_IsA401(t *testing.T) {
	h, src := harnessWithRule(t)
	src.err = authz.ErrSecondCredentialRefused
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 401 {
		t.Fatalf("an unverifiable credential refuses the request on the credential family: %v", err)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
		t.Fatal("a refused request must persist nothing and touch no rail")
	}
}

func TestApproverRule_CredentialFromAnotherOrg_IsA403(t *testing.T) {
	h, src := harnessWithRule(t)
	src.err = authz.ErrSecondCredentialForeign
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 403 {
		t.Fatalf("a credential with no standing in the org refuses on the foreign family: %v", err)
	}
}

func TestApproverRule_ResolverUnwired_IsOperational(t *testing.T) {
	h := newHarness()
	seedSaleWorth(h, 30000, 0)
	h.svc = h.svc.WithRoleConfig(defaultConfig(t)) // no approver source wired
	h.caller = salesCaller()

	in := aboveAmountReturn()
	in.ApproverCredential = "Bearer something-presented"
	_, err := h.svc.Return(clerkRequestCtx(), in)
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 503 {
		t.Fatalf("a presented credential with no resolver wired is an operational refusal: %v", err)
	}
}

// An under-amount return ignores the header entirely: the rule is never
// asked, the resolver is never called, the return commits.
func TestApproverRule_UnderAmount_HeaderNeverConsulted(t *testing.T) {
	h, src := harnessWithRule(t)
	seedSaleWorth(h, 20000, 0)
	h.caller = salesCaller()

	out, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	if err != nil {
		t.Fatalf("an under-amount return commits: %v", err)
	}
	if out.TotalCents != 20000 || src.calls != 0 {
		t.Fatalf("the resolver must not be consulted under the amount: calls=%d", src.calls)
	}
}
