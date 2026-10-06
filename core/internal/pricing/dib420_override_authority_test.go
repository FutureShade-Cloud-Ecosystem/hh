package pricing

// dib420_override_authority_test.go, the two arms of DIB-420, plus the edges that make the ruling
// mean something.
//
// THE FINDING HAD NO TEST. REPORT-W4-D escalated this as E-1 and said so plainly: "I did not write a
// test cementing this. It is a ticket draft." So there was no W4-D test to flip, the pins below are
// new, and the one shipped test that DID encode the old behaviour (TestResolve_MarginFloor_Approved
// OverridePasses, service_test.go U12) is flipped in place, with a note saying what it used to prove.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - authority doubles -

// grantingAuthority says yes to everybody. It stands in for a caller who genuinely holds authority.
type grantingAuthority struct {
	source string
	calls  int
	seenID string
	seenBr string
}

func (a *grantingAuthority) MarginOverrideAuthority(_ context.Context, userID, branchID string) (OverrideGrant, error) {
	a.calls++
	a.seenID, a.seenBr = userID, branchID
	return OverrideGrant{Granted: true, Source: a.source}, nil
}

// refusingAuthority is a wired authority that finds none, the ordinary case for a salesperson.
type refusingAuthority struct{ calls int }

func (a *refusingAuthority) MarginOverrideAuthority(_ context.Context, _, _ string) (OverrideGrant, error) {
	a.calls++
	return OverrideGrant{}, nil
}

// erroringAuthority is the approvals substrate being unreachable.
type erroringAuthority struct{}

func (erroringAuthority) MarginOverrideAuthority(_ context.Context, _, _ string) (OverrideGrant, error) {
	return OverrideGrant{}, errors.New("approvals substrate unreachable")
}

// unnamedGrantAuthority says yes without saying why, an adapter bug that must not become a price.
type unnamedGrantAuthority struct{}

func (unnamedGrantAuthority) MarginOverrideAuthority(_ context.Context, _, _ string) (OverrideGrant, error) {
	return OverrideGrant{Granted: true}, nil
}

// belowFloorStore is the fixture every arm shares: list 10000, cost 8500 → margin 1500 bps against a
// configured 2000 floor. Below the floor by a margin nobody can argue is a rounding artefact.
func belowFloorStore() *fakeStore {
	return &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
}

func belowFloorInput(override bool) ResolveInput {
	return base(ResolveInput{
		CategoryID: "pavers", UOMEntered: "EA", CostCents: 8500,
		OverrideRequested: override, UserID: "3b420000-0000-4000-9000-000000000001",
		BranchID: "bb700000-0000-4000-9000-000000000001",
	})
}

// - ARM 1: the flag ALONE is refused -

// TestDIB420_FlagAloneDoesNotBypassTheMarginGate is the named test for the first acceptance arm.
// This is the exact request W4-D described, a body with has_approved_override:true behind readGuard
// , and with no authority wired it must be REFUSED, not honoured.
func TestDIB420_FlagAloneDoesNotBypassTheMarginGate(t *testing.T) {
	svc := newSvc(belowFloorStore(), "pavers") // no WithOverrideAuthority: this is what ships
	bd := resolve(t, svc, belowFloorInput(true))

	if !bd.Blocked {
		t.Fatalf("the client flag ALONE must not clear the margin floor (DIB-420), got %+v", bd)
	}
	if bd.BlockPolicy != BlockMarginBelowMinimum {
		t.Fatalf("the refusal must still route to the A-20 approval queue, got block_policy=%q", bd.BlockPolicy)
	}
	if bd.OverrideAuthority != "" {
		t.Fatalf("a refused request must name NO authority, got %q", bd.OverrideAuthority)
	}
	if !bd.OverrideRequested {
		t.Fatal("the breakdown must echo that an override WAS requested, a refusal that looks like no request is how a counter learns to keep clicking")
	}
	if bd.Detail == "" || bd.Detail == "Below the configured margin floor, manager approval required" {
		t.Fatalf("a refused REQUEST must say the request was seen and not honoured, got %q", bd.Detail)
	}
}

// TestDIB420_WiredAuthorityThatFindsNoneRefuses is the ordinary production refusal: the seam IS
// wired (main.go always wires it), the caller is an ordinary salesperson, and the answer is no.
func TestDIB420_WiredAuthorityThatFindsNoneRefuses(t *testing.T) {
	auth := &refusingAuthority{}
	svc := newSvc(belowFloorStore(), "pavers").WithOverrideAuthority(auth)
	bd := resolve(t, svc, belowFloorInput(true))

	if !bd.Blocked {
		t.Fatalf("an override request with no authority behind it must be refused, got %+v", bd)
	}
	if auth.calls != 1 {
		t.Fatalf("the server must ASK (exactly once), got %d lookups", auth.calls)
	}
}

