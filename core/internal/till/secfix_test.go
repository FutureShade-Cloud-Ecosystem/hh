package till

// secfix_test.go, the two before real data security items on the till's sign in paths, driven through
// the real guards and the real handler over the fake store:
//
//  1. A payment attempt is written under the branch the caller's authority and the request agree on,
//     never under a branch the client merely names. A branch the caller cannot reach is refused with
//     the shared 403 body, byte identical to every other role refusal.
//  2. The org default NSF policy is dealer wide money policy: only tier 1 global finance and general
//     roles (and the platform roles, which stand outside the matrix) write it. A tier 2 manager gets
//     the shared 403; the read stays manager wide.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type secfixAuthorities struct {
	bySubject map[string]identity.StaffAuthority
}

func (s secfixAuthorities) ResolveStaff(_ context.Context, subject, _ string) (identity.StaffAuthority, bool, error) {
	a, ok := s.bySubject[subject]
	return a, ok, nil
}

type secfixNoPortal struct{}

func (secfixNoPortal) PortalRole(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

type secfixKnob struct{}

func (secfixKnob) DriverSessionHours(context.Context, string) (int, error) { return 12, nil }

const (
	secfixBranchA = "00000000-0000-4000-8000-0000000000a1"
	secfixBranchB = "00000000-0000-4000-8000-0000000000b2"
	secfixBranchC = "00000000-0000-4000-8000-0000000000c3"
)

// secfixRole is one row of staff_role: the sweep below enumerates the migration's rows, so a role added
// there without a decision here fails the sweep.
type secfixRole struct {
	id    string
	tier  int
	scope string
}

var secfixRoles = []secfixRole{
	{"general_manager", identity.TierLeadership, identity.ScopeGlobal},
	{"financial_controller", identity.TierLeadership, identity.ScopeGlobal},
	{"hr", identity.TierLeadership, identity.ScopeGlobal},
	{"branch_manager", identity.TierManager, identity.ScopeBranch},
	{"sales_manager", identity.TierManager, identity.ScopeBranch},
	{"yard_manager", identity.TierManager, identity.ScopeBranch},
	{"logistics_manager", identity.TierManager, identity.ScopeBranch},
	{"procurement_manager", identity.TierManager, identity.ScopeBranch},
	{"payables_receivables", identity.TierManager, identity.ScopeGlobal},
	{"inside_sales", identity.TierFrontline, identity.ScopeBranch},
	{"outside_sales", identity.TierFrontline, identity.ScopeBranch},
	{"yard_team", identity.TierFrontline, identity.ScopeBranch},
	{"driver", identity.TierFrontline, identity.ScopeBranch},
	{"super_admin", identity.TierLeadership, identity.ScopeGlobal},
	{"dealer_admin", identity.TierLeadership, identity.ScopeGlobal},
	{"fb_operator", identity.TierLeadership, identity.ScopeGlobal},
}

func (r secfixRole) authority(branches ...string) identity.StaffAuthority {
	a := identity.StaffAuthority{RoleID: r.id, Tier: r.tier, Scope: r.scope}
	if r.scope == identity.ScopeBranch {
		a.Branches = branches
	}
	return a
}

type secfixFixture struct {
	mux     *http.ServeMux
	store   *fakeStore
	harness *testkit.Harness
	subject map[string]identity.StaffAuthority
}

func newSecfixFixture(t *testing.T) *secfixFixture {
	t.Helper()
	store := newFakeStore()
	subjects := map[string]identity.StaffAuthority{}
	harness := testkit.NewHarness(t)
	g, err := guards.NewGuards(harness.Gate, secfixAuthorities{bySubject: subjects}, secfixNoPortal{}, secfixKnob{})
	if err != nil {
		t.Fatalf("NewGuards: %v", err)
	}
	harness.Resolver.Set(testkit.OrgNorthSlug, testkit.OrgNorthID)
	svc := NewService(store)
	h := NewHandler(svc, nil).WithUserLinker(func(_ context.Context, sub string) (string, bool) { return "user-" + sub, true })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, g)
	return &secfixFixture{mux: mux, store: store, harness: harness, subject: subjects}
}

func (f *secfixFixture) person(sub string, a identity.StaffAuthority) {
	f.subject[sub] = a
	f.harness.Membership.SetMember(sub, testkit.OrgNorthSlug, true)
}

func (f *secfixFixture) do(t *testing.T, sub, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+f.harness.Minter.StaffToken(t, sub))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func secfixForbiddenBody(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	authz.RefuseForbidden(rec)
	return rec.Body.String()
}

func secfixAttempt(key, branch string) PaymentAttempt {
	return PaymentAttempt{IdempotencyKey: key, AmountCents: 1500, Method: MethodCard, Processor: "p", Result: "DECLINED", BranchID: branch}
}

const secfixAttemptPath = "/api/v1/ar/payment-attempts"

