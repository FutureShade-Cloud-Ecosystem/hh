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

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
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

// fakeRefusalTrail wraps the fake recorder with the audit read the
// citation uses: one refusal row id per sale.
type fakeRefusalTrail struct {
	*fakeRecorder
	refusals map[string]string
}

func (f fakeRefusalTrail) ReopenTransaction(_ context.Context, _, entityID string) ([]audit.Entry, error) {
	if id, ok := f.refusals[entityID]; ok {
		return []audit.Entry{{ID: id, Action: auditActionRefundApprovalRefused}}, nil
	}
	return nil, nil
}

func withRefusalTrail(h *harness, refusals map[string]string) {
	h.svc = h.svc.WithAudit(fakeRefusalTrail{fakeRecorder: h.rec, refusals: refusals})
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
	// The linker names the approver in the staff users id space, one
	// identifier kind for both people (the lead's P2-4 and P3-3 rulings).
	h.svc = h.svc.WithUserLinker(func(context.Context, string) (string, bool) {
		return "staff-manager-uuid", true
	})

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
	if ev["decided_by"] != "staff-manager-uuid" || ev["decided_by_role"] != "sales_manager" || ev["rang_by"] != "u-harness" {
		t.Fatalf("the record must name the approver who decided and the clerk who rang it, staff ids: %v", ev)
	}
	if ev["decided_by_subject"] != "plat-manager" {
		t.Fatalf("the record must keep the platform subject as the directory reference: %v", ev)
	}
	approval, isMap := ev["approval"].(map[string]any)
	if !isMap || approval["refusal"] != refundApprovalRequiredError {
		t.Fatalf("the record must cite the refusal: %v", ev["approval"])
	}
	if approval["refund_cents"] != int64(30000) {
		t.Fatalf("the event carries the refund total under its own name: %v", approval)
	}
	if _, has := approval["amount_cents"]; has {
		t.Fatalf("the event must not reuse the contract's configured-amount key: %v", approval)
	}
	if len(h.rec.rows) != 1 || h.rec.rows[0].Action != "RETURN_COMPLETED" {
		t.Fatalf("want the completion row, got %v", h.rec.rows)
	}
	row := h.rec.rows[0]
	if row.Extra["approver_id"] != "staff-manager-uuid" || row.Extra["amount_cents"] != int64(30000) {
		t.Fatalf("the audit row must name the approver by staff id and keep the amount: %+v", row.Extra)
	}
	if row.Extra["subject_ref"] != "plat-manager" {
		t.Fatalf("the audit row must keep the platform subject as the directory reference: %+v", row.Extra)
	}
	if row.ActorID != "u-harness" || row.Reason != refundApprovalRequiredError {
		t.Fatalf("the audit row must keep the clerk as the ringer and cite the refusal: %+v", row)
	}
}

// A platform subject with a seven-digit run, the redaction chokepoint's
// shape: the row must still name the approver, by the stable staff id.
func TestApproverRule_NumericSubject_ApproverSurvivesTheRow(t *testing.T) {
	h, src := harnessWithRule(t)
	approved := qualifyingApprover()
	approved.PlatformUserID = "dir-7731409-approver"
	src.resolved = approved
	h.caller = salesCaller()
	h.svc = h.svc.WithUserLinker(func(ctx context.Context, sub string) (string, bool) {
		if sub == "dir-7731409-approver" {
			return "staff-manager-uuid", true
		}
		return "", false
	})

	if _, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(h.rec.rows) != 1 {
		t.Fatalf("want the completion row, got %v", h.rec.rows)
	}
	if h.rec.rows[0].Extra["approver_id"] != "staff-manager-uuid" {
		t.Fatalf("the approver must be named by the stable staff id, not the redactable subject: %+v", h.rec.rows[0].Extra)
	}
}