// TestDIB420_AuthorityErrorFailsClosed pins the ruling that matters most on the worst day: when the
// approvals substrate cannot be reached, "I could not check" is NOT "it is fine". An outage must not
// become a pricing hole, because an outage is when somebody is most motivated to try.
func TestDIB420_AuthorityErrorFailsClosed(t *testing.T) {
	svc := newSvc(belowFloorStore(), "pavers").WithOverrideAuthority(erroringAuthority{})
	if bd := resolve(t, svc, belowFloorInput(true)); !bd.Blocked {
		t.Fatalf("a FAILED authority lookup must refuse (fail closed), got %+v", bd)
	}
}

// TestDIB420_UnnamedGrantIsRefused: an adapter that says yes without naming a source is telling us it
// does not know why. An unattributable below-floor price is the defect wearing a uniform.
func TestDIB420_UnnamedGrantIsRefused(t *testing.T) {
	svc := newSvc(belowFloorStore(), "pavers").WithOverrideAuthority(unnamedGrantAuthority{})
	if bd := resolve(t, svc, belowFloorInput(true)); !bd.Blocked {
		t.Fatalf("a grant with no named authority source must be refused, got %+v", bd)
	}
}

// - ARM 2: the flag PLUS a server-side authority is honoured -

// TestDIB420_RequestWithServerSideAuthorityIsHonoured is the named test for the second acceptance
// arm, run once per authority source. Honouring it is not enough: the breakdown must name WHICH
// authority, and a live resolve must leave an audit row that says the same thing.
func TestDIB420_RequestWithServerSideAuthorityIsHonoured(t *testing.T) {
	for _, tc := range []struct {
		source string
		reason string
	}{
		{AuthorityApproval, BypassApprovedOverr},
		{AuthorityManagerScope, BypassManagerScope},
	} {
		t.Run(tc.source, func(t *testing.T) {
			st := belowFloorStore()
			auth := &grantingAuthority{source: tc.source}
			svc := newSvc(st, "pavers").WithOverrideAuthority(auth)
			in := belowFloorInput(true)
			bd := resolve(t, svc, in)

			if bd.Blocked {
				t.Fatalf("an override backed by %s must be honoured, got %+v", tc.source, bd)
			}
			if bd.OverrideAuthority != tc.source {
				t.Fatalf("the breakdown must name the authority %q, got %q", tc.source, bd.OverrideAuthority)
			}
			if bd.BypassReason != tc.reason {
				t.Fatalf("the honoured override must carry bypass_reason %q, got %q", tc.reason, bd.BypassReason)
			}
			// The authority was asked about THE CALLER the auth guard resolved and THE BRANCH being
			// priced, never about anything the body could name on its own.
			if auth.seenID != in.UserID || auth.seenBr != in.BranchID {
				t.Fatalf("the lookup must use the resolved caller + priced branch, asked about (%q,%q) want (%q,%q)",
					auth.seenID, auth.seenBr, in.UserID, in.BranchID)
			}
			// One BYPASS audit row, naming the authority.
			var found int
			for _, a := range st.audits {
				if a.Action == "BYPASS" && a.EntityType == "margin_enforcement" {
					found++
					if a.BypassReason != tc.reason {
						t.Fatalf("audit row must carry reason %q, got %q", tc.reason, a.BypassReason)
					}
					if a.UserID != in.UserID {
						t.Fatalf("audit row must attribute the resolved caller %q, got %q", in.UserID, a.UserID)
					}
				}
			}
			if found != 1 {
				t.Fatalf("an honoured override must write exactly ONE BYPASS audit row, got %d (%+v)", found, st.audits)
			}
		})
	}
}

// TestDIB420_NoRequestNeverAsksTheAuthority: the lookup is only reached when the price is below the
// floor AND the caller asked. A resolve that prices above the floor, or one that never asked, must
// not pay for a DB round trip, and must certainly not acquire an override it did not request.
func TestDIB420_NoRequestNeverAsksTheAuthority(t *testing.T) {
	// Below the floor, no request.
	auth := &grantingAuthority{source: AuthorityApproval}
	bd := resolve(t, newSvc(belowFloorStore(), "pavers").WithOverrideAuthority(auth), belowFloorInput(false))
	if !bd.Blocked {
		t.Fatalf("no override was requested, so the below-floor price must block, got %+v", bd)
	}
	if auth.calls != 0 {
		t.Fatalf("a resolve that did not ask for an override must not consult the authority, got %d lookups", auth.calls)
	}
	if bd.OverrideRequested {
		t.Fatal("override_requested must be false when none was requested")
	}

	// Above the floor, WITH a request: nothing to clear, so nothing is asked and nothing is recorded.
	st := &fakeStore{
		listPrice: &ListPrice{"blu60", 10000, "EA"},
		catPolicy: &CategoryPolicy{CategoryID: "pavers", MarginTargetBps: 3000, MarginFloorBps: 2000},
	}
	auth2 := &grantingAuthority{source: AuthorityApproval}
	in := belowFloorInput(true)
	in.CostCents = 5000 // margin 5000 bps, comfortably above the 2000 floor
	bd2 := resolve(t, newSvc(st, "pavers").WithOverrideAuthority(auth2), in)
	if bd2.Blocked {
		t.Fatalf("an above-floor price must not block, got %+v", bd2)
	}
	if auth2.calls != 0 {
		t.Fatalf("an above-floor resolve must not consult the authority, got %d lookups", auth2.calls)
	}
	if bd2.OverrideAuthority != "" || bd2.BypassReason != "" {
		t.Fatalf("an above-floor price must not be labelled as an override, got %+v", bd2)
	}
	if len(st.audits) != 0 {
		t.Fatalf("an above-floor price must write no bypass audit, got %+v", st.audits)
	}
}