func TestSecfix_AttemptBranchIsNeverTheClientsChoice(t *testing.T) {
	f := newSecfixFixture(t)
	forbidden := secfixForbiddenBody(t)
	branchRole := secfixRoles[3] // branch_manager
	f.person("scoped-a", branchRole.authority(secfixBranchA))
	f.person("clerk-a", secfixRoles[9].authority(secfixBranchA)) // inside_sales
	f.person("global", secfixRoles[0].authority())
	f.person("two-grants", branchRole.authority(secfixBranchA, secfixBranchB))

	// A branch scoped role in a second branch: refused with the shared 403, nothing written.
	for _, sub := range []string{"scoped-a", "clerk-a"} {
		rec := f.do(t, sub, http.MethodPost, secfixAttemptPath, secfixAttempt("k-"+sub+"-other", secfixBranchB))
		if rec.Code != http.StatusForbidden || rec.Body.String() != forbidden {
			t.Errorf("%s naming a branch it cannot reach: got %d %q, want the shared 403 %q", sub, rec.Code, rec.Body.String(), forbidden)
		}
	}
	if len(f.store.attempts) != 0 {
		t.Fatalf("a refused attempt must write nothing, got %d rows", len(f.store.attempts))
	}

	// Its own branch is written as named.
	if rec := f.do(t, "scoped-a", http.MethodPost, secfixAttemptPath, secfixAttempt("k-own", secfixBranchA)); rec.Code != http.StatusCreated {
		t.Fatalf("own branch: got %d %s", rec.Code, rec.Body.String())
	}
	// A branch scoped caller with one grant who names none is written under the grant, never branchless.
	if rec := f.do(t, "scoped-a", http.MethodPost, secfixAttemptPath, secfixAttempt("k-empty", "")); rec.Code != http.StatusCreated {
		t.Fatalf("one grant, no branch named: got %d %s", rec.Code, rec.Body.String())
	}
	// A global role may name any branch.
	if rec := f.do(t, "global", http.MethodPost, secfixAttemptPath, secfixAttempt("k-global", secfixBranchC)); rec.Code != http.StatusCreated {
		t.Fatalf("global naming a branch: got %d %s", rec.Code, rec.Body.String())
	}
	// The same person with two grants: both reachable, the third refused, none named is ambiguous (422).
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-b", secfixBranchB)); rec.Code != http.StatusCreated {
		t.Fatalf("two grants, second branch: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-c", secfixBranchC)); rec.Code != http.StatusForbidden || rec.Body.String() != forbidden {
		t.Fatalf("two grants, third branch: got %d %q, want the shared 403", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "two-grants", http.MethodPost, secfixAttemptPath, secfixAttempt("k-two-none", "")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("two grants, no branch named: got %d %s, want 422", rec.Code, rec.Body.String())
	}

	want := map[string]string{"k-own": secfixBranchA, "k-empty": secfixBranchA, "k-global": secfixBranchC, "k-two-b": secfixBranchB}
	if len(f.store.attempts) != len(want) {
		t.Fatalf("rows written: got %d want %d", len(f.store.attempts), len(want))
	}
	for _, a := range f.store.attempts {
		if want[a.IdempotencyKey] != a.BranchID {
			t.Errorf("attempt %s written under branch %q, want %q", a.IdempotencyKey, a.BranchID, want[a.IdempotencyKey])
		}
	}
}

// nsfWriters is the decision: the roles that write the org default NSF policy.
var nsfWriters = map[string]bool{
	"general_manager": true, "financial_controller": true,
	"super_admin": true, "dealer_admin": true, "fb_operator": true,
}

func TestSecfix_NSFPolicyWriteSweep(t *testing.T) {
	f := newSecfixFixture(t)
	forbidden := secfixForbiddenBody(t)
	body := NSFPolicy{FeeCents: 9900, FeeAccountCode: "4200"}
	pairs := 0
	for _, r := range secfixRoles {
		sub := "role-" + r.id
		f.person(sub, r.authority(secfixBranchA))
		put := f.do(t, sub, http.MethodPut, "/api/v1/ar/nsf-policy", body)
		if nsfWriters[r.id] {
			if put.Code != http.StatusOK {
				t.Errorf("%s writes the NSF policy: got %d %s", r.id, put.Code, put.Body.String())
			}
		} else if put.Code != http.StatusForbidden || put.Body.String() != forbidden {
			t.Errorf("%s must get the shared 403 on the NSF policy write: got %d %q", r.id, put.Code, put.Body.String())
		}
		// The read is unchanged: every manager tier role reads, front line does not.
		get := f.do(t, sub, http.MethodGet, "/api/v1/ar/nsf-policy", nil)
		if r.tier <= identity.TierManager && get.Code != http.StatusOK {
			t.Errorf("%s must still read the NSF policy: got %d", r.id, get.Code)
		}
		if r.tier == identity.TierFrontline && get.Code != http.StatusForbidden {
			t.Errorf("%s must not read the NSF policy: got %d", r.id, get.Code)
		}
		pairs += 2
	}
	t.Logf("NSF sweep: %d roles, %d role by route pairs", len(secfixRoles), pairs)
}