// The committed decision cites the refusal's own audit row id when the
// trail can name one (the lead's P3-3 ruling).
func TestApproverRule_CitesTheRefusalRow(t *testing.T) {
	h, src := harnessWithRule(t)
	src.resolved = qualifyingApprover()
	h.caller = salesCaller()
	h.svc = h.svc.WithUserLinker(func(context.Context, string) (string, bool) {
		return "staff-manager-uuid", true
	})
	withRefusalTrail(h, map[string]string{"order-1": "refusal-row-abc"})

	if _, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ev, ok := h.store.lastEvent("pos.return.committed")
	if !ok {
		t.Fatal("the committed record must exist")
	}
	approval := ev["approval"].(map[string]any)
	if approval["refusal_id"] != "refusal-row-abc" {
		t.Fatalf("the decision must cite the refusal's audit row id: %v", approval)
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

// A presented credential is judged wherever it rides (the resolution runs
// before the transaction, the lead's P3-6 ruling): an under-amount return
// with a QUALIFYING credential commits ordinarily and names no decision,
// while an under-amount return with a credential that cannot be judged
// refuses on its own family.
func TestApproverRule_UnderAmount_QualifyingCredentialCommitsOrdinarily(t *testing.T) {
	h, src := harnessWithRule(t)
	seedSaleWorth(h, 20000, 0)
	src.resolved = qualifyingApprover()
	h.caller = salesCaller()
	h.svc = h.svc.WithUserLinker(func(context.Context, string) (string, bool) {
		return "staff-manager-uuid", true
	})

	out, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	if err != nil {
		t.Fatalf("an under-amount return commits: %v", err)
	}
	if out.TotalCents != 20000 || src.calls != 1 {
		t.Fatalf("the presented credential is judged once: calls=%d", src.calls)
	}
	if len(h.rec.rows) != 1 || h.rec.rows[0].Extra["approver_id"] != nil {
		t.Fatalf("an under-amount commit names no decision: %+v", h.rec.rows)
	}
}

func TestApproverRule_UnderAmount_UnjudgeableCredentialRefuses(t *testing.T) {
	h, src := harnessWithRule(t)
	seedSaleWorth(h, 20000, 0)
	src.err = authz.ErrSecondCredentialRefused
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 401 {
		t.Fatalf("a presented credential that did not verify refuses even under the amount: %v", err)
	}
	if len(h.store.returnRows) != 0 {
		t.Fatal("a refused request must persist nothing")
	}
}

// A credential that cannot be judged writes its own audit row naming the
// clerk and the sale (the lead's P3-5 ruling).
func TestApproverRule_UnjudgeableCredential_IsInTheTrail(t *testing.T) {
	h, src := harnessWithRule(t)
	src.err = authz.ErrSecondCredentialRefused
	h.caller = salesCaller()

	_, err := h.svc.Return(clerkRequestCtx(), aboveAmountReturn())
	if err == nil {
		t.Fatal("want the refusal")
	}
	var credentialRows []audit.SensitiveAccess
	for _, row := range h.rec.rows {
		if row.Action == auditActionRefundCredentialRefused {
			credentialRows = append(credentialRows, row)
		}
	}
	if len(credentialRows) != 1 {
		t.Fatalf("want the credential refusal's audit row, got %v", h.rec.rows)
	}
	row := credentialRows[0]
	if row.ActorID != "u-harness" || row.EntityID != "order-1" || row.Extra["status"] != "did_not_verify" {
		t.Fatalf("the row must name the clerk, the sale and the why: %+v", row)
	}
}

// The isolating leg for the not-the-clerk check (the lead's P3-2 ruling):
// a LISTED manager attaches their own credential to their own return. The
// amount rule would never fire for them, and the return refuses anyway:
// the one answer the not-the-clerk check exists for.
func TestApproverRule_ListedCallersOwnCredential_RefusesEvenThoughTheRuleIsQuiet(t *testing.T) {
	h, src := harnessWithRule(t)
	seedSaleWorth(h, 30000, 0)
	approved := qualifyingApprover()
	approved.PlatformUserID = "plat-manager"
	src.resolved = approved
	// The caller IS the manager: a listed role, so the amount rule alone
	// would let the return through.
	h.caller = staffcontext.StaffContext{UserID: "u-mgr", RoleCode: "SALES_MANAGER",
		Tier: staffcontext.TierManager, Scope: staffcontext.ScopeBranch, Branches: map[string]bool{"branch-trt": true}}
	ctx := context.WithValue(context.Background(), requestOrgKey{}, requestOrg{
		OrgSlug: "hh-demo", OrgID: "org-1", PlatformUserID: "plat-manager",
	})
	ctx = staffcontext.ContextWithStaffUserID(ctx, "u-mgr")

	_, err := h.svc.Return(ctx, aboveAmountReturn())
	var refusal ErrRefundApprovalRequired
	if !errors.As(err, &refusal) {
		t.Fatalf("a listed caller's own credential must refuse their own refund: %v", err)
	}
	if len(h.store.returnRows) != 0 || h.tender.refundCalled {
		t.Fatal("a refused return must persist nothing and touch no rail")
	}
}