// TestDIB420_DryRunHonoursButNeverAudits keeps finding #5 true through the new branch: the low-margin
// PREVIEW resolves dry-run, and a read-only preview must not write to the audit log. An honoured
// override on a preview is still honoured (the preview must show what the live price would be), it
// simply leaves no row, because nothing was priced.
func TestDIB420_DryRunHonoursButNeverAudits(t *testing.T) {
	st := belowFloorStore()
	svc := newSvc(st, "pavers").WithOverrideAuthority(&grantingAuthority{source: AuthorityApproval})
	out, err := svc.LowMarginReport(context.Background(), []ResolveInput{belowFloorInput(true)})
	if err != nil {
		t.Fatalf("LowMarginReport: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("the below-floor line must still surface in the classifier, got %d (%+v)", len(out), out)
	}
	if len(st.audits) != 0 {
		t.Fatalf("a dry-run preview must write ZERO audit rows, got %+v", st.audits)
	}
}

// TestDIB420_OverrideDoesNotHideALineFromTheLowMarginClassifier corrects the second half of W4-D's
// E-1, on the record. E-1 claimed a caller could "hide breaching lines from the low-margin
// classifier" by setting the flag per line. THEY COULD NOT, and the reason is worth pinning so that
// nobody later "fixes" the classifier by teaching it about overrides: LowMarginReport grades
// bd.MarginBps against the target/floor and never reads bd.Blocked, so an override changes the
// ENFORCEMENT and not the ARITHMETIC. A 15% line is a 15% line whoever approved it.
func TestDIB420_OverrideDoesNotHideALineFromTheLowMarginClassifier(t *testing.T) {
	svc := newSvc(belowFloorStore(), "pavers").WithOverrideAuthority(&grantingAuthority{source: AuthorityManagerScope})
	with, err := svc.LowMarginReport(context.Background(), []ResolveInput{belowFloorInput(true)})
	if err != nil {
		t.Fatalf("LowMarginReport (override): %v", err)
	}
	without, err := newSvc(belowFloorStore(), "pavers").LowMarginReport(context.Background(), []ResolveInput{belowFloorInput(false)})
	if err != nil {
		t.Fatalf("LowMarginReport (no override): %v", err)
	}
	if len(with) != 1 || len(without) != 1 {
		t.Fatalf("both arms must surface the breaching line, got %d and %d", len(with), len(without))
	}
	if with[0].MarginBps != without[0].MarginBps || with[0].Level != without[0].Level {
		t.Fatalf("an override must not change the CLASSIFICATION: %+v vs %+v", with[0], without[0])
	}
}

// - the route: the body flag never reaches the decision (D3, the QD-1 review's P2-5) -
//
// The service seam above is for a LIVE caller that binds its request to a
// document. The generic preview route is not one: through
// has_approved_override in its body, any approved margin card answered
// "granted" whatever its subject, and false BYPASS rows went into the log
// QD-2 and QD-4 read. The route reads the flag (the wire name is
// unchanged) and must drop it: a preview below the floor says blocked,
// and the write path that may clear it is the quote module's own floor
// decision, which binds the request to the line's own row.
func TestResolveRoute_BodyOverrideFlagNeverGrants(t *testing.T) {
	st := belowFloorStore()
	svc := newSvc(st, "pavers").WithOverrideAuthority(&grantingAuthority{source: AuthorityApproval})
	h := NewHandler(svc, nil)

	body := `{"product_id":"blu60","customer_id":"abc","category_id":"pavers","customer_type":"CONTRACTOR","qty_entered":1,"uom_entered":"EA","cost_cents":8500,"has_approved_override":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pricing/resolve", strings.NewReader(body))
	req = req.WithContext(identity.ContextWithStaffUserID(req.Context(), "3b420000-0000-4000-9000-000000000001"))
	h.resolve(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d (%s)", rec.Code, rec.Body.String())
	}
	var bd Breakdown
	if err := json.Unmarshal(rec.Body.Bytes(), &bd); err != nil {
		t.Fatalf("decode the breakdown: %v", err)
	}
	if !bd.Blocked {
		t.Fatalf("the body flag must not carry the preview past the floor, got %+v", bd)
	}
	if bd.OverrideAuthority != "" {
		t.Fatalf("a preview grants no authority, got %q", bd.OverrideAuthority)
	}
	for _, a := range st.audits {
		if a.Action == "BYPASS" {
			t.Fatalf("a preview asked with the body flag must write no BYPASS row, got %+v", a)
		}
	}
}
